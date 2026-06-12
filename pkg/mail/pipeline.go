package mail

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	defaultPipelinePageSize        = 100
	defaultUIDValidityRescanWindow = 1000
)

// Pipeline coordinates incremental mailbox ingestion across the provider,
// durable checkpoint, message processing, and document emission boundaries.
//
// A Pipeline processes mailboxes serially and isolates failures to the message
// that caused them. A page cursor is advanced only when every upsert in that
// page has been emitted successfully. This preserves at-least-once delivery:
// successful messages from a partially failed page may be emitted again when
// the page is retried, so Emitter implementations must be idempotent by
// Document.ID.
type Pipeline struct {
	Connector  Connector
	StateStore StateStore
	Processor  Processor
	Emitter    Emitter

	// SourceID, Provider, and AccountID form the durable mailbox checkpoint
	// key. Provider and AccountID may be left empty when a connector does not
	// need those dimensions to distinguish its mailboxes.
	SourceID  string
	Provider  string
	AccountID string

	// PageSize controls the maximum number of changes requested per provider
	// call. Values less than one use a conservative default.
	PageSize int

	// UIDValidityRescanWindow bounds the number of changes processed in the run
	// that discovers an IMAP UIDVALIDITY reset. The reset checkpoint starts at
	// UID zero so older messages are not skipped; if more work remains, the
	// committed cursor lets a later reconciliation resume safely. Values less
	// than one use a conservative default.
	UIDValidityRescanWindow int

	// FetchOptions controls message retrieval. IncludeBody is forced to true
	// because Processor requires the RFC 5322 stream.
	FetchOptions FetchOptions

	// RetryPolicy bounds retries for connector, parser, and emitter failures.
	// Zero values use conservative defaults.
	RetryPolicy RetryPolicy

	// Sleep waits between retry attempts. Tests may inject a deterministic
	// implementation; nil uses a context-aware timer.
	Sleep func(context.Context, time.Duration) error

	// LifecycleEventSink receives closed, redacted operational lifecycle
	// events. Delivery is observational and never participates in document or
	// checkpoint durability. Transient failures are retried according to
	// LifecycleEventRetryPolicy; exhausted failures and sink panics are dropped.
	LifecycleEventSink LifecycleEventSink

	// LifecycleEventRetryPolicy bounds retries of lifecycle event delivery.
	// A zero value performs one best-effort attempt without delaying ingestion.
	LifecycleEventRetryPolicy RetryPolicy

	// LogHook receives structured, redacted lifecycle events. Logging is
	// observational: a nil hook is a no-op and hook panics do not stop ingestion.
	LogHook LogHook

	// Now supplies timestamps for logging durations. Nil uses time.Now.
	Now func() time.Time
}

// NewPipeline constructs a Pipeline from its four side-effecting dependencies.
// Source and provider identity, page size, and fetch limits can be configured
// on the returned value before Run is called.
func NewPipeline(connector Connector, stateStore StateStore, processor Processor, emitter Emitter) *Pipeline {
	return &Pipeline{
		Connector:  connector,
		StateStore: stateStore,
		Processor:  processor,
		Emitter:    emitter,
	}
}

// Run lists all connector mailboxes and ingests changes after each mailbox's
// durable checkpoint. Message fetch, processing, and emission failures are
// joined and returned after the remaining messages in the same page have had a
// chance to run. Cancellation stops new work immediately.
func (p *Pipeline) Run(ctx context.Context) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	mailboxes, _, err := retryValue(ctx, p.RetryPolicy, p.sleep, func() ([]Mailbox, error) {
		return p.Connector.ListMailboxes(ctx)
	})
	if err != nil {
		return fmt.Errorf("list mailboxes: %w", err)
	}

	var runErrors []error
	for _, mailbox := range mailboxes {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(runErrors, err)...)
		}
		if err := p.runMailbox(ctx, mailbox); err != nil {
			runErrors = append(runErrors, err)
		}
	}
	return errors.Join(runErrors...)
}

func (p *Pipeline) runMailbox(ctx context.Context, mailbox Mailbox) (runErr error) {
	started := p.now()
	stats := &reconciliationLifecycleStats{}
	identity := NewEmailEventIdentity(p.SourceID, p.Provider, p.AccountID, mailbox)
	p.emitLog(ctx, p.mailboxLogEvent(mailbox, LogStateStarted, time.Time{}, nil))
	p.emitLifecycleEvent(ctx, EmailEventListenerStarted, ListenerStartedEventPayload{
		SchemaVersion:      EmailEventSchemaVersion,
		EmailEventIdentity: identity,
		ListenerCount:      1,
	})
	defer func() {
		recordMailReconciliationRun()
		if runErr != nil && stats.failed == 0 {
			stats.failed++
		}
		p.emitLifecycleEvent(ctx, EmailEventReconciliationCompleted, ReconciliationCompletedEventPayload{
			SchemaVersion:      EmailEventSchemaVersion,
			EmailEventIdentity: identity,
			DiscoveredCount:    stats.discovered,
			FetchedCount:       stats.fetched,
			ParsedCount:        stats.parsed,
			FailedCount:        stats.failed,
			CompletedCount:     stats.completed,
			SkippedCount:       stats.skipped,
			QuarantinedCount:   stats.quarantined,
			RetryCount:         stats.retries,
		})
		p.emitLifecycleEvent(ctx, EmailEventListenerStopped, ListenerStoppedEventPayload{
			SchemaVersion:      EmailEventSchemaVersion,
			EmailEventIdentity: identity,
			ListenerCount:      1,
		})
		state := LogStateSucceeded
		if runErr != nil {
			state = LogStateFailed
		}
		p.emitLog(ctx, p.mailboxLogEvent(mailbox, state, started, runErr))
	}()
	key := MailboxKey{
		SourceID:  p.SourceID,
		Provider:  p.Provider,
		AccountID: p.AccountID,
		Mailbox:   mailbox,
	}
	checkpoint, err := p.StateStore.LoadCheckpoint(ctx, key)
	if err != nil {
		return fmt.Errorf("load checkpoint for mailbox %q: %w", mailboxIdentity(mailbox), err)
	}

	cursor := checkpoint.Cursor
	seen := make(map[string]struct{})
	rescanRemaining := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		limit := p.pageSize()
		if rescanRemaining > 0 && rescanRemaining < limit {
			limit = rescanRemaining
		}
		page, _, err := retryValue(ctx, p.RetryPolicy, p.sleep, func() (ChangePage, error) {
			return p.Connector.ListChanges(ctx, mailbox, cursor, limit)
		})
		if err != nil {
			return fmt.Errorf("list changes for mailbox %q: %w", mailboxIdentity(mailbox), err)
		}

		if resetValidity, reset := uidValidityReset(cursor, page); reset {
			if resetValidity == 0 {
				return &Error{
					Kind:      ErrorCheckpointReset,
					Operation: "reconcile mailbox",
					Message:   fmt.Sprintf("mailbox %q reset did not provide a new UIDVALIDITY", mailboxIdentity(mailbox)),
				}
			}
			if cursor.UID == 0 && cursor.UIDValidity == resetValidity {
				return &Error{
					Kind:      ErrorCheckpointReset,
					Operation: "reconcile mailbox",
					Message:   fmt.Sprintf("mailbox %q repeated UIDVALIDITY reset %d", mailboxIdentity(mailbox), resetValidity),
				}
			}

			next := checkpoint
			next.Cursor = Cursor{UIDValidity: resetValidity}
			next.ErrorCount = 0
			next.LastError = ""
			if err := p.StateStore.CommitCheckpoint(ctx, key, checkpoint.Version, next); err != nil {
				return fmt.Errorf("commit UIDVALIDITY reset for mailbox %q: %w", mailboxIdentity(mailbox), err)
			}
			checkpoint, err = p.StateStore.LoadCheckpoint(ctx, key)
			if err != nil {
				return fmt.Errorf("reload checkpoint for mailbox %q after UIDVALIDITY reset: %w", mailboxIdentity(mailbox), err)
			}
			cursor = checkpoint.Cursor
			rescanRemaining = p.uidValidityRescanWindow()
			clear(seen)
			continue
		}

		if rescanRemaining > 0 && len(page.Changes) > rescanRemaining {
			return fmt.Errorf("list changes for mailbox %q: provider returned %d changes for reset rescan limit %d", mailboxIdentity(mailbox), len(page.Changes), rescanRemaining)
		}

		messageErrors := p.processPage(ctx, page, seen, stats)
		if err := ctx.Err(); err != nil {
			return errors.Join(append(messageErrors, err)...)
		}

		next := checkpoint
		if len(messageErrors) == 0 {
			next.Cursor = page.Next
			next.ErrorCount = 0
			next.LastError = ""
		} else {
			next.ErrorCount++
			next.LastError = boundedPipelineError(errors.Join(messageErrors...))
		}
		if err := p.StateStore.CommitCheckpoint(ctx, key, checkpoint.Version, next); err != nil {
			messageErrors = append(messageErrors, fmt.Errorf("commit checkpoint for mailbox %q: %w", mailboxIdentity(mailbox), err))
			return errors.Join(messageErrors...)
		}

		committed, err := p.StateStore.LoadCheckpoint(ctx, key)
		if err != nil {
			messageErrors = append(messageErrors, fmt.Errorf("reload checkpoint for mailbox %q: %w", mailboxIdentity(mailbox), err))
			return errors.Join(messageErrors...)
		}
		checkpoint = committed

		if len(messageErrors) != 0 {
			return errors.Join(messageErrors...)
		}
		if !page.More {
			return nil
		}
		if page.Next == cursor {
			return fmt.Errorf("list changes for mailbox %q: provider returned a non-advancing cursor", mailboxIdentity(mailbox))
		}
		cursor = page.Next
		if rescanRemaining > 0 {
			rescanRemaining -= len(page.Changes)
			if rescanRemaining <= 0 {
				return nil
			}
		}
	}
}

func uidValidityReset(cursor Cursor, page ChangePage) (uint32, bool) {
	for _, change := range page.Changes {
		if change.Kind == ChangeReset {
			if change.Ref.UIDValidity != 0 {
				return change.Ref.UIDValidity, true
			}
			return page.Next.UIDValidity, true
		}
		if cursor.UIDValidity != 0 && change.Ref.UIDValidity != 0 && cursor.UIDValidity != change.Ref.UIDValidity {
			return change.Ref.UIDValidity, true
		}
	}
	if cursor.UIDValidity != 0 && page.Next.UIDValidity != 0 && cursor.UIDValidity != page.Next.UIDValidity {
		return page.Next.UIDValidity, true
	}
	return 0, false
}

type reconciliationLifecycleStats struct {
	discovered  uint64
	fetched     uint64
	parsed      uint64
	failed      uint64
	completed   uint64
	skipped     uint64
	quarantined uint64
	retries     uint64
}

type messageLifecycleOutcome struct {
	fetched            bool
	parsed             bool
	completed          bool
	failed             bool
	quarantined        bool
	retries            uint64
	attachments        uint64
	skippedAttachments uint64
	extractedLinks     uint64
}

func (stats *reconciliationLifecycleStats) add(outcome messageLifecycleOutcome) {
	recordMailMessageOutcome(outcome)
	if outcome.fetched {
		stats.fetched++
	}
	if outcome.parsed {
		stats.parsed++
	}
	if outcome.completed {
		stats.completed++
	}
	if outcome.failed {
		stats.failed++
	}
	if outcome.quarantined {
		stats.quarantined++
	}
	stats.retries += outcome.retries
}

func (p *Pipeline) processPage(ctx context.Context, page ChangePage, seen map[string]struct{}, stats *reconciliationLifecycleStats) []error {
	var messageErrors []error
	for _, change := range page.Changes {
		if err := ctx.Err(); err != nil {
			return append(messageErrors, err)
		}
		if change.Kind != ChangeUpsert {
			stats.skipped++
			recordMailMessageSkipped()
			continue
		}

		identity := pipelineChangeIdentity(change.Ref)
		if _, duplicate := seen[identity]; duplicate {
			stats.skipped++
			recordMailMessageSkipped()
			continue
		}
		seen[identity] = struct{}{}
		stats.discovered++
		recordMailMessageDiscovered()

		ref := change.Ref
		messageIdentity := NewEmailMessageEventIdentity(p.SourceID, p.lifecycleMessageRef(ref))
		p.emitLifecycleEvent(ctx, EmailEventMessageDiscovered, MessageDiscoveredEventPayload{
			SchemaVersion:             EmailEventSchemaVersion,
			EmailMessageEventIdentity: messageIdentity,
			DiscoveredCount:           1,
		})

		outcome, err := p.processMessage(ctx, ref, messageIdentity)
		stats.add(outcome)
		if err != nil {
			messageErrors = append(messageErrors, err)
		}
	}
	return messageErrors
}

func (p *Pipeline) processMessage(ctx context.Context, ref MessageRef, identity EmailMessageEventIdentity) (outcome messageLifecycleOutcome, processErr error) {
	started := p.now()
	terminalState := LogStateSucceeded
	var terminalErr error
	attempts := 0
	p.emitLog(ctx, p.messageLogEvent(ref, LogStateStarted, time.Time{}, nil))
	defer func() {
		if attempts > 1 {
			outcome.retries = uint64(attempts - 1)
		}
		if processErr != nil {
			outcome.failed = true
			terminalState = LogStateFailed
			terminalErr = processErr
		}
		p.emitLog(ctx, p.messageLogEvent(ref, terminalState, started, terminalErr))
	}()
	_, decision, err := retryValue(ctx, p.RetryPolicy, p.sleep, func() (struct{}, error) {
		attempts++
		options := p.FetchOptions
		options.IncludeBody = true
		raw, err := p.Connector.OpenMessage(ctx, ref, options)
		if err != nil {
			return struct{}{}, fmt.Errorf("fetch mailbox message: %w", err)
		}
		if raw.RFC822 == nil {
			return struct{}{}, errors.New("fetch mailbox message: connector returned a nil message stream")
		}

		outcome.fetched = true
		fetchedBytes := ref.Size
		if raw.Ref.Size > 0 {
			fetchedBytes = raw.Ref.Size
		}
		if fetchedBytes < 0 {
			fetchedBytes = 0
		}
		p.emitLifecycleEvent(ctx, EmailEventMessageFetched, MessageFetchedEventPayload{
			SchemaVersion:             EmailEventSchemaVersion,
			EmailMessageEventIdentity: identity,
			FetchedCount:              1,
			FetchedBytes:              uint64(fetchedBytes),
		})

		document, processErr := p.Processor.Process(ctx, raw)
		_ = raw.RFC822.Close()
		if processErr != nil {
			return struct{}{}, fmt.Errorf("process mailbox message: %w", processErr)
		}
		outcome.parsed = true
		outcome.attachments, outcome.skippedAttachments, outcome.extractedLinks = documentExtractionMetricCounts(document)
		p.emitLifecycleEvent(ctx, EmailEventMessageParsed, MessageParsedEventPayload{
			SchemaVersion:             EmailEventSchemaVersion,
			EmailMessageEventIdentity: identity,
			ParsedCount:               1,
			RecipientCount:            uint64(len(document.To) + len(document.CC) + len(document.BCC)),
			HeaderCount:               uint64(len(document.Headers.Values)),
			AttachmentCount:           uint64(len(document.Attachments)),
		})
		if err := p.Emitter.Emit(ctx, document); err != nil {
			return struct{}{}, fmt.Errorf("emit mailbox message: %w", err)
		}
		outcome.completed = true
		p.emitLifecycleEvent(ctx, EmailEventMessageCompleted, MessageCompletedEventPayload{
			SchemaVersion:             EmailEventSchemaVersion,
			EmailMessageEventIdentity: identity,
			CompletedCount:            1,
		})
		return struct{}{}, nil
	})
	if attempts > 1 {
		outcome.retries = uint64(attempts - 1)
	}
	if decision.Action == RetryActionDiscard {
		outcome.failed = true
		outcome.quarantined = true
		terminalState = LogStateDiscarded
		terminalErr = err
		p.emitMessageFailedEvent(ctx, identity, outcome.retries)
		return outcome, nil
	}
	if err != nil {
		outcome.failed = true
		p.emitMessageFailedEvent(ctx, identity, outcome.retries)
	}
	return outcome, err
}

func (p *Pipeline) emitMessageFailedEvent(ctx context.Context, identity EmailMessageEventIdentity, retries uint64) {
	p.emitLifecycleEvent(ctx, EmailEventMessageFailed, MessageFailedEventPayload{
		SchemaVersion:             EmailEventSchemaVersion,
		EmailMessageEventIdentity: identity,
		FailedCount:               1,
		RetryCount:                retries,
	})
}

func retryValue[T any](ctx context.Context, policy RetryPolicy, sleep func(context.Context, time.Duration) error, operation func() (T, error)) (T, RetryDecision, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		value, err := operation()
		if err == nil {
			return value, RetryDecision{}, nil
		}
		decision := DecideRetry(err, attempt, policy)
		if decision.Action != RetryActionRetry {
			return zero, decision, err
		}
		if err := sleep(ctx, decision.Delay); err != nil {
			return zero, RetryDecision{Action: RetryActionFail, Reason: RetryReasonCanceled}, err
		}
	}
}

func (p *Pipeline) sleep(ctx context.Context, delay time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Pipeline) validate() error {
	var missing []error
	if p == nil {
		return errors.New("mail pipeline is nil")
	}
	if p.Connector == nil {
		missing = append(missing, errors.New("mail pipeline connector is nil"))
	}
	if p.StateStore == nil {
		missing = append(missing, errors.New("mail pipeline state store is nil"))
	}
	if p.Processor == nil {
		missing = append(missing, errors.New("mail pipeline processor is nil"))
	}
	if p.Emitter == nil {
		missing = append(missing, errors.New("mail pipeline emitter is nil"))
	}
	return errors.Join(missing...)
}

func (p *Pipeline) pageSize() int {
	if p.PageSize > 0 {
		return p.PageSize
	}
	return defaultPipelinePageSize
}

func (p *Pipeline) uidValidityRescanWindow() int {
	if p.UIDValidityRescanWindow > 0 {
		return p.UIDValidityRescanWindow
	}
	return defaultUIDValidityRescanWindow
}

func pipelineChangeIdentity(ref MessageRef) string {
	mailbox := mailboxIdentity(ref.Mailbox)
	if ref.ProviderMessageID != "" {
		return fmt.Sprintf("%s/%s/%s/id:%s/version:%s", ref.Provider, ref.AccountID, mailbox, ref.ProviderMessageID, ref.Version)
	}
	return fmt.Sprintf("%s/%s/%s/uidvalidity:%d/uid:%d/version:%s", ref.Provider, ref.AccountID, mailbox, ref.UIDValidity, ref.UID, ref.Version)
}

func boundedPipelineError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) <= maxMailErrorLength {
		return message
	}
	return message[:maxMailErrorLength]
}
