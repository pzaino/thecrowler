package mail

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestIMAPConnectorImplementsInterfaces(t *testing.T) {
	var connector any = &IMAPConnector{}
	if _, ok := connector.(Connector); !ok {
		t.Fatal("IMAPConnector does not implement Connector")
	}
	if _, ok := connector.(io.Closer); !ok {
		t.Fatal("IMAPConnector does not implement io.Closer")
	}
}

func TestNewIMAPConnectorAuthenticatesPasswordAndToken(t *testing.T) {
	tests := []struct {
		name string
		auth IMAPAuth
	}{
		{name: "password", auth: IMAPAuth{Username: "reader", Password: "secret"}},
		{name: "token placeholder", auth: IMAPAuth{Username: "reader", Token: "access-token"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeIMAPClient{}
			connector, err := newIMAPConnector(context.Background(), IMAPConnectorConfig{
				Host: "mail.example.test", Auth: test.auth,
			}, func(_ context.Context, config IMAPConnectorConfig) (imapClient, error) {
				if config.Port != 993 || config.TLSPolicy != IMAPTLSImplicit {
					t.Fatalf("normalized transport = (%d, %q), want (993, implicit)", config.Port, config.TLSPolicy)
				}
				return fake, nil
			})
			if err != nil {
				t.Fatalf("newIMAPConnector() error = %v", err)
			}
			defer connector.Close() //nolint:errcheck
			if !reflect.DeepEqual(fake.auth, test.auth) {
				t.Fatalf("authenticated with %#v, want %#v", fake.auth, test.auth)
			}
		})
	}
}

func TestNewIMAPConnectorClosesClientWhenAuthenticationFails(t *testing.T) {
	fake := &fakeIMAPClient{authenticateErr: errors.New("denied")}
	_, err := newIMAPConnector(context.Background(), testIMAPConfig(), func(context.Context, IMAPConnectorConfig) (imapClient, error) {
		return fake, nil
	})
	if err == nil {
		t.Fatal("newIMAPConnector() error = nil, want authentication error")
	}
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorAuthentication {
		t.Fatalf("error = %T %v, want ErrorAuthentication", err, err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", fake.closeCalls)
	}
}

func TestIMAPConnectorListsFiltersAndSelectsMailboxes(t *testing.T) {
	fake := &fakeIMAPClient{
		mailboxes: []imapMailbox{
			{Name: "Trash", Selectable: true},
			{Name: "Archive", Selectable: true},
			{Name: "Folders", Selectable: false},
			{Name: "INBOX", Selectable: true},
		},
		statuses: map[string]imapMailboxStatus{"INBOX": {UIDValidity: 77}},
	}
	config := testIMAPConfig()
	config.Mailboxes = MailboxSelector{Include: []string{"INBOX", "Archive", "Trash"}, Exclude: []string{"Trash"}}
	connector := authenticatedTestIMAPConnector(t, config, fake)

	mailboxes, err := connector.ListMailboxes(context.Background())
	if err != nil {
		t.Fatalf("ListMailboxes() error = %v", err)
	}
	want := []Mailbox{{ID: "Archive", Name: "Archive"}, {ID: "INBOX", Name: "INBOX"}}
	if !reflect.DeepEqual(mailboxes, want) {
		t.Fatalf("ListMailboxes() = %#v, want %#v", mailboxes, want)
	}
	uidValidity, err := connector.SelectMailbox(context.Background(), Mailbox{ID: "INBOX"})
	if err != nil {
		t.Fatalf("SelectMailbox() error = %v", err)
	}
	if uidValidity != 77 || fake.selected != "INBOX" {
		t.Fatalf("selection = (%q, %d), want (INBOX, 77)", fake.selected, uidValidity)
	}
}

func TestIMAPConnectorListChangesUsesUIDsAndHandlesReset(t *testing.T) {
	fake := &fakeIMAPClient{
		statuses: map[string]imapMailboxStatus{"INBOX": {UIDValidity: 9}},
		uids:     []uint32{13, 11, 12},
		messages: map[uint32]imapMessage{
			11: {UID: 11, Size: 101},
			12: {UID: 12, Size: 102},
			13: {UID: 13, Size: 103},
		},
	}
	config := testIMAPConfig()
	config.AccountID = "account-1"
	connector := authenticatedTestIMAPConnector(t, config, fake)
	mailbox := Mailbox{ID: "INBOX", Name: "Inbox"}

	page, err := connector.ListChanges(context.Background(), mailbox, Cursor{UID: 10, UIDValidity: 9}, 2)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(page.Changes) != 2 || page.Changes[0].Ref.UID != 11 || page.Changes[1].Ref.UID != 12 {
		t.Fatalf("changes = %#v, want UIDs 11 and 12", page.Changes)
	}
	if !page.More || page.Next != (Cursor{UID: 12, UIDValidity: 9}) {
		t.Fatalf("page continuation = (%#v, %v), want UID 12 and more", page.Next, page.More)
	}
	if page.Changes[0].Ref.Provider != "imap" || page.Changes[0].Ref.AccountID != "account-1" {
		t.Fatalf("message ref = %#v, want provider/account identity", page.Changes[0].Ref)
	}
	if fake.searchFirst != 11 {
		t.Fatalf("SearchUIDs first = %d, want 11", fake.searchFirst)
	}

	reset, err := connector.ListChanges(context.Background(), mailbox, Cursor{UID: 100, UIDValidity: 8}, 10)
	if err != nil {
		t.Fatalf("reset ListChanges() error = %v", err)
	}
	if len(reset.Changes) != 1 || reset.Changes[0].Kind != ChangeReset || reset.Next.UIDValidity != 9 {
		t.Fatalf("reset page = %#v, want UIDVALIDITY reset to 9", reset)
	}
}

func TestIMAPConnectorOpenMessageAndSizeLimit(t *testing.T) {
	fake := &fakeIMAPClient{
		statuses: map[string]imapMailboxStatus{"INBOX": {UIDValidity: 9}},
		messages: map[uint32]imapMessage{42: {UID: 42, Size: 5}},
		bodies:   map[uint32][]byte{42: []byte("hello")},
	}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)
	ref := MessageRef{Mailbox: Mailbox{ID: "INBOX"}, UID: 42, UIDValidity: 9}

	raw, err := connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true, MaxBytes: 5})
	if err != nil {
		t.Fatalf("OpenMessage() error = %v", err)
	}
	defer raw.RFC822.Close() //nolint:errcheck
	data, err := io.ReadAll(raw.RFC822)
	if err != nil || string(data) != "hello" {
		t.Fatalf("message body = %q, %v; want hello", data, err)
	}
	if raw.Ref.UID != 42 || raw.Ref.UIDValidity != 9 {
		t.Fatalf("message ref = %#v, want UID tuple 9/42", raw.Ref)
	}

	_, err = connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true, MaxBytes: 4})
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized {
		t.Fatalf("oversize error = %T %v, want ErrorOversized", err, err)
	}
}

func TestIMAPConnectorOperationHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	fake := &fakeIMAPClient{listStarted: started, blockList: true}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := connector.ListMailboxes(ctx)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListMailboxes() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListMailboxes() did not return promptly after cancellation")
	}
}

func TestIMAPConnectorCloseIsCleanAndIdempotent(t *testing.T) {
	fake := &fakeIMAPClient{}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)
	if err := connector.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := connector.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if fake.logoutCalls != 1 || fake.closeCalls != 1 {
		t.Fatalf("shutdown calls = logout %d, close %d; want 1 each", fake.logoutCalls, fake.closeCalls)
	}
	if _, err := connector.ListMailboxes(context.Background()); err == nil {
		t.Fatal("operation after Close() error = nil")
	}
}

func TestIMAPConnectorConfigFromSource(t *testing.T) {
	config, err := IMAPConnectorConfigFromSource(SourceConfig{
		Connector: ConnectorConfig{
			Provider: "IMAP", Endpoint: "imaps://mail.example.test:1993", Timeout: time.Minute,
			TLS: TLSConfig{ServerName: "imap.example.test"},
		},
		Auth:      AuthConfig{Identity: "account-1"},
		Mailboxes: MailboxConfig{Include: []string{"INBOX"}},
	}, IMAPAuth{Username: "reader", Password: "secret"})
	if err != nil {
		t.Fatalf("IMAPConnectorConfigFromSource() error = %v", err)
	}
	if config.Host != "mail.example.test" || config.Port != 1993 || config.TLSPolicy != IMAPTLSImplicit {
		t.Fatalf("endpoint config = %#v", config)
	}
	if config.AccountID != "account-1" || config.TLS.ServerName != "imap.example.test" || !reflect.DeepEqual(config.Mailboxes.Include, []string{"INBOX"}) {
		t.Fatalf("translated config = %#v", config)
	}
}

func testIMAPConfig() IMAPConnectorConfig {
	return IMAPConnectorConfig{
		Host:      "mail.example.test",
		Port:      993,
		TLSPolicy: IMAPTLSImplicit,
		Timeout:   time.Second,
		Auth:      IMAPAuth{Username: "reader", Password: "secret"},
	}
}

func authenticatedTestIMAPConnector(t *testing.T, config IMAPConnectorConfig, fake *fakeIMAPClient) *IMAPConnector {
	t.Helper()
	connector, err := newIMAPConnector(context.Background(), config, func(context.Context, IMAPConnectorConfig) (imapClient, error) {
		return fake, nil
	})
	if err != nil {
		t.Fatalf("newIMAPConnector() error = %v", err)
	}
	t.Cleanup(func() { _ = connector.Close() })
	return connector
}

type fakeIMAPClient struct {
	mu              sync.Mutex
	auth            IMAPAuth
	authenticateErr error
	mailboxes       []imapMailbox
	statuses        map[string]imapMailboxStatus
	uids            []uint32
	messages        map[uint32]imapMessage
	bodies          map[uint32][]byte
	selected        string
	searchFirst     uint32
	listStarted     chan struct{}
	blockList       bool
	logoutCalls     int
	closeCalls      int
}

func (f *fakeIMAPClient) Authenticate(ctx context.Context, auth IMAPAuth) error {
	f.auth = auth
	return f.authenticateErr
}

func (f *fakeIMAPClient) ListMailboxes(ctx context.Context) ([]imapMailbox, error) {
	if f.listStarted != nil {
		close(f.listStarted)
	}
	if f.blockList {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return append([]imapMailbox(nil), f.mailboxes...), nil
}

func (f *fakeIMAPClient) SelectMailbox(_ context.Context, name string) (imapMailboxStatus, error) {
	f.selected = name
	return f.statuses[name], nil
}

func (f *fakeIMAPClient) SearchUIDs(_ context.Context, first uint32) ([]uint32, error) {
	f.searchFirst = first
	return append([]uint32(nil), f.uids...), nil
}

func (f *fakeIMAPClient) FetchMetadata(_ context.Context, uids []uint32) ([]imapMessage, error) {
	messages := make([]imapMessage, 0, len(uids))
	for _, uid := range uids {
		if message, ok := f.messages[uid]; ok {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func (f *fakeIMAPClient) FetchMessage(_ context.Context, uid uint32, _ FetchOptions) (imapMessage, []byte, error) {
	return f.messages[uid], append([]byte(nil), f.bodies[uid]...), nil
}

func (f *fakeIMAPClient) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logoutCalls++
	return nil
}

func (f *fakeIMAPClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}
