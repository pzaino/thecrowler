package mail

import (
	"context"
	"errors"
	"fmt"
)

const defaultPipelinePageSize = 100

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

	// FetchOptions controls message retrieval. IncludeBody is forced to true
	// because Processor requires the RFC 5322 stream.
	FetchOptions FetchOptions
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

	mailboxes, err := p.Connector.ListMailboxes(ctx)
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

func (p *Pipeline) runMailbox(ctx context.Context, mailbox Mailbox) error {
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
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		page, err := p.Connector.ListChanges(ctx, mailbox, cursor, p.pageSize())
		if err != nil {
			return fmt.Errorf("list changes for mailbox %q: %w", mailboxIdentity(mailbox), err)
		}

		messageErrors := p.processPage(ctx, page, seen)
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
	}
}

func (p *Pipeline) processPage(ctx context.Context, page ChangePage, seen map[string]struct{}) []error {
	var messageErrors []error
	for _, change := range page.Changes {
		if err := ctx.Err(); err != nil {
			return append(messageErrors, err)
		}
		if change.Kind != ChangeUpsert {
			continue
		}

		identity := pipelineChangeIdentity(change.Ref)
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}

		if err := p.processMessage(ctx, change.Ref); err != nil {
			messageErrors = append(messageErrors, err)
		}
	}
	return messageErrors
}

func (p *Pipeline) processMessage(ctx context.Context, ref MessageRef) error {
	options := p.FetchOptions
	options.IncludeBody = true
	raw, err := p.Connector.OpenMessage(ctx, ref, options)
	if err != nil {
		return fmt.Errorf("fetch mailbox message: %w", err)
	}
	if raw.RFC822 == nil {
		return errors.New("fetch mailbox message: connector returned a nil message stream")
	}
	defer raw.RFC822.Close()

	document, err := p.Processor.Process(ctx, raw)
	if err != nil {
		return fmt.Errorf("process mailbox message: %w", err)
	}
	if err := p.Emitter.Emit(ctx, document); err != nil {
		return fmt.Errorf("emit mailbox message: %w", err)
	}
	return nil
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
