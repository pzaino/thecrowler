package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	mailconfig "github.com/pzaino/thecrowler/pkg/mail/config"
)

// LogHook receives allowlisted, provider-neutral mail ingestion events.
// Implementations should serialize only LogEvent's exported fields. The event
// deliberately cannot carry message bodies, addresses, headers, credentials,
// tokens, provider message identifiers, or error text.
type LogHook func(context.Context, LogEvent)

// LogState is a stable lifecycle state for a mail ingestion operation.
type LogState string

const (
	// LogStateStarted indicates the operation has started and is in progress.
	LogStateStarted LogState = "started"
	// LogStateSucceeded indicates the operation has completed successfully.
	LogStateSucceeded LogState = "succeeded"
	// LogStateFailed indicates the operation has completed with a failure.
	LogStateFailed LogState = "failed"
	// LogStateDiscarded indicates the operation has been discarded without processing.
	LogStateDiscarded LogState = "discarded"
)

// LogErrorCategory is a bounded, provider-neutral failure classification.
type LogErrorCategory string

const (
	// LogErrorAuthentication indicates an authentication failure.
	LogErrorAuthentication LogErrorCategory = "authentication"
	// LogErrorCanceled indicates the operation was canceled.
	LogErrorCanceled LogErrorCategory = "canceled"
	// LogErrorCheckpoint indicates a checkpoint reset.
	LogErrorCheckpoint LogErrorCategory = "checkpoint_reset"
	// LogErrorMalformed indicates a malformed request or data.
	LogErrorMalformed LogErrorCategory = "malformed"
	// LogErrorOversized indicates the data is oversized.
	LogErrorOversized LogErrorCategory = "oversized"
	// LogErrorPartial indicates a partial failure.
	LogErrorPartial LogErrorCategory = "partial"
	// LogErrorPolicy indicates a policy violation.
	LogErrorPolicy LogErrorCategory = "policy"
	// LogErrorRateLimit indicates a rate limit has been exceeded.
	LogErrorRateLimit LogErrorCategory = "rate_limit"
	// LogErrorTimeout indicates a timeout occurred.
	LogErrorTimeout LogErrorCategory = "timeout"
	// LogErrorTransient indicates a transient failure.
	LogErrorTransient LogErrorCategory = "transient"
	// LogErrorUnknown indicates an unknown failure.
	LogErrorUnknown LogErrorCategory = "unknown"
)

const (
	logOperationMailbox = "mailbox_reconcile"
	logOperationMessage = "message_process"
)

// LogEvent is the complete structured logging surface exposed by pkg/mail.
// MailboxID and MessageID are deterministic SHA-256 digests suitable for
// correlation without disclosing provider-visible identifiers.
type LogEvent struct {
	Operation     string           `json:"operation"`
	SourceID      string           `json:"source_id,omitempty"`
	MailboxID     string           `json:"mailbox_id,omitempty"`
	MessageID     string           `json:"message_id,omitempty"`
	State         LogState         `json:"state"`
	Duration      time.Duration    `json:"duration"`
	ErrorCategory LogErrorCategory `json:"error_category,omitempty"`
}

// MarshalJSON enforces redaction even when callers serialize a LogEvent
// directly instead of passing it through Pipeline.emitLog.
func (event LogEvent) MarshalJSON() ([]byte, error) {
	type wireLogEvent LogEvent
	return json.Marshal(wireLogEvent(redactLogEvent(event)))
}

func setDefaultLogFacility(p *Pipeline) {
	p.LogHook = func(_ context.Context, event LogEvent) {
		encoded, err := json.Marshal(event)
		if err != nil {
			return
		}

		level := cmn.DbgLvlInfo
		if event.State == LogStateFailed {
			level = cmn.DbgLvlError
		}

		cmn.DebugMsg(level, "mail_event=%s", encoded)
	}
}

func (p *Pipeline) emitLog(ctx context.Context, event LogEvent) {
	if p == nil {
		// This should never happen, but we defensively handle a nil Pipeline to avoid
		// panics in the logging path.
		p = &Pipeline{}
	}

	// Observability must not change ingestion behavior, including when a
	// caller-provided logging adapter panics.
	defer func() { _ = recover() }()

	if p.LogHook == nil {
		setDefaultLogFacility(p)
	}
	p.LogHook(ctx, redactLogEvent(event))
}

func redactLogEvent(event LogEvent) LogEvent {
	event.Operation = mailconfig.RedactString(event.Operation)
	event.SourceID = mailconfig.RedactString(event.SourceID)
	event.MailboxID = mailconfig.RedactString(event.MailboxID)
	event.MessageID = mailconfig.RedactString(event.MessageID)
	event.State = LogState(mailconfig.RedactString(string(event.State)))
	event.ErrorCategory = LogErrorCategory(mailconfig.RedactString(string(event.ErrorCategory)))
	return event
}

func (p *Pipeline) now() time.Time {
	if p != nil && p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pipeline) mailboxLogEvent(mailbox Mailbox, state LogState, started time.Time, err error) LogEvent {
	return LogEvent{
		Operation:     logOperationMailbox,
		SourceID:      p.SourceID,
		MailboxID:     safeMailboxIdentity(mailbox),
		State:         state,
		Duration:      p.logDuration(started),
		ErrorCategory: logErrorCategory(err),
	}
}

func (p *Pipeline) messageLogEvent(ref MessageRef, state LogState, started time.Time, err error) LogEvent {
	return LogEvent{
		Operation:     logOperationMessage,
		SourceID:      p.SourceID,
		MailboxID:     safeMailboxIdentity(ref.Mailbox),
		MessageID:     safeMessageIdentity(ref),
		State:         state,
		Duration:      p.logDuration(started),
		ErrorCategory: logErrorCategory(err),
	}
}

func (p *Pipeline) logDuration(started time.Time) time.Duration {
	if started.IsZero() {
		return 0
	}
	return elapsedSince(p.now(), started)
}

func elapsedSince(now, started time.Time) time.Duration {
	if started.IsZero() || now.Before(started) {
		return 0
	}
	return now.Sub(started)
}

func safeMailboxIdentity(mailbox Mailbox) string {
	identity := strings.TrimSpace(mailbox.ID)
	if identity == "" {
		identity = strings.TrimSpace(mailbox.Name)
	}
	if identity == "" {
		return ""
	}
	return safeLogDigest("mailbox", identity)
}

func safeMessageIdentity(ref MessageRef) string {
	components := []string{
		strings.TrimSpace(ref.Provider),
		strings.TrimSpace(ref.AccountID),
		safeMailboxIdentity(ref.Mailbox),
	}
	if providerID := strings.TrimSpace(ref.ProviderMessageID); providerID != "" {
		components = append(components, "provider", providerID, strings.TrimSpace(ref.Version))
	} else {
		components = append(components,
			"imap",
			strconv.FormatUint(uint64(ref.UIDValidity), 10),
			strconv.FormatUint(uint64(ref.UID), 10),
			strings.TrimSpace(ref.Version),
		)
	}
	return safeLogDigest(components...)
}

func safeLogDigest(components ...string) string {
	hash := sha256.New()
	for _, component := range components {
		_, _ = hash.Write([]byte(strconv.Itoa(len(component))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(component))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func logErrorCategory(err error) LogErrorCategory {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return LogErrorCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return LogErrorTimeout
	}

	var mailErr *Error
	if errors.As(err, &mailErr) {
		switch mailErr.Kind {
		case ErrorAuthentication:
			return LogErrorAuthentication
		case ErrorCheckpointReset:
			return LogErrorCheckpoint
		case ErrorMalformed:
			return LogErrorMalformed
		case ErrorOversized:
			return LogErrorOversized
		case ErrorPartial:
			return LogErrorPartial
		case ErrorPolicy:
			return LogErrorPolicy
		case ErrorRateLimit:
			return LogErrorRateLimit
		case ErrorTimeout:
			return LogErrorTimeout
		case ErrorTransient, ErrorNetwork:
			return LogErrorTransient
		}
	}

	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return LogErrorTimeout
	}
	return LogErrorUnknown
}
