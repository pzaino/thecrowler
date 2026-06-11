package mail

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
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

func TestIMAPIdleListenerReconnectsAfterDisconnectWithoutOverlappingSessions(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	config := testIMAPConfig()
	config.ReconnectBackoff = time.Millisecond
	config.MaxReconnectBackoff = 4 * time.Millisecond
	listener, err := newIMAPIdleListener(config, factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, []MailboxKey{{Mailbox: Mailbox{ID: "INBOX"}}}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()

	first := factory.awaitClients(t, 1)[0]
	first.awaitIdle(t, 1)
	first.fail <- errors.New("server disconnected")
	first.awaitClosed(t)

	second := factory.awaitClients(t, 2)[1]
	second.awaitIdle(t, 1)
	if got := factory.maximumOpen(); got != 1 {
		t.Fatalf("maximum simultaneously open clients = %d, want 1", got)
	}
	status := listener.Status()
	if status.Degraded || status.ActiveSessions != 1 || status.ReconnectCount != 1 || status.LastError != "" {
		t.Fatalf("recovered status = %#v, want one healthy session after one reconnect", status)
	}

	cancel()
	if err := awaitValue(t, done, "listener cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen() error = %v, want context.Canceled", err)
	}
}

func TestIMAPIdleListenerAuthenticationFailureIsDegradedAndCancellationInterruptsBackoff(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	factory.authErrors = []error{errors.New("credentials rejected")}
	config := testIMAPConfig()
	config.ReconnectBackoff = time.Hour
	config.MaxReconnectBackoff = time.Hour
	listener, err := newIMAPIdleListener(config, factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, []MailboxKey{{Mailbox: Mailbox{ID: "INBOX"}}}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()

	client := factory.awaitClients(t, 1)[0]
	client.awaitClosed(t)
	status := awaitIMAPIdleStatus(t, listener, func(status IMAPIdleListenerStatus) bool {
		return status.Running && status.Degraded && status.ActiveSessions == 0 && status.ReconnectCount == 1 && status.LastError != ""
	})
	if status.LastError == "" {
		t.Fatalf("authentication failure status = %#v, want a categorized error", status)
	}

	cancel()
	if err := awaitValue(t, done, "cancelled reconnect wait"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen() error = %v, want context.Canceled", err)
	}
	if got := factory.clientCount(); got != 1 {
		t.Fatalf("created clients = %d, want 1", got)
	}
}

func TestIMAPIdleListenerRecoversAfterAuthenticationFailure(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	factory.authErrors = []error{errors.New("temporary authentication failure")}
	config := testIMAPConfig()
	config.ReconnectBackoff = time.Millisecond
	config.MaxReconnectBackoff = 2 * time.Millisecond
	listener, err := newIMAPIdleListener(config, factory.newClient)
	if err != nil {
		t.Fatalf("newIMAPIdleListener() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, []MailboxKey{{Mailbox: Mailbox{ID: "INBOX"}}}, idleEventSinkFunc(func(context.Context, MailboxKey) error { return nil }))
	}()

	factory.awaitClients(t, 1)[0].awaitClosed(t)
	recovered := factory.awaitClients(t, 2)[1]
	recovered.awaitIdle(t, 1)
	status := listener.Status()
	if status.Degraded || status.ActiveSessions != 1 || status.LastError != "" {
		t.Fatalf("status after authentication recovery = %#v, want healthy", status)
	}

	cancel()
	awaitValue(t, done, "listener shutdown after recovery")
}

func TestIMAPIdleListenerPeriodicallyReissuesIdle(t *testing.T) {
	factory := newFakeIMAPIdleFactory()
	config := testIMAPConfig()
	config.IdleReissueInterval = 10 * time.Millisecond
	listener, err := newIMAPIdleListener(config, factory.newClient)
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
	client.awaitIdle(t, 2)
	if client.stoppedCount() != 1 {
		t.Fatalf("stopped IDLE sessions after timer = %d, want 1", client.stoppedCount())
	}
	status := listener.Status()
	if status.Degraded || status.ActiveSessions != 1 || status.ReconnectCount != 0 {
		t.Fatalf("status after IDLE reissue = %#v, want one healthy connection without reconnect", status)
	}

	cancel()
	awaitValue(t, done, "listener shutdown after IDLE reissue")
}

func TestNextIMAPIdleBackoffIsBounded(t *testing.T) {
	maximum := 8 * time.Second
	for _, test := range []struct {
		current time.Duration
		want    time.Duration
	}{
		{current: time.Second, want: 2 * time.Second},
		{current: 4 * time.Second, want: 8 * time.Second},
		{current: 7 * time.Second, want: 8 * time.Second},
		{current: 8 * time.Second, want: 8 * time.Second},
	} {
		if got := nextIMAPIdleBackoff(test.current, maximum); got != test.want {
			t.Errorf("nextIMAPIdleBackoff(%s, %s) = %s, want %s", test.current, maximum, got, test.want)
		}
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

func awaitIMAPIdleStatus(t *testing.T, listener *IMAPIdleListener, ready func(IMAPIdleListenerStatus) bool) IMAPIdleListenerStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		status := listener.Status()
		if ready(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener status did not reach expected state; last status = %#v", status)
		}
		time.Sleep(time.Millisecond)
	}
}

type idleEventSinkFunc func(context.Context, MailboxKey) error

func (f idleEventSinkFunc) Notify(ctx context.Context, mailbox MailboxKey) error {
	return f(ctx, mailbox)
}

type fakeIMAPIdleFactory struct {
	mu         sync.Mutex
	clients    []*fakeIMAPIdleClient
	created    chan *fakeIMAPIdleClient
	authErrors []error
	open       int
	maxOpen    int
}

func newFakeIMAPIdleFactory() *fakeIMAPIdleFactory {
	return &fakeIMAPIdleFactory{created: make(chan *fakeIMAPIdleClient, 8)}
}

func (f *fakeIMAPIdleFactory) newClient(context.Context, IMAPConnectorConfig) (imapIdleClient, error) {
	f.mu.Lock()
	var authErr error
	if len(f.authErrors) != 0 {
		authErr = f.authErrors[0]
		f.authErrors = f.authErrors[1:]
	}
	f.open++
	if f.open > f.maxOpen {
		f.maxOpen = f.open
	}
	client := &fakeIMAPIdleClient{
		selected: make(chan string, 1),
		started:  make(chan fakeIdleSession, 8),
		fail:     make(chan error, 1),
		closed:   make(chan struct{}),
		authErr:  authErr,
		onClose: func() {
			f.mu.Lock()
			f.open--
			f.mu.Unlock()
		},
	}
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

func (f *fakeIMAPIdleFactory) clientCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

func (f *fakeIMAPIdleFactory) maximumOpen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxOpen
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
	authErr     error
	onClose     func()
}

func (f *fakeIMAPIdleClient) Authenticate(_ context.Context, auth IMAPAuth) error {
	f.mu.Lock()
	f.auth = auth
	err := f.authErr
	f.mu.Unlock()
	return err
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
		if f.onClose != nil {
			f.onClose()
		}
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
