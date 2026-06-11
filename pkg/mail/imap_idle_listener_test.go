package mail

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestIMAPIdleListenerStartsPriorityMailboxSessions(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	config := testIMAPConfig()
	config.Mailboxes = MailboxSelector{
		Include: []string{"Archive", "INBOX", "Missing"},
		Exclude: []string{"Spam"},
	}
	listener, err := newIMAPIdleListener(config, factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	mailboxes := []MailboxKey{
		{SourceID: "source", Provider: "imap", AccountID: "account", Mailbox: Mailbox{ID: "INBOX", Name: "Inbox"}},
		{SourceID: "source", Provider: "imap", AccountID: "account", Mailbox: Mailbox{ID: "Spam", Name: "Spam"}},
		{SourceID: "source", Provider: "imap", AccountID: "account", Mailbox: Mailbox{ID: "Archive", Name: "Archive"}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, mailboxes, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()

	clients := factory.awaitClients(t, 2)
	selected := []string{clients[0].awaitSelected(t), clients[1].awaitSelected(t)}
	sort.Strings(selected)
	if want := []string{"Archive", "INBOX"}; !reflect.DeepEqual(selected, want) {
		t.Fatalf("selected mailboxes = %#v, want %#v", selected, want)
	}
	for _, client := range clients {
		client.awaitIdle(t, 1)
		if !reflect.DeepEqual(client.auth, config.Auth) {
			t.Fatalf("Authenticate() auth = %#v, want %#v", client.auth, config.Auth)
		}
	}

	cancel()
	if err := awaitValue(t, done, "IMAP IDLE listener cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen() error = %v, want context.Canceled", err)
	}
	for _, client := range clients {
		client.awaitClosed(t)
		if client.logoutCalls != 1 || client.closeCalls != 1 {
			t.Fatalf("shutdown calls = logout %d, close %d; want 1 each", client.logoutCalls, client.closeCalls)
		}
	}
}

func TestIMAPIdleListenerLeavesIdleEnqueuesAndResumes(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	config := testIMAPConfig()
	config.Mailboxes = MailboxSelector{Include: []string{"INBOX"}}
	listener, err := newIMAPIdleListener(config, factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	mailbox := MailboxKey{SourceID: "source", Provider: "imap", AccountID: "account", Mailbox: Mailbox{ID: "INBOX"}}

	notified := make(chan MailboxKey, 1)
	var client *fakeIMAPIdleClient
	sink := idleEventSinkFunc(func(_ context.Context, got MailboxKey) error {
		if client.stoppedCount() != 1 {
			t.Fatalf("Notify() called before the first IDLE session stopped")
		}
		notified <- got
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Listen(ctx, []MailboxKey{mailbox}, sink) }()

	client = factory.awaitClients(t, 1)[0]
	client.awaitSelected(t)
	first := client.awaitIdle(t, 1)
	first.change <- struct{}{}
	if got := awaitValue(t, notified, "reconciliation hint"); got != mailbox {
		t.Fatalf("Notify() mailbox = %#v, want %#v", got, mailbox)
	}
	client.awaitIdle(t, 2)

	cancel()
	if err := awaitValue(t, done, "IMAP IDLE listener shutdown"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen() error = %v, want context.Canceled", err)
	}
}

func TestIMAPIdleListenerCancellationInterruptsIdle(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	listener, err := newIMAPIdleListener(testIMAPConfig(), factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, []MailboxKey{{Mailbox: Mailbox{ID: "INBOX"}}}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()

	client := factory.awaitClients(t, 1)[0]
	client.awaitIdle(t, 1)
	cancel()
	if err := awaitValue(t, done, "cancelled IDLE call"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen() error = %v, want context.Canceled", err)
	}
	if client.stoppedCount() != 1 {
		t.Fatalf("stopped IDLE sessions = %d, want 1", client.stoppedCount())
	}
	client.awaitClosed(t)
}

func TestIMAPIdleListenerShutdownsAllSessionsWhenOneFails(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	config := testIMAPConfig()
	config.Mailboxes = MailboxSelector{Include: []string{"INBOX", "Archive"}}
	listener, err := newIMAPIdleListener(config, factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(context.Background(), []MailboxKey{
			{Mailbox: Mailbox{ID: "INBOX"}},
			{Mailbox: Mailbox{ID: "Archive"}},
		}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()

	clients := factory.awaitClients(t, 2)
	for _, client := range clients {
		client.awaitIdle(t, 1)
	}
	sentinel := errors.New("server disconnected")
	clients[0].fail <- sentinel
	if err := awaitValue(t, done, "listener failure shutdown"); !errors.Is(err, sentinel) {
		t.Fatalf("Listen() error = %v, want sentinel", err)
	}
	for _, client := range clients {
		client.awaitClosed(t)
	}
	if clients[1].stoppedCount() != 1 {
		t.Fatalf("peer stopped IDLE sessions = %d, want 1", clients[1].stoppedCount())
	}
}

func TestIMAPIdleListenerRejectsOverlappingListenCalls(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	listener, err := newIMAPIdleListener(testIMAPConfig(), factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, []MailboxKey{{Mailbox: Mailbox{ID: "INBOX"}}}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()
	factory.awaitClients(t, 1)[0].awaitIdle(t, 1)

	err = listener.Listen(context.Background(), []MailboxKey{{Mailbox: Mailbox{ID: "Archive"}}}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	if !errors.Is(err, ErrIMAPIdleListenerRunning) {
		t.Fatalf("overlapping Listen() error = %v, want ErrIMAPIdleListenerRunning", err)
	}
	cancel()
	awaitValue(t, done, "first listener shutdown")
}

type idleEventSinkFunc func(context.Context, MailboxKey) error

func (f idleEventSinkFunc) Notify(ctx context.Context, mailbox MailboxKey) error {
	return f(ctx, mailbox)
}

type fakeIMAPIdleFactory struct {
	mu      sync.Mutex
	clients []*fakeIMAPIdleClient
	created chan *fakeIMAPIdleClient
}

func newFakeIMAPIdleFactory() *fakeIMAPIdleFactory {
	return &fakeIMAPIdleFactory{created: make(chan *fakeIMAPIdleClient, 8)}
}

func (f *fakeIMAPIdleFactory) newClient(context.Context, IMAPConnectorConfig) (imapIdleClient, error) {
	client := &fakeIMAPIdleClient{
		selected: make(chan string, 1),
		started:  make(chan fakeIdleSession, 8),
		fail:     make(chan error, 1),
		closed:   make(chan struct{}),
	}
	f.mu.Lock()
	f.clients = append(f.clients, client)
	f.mu.Unlock()
	f.created <- client
	return client, nil
}

func (f *fakeIMAPIdleFactory) awaitClients(t *testing.T, count int) []*fakeIMAPIdleClient {
	t.Helper()
	for {
		f.mu.Lock()
		if len(f.clients) >= count {
			clients := append([]*fakeIMAPIdleClient(nil), f.clients[:count]...)
			f.mu.Unlock()
			return clients
		}
		f.mu.Unlock()
		awaitValue(t, f.created, "IMAP IDLE client creation")
	}
}

type fakeIdleSession struct {
	call   int
	change chan struct{}
}

type fakeIMAPIdleClient struct {
	mu          sync.Mutex
	auth        IMAPAuth
	selected    chan string
	started     chan fakeIdleSession
	fail        chan error
	closed      chan struct{}
	idleCalls   int
	stopped     int
	logoutCalls int
	closeCalls  int
}

func (f *fakeIMAPIdleClient) Authenticate(_ context.Context, auth IMAPAuth) error {
	f.mu.Lock()
	f.auth = auth
	f.mu.Unlock()
	return nil
}

func (f *fakeIMAPIdleClient) SelectMailbox(_ context.Context, mailbox string) (imapMailboxStatus, error) {
	f.selected <- mailbox
	return imapMailboxStatus{UIDValidity: 1}, nil
}

func (f *fakeIMAPIdleClient) Idle(ctx context.Context, stop <-chan struct{}, changes chan<- struct{}) error {
	f.mu.Lock()
	f.idleCalls++
	call := f.idleCalls
	f.mu.Unlock()
	change := make(chan struct{}, 1)
	f.started <- fakeIdleSession{call: call, change: change}
	select {
	case <-ctx.Done():
		f.recordStopped()
		return ctx.Err()
	case <-stop:
		f.recordStopped()
		return nil
	case err := <-f.fail:
		return err
	case <-change:
		changes <- struct{}{}
		<-stop
		f.recordStopped()
		return nil
	}
}

func (f *fakeIMAPIdleClient) Logout(context.Context) error {
	f.mu.Lock()
	f.logoutCalls++
	f.mu.Unlock()
	return nil
}

func (f *fakeIMAPIdleClient) Close() error {
	f.mu.Lock()
	f.closeCalls++
	if f.closeCalls == 1 {
		close(f.closed)
	}
	f.mu.Unlock()
	return nil
}

func (f *fakeIMAPIdleClient) recordStopped() {
	f.mu.Lock()
	f.stopped++
	f.mu.Unlock()
}

func (f *fakeIMAPIdleClient) stoppedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

func (f *fakeIMAPIdleClient) awaitSelected(t *testing.T) string {
	return awaitValue(t, f.selected, "mailbox selection")
}

func (f *fakeIMAPIdleClient) awaitIdle(t *testing.T, call int) fakeIdleSession {
	t.Helper()
	for {
		session := awaitValue(t, f.started, "IDLE session start")
		if session.call == call {
			return session
		}
	}
}

func (f *fakeIMAPIdleClient) awaitClosed(t *testing.T) {
	awaitSignal(t, f.closed, "IMAP IDLE client close")
}
