package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestPipelineRunSuccess(t *testing.T) {
	mailbox := Mailbox{ID: "inbox", Name: "Inbox"}
	first := pipelineTestRef(mailbox, "first")
	second := pipelineTestRef(mailbox, "second")
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {
				Changes: []Change{{Kind: ChangeUpsert, Ref: first}},
				Next:    Cursor{Token: "page-1"},
				More:    true,
			},
			{Token: "page-1"}: {
				Changes: []Change{{Kind: ChangeUpsert, Ref: second}},
				Next:    Cursor{Token: "page-2"},
			},
		},
	}
	store := NewMemoryStateStore()
	processor := &pipelineFakeProcessor{}
	emitter := &pipelineFakeEmitter{}
	pipeline := NewPipeline(connector, store, processor, emitter)
	pipeline.SourceID = "source"
	pipeline.Provider = "fake"
	pipeline.AccountID = "account"
	pipeline.PageSize = 25
	pipeline.FetchOptions.MaxBytes = 4096

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := connector.openedIDs(), []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("opened message IDs = %v, want %v", got, want)
	}
	if got, want := processor.processedIDs(), []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("processed message IDs = %v, want %v", got, want)
	}
	if got, want := emitter.emittedIDs(), []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("emitted document IDs = %v, want %v", got, want)
	}
	if connector.lastLimit != 25 {
		t.Fatalf("ListChanges() limit = %d, want 25", connector.lastLimit)
	}
	if !connector.lastOptions.IncludeBody || connector.lastOptions.MaxBytes != 4096 {
		t.Fatalf("OpenMessage() options = %#v, want body and 4096-byte limit", connector.lastOptions)
	}
	for id, stream := range connector.streams {
		if !stream.closed {
			t.Errorf("message %q stream was not closed", id)
		}
	}

	checkpoint, err := store.LoadCheckpoint(context.Background(), MailboxKey{
		SourceID: "source", Provider: "fake", AccountID: "account", Mailbox: mailbox,
	})
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if checkpoint.Cursor.Token != "page-2" || checkpoint.ErrorCount != 0 || checkpoint.LastError != "" {
		t.Fatalf("checkpoint = %#v, want successful page-2 checkpoint", checkpoint)
	}
}

func TestPipelineResumesFromStoredCheckpoint(t *testing.T) {
	mailbox := Mailbox{ID: "inbox"}
	stored := Cursor{UIDValidity: 7, UID: 40}
	next := Cursor{UIDValidity: 7, UID: 42}
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			stored: {
				Changes: []Change{
					{Kind: ChangeUpsert, Ref: pipelineIMAPTestRef(mailbox, 7, 41)},
					{Kind: ChangeUpsert, Ref: pipelineIMAPTestRef(mailbox, 7, 42)},
				},
				Next: next,
			},
		},
	}
	store := NewMemoryStateStore()
	key := MailboxKey{Mailbox: mailbox}
	if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{Cursor: stored}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	pipeline := NewPipeline(connector, store, &pipelineFakeProcessor{}, &pipelineFakeEmitter{})

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := connector.listedCursors(), []Cursor{stored}; !equalCursors(got, want) {
		t.Fatalf("ListChanges() cursors = %#v, want %#v", got, want)
	}
	if got, want := connector.openedIDs(), []string{"uid-41", "uid-42"}; !equalStrings(got, want) {
		t.Fatalf("opened message IDs = %v, want %v", got, want)
	}
	checkpoint, err := store.LoadCheckpoint(context.Background(), key)
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if checkpoint.Cursor != next {
		t.Fatalf("checkpoint cursor = %#v, want %#v", checkpoint.Cursor, next)
	}
}

func TestPipelineUIDValidityResetUsesBoundedResumableRescan(t *testing.T) {
	mailbox := Mailbox{ID: "inbox"}
	stored := Cursor{UIDValidity: 7, UID: 200}
	resetStart := Cursor{UIDValidity: 8}
	rescanNext := Cursor{UIDValidity: 8, UID: 2}
	caughtUp := Cursor{UIDValidity: 8, UID: 3}
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			stored: {
				// A connector can expose the new UIDVALIDITY through ordinary
				// listed metadata even when it does not emit ChangeReset.
				Changes: []Change{{Kind: ChangeUpsert, Ref: pipelineIMAPTestRef(mailbox, 8, 3)}},
				Next:    caughtUp,
			},
			resetStart: {
				Changes: []Change{
					{Kind: ChangeUpsert, Ref: pipelineIMAPTestRef(mailbox, 8, 1)},
					{Kind: ChangeUpsert, Ref: pipelineIMAPTestRef(mailbox, 8, 2)},
				},
				Next: rescanNext,
				More: true,
			},
			rescanNext: {
				Changes: []Change{{Kind: ChangeUpsert, Ref: pipelineIMAPTestRef(mailbox, 8, 3)}},
				Next:    caughtUp,
			},
		},
	}
	store := NewMemoryStateStore()
	key := MailboxKey{Mailbox: mailbox}
	if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{Cursor: stored}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	processor := &pipelineFakeProcessor{}
	emitter := &pipelineFakeEmitter{}
	pipeline := NewPipeline(connector, store, processor, emitter)
	pipeline.PageSize = 10
	pipeline.UIDValidityRescanWindow = 2

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if got, want := connector.listedCursors(), []Cursor{stored, resetStart}; !equalCursors(got, want) {
		t.Fatalf("first ListChanges() cursors = %#v, want %#v", got, want)
	}
	if got, want := connector.listedLimits(), []int{10, 2}; !equalInts(got, want) {
		t.Fatalf("first ListChanges() limits = %v, want %v", got, want)
	}
	if got, want := processor.processedIDs(), []string{"uid-1", "uid-2"}; !equalStrings(got, want) {
		t.Fatalf("first processed message IDs = %v, want %v", got, want)
	}
	checkpoint, err := store.LoadCheckpoint(context.Background(), key)
	if err != nil {
		t.Fatalf("LoadCheckpoint() after first run error = %v", err)
	}
	if checkpoint.Cursor != rescanNext {
		t.Fatalf("checkpoint after bounded rescan = %#v, want %#v", checkpoint.Cursor, rescanNext)
	}

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got, want := processor.processedIDs(), []string{"uid-1", "uid-2", "uid-3"}; !equalStrings(got, want) {
		t.Fatalf("processed message IDs after resume = %v, want %v", got, want)
	}
	checkpoint, err = store.LoadCheckpoint(context.Background(), key)
	if err != nil {
		t.Fatalf("LoadCheckpoint() after resume error = %v", err)
	}
	if checkpoint.Cursor != caughtUp {
		t.Fatalf("checkpoint after resume = %#v, want %#v", checkpoint.Cursor, caughtUp)
	}
}

func TestPipelineEmptyMailboxCommitsProviderCursor(t *testing.T) {
	mailbox := Mailbox{ID: "empty"}
	current := Cursor{UIDValidity: 12}
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {Next: current},
		},
	}
	store := NewMemoryStateStore()
	pipeline := NewPipeline(connector, store, &pipelineFakeProcessor{}, &pipelineFakeEmitter{})

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := connector.openedIDs(); len(got) != 0 {
		t.Fatalf("opened message IDs = %v, want none", got)
	}
	checkpoint, err := store.LoadCheckpoint(context.Background(), MailboxKey{Mailbox: mailbox})
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if checkpoint.Cursor != current {
		t.Fatalf("checkpoint cursor = %#v, want %#v", checkpoint.Cursor, current)
	}
}

func TestPipelineSkipsDuplicateChanges(t *testing.T) {
	mailbox := Mailbox{ID: "inbox"}
	ref := pipelineTestRef(mailbox, "same")
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {
				Changes: []Change{
					{Kind: ChangeUpsert, Ref: ref},
					{Kind: ChangeUpsert, Ref: ref},
				},
				Next: Cursor{Token: "done"},
			},
			{Token: "done"}: {Next: Cursor{Token: "done"}},
		},
	}
	processor := &pipelineFakeProcessor{}
	emitter := &pipelineFakeEmitter{}
	pipeline := NewPipeline(connector, NewMemoryStateStore(), processor, emitter)

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if got := len(connector.openedIDs()); got != 1 {
		t.Fatalf("OpenMessage() calls = %d, want 1", got)
	}
	if got := len(processor.processedIDs()); got != 1 {
		t.Fatalf("Process() calls = %d, want 1", got)
	}
	if got := len(emitter.emittedIDs()); got != 1 {
		t.Fatalf("Emit() calls = %d, want 1", got)
	}
}

func TestPipelineStopsOnCancellation(t *testing.T) {
	mailbox := Mailbox{ID: "inbox"}
	first := pipelineTestRef(mailbox, "first")
	second := pipelineTestRef(mailbox, "second")
	ctx, cancel := context.WithCancel(context.Background())
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {
				Changes: []Change{{Kind: ChangeUpsert, Ref: first}, {Kind: ChangeUpsert, Ref: second}},
				Next:    Cursor{Token: "done"},
			},
		},
	}
	processor := &pipelineFakeProcessor{afterProcess: cancel}
	pipeline := NewPipeline(connector, NewMemoryStateStore(), processor, &pipelineFakeEmitter{})

	err := pipeline.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if got, want := connector.openedIDs(), []string{"first"}; !equalStrings(got, want) {
		t.Fatalf("opened message IDs = %v, want %v", got, want)
	}
	if stream := connector.streams["first"]; stream == nil || !stream.closed {
		t.Fatal("first message stream was not closed after cancellation")
	}
	if len(connector.streams) != 1 {
		t.Fatalf("opened streams = %d, want 1", len(connector.streams))
	}
}

func TestPipelineIsolatesMixedMessageFailures(t *testing.T) {
	mailbox := Mailbox{ID: "inbox"}
	fetchFailure := pipelineTestRef(mailbox, "fetch-failure")
	processFailure := pipelineTestRef(mailbox, "process-failure")
	emitFailure := pipelineTestRef(mailbox, "emit-failure")
	success := pipelineTestRef(mailbox, "success")
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {
				Changes: []Change{
					{Kind: ChangeUpsert, Ref: fetchFailure},
					{Kind: ChangeUpsert, Ref: processFailure},
					{Kind: ChangeUpsert, Ref: emitFailure},
					{Kind: ChangeUpsert, Ref: success},
				},
				Next: Cursor{Token: "not-safe-to-commit"},
			},
		},
		openErrors: map[string]error{"fetch-failure": errors.New("fetch broke")},
	}
	processor := &pipelineFakeProcessor{failures: map[string]error{"process-failure": errors.New("parse broke")}}
	emitter := &pipelineFakeEmitter{failures: map[string]error{"emit-failure": errors.New("sink broke")}}
	store := NewMemoryStateStore()
	pipeline := NewPipeline(connector, store, processor, emitter)

	err := pipeline.Run(context.Background())
	for _, want := range []string{"fetch broke", "parse broke", "sink broke"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Run() error = %v, want it to contain %q", err, want)
		}
	}
	if got, want := connector.openedIDs(), []string{"fetch-failure", "process-failure", "emit-failure", "success"}; !equalStrings(got, want) {
		t.Fatalf("opened message IDs = %v, want %v", got, want)
	}
	if got, want := emitter.emittedIDs(), []string{"emit-failure", "success"}; !equalStrings(got, want) {
		t.Fatalf("emitted document IDs = %v, want %v", got, want)
	}
	checkpoint, loadErr := store.LoadCheckpoint(context.Background(), MailboxKey{Mailbox: mailbox})
	if loadErr != nil {
		t.Fatalf("LoadCheckpoint() error = %v", loadErr)
	}
	if checkpoint.Cursor != (Cursor{}) {
		t.Fatalf("checkpoint cursor = %#v, want original cursor after partial failure", checkpoint.Cursor)
	}
	if checkpoint.ErrorCount != 1 || checkpoint.LastError == "" {
		t.Fatalf("checkpoint failure state = %#v, want one recorded page failure", checkpoint)
	}
	for _, id := range []string{"process-failure", "emit-failure", "success"} {
		if stream := connector.streams[id]; stream == nil || !stream.closed {
			t.Errorf("message %q stream was not closed", id)
		}
	}
}

type pipelineFakeConnector struct {
	mailboxes  []Mailbox
	pages      map[Cursor]ChangePage
	openErrors map[string]error

	mu          sync.Mutex
	opened      []string
	streams     map[string]*pipelineFakeReadCloser
	cursors     []Cursor
	limits      []int
	lastLimit   int
	lastOptions FetchOptions
}

func (f *pipelineFakeConnector) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	return f.mailboxes, ctx.Err()
}

func (f *pipelineFakeConnector) ListChanges(ctx context.Context, _ Mailbox, cursor Cursor, limit int) (ChangePage, error) {
	f.mu.Lock()
	f.cursors = append(f.cursors, cursor)
	f.limits = append(f.limits, limit)
	f.lastLimit = limit
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ChangePage{}, err
	}
	return f.pages[cursor], nil
}

func (f *pipelineFakeConnector) OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error) {
	id := ref.ProviderMessageID
	f.mu.Lock()
	f.opened = append(f.opened, id)
	f.lastOptions = options
	failure := f.openErrors[id]
	if failure == nil {
		if f.streams == nil {
			f.streams = make(map[string]*pipelineFakeReadCloser)
		}
		stream := &pipelineFakeReadCloser{Reader: strings.NewReader("message " + id)}
		f.streams[id] = stream
		f.mu.Unlock()
		return RawMessage{Ref: ref, RFC822: stream}, ctx.Err()
	}
	f.mu.Unlock()
	return RawMessage{}, failure
}

func (f *pipelineFakeConnector) openedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

func (f *pipelineFakeConnector) listedCursors() []Cursor {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Cursor(nil), f.cursors...)
}

func (f *pipelineFakeConnector) listedLimits() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.limits...)
}

type pipelineFakeReadCloser struct {
	io.Reader
	closed bool
}

func (f *pipelineFakeReadCloser) Close() error {
	f.closed = true
	return nil
}

type pipelineFakeProcessor struct {
	mu           sync.Mutex
	processed    []string
	failures     map[string]error
	afterProcess func()
}

func (f *pipelineFakeProcessor) Process(_ context.Context, message RawMessage) (Document, error) {
	id := message.Ref.ProviderMessageID
	f.mu.Lock()
	f.processed = append(f.processed, id)
	failure := f.failures[id]
	afterProcess := f.afterProcess
	f.mu.Unlock()
	if afterProcess != nil {
		afterProcess()
	}
	if failure != nil {
		return Document{}, failure
	}
	return Document{ID: id, Ref: message.Ref}, nil
}

func (f *pipelineFakeProcessor) processedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.processed...)
}

type pipelineFakeEmitter struct {
	mu       sync.Mutex
	emitted  []string
	failures map[string]error
}

func (f *pipelineFakeEmitter) Emit(_ context.Context, document Document) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, document.ID)
	return f.failures[document.ID]
}

func (f *pipelineFakeEmitter) emittedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.emitted...)
}

func pipelineIMAPTestRef(mailbox Mailbox, uidValidity, uid uint32) MessageRef {
	return MessageRef{
		Provider:          "imap",
		AccountID:         "account",
		Mailbox:           mailbox,
		UID:               uid,
		UIDValidity:       uidValidity,
		ProviderMessageID: fmt.Sprintf("uid-%d", uid),
		Version:           "1",
	}
}

func pipelineTestRef(mailbox Mailbox, id string) MessageRef {
	return MessageRef{
		Provider:          "fake",
		AccountID:         "account",
		Mailbox:           mailbox,
		ProviderMessageID: id,
		Version:           "1",
	}
}

func equalCursors(left, right []Cursor) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
