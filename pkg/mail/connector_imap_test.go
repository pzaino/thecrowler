package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	imap "github.com/emersion/go-imap"
)

func TestIMAPErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		err       error
		want      ErrorKind
	}{
		{name: "authentication", operation: "authenticate", err: errors.New("credentials rejected"), want: ErrorAuthentication},
		{name: "network", operation: "list mailboxes", err: testIMAPNetworkError{}, want: ErrorNetwork},
		{name: "timeout", operation: "search messages", err: testIMAPTimeoutError{}, want: ErrorTimeout},
		{name: "deadline", operation: "fetch message", err: context.DeadlineExceeded, want: ErrorTimeout},
		{name: "missing mailbox", operation: "select mailbox", err: imapStatusError(imap.StatusRespNo, "NONEXISTENT", "mailbox secret-folder does not exist"), want: ErrorMailboxNotFound},
		{name: "missing message", operation: "fetch message", err: errIMAPMessageNotFound, want: ErrorMessageNotFound},
		{name: "rate limit", operation: "search messages", err: imapStatusError(imap.StatusRespNo, "LIMIT", "retry after sending token-secret"), want: ErrorRateLimit},
		{name: "unsupported response code", operation: "search messages", err: imapStatusError(imap.StatusRespNo, "CANNOT", "server payload"), want: ErrorUnsupported},
		{name: "bad command", operation: "fetch message", err: imapStatusError(imap.StatusRespBad, "", "unknown extension with server payload"), want: ErrorUnsupported},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := imapError(test.operation, test.err)
			var mailErr *Error
			if !errors.As(err, &mailErr) {
				t.Fatalf("imapError() = %T %v, want *Error", err, err)
			}
			if mailErr.Kind != test.want {
				t.Fatalf("imapError().Kind = %q, want %q", mailErr.Kind, test.want)
			}
			if mailErr.Cause == nil {
				t.Fatal("imapError().Cause = nil, want safe diagnostic cause")
			}
		})
	}
}

func TestIMAPErrorRedactsCredentialsAndServerPayloadFromErrorAndCause(t *testing.T) {
	t.Parallel()

	const secret = "reader:password-token-secret"
	err := imapError("authenticate", imapStatusError(
		imap.StatusRespNo,
		"AUTHENTICATIONFAILED",
		"login rejected for "+secret,
	))
	var mailErr *Error
	if !errors.As(err, &mailErr) {
		t.Fatalf("imapError() = %T %v, want *Error", err, err)
	}
	for name, value := range map[string]string{
		"public error":     err.Error(),
		"diagnostic cause": mailErr.Cause.Error(),
		"formatted error":  fmt.Sprintf("%+v", err),
	} {
		if strings.Contains(value, secret) || strings.Contains(value, "login rejected") {
			t.Fatalf("%s %q leaked credentials or server payload", name, value)
		}
	}
	if !strings.Contains(mailErr.Cause.Error(), "AUTHENTICATIONFAILED") {
		t.Fatalf("diagnostic cause = %q, want safe response code", mailErr.Cause)
	}
}

func imapStatusError(responseType imap.StatusRespType, code imap.StatusRespCode, info string) error {
	return &imap.ErrStatusResp{Resp: &imap.StatusResp{Type: responseType, Code: code, Info: info}}
}

type testIMAPNetworkError struct{}

func (testIMAPNetworkError) Error() string   { return "network endpoint secret.example.test failed" }
func (testIMAPNetworkError) Timeout() bool   { return false }
func (testIMAPNetworkError) Temporary() bool { return true }

type testIMAPTimeoutError struct{ testIMAPNetworkError }

func (testIMAPTimeoutError) Timeout() bool { return true }

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

func TestIMAPConnectorListChangesPagesUIDsAndCapturesMetadata(t *testing.T) {
	internalDate := time.Date(2026, time.June, 10, 12, 30, 0, 0, time.UTC)
	envelopeDate := time.Date(2026, time.June, 9, 8, 15, 0, 0, time.UTC)
	fake := &fakeIMAPClient{
		statuses: map[string]imapMailboxStatus{"INBOX": {UIDValidity: 44}},
		uids:     []uint32{15, 12, 11, 13, 12},
		messages: map[uint32]imapMessage{
			11: {
				UID: 11, InternalDate: internalDate, Flags: []string{"\\Seen", "$label"}, Size: 2048,
				Envelope: &MessageEnvelope{
					Date: envelopeDate, Subject: "Discovery metadata",
					From:      []Address{{Name: "Sender", Address: "sender@example.test", Normalized: "sender@example.test"}},
					Sender:    []Address{{Address: "agent@example.test", Normalized: "agent@example.test"}},
					ReplyTo:   []Address{{Address: "reply@example.test", Normalized: "reply@example.test"}},
					To:        []Address{{Address: "to@example.test", Normalized: "to@example.test"}},
					CC:        []Address{{Address: "cc@example.test", Normalized: "cc@example.test"}},
					BCC:       []Address{{Address: "bcc@example.test", Normalized: "bcc@example.test"}},
					InReplyTo: "<parent@example.test>", MessageID: "<message@example.test>",
				},
			},
			12: {UID: 12, Size: 1024},
			13: {UID: 13, Size: 512},
			15: {UID: 15, Size: 256},
		},
	}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)
	mailbox := Mailbox{ID: "INBOX", Name: "Inbox"}

	first, err := connector.ListChanges(context.Background(), mailbox, Cursor{UID: 10, UIDValidity: 44}, 2)
	if err != nil {
		t.Fatalf("first ListChanges() error = %v", err)
	}
	if got := changeUIDs(first.Changes); !reflect.DeepEqual(got, []uint32{11, 12}) {
		t.Fatalf("first page UIDs = %v, want [11 12]", got)
	}
	if !first.More || first.Next != (Cursor{UID: 12, UIDValidity: 44}) {
		t.Fatalf("first page continuation = (%#v, %v), want UID 12 and more", first.Next, first.More)
	}
	ref := first.Changes[0].Ref
	if ref.InternalDate != internalDate || !reflect.DeepEqual(ref.Flags, []string{"\\Seen", "$label"}) || ref.Size != 2048 {
		t.Fatalf("discovered message metadata = %#v", ref)
	}
	if !reflect.DeepEqual(ref.Envelope, fake.messages[11].Envelope) {
		t.Fatalf("discovered envelope = %#v, want %#v", ref.Envelope, fake.messages[11].Envelope)
	}

	second, err := connector.ListChanges(context.Background(), mailbox, first.Next, 2)
	if err != nil {
		t.Fatalf("second ListChanges() error = %v", err)
	}
	if got := changeUIDs(second.Changes); !reflect.DeepEqual(got, []uint32{13, 15}) {
		t.Fatalf("second page UIDs = %v, want [13 15]", got)
	}
	if second.More || second.Next != (Cursor{UID: 15, UIDValidity: 44}) {
		t.Fatalf("second page continuation = (%#v, %v), want UID 15 and complete", second.Next, second.More)
	}
	if !reflect.DeepEqual(fake.searchFirsts, []uint32{11, 13}) {
		t.Fatalf("SearchUIDs starts = %v, want [11 13]", fake.searchFirsts)
	}
	if !reflect.DeepEqual(fake.fetchUIDs, [][]uint32{{11, 12}, {13, 15}}) {
		t.Fatalf("FetchMetadata UIDs = %v, want [[11 12] [13 15]]", fake.fetchUIDs)
	}
}

func TestIMAPConnectorListChangesEmptyResult(t *testing.T) {
	fake := &fakeIMAPClient{
		statuses: map[string]imapMailboxStatus{"INBOX": {UIDValidity: 7}},
	}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)

	page, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{UID: 20, UIDValidity: 7}, 50)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(page.Changes) != 0 || page.More || page.Next != (Cursor{UID: 20, UIDValidity: 7}) {
		t.Fatalf("empty page = %#v", page)
	}
	if len(fake.fetchUIDs) != 0 {
		t.Fatalf("FetchMetadata calls = %v, want none", fake.fetchUIDs)
	}
}

func TestIMAPConnectorListChangesReportsDeletedUID(t *testing.T) {
	fake := &fakeIMAPClient{
		statuses: map[string]imapMailboxStatus{"INBOX": {UIDValidity: 31}},
		uids:     []uint32{101, 102},
		messages: map[uint32]imapMessage{102: {UID: 102, Size: 4096}},
	}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)
	mailbox := Mailbox{ID: "INBOX", Name: "Inbox"}

	page, err := connector.ListChanges(context.Background(), mailbox, Cursor{UID: 100, UIDValidity: 31}, 10)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(page.Changes) != 2 || page.Changes[0].Kind != ChangeDelete || page.Changes[1].Kind != ChangeUpsert {
		t.Fatalf("changes = %#v, want delete then upsert", page.Changes)
	}
	deleted := page.Changes[0].Ref
	if deleted.UID != 101 || deleted.UIDValidity != 31 || deleted.Mailbox != mailbox {
		t.Fatalf("deleted ref = %#v, want mailbox UID tuple 31/101", deleted)
	}
	if page.Next != (Cursor{UID: 102, UIDValidity: 31}) {
		t.Fatalf("next cursor = %#v, want UID 102", page.Next)
	}
}

func TestIMAPConnectorListChangesResetsAfterUIDValidityChange(t *testing.T) {
	fake := &fakeIMAPClient{
		statusSequence: []imapMailboxStatus{{UIDValidity: 9}, {UIDValidity: 10}},
		uids:           []uint32{41},
		messages:       map[uint32]imapMessage{41: {UID: 41}},
	}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)
	mailbox := Mailbox{ID: "INBOX"}

	first, err := connector.ListChanges(context.Background(), mailbox, Cursor{UID: 40, UIDValidity: 9}, 10)
	if err != nil {
		t.Fatalf("first ListChanges() error = %v", err)
	}
	reset, err := connector.ListChanges(context.Background(), mailbox, first.Next, 10)
	if err != nil {
		t.Fatalf("second ListChanges() error = %v", err)
	}
	if len(reset.Changes) != 1 || reset.Changes[0].Kind != ChangeReset {
		t.Fatalf("reset changes = %#v, want one reset", reset.Changes)
	}
	if reset.Next != (Cursor{UIDValidity: 10}) || reset.More {
		t.Fatalf("reset continuation = %#v, more %v", reset.Next, reset.More)
	}
	if len(fake.searchFirsts) != 1 {
		t.Fatalf("SearchUIDs calls = %v, want no search after UIDVALIDITY change", fake.searchFirsts)
	}
}

func changeUIDs(changes []Change) []uint32 {
	uids := make([]uint32, len(changes))
	for i, change := range changes {
		uids[i] = change.Ref.UID
	}
	return uids
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
		Listener: ListenerConfig{
			ReconnectBackoff:    2 * time.Second,
			MaxReconnectBackoff: 30 * time.Second,
			IdleReissueInterval: 20 * time.Minute,
		},
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
	if config.ReconnectBackoff != 2*time.Second || config.MaxReconnectBackoff != 30*time.Second || config.IdleReissueInterval != 20*time.Minute {
		t.Fatalf("listener timing config = %#v", config)
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
	statusSequence  []imapMailboxStatus
	uids            []uint32
	messages        map[uint32]imapMessage
	bodies          map[uint32][]byte
	selected        string
	searchFirst     uint32
	searchFirsts    []uint32
	fetchUIDs       [][]uint32
	listStarted     chan struct{}
	blockList       bool
	fetchMessageErr error
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
	if len(f.statusSequence) > 0 {
		status := f.statusSequence[0]
		f.statusSequence = f.statusSequence[1:]
		return status, nil
	}
	return f.statuses[name], nil
}

func (f *fakeIMAPClient) SearchUIDs(_ context.Context, first uint32) ([]uint32, error) {
	f.searchFirst = first
	f.searchFirsts = append(f.searchFirsts, first)
	return append([]uint32(nil), f.uids...), nil
}

func (f *fakeIMAPClient) FetchMetadata(_ context.Context, uids []uint32) ([]imapMessage, error) {
	f.fetchUIDs = append(f.fetchUIDs, append([]uint32(nil), uids...))
	messages := make([]imapMessage, 0, len(uids))
	for _, uid := range uids {
		if message, ok := f.messages[uid]; ok {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func (f *fakeIMAPClient) FetchMessage(_ context.Context, uid uint32, _ FetchOptions) (imapMessage, []byte, error) {
	if f.fetchMessageErr != nil {
		return imapMessage{}, nil, f.fetchMessageErr
	}
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

func TestIMAPConnectorOpenMessageMissing(t *testing.T) {
	fake := &fakeIMAPClient{
		statuses:        map[string]imapMailboxStatus{"INBOX": {UIDValidity: 9}},
		fetchMessageErr: errors.New("IMAP message not found"),
	}
	connector := authenticatedTestIMAPConnector(t, testIMAPConfig(), fake)

	_, err := connector.OpenMessage(context.Background(), MessageRef{
		Mailbox: Mailbox{ID: "INBOX"}, UID: 404, UIDValidity: 9,
	}, FetchOptions{IncludeBody: true, MaxBytes: 1024})
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorMessageNotFound {
		t.Fatalf("OpenMessage() error = %T %v, want ErrorMessageNotFound", err, err)
	}
}

func TestReadIMAPLiteralSuccess(t *testing.T) {
	const message = "From: sender@example.test\r\nSubject: complete\r\n\r\nbody"
	data, err := readIMAPLiteral(context.Background(), strings.NewReader(message), int64(len(message)), int64(len(message)))
	if err != nil {
		t.Fatalf("readIMAPLiteral() error = %v", err)
	}
	if string(data) != message {
		t.Fatalf("readIMAPLiteral() = %q, want complete RFC822 message", data)
	}
}

func TestReadIMAPLiteralRejectsDeclaredOversizeBeforeRead(t *testing.T) {
	reader := &countingReader{Reader: strings.NewReader("not read")}
	_, err := readIMAPLiteral(context.Background(), reader, 9, 8)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized {
		t.Fatalf("readIMAPLiteral() error = %T %v, want ErrorOversized", err, err)
	}
	if reader.reads != 0 {
		t.Fatalf("literal reads = %d, want zero after metadata size rejection", reader.reads)
	}
}

func TestReadIMAPLiteralRejectsUndeclaredOversizeDuringBoundedRead(t *testing.T) {
	reader := strings.NewReader("123456789")
	_, err := readIMAPLiteral(context.Background(), reader, 0, 8)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized {
		t.Fatalf("readIMAPLiteral() error = %T %v, want ErrorOversized", err, err)
	}
	if reader.Len() != 0 {
		t.Fatalf("unread literal bytes = %d, want bounded read of limit plus one", reader.Len())
	}
}

func TestReadIMAPLiteralReportsShortRead(t *testing.T) {
	_, err := readIMAPLiteral(context.Background(), strings.NewReader("short"), 10, 20)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readIMAPLiteral() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadIMAPLiteralHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingReader{cancel: cancel, data: []byte("partial")}
	_, err := readIMAPLiteral(ctx, reader, 0, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readIMAPLiteral() error = %v, want context.Canceled", err)
	}
}

type countingReader struct {
	io.Reader
	reads int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	r.reads++
	return r.Reader.Read(buffer)
}

type cancelingReader struct {
	cancel context.CancelFunc
	data   []byte
	read   bool
}

func (r *cancelingReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	n := copy(buffer, r.data)
	r.cancel()
	return n, nil
}
