package mail

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type sourceRunnerFakeConnector struct {
	mailbox Mailbox
	page    ChangePage
	message RawMessage
}

func (fake *sourceRunnerFakeConnector) ListMailboxes(context.Context) ([]Mailbox, error) {
	return []Mailbox{fake.mailbox}, nil
}

func (fake *sourceRunnerFakeConnector) ListChanges(context.Context, Mailbox, Cursor, int) (ChangePage, error) {
	return fake.page, nil
}

func (fake *sourceRunnerFakeConnector) OpenMessage(context.Context, MessageRef, FetchOptions) (RawMessage, error) {
	return fake.message, nil
}

type sourceRunnerFakeStateStore struct {
	commits int
	next    Checkpoint
}

func (*sourceRunnerFakeStateStore) LoadCheckpoint(context.Context, MailboxKey) (Checkpoint, error) {
	return Checkpoint{}, nil
}

func (fake *sourceRunnerFakeStateStore) CommitCheckpoint(_ context.Context, _ MailboxKey, _ string, next Checkpoint) error {
	fake.commits++
	fake.next = next
	return nil
}

type sourceRunnerFakeProcessor struct {
	document Document
}

func (fake sourceRunnerFakeProcessor) Process(_ context.Context, message RawMessage) (Document, error) {
	if message.RFC822 != nil {
		_ = message.RFC822.Close()
	}
	return fake.document, nil
}

type sourceRunnerRecordingEmitter struct {
	documents []Document
}

func (emitter *sourceRunnerRecordingEmitter) Emit(_ context.Context, document Document) error {
	emitter.documents = append(emitter.documents, document)
	return nil
}

func TestPipelineRunnerUsesInjectedDependencies(t *testing.T) {
	mailbox := Mailbox{ID: "inbox", Name: "INBOX"}
	ref := MessageRef{Provider: "maildir", Mailbox: mailbox, ProviderMessageID: "message-1"}
	connector := &sourceRunnerFakeConnector{
		mailbox: mailbox,
		page: ChangePage{
			Changes: []Change{{Kind: ChangeUpsert, Ref: ref}},
			Next:    Cursor{Token: "next"},
		},
		message: RawMessage{Ref: ref, RFC822: io.NopCloser(strings.NewReader("message"))},
	}
	state := &sourceRunnerFakeStateStore{}
	emitter := &sourceRunnerRecordingEmitter{}
	runner := NewPipelineRunner(PipelineDependencies{
		Connector:  connector,
		StateStore: state,
		Processor:  sourceRunnerFakeProcessor{document: Document{ID: "document-1"}},
	})

	err := runner.RunSource(context.Background(), SourceRunRequest{
		SourceID: "42",
		Config:   sourceRunnerConfig(),
		Emitter:  emitter,
	})
	if err != nil {
		t.Fatalf("RunSource() error = %v", err)
	}
	if len(emitter.documents) != 1 || emitter.documents[0].ID != "document-1" {
		t.Fatalf("emitted documents = %+v", emitter.documents)
	}
	if state.commits != 1 || state.next.Cursor.Token != "next" {
		t.Fatalf("state commits = %d, checkpoint = %+v", state.commits, state.next)
	}
}

func TestPipelineRunnerConstructsConnectorInsideMail(t *testing.T) {
	config := sourceRunnerConfig()
	connector := &sourceRunnerFakeConnector{mailbox: Mailbox{ID: "inbox", Name: "INBOX"}, page: ChangePage{}}
	var factoryConfig SourceConfig
	factoryCalls := 0
	runner := NewPipelineRunner(PipelineDependencies{
		ConnectorFactory: ConnectorFactoryFunc(func(_ context.Context, got SourceConfig) (Connector, error) {
			factoryCalls++
			factoryConfig = got
			return connector, nil
		}),
		StateStore: &sourceRunnerFakeStateStore{},
		Processor:  sourceRunnerFakeProcessor{},
	})

	err := runner.RunSource(context.Background(), SourceRunRequest{
		SourceID: "7",
		Config:   config,
		Emitter:  &sourceRunnerRecordingEmitter{},
	})
	if err != nil {
		t.Fatalf("RunSource() error = %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if factoryConfig.Connector.Provider != config.Connector.Provider {
		t.Errorf("factory provider = %q, want %q", factoryConfig.Connector.Provider, config.Connector.Provider)
	}
}

func TestPipelineRunnerReportsConstructionErrors(t *testing.T) {
	wantErr := errors.New("credentials unavailable")
	runner := NewPipelineRunner(PipelineDependencies{
		ConnectorFactory: ConnectorFactoryFunc(func(context.Context, SourceConfig) (Connector, error) {
			return nil, wantErr
		}),
		StateStore: &sourceRunnerFakeStateStore{},
	})

	err := runner.RunSource(context.Background(), SourceRunRequest{
		SourceID: "7",
		Config:   sourceRunnerConfig(),
		Emitter:  &sourceRunnerRecordingEmitter{},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunSource() error = %v, want %v", err, wantErr)
	}
}

func sourceRunnerConfig() SourceConfig {
	config := DefaultSourceConfig()
	config.Connector.Provider = "maildir"
	config.Connector.Endpoint = "maildir:///tmp/mail"
	return config
}
