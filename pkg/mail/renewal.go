package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultRenewalRetryDelay = 5 * time.Minute

// RenewalStatus is the deterministic lifecycle state of a provider subscription.
type RenewalStatus string

const (
	RenewalStatusHealthy RenewalStatus = "healthy"
	RenewalStatusDue     RenewalStatus = "due"
	RenewalStatusExpired RenewalStatus = "expired"
	RenewalStatusFailed  RenewalStatus = "failed"
)

// Valid reports whether status is a supported persisted renewal state.
func (s RenewalStatus) Valid() bool {
	switch s {
	case RenewalStatusHealthy, RenewalStatusDue, RenewalStatusExpired, RenewalStatusFailed:
		return true
	default:
		return false
	}
}

// RenewalMetadata is durable provider-subscription lifecycle state. Providers
// such as Gmail use it for expiring watches, while schedulers can make renewal
// decisions without depending on provider SDK types.
type RenewalMetadata struct {
	SubscriptionID string        `json:"subscription_id,omitempty" yaml:"subscription_id,omitempty"`
	ResourcePath   string        `json:"resource_path,omitempty" yaml:"resource_path,omitempty"`
	Status         RenewalStatus `json:"status,omitempty" yaml:"status,omitempty"`
	LastRenewedAt  time.Time     `json:"last_renewed_at,omitempty" yaml:"last_renewed_at,omitempty"`
	ExpiresAt      time.Time     `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	LastAttemptAt  time.Time     `json:"last_attempt_at,omitempty" yaml:"last_attempt_at,omitempty"`
	FailureCount   uint32        `json:"failure_count,omitempty" yaml:"failure_count,omitempty"`
	LastError      string        `json:"last_error,omitempty" yaml:"last_error,omitempty"`
}

// RenewalResult contains the authoritative expiration returned by a provider
// after creating or renewing a subscription.
type RenewalResult struct {
	SubscriptionID string
	ResourcePath   string
	ExpiresAt      time.Time
}

// SubscriptionRenewer renews an expiring provider notification subscription.
// It deliberately accepts only a provider-neutral mailbox key.
type SubscriptionRenewer interface {
	Renew(ctx context.Context, key MailboxKey) (RenewalResult, error)
}

// RenewalScheduler is a provider-neutral hook for arranging the next renewal
// attempt. Durable state remains in StateStore; this hook may target a timer,
// job queue, cron adapter, or another scheduler.
type RenewalScheduler interface {
	ScheduleRenewal(ctx context.Context, key MailboxKey, at time.Time) error
}

// RenewalDecision reports what RenewIfDue decided and what was persisted.
type RenewalDecision struct {
	Due         bool
	Expired     bool
	Attempted   bool
	Renewed     bool
	NextAttempt time.Time
	Status      RenewalStatus
	Metadata    RenewalMetadata
}

// RenewalCoordinator decides when provider subscriptions need renewal,
// performs due attempts, persists every attempted outcome, and invokes an
// optional provider-neutral scheduling hook for the next attempt.
type RenewalCoordinator struct {
	Store        StateStore
	Renewer      SubscriptionRenewer
	Scheduler    RenewalScheduler
	SafetyMargin time.Duration
	RetryDelay   time.Duration
	Now          func() time.Time
}

// RenewalStatusAt classifies metadata at a specific instant. A recorded
// failure takes precedence so operators can distinguish a failed renewal from
// a merely due one; expiration otherwise takes precedence over due.
func RenewalStatusAt(now time.Time, metadata RenewalMetadata, safetyMargin time.Duration) RenewalStatus {
	due, expired := RenewalDue(now, metadata, safetyMargin)
	if metadata.FailureCount > 0 || metadata.Status == RenewalStatusFailed {
		return RenewalStatusFailed
	}
	if expired {
		return RenewalStatusExpired
	}
	if due {
		return RenewalStatusDue
	}
	return RenewalStatusHealthy
}

// NextRenewalAt returns the deterministic safe scheduling boundary. When a
// provider grants a lifetime shorter than twice the configured margin, the
// margin is capped at half that lifetime to avoid an immediate renewal loop.
func NextRenewalAt(metadata RenewalMetadata, safetyMargin time.Duration) time.Time {
	if metadata.ExpiresAt.IsZero() {
		return time.Time{}
	}
	if safetyMargin < 0 {
		safetyMargin = 0
	}
	expiresAt := metadata.ExpiresAt.UTC()
	lastRenewedAt := metadata.LastRenewedAt.UTC()
	if !lastRenewedAt.IsZero() && expiresAt.After(lastRenewedAt) {
		halfLifetime := expiresAt.Sub(lastRenewedAt) / 2
		if safetyMargin > halfLifetime {
			safetyMargin = halfLifetime
		}
	}
	return expiresAt.Add(-safetyMargin)
}

// RenewalDue returns whether metadata has no expiration or has reached its
// deterministic safe scheduling boundary. An expiration at now is both due
// and expired.
func RenewalDue(now time.Time, metadata RenewalMetadata, safetyMargin time.Duration) (due, expired bool) {
	now = now.UTC()
	if metadata.ExpiresAt.IsZero() {
		return true, false
	}
	expiresAt := metadata.ExpiresAt.UTC()
	expired = !expiresAt.After(now)
	return expired || !NextRenewalAt(metadata, safetyMargin).After(now), expired
}

// RenewIfDue loads the current checkpoint, renews when required, commits the
// success or failure with compare-and-swap semantics, then schedules the next
// due time (or bounded retry time after a failure).
func (c RenewalCoordinator) RenewIfDue(ctx context.Context, key MailboxKey) (RenewalDecision, error) {
	if c.Store == nil {
		return RenewalDecision{}, errors.New("mail: renewal coordinator requires a state store")
	}
	if c.Renewer == nil {
		return RenewalDecision{}, errors.New("mail: renewal coordinator requires a subscription renewer")
	}
	if c.SafetyMargin < 0 {
		return RenewalDecision{}, errors.New("mail: renewal safety margin cannot be negative")
	}
	if err := ctx.Err(); err != nil {
		return RenewalDecision{}, err
	}

	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	checkpoint, err := c.Store.LoadCheckpoint(ctx, key)
	if err != nil {
		return RenewalDecision{}, fmt.Errorf("mail: load renewal state: %w", err)
	}
	decision := RenewalDecision{Metadata: checkpoint.Renewal}
	decision.Due, decision.Expired = RenewalDue(now, checkpoint.Renewal, c.SafetyMargin)
	decision.Status = RenewalStatusAt(now, checkpoint.Renewal, c.SafetyMargin)
	if !decision.Due {
		decision.NextAttempt = NextRenewalAt(checkpoint.Renewal, c.SafetyMargin)
		if err := c.schedule(ctx, key, decision.NextAttempt); err != nil {
			return decision, err
		}
		return decision, nil
	}

	decision.Attempted = true
	result, renewErr := c.Renewer.Renew(ctx, key)
	next := checkpoint
	next.Version = ""
	next.Renewal.LastAttemptAt = now
	if renewErr == nil && !result.ExpiresAt.After(now) {
		renewErr = errors.New("provider returned a watch expiration that is not in the future")
	}
	if renewErr != nil {
		next.Renewal.FailureCount++
		next.Renewal.LastError = boundedRenewalError(renewErr)
		next.Renewal.Status = RenewalStatusFailed
		decision.Status = RenewalStatusFailed
		decision.NextAttempt = safeRenewalRetryAt(now, next.Renewal.ExpiresAt, c.retryDelay())
	} else {
		next.Renewal.SubscriptionID = strings.TrimSpace(result.SubscriptionID)
		next.Renewal.ResourcePath = strings.TrimSpace(result.ResourcePath)
		next.Renewal.Status = RenewalStatusHealthy
		next.Renewal.LastRenewedAt = now
		next.Renewal.ExpiresAt = result.ExpiresAt.UTC()
		next.Renewal.FailureCount = 0
		next.Renewal.LastError = ""
		decision.Renewed = true
		decision.Status = RenewalStatusHealthy
		decision.NextAttempt = NextRenewalAt(next.Renewal, c.SafetyMargin)
	}

	if err := c.Store.CommitCheckpoint(ctx, key, checkpoint.Version, next); err != nil {
		return decision, fmt.Errorf("mail: persist renewal outcome: %w", err)
	}
	decision.Metadata = next.Renewal
	if err := c.schedule(ctx, key, decision.NextAttempt); err != nil {
		return decision, err
	}
	if renewErr != nil {
		return decision, fmt.Errorf("mail: renew provider subscription: %w", renewErr)
	}
	return decision, nil
}

func safeRenewalRetryAt(now, expiresAt time.Time, retryDelay time.Duration) time.Time {
	retryAt := now.Add(retryDelay)
	if expiresAt.IsZero() || !expiresAt.After(now) || retryAt.Before(expiresAt) {
		return retryAt
	}
	// Keep the retry strictly before expiration while avoiding a tight loop.
	return now.Add(expiresAt.Sub(now) / 2)
}

func (c RenewalCoordinator) retryDelay() time.Duration {
	if c.RetryDelay > 0 {
		return c.RetryDelay
	}
	return defaultRenewalRetryDelay
}

func (c RenewalCoordinator) schedule(ctx context.Context, key MailboxKey, at time.Time) error {
	if c.Scheduler == nil {
		return nil
	}
	if err := c.Scheduler.ScheduleRenewal(ctx, key, at); err != nil {
		return fmt.Errorf("mail: schedule subscription renewal: %w", err)
	}
	return nil
}

func boundedRenewalError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) <= maxMailErrorLength {
		return message
	}
	return message[:maxMailErrorLength]
}
