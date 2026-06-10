package mail

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrorKind classifies failures without exposing provider-specific or secret
// details to callers.
type ErrorKind string

const (
	// ErrorTransient identifies a temporary provider or network failure.
	ErrorTransient ErrorKind = "transient"
	// ErrorTimeout identifies a provider operation that exceeded its deadline.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorRateLimit identifies provider throttling. RetryAfter may carry the
	// provider's requested delay.
	ErrorRateLimit ErrorKind = "rate_limit"
	// ErrorPartial identifies an incomplete parser result that may succeed when
	// the message is fetched and parsed again.
	ErrorPartial ErrorKind = "partial"
	// ErrorAuthentication identifies rejected or expired credentials.
	ErrorAuthentication ErrorKind = "authentication"
	// ErrorPermission identifies a permanent authorization failure.
	ErrorPermission ErrorKind = "permission"
	// ErrorConfiguration identifies invalid or unsupported source settings.
	ErrorConfiguration ErrorKind = "configuration"
	// ErrorMalformed identifies unsafe or unrecoverable message content.
	ErrorMalformed ErrorKind = "malformed"
	// ErrorOversized identifies a message that exceeds a configured hard limit.
	ErrorOversized ErrorKind = "oversized"
	// ErrorPolicy identifies content rejected by another configured policy.
	ErrorPolicy ErrorKind = "policy"
	// ErrorCheckpointReset identifies provider state that requires full
	// reconciliation instead of an incremental retry.
	ErrorCheckpointReset ErrorKind = "checkpoint_reset"
)

// Error is a provider-neutral mail failure. Message must be safe to expose in
// logs and status records; Cause is available for internal inspection only.
type Error struct {
	Kind       ErrorKind
	Operation  string
	Message    string
	RetryAfter time.Duration
	Cause      error
}

// Error returns a redacted, provider-neutral description of the failure.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Operation == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Operation
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Message)
}

// Unwrap exposes the internal cause to errors.Is and errors.As without adding
// it to the redacted Error string.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RetryAction describes what an ingestion caller should do after a failure.
type RetryAction string

const (
	// RetryActionRetry repeats the failed operation after Decision.Delay.
	RetryActionRetry RetryAction = "retry"
	// RetryActionDiscard permanently skips message content that cannot become
	// valid through another fetch. It is not used for mailbox-level failures.
	RetryActionDiscard RetryAction = "discard"
	// RetryActionFail stops retrying and surfaces the failure for operator or
	// configuration intervention.
	RetryActionFail RetryAction = "fail"
)

// RetryReason is a stable, provider-neutral explanation for a retry decision.
type RetryReason string

const (
	RetryReasonTransient      RetryReason = "transient"
	RetryReasonTimeout        RetryReason = "timeout"
	RetryReasonRateLimit      RetryReason = "rate_limit"
	RetryReasonParserPartial  RetryReason = "parser_partial"
	RetryReasonOversized      RetryReason = "oversized"
	RetryReasonAuthentication RetryReason = "authentication"
	RetryReasonMalformed      RetryReason = "malformed"
	RetryReasonPermanent      RetryReason = "permanent"
	RetryReasonCanceled       RetryReason = "canceled"
	RetryReasonExhausted      RetryReason = "exhausted"
)

// RetryDecision is the typed outcome of applying a RetryPolicy to a failure.
type RetryDecision struct {
	Action RetryAction
	Reason RetryReason
	Delay  time.Duration
}

// RetryPolicy bounds retries and exponential backoff. MaxAttempts includes the
// initial call. RetryAfter hints are capped by MaxBackoff.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

var defaultRetryPolicy = RetryPolicy{
	MaxAttempts:    3,
	InitialBackoff: time.Second,
	MaxBackoff:     30 * time.Second,
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaultRetryPolicy.MaxAttempts
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = defaultRetryPolicy.InitialBackoff
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = defaultRetryPolicy.MaxBackoff
	}
	if p.InitialBackoff > p.MaxBackoff {
		p.InitialBackoff = p.MaxBackoff
	}
	return p
}

// Backoff returns the capped exponential delay after the given failed attempt.
// failedAttempt is one-based: the first failure uses InitialBackoff.
func (p RetryPolicy) Backoff(failedAttempt int) time.Duration {
	p = p.normalized()
	if failedAttempt <= 1 {
		return p.InitialBackoff
	}
	delay := p.InitialBackoff
	for i := 1; i < failedAttempt; i++ {
		if delay >= p.MaxBackoff || delay > p.MaxBackoff/2 {
			return p.MaxBackoff
		}
		delay *= 2
	}
	if delay > p.MaxBackoff {
		return p.MaxBackoff
	}
	return delay
}

// DecideRetry classifies err and returns a bounded action and delay.
// failedAttempt is one-based and counts the call that produced err.
func DecideRetry(err error, failedAttempt int, policy RetryPolicy) RetryDecision {
	if err == nil {
		return RetryDecision{Action: RetryActionFail, Reason: RetryReasonPermanent}
	}
	if errors.Is(err, context.Canceled) {
		return RetryDecision{Action: RetryActionFail, Reason: RetryReasonCanceled}
	}

	policy = policy.normalized()
	reason, retryable, discard, retryAfter := classifyRetryError(err)
	if discard {
		return RetryDecision{Action: RetryActionDiscard, Reason: reason}
	}
	if !retryable {
		return RetryDecision{Action: RetryActionFail, Reason: reason}
	}
	if failedAttempt >= policy.MaxAttempts {
		return RetryDecision{Action: RetryActionFail, Reason: RetryReasonExhausted}
	}

	delay := policy.Backoff(failedAttempt)
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > policy.MaxBackoff {
		delay = policy.MaxBackoff
	}
	return RetryDecision{Action: RetryActionRetry, Reason: reason, Delay: delay}
}

func classifyRetryError(err error) (reason RetryReason, retryable, discard bool, retryAfter time.Duration) {
	var mailErr *Error
	if errors.As(err, &mailErr) {
		switch mailErr.Kind {
		case ErrorTransient:
			return RetryReasonTransient, true, false, mailErr.RetryAfter
		case ErrorTimeout:
			return RetryReasonTimeout, true, false, mailErr.RetryAfter
		case ErrorRateLimit:
			return RetryReasonRateLimit, true, false, mailErr.RetryAfter
		case ErrorPartial:
			return RetryReasonParserPartial, true, false, mailErr.RetryAfter
		case ErrorOversized, ErrorPolicy:
			return RetryReasonOversized, false, true, 0
		case ErrorMalformed:
			return RetryReasonMalformed, false, true, 0
		case ErrorAuthentication:
			return RetryReasonAuthentication, false, false, 0
		default:
			return RetryReasonPermanent, false, false, 0
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return RetryReasonTimeout, true, false, 0
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return RetryReasonTimeout, true, false, 0
	}
	return RetryReasonPermanent, false, false, 0
}

// Retryable reports whether err belongs to a class that can be retried. It does
// not apply an attempt budget; callers that execute retries should use
// DecideRetry.
func Retryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	_, retryable, _, _ := classifyRetryError(err)
	return retryable
}
