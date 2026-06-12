package mail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testPOP3Config() POP3ConnectorConfig {
	return POP3ConnectorConfig{
		Host: "pop.example.test", AccountID: "account-1",
		Auth: POP3Auth{Username: "user", Password: "secret"},
	}
}

func authenticatedTestPOP3Connector(t *testing.T, config POP3ConnectorConfig, fake *fakePOP3Client) *POP3Connector {
	t.Helper()
	connector, err := newPOP3Connector(context.Background(), config, func(context.Context, POP3ConnectorConfig) (pop3Client, error) {
		return fake, nil
	})
	if err != nil {
		t.Fatalf("newPOP3Connector() error = %v", err)
	}
	return connector
}

func TestPOP3ConnectorImplementsConnector(t *testing.T) {
	var _ Connector = (*POP3Connector)(nil)
}

func TestNewPOP3ConnectorAuthenticates(t *testing.T) {
	fake := &fakePOP3Client{}
	config := testPOP3Config()
	connector := authenticatedTestPOP3Connector(t, config, fake)
	defer connector.Close() //nolint:errcheck
	if fake.auth != config.Auth {
		t.Fatalf("Authenticate() auth = %#v, want %#v", fake.auth, config.Auth)
	}
}

func TestNewPOP3ConnectorClosesAfterAuthenticationFailure(t *testing.T) {
	fake := &fakePOP3Client{authenticateErr: errors.New("bad credentials")}
	_, err := newPOP3Connector(context.Background(), testPOP3Config(), func(context.Context, POP3ConnectorConfig) (pop3Client, error) {
		return fake, nil
	})
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorAuthentication {
		t.Fatalf("newPOP3Connector() error = %T %v, want authentication error", err, err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", fake.closeCalls)
	}
}

func TestNewPOP3ConnectorClosesWhenAuthenticationIsCanceled(t *testing.T) {
	started := make(chan struct{})
	fake := &fakePOP3Client{authenticateStarted: started, blockAuthenticate: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := newPOP3Connector(ctx, testPOP3Config(), func(context.Context, POP3ConnectorConfig) (pop3Client, error) {
			return fake, nil
		})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("newPOP3Connector() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("newPOP3Connector() did not return after authentication cancellation")
	}
	if fake.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", fake.closeCalls)
	}
}

func TestPOP3ConnectorSecureDefaultsAndValidation(t *testing.T) {
	implicit := normalizePOP3Config(testPOP3Config())
	if implicit.TLSPolicy != POP3TLSImplicit || implicit.Port != 995 {
		t.Fatalf("implicit defaults = policy %q port %d, want implicit/995", implicit.TLSPolicy, implicit.Port)
	}
	startTLSConfig := testPOP3Config()
	startTLSConfig.TLSPolicy = POP3TLSStartTLS
	startTLS := normalizePOP3Config(startTLSConfig)
	if startTLS.Port != 110 {
		t.Fatalf("STARTTLS default port = %d, want 110", startTLS.Port)
	}
	plain := testPOP3Config()
	plain.TLSPolicy = POP3TLSNone
	plain.TLS.ServerName = "mail.example.test"
	if err := validatePOP3Config(normalizePOP3Config(plain)); err == nil {
		t.Fatal("validatePOP3Config() accepted TLS options without TLS")
	}
}

func TestNewPOP3ConnectorHonorsCanceledContextBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := newPOP3Connector(ctx, testPOP3Config(), func(context.Context, POP3ConnectorConfig) (pop3Client, error) {
		called = true
		return &fakePOP3Client{}, nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("newPOP3Connector() = (%v, dialed %v), want context.Canceled without dial", err, called)
	}
}

func TestPOP3ConnectorSingleMailbox(t *testing.T) {
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), &fakePOP3Client{})
	defer connector.Close() //nolint:errcheck
	mailboxes, err := connector.ListMailboxes(context.Background())
	if err != nil {
		t.Fatalf("ListMailboxes() error = %v", err)
	}
	want := []Mailbox{{ID: "INBOX", Name: "INBOX"}}
	if !reflect.DeepEqual(mailboxes, want) {
		t.Fatalf("ListMailboxes() = %#v, want %#v", mailboxes, want)
	}
	_, err = connector.ListChanges(context.Background(), Mailbox{ID: "Archive"}, Cursor{}, 10)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorMailboxNotFound {
		t.Fatalf("ListChanges(Archive) error = %T %v, want mailbox-not-found", err, err)
	}
}

func TestPOP3ConnectorPollingPagesAndDetectsDeletion(t *testing.T) {
	fake := &fakePOP3Client{messages: []pop3Message{
		{Number: 1, UIDL: "uid-a", Size: 10},
		{Number: 2, UIDL: "uid-b", Size: 20},
		{Number: 3, UIDL: "uid-c", Size: 30},
	}}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck
	mailbox := Mailbox{ID: "INBOX"}

	first, err := connector.ListChanges(context.Background(), mailbox, Cursor{}, 2)
	if err != nil {
		t.Fatalf("first ListChanges() error = %v", err)
	}
	if got := pop3Versions(first.Changes); !reflect.DeepEqual(got, []string{"uid-a", "uid-b"}) || !first.More {
		t.Fatalf("first page = %#v, want uid-a/uid-b with more", first)
	}
	for _, change := range first.Changes {
		if change.Ref.ProviderMessageID != "" || change.Ref.Version == "" {
			t.Fatalf("POP3 ref identity = %#v, want weak UIDL locator in Version only", change.Ref)
		}
	}
	second, err := connector.ListChanges(context.Background(), mailbox, first.Next, 2)
	if err != nil {
		t.Fatalf("second ListChanges() error = %v", err)
	}
	if got := pop3Versions(second.Changes); !reflect.DeepEqual(got, []string{"uid-c"}) || second.More {
		t.Fatalf("second page = %#v, want uid-c complete", second)
	}
	if fake.listCalls != 1 {
		t.Fatalf("List() calls during one snapshot = %d, want 1", fake.listCalls)
	}

	fake.messages = []pop3Message{{Number: 1, UIDL: "uid-a", Size: 10}, {Number: 2, UIDL: "uid-c", Size: 30}}
	third, err := connector.ListChanges(context.Background(), mailbox, second.Next, 10)
	if err != nil {
		t.Fatalf("third ListChanges() error = %v", err)
	}
	if len(third.Changes) != 1 || third.Changes[0].Kind != ChangeDelete || third.Changes[0].Ref.Version != "uid-b" {
		t.Fatalf("deletion page = %#v, want deletion of uid-b", third)
	}
}

func TestPOP3ConnectorOpenMessageResolvesRenumberedUIDLAndBoundsSize(t *testing.T) {
	fake := &fakePOP3Client{
		messages: []pop3Message{{Number: 4, UIDL: "stable-uidl", Size: 5}},
		bodies:   map[int][]byte{4: []byte("hello")},
	}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck
	ref := MessageRef{Mailbox: Mailbox{ID: "INBOX"}, UID: 1, Version: "stable-uidl"}
	raw, err := connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true, MaxBytes: 5})
	if err != nil {
		t.Fatalf("OpenMessage() error = %v", err)
	}
	defer raw.RFC822.Close() //nolint:errcheck
	data, readErr := io.ReadAll(raw.RFC822)
	if readErr != nil || string(data) != "hello" || fake.retrieveNumber != 4 {
		t.Fatalf("OpenMessage() = %q, read %v, RETR %d; want hello via RETR 4", data, readErr, fake.retrieveNumber)
	}
	_, err = connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true, MaxBytes: 4})
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized {
		t.Fatalf("oversize error = %T %v, want ErrorOversized", err, err)
	}
}

func TestPOP3ConnectorUIDLListingDoesNotRetrieveContent(t *testing.T) {
	fake := &fakePOP3Client{messages: []pop3Message{{Number: 1, UIDL: "server-uid", Size: 12}}}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck

	page, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{}, 10)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(page.Changes) != 1 || page.Changes[0].Ref.Version != "server-uid" {
		t.Fatalf("ListChanges() = %#v, want server UIDL", page)
	}
	if fake.retrieveCalls != 0 {
		t.Fatalf("Retrieve() calls = %d, want 0 when UIDL is available", fake.retrieveCalls)
	}
}

func TestPOP3ConnectorMissingUIDLUsesFingerprintsAcrossRenumberingAndDeletion(t *testing.T) {
	fake := &fakePOP3Client{
		messages: []pop3Message{{Number: 1, Size: 5}, {Number: 2, Size: 4}},
		bodies:   map[int][]byte{1: []byte("alpha"), 2: []byte("beta")},
	}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck
	mailbox := Mailbox{ID: "INBOX"}

	first, err := connector.ListChanges(context.Background(), mailbox, Cursor{}, 10)
	if err != nil {
		t.Fatalf("first ListChanges() error = %v", err)
	}
	if len(first.Changes) != 2 {
		t.Fatalf("first changes = %#v, want two", first.Changes)
	}
	alphaVersion := first.Changes[0].Ref.Version
	betaVersion := first.Changes[1].Ref.Version
	if !strings.HasPrefix(alphaVersion, "sha256:") || !strings.HasSuffix(alphaVersion, ":1") || !strings.HasPrefix(betaVersion, "sha256:") {
		t.Fatalf("fallback versions = %q, %q, want SHA-256 evidence", alphaVersion, betaVersion)
	}

	fake.messages = []pop3Message{{Number: 4, Size: 5}, {Number: 7, Size: 4}}
	fake.bodies = map[int][]byte{4: []byte("alpha"), 7: []byte("beta")}
	renumbered, err := connector.ListChanges(context.Background(), mailbox, first.Next, 10)
	if err != nil {
		t.Fatalf("renumbered ListChanges() error = %v", err)
	}
	if len(renumbered.Changes) != 0 {
		t.Fatalf("renumbered changes = %#v, want none", renumbered.Changes)
	}

	fake.messages = []pop3Message{{Number: 1, Size: 5}}
	fake.bodies = map[int][]byte{1: []byte("alpha")}
	deleted, err := connector.ListChanges(context.Background(), mailbox, renumbered.Next, 10)
	if err != nil {
		t.Fatalf("deletion ListChanges() error = %v", err)
	}
	if len(deleted.Changes) != 1 || deleted.Changes[0].Kind != ChangeDelete || deleted.Changes[0].Ref.Version != betaVersion {
		t.Fatalf("deletion changes = %#v, want deletion of %q", deleted.Changes, betaVersion)
	}
}

func TestPOP3ConnectorDuplicateContentKeepsDistinctOccurrences(t *testing.T) {
	fake := &fakePOP3Client{
		messages: []pop3Message{{Number: 1, Size: 4}, {Number: 2, Size: 4}},
		bodies:   map[int][]byte{1: []byte("same"), 2: []byte("same")},
	}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck
	mailbox := Mailbox{ID: "INBOX"}

	first, err := connector.ListChanges(context.Background(), mailbox, Cursor{}, 10)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(first.Changes) != 2 || first.Changes[0].Ref.Version == first.Changes[1].Ref.Version {
		t.Fatalf("duplicate-content changes = %#v, want two distinct occurrences", first.Changes)
	}
	if !strings.HasSuffix(first.Changes[0].Ref.Version, ":1") || !strings.HasSuffix(first.Changes[1].Ref.Version, ":2") {
		t.Fatalf("duplicate-content versions = %q and %q, want occurrence suffixes", first.Changes[0].Ref.Version, first.Changes[1].Ref.Version)
	}

	fake.messages = []pop3Message{{Number: 9, Size: 4}}
	fake.bodies = map[int][]byte{9: []byte("same")}
	second, err := connector.ListChanges(context.Background(), mailbox, first.Next, 10)
	if err != nil {
		t.Fatalf("second ListChanges() error = %v", err)
	}
	if len(second.Changes) != 1 || second.Changes[0].Kind != ChangeDelete || !strings.HasSuffix(second.Changes[0].Ref.Version, ":2") {
		t.Fatalf("duplicate deletion = %#v, want deletion of second occurrence", second.Changes)
	}
}

func TestPOP3ConnectorOpenFingerprintAfterRenumbering(t *testing.T) {
	body := []byte("raw message")
	version := "sha256:" + fingerprintPOP3Message(body) + ":1"
	fake := &fakePOP3Client{
		messages: []pop3Message{{Number: 8, Size: int64(len(body))}},
		bodies:   map[int][]byte{8: body},
	}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck

	raw, err := connector.OpenMessage(context.Background(), MessageRef{
		Mailbox: Mailbox{ID: "INBOX"}, UID: 1, Version: version, Size: int64(len(body)),
	}, FetchOptions{IncludeBody: true})
	if err != nil {
		t.Fatalf("OpenMessage() error = %v", err)
	}
	defer raw.RFC822.Close() //nolint:errcheck
	got, err := io.ReadAll(raw.RFC822)
	if err != nil || !reflect.DeepEqual(got, body) || fake.retrieveNumber != 8 {
		t.Fatalf("OpenMessage() = %q, %v via RETR %d; want renumbered content", got, err, fake.retrieveNumber)
	}
}

func TestPOP3ConnectorEnforcesConfiguredSizeAndClassifiesFetchErrors(t *testing.T) {
	config := testPOP3Config()
	config.MaxMessageBytes = 4
	fake := &fakePOP3Client{
		messages: []pop3Message{{Number: 1, UIDL: "large", Size: 5}},
		bodies:   map[int][]byte{1: []byte("12345")},
	}
	connector := authenticatedTestPOP3Connector(t, config, fake)
	defer connector.Close() //nolint:errcheck
	ref := MessageRef{Mailbox: Mailbox{ID: "INBOX"}, Version: "large"}

	_, err := connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true})
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized || fake.retrieveCalls != 0 {
		t.Fatalf("advertised-size error = %T %v, retrieves %d; want preflight oversized", err, err, fake.retrieveCalls)
	}

	fake.messages[0].Size = 4
	_, err = connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true})
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized || fake.retrieveMaxBytes != 4 {
		t.Fatalf("actual-size error = %T %v, max %d; want bounded oversized", err, err, fake.retrieveMaxBytes)
	}

	fetchFailure := errors.New("server fetch failed")
	fake.bodies[1] = []byte("1234")
	fake.retrieveErr = map[int]error{1: fetchFailure}
	_, err = connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true})
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorTransient {
		t.Fatalf("fetch error = %T %v, want transient POP3 error", err, err)
	}
}

func TestPOP3ConnectorMissingUIDLListingEnforcesSizePolicy(t *testing.T) {
	config := testPOP3Config()
	config.MaxMessageBytes = 4
	fake := &fakePOP3Client{messages: []pop3Message{{Number: 1, Size: 5}}}
	connector := authenticatedTestPOP3Connector(t, config, fake)
	defer connector.Close() //nolint:errcheck

	_, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{}, 10)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized || fake.retrieveCalls != 0 {
		t.Fatalf("ListChanges() error = %T %v, retrieves %d; want preflight oversized", err, err, fake.retrieveCalls)
	}
}

func TestPOP3ConnectorMissingUIDLListingPropagatesFetchError(t *testing.T) {
	fake := &fakePOP3Client{
		messages:    []pop3Message{{Number: 1, Size: 4}},
		retrieveErr: map[int]error{1: errors.New("RETR failed")},
	}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	defer connector.Close() //nolint:errcheck

	_, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{}, 10)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorTransient || mailErr.Operation != "fetch message" {
		t.Fatalf("ListChanges() error = %T %v, want transient fetch error", err, err)
	}
}

func TestWirePOP3ClientListAcceptsUnsupportedUIDL(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close() //nolint:errcheck
	defer serverConn.Close() //nolint:errcheck
	client := newWirePOP3Client(clientConn, time.Second)
	serverDone := make(chan error, 1)
	go func() {
		reader := textproto.NewReader(bufio.NewReader(serverConn))
		writer := textproto.NewWriter(bufio.NewWriter(serverConn))
		line, err := reader.ReadLine()
		if err != nil || line != "LIST" {
			serverDone <- fmt.Errorf("LIST command = %q, %v", line, err)
			return
		}
		if err := writer.PrintfLine("+OK 1 messages"); err != nil {
			serverDone <- err
			return
		}
		if err := writer.PrintfLine("1 12"); err != nil {
			serverDone <- err
			return
		}
		if err := writer.PrintfLine("."); err != nil {
			serverDone <- err
			return
		}
		line, err = reader.ReadLine()
		if err != nil || line != "UIDL" {
			serverDone <- fmt.Errorf("UIDL command = %q, %v", line, err)
			return
		}
		serverDone <- writer.PrintfLine("-ERR UIDL unsupported")
	}()

	messages, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Number != 1 || messages[0].Size != 12 || messages[0].UIDL != "" {
		t.Fatalf("List() = %#v, want LIST metadata without UIDL", messages)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestPOP3ConnectorOperationHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	fake := &fakePOP3Client{listStarted: started, blockList: true}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := connector.ListChanges(ctx, Mailbox{ID: "INBOX"}, Cursor{}, 10)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListChanges() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListChanges() did not return promptly after cancellation")
	}
}

func TestPOP3ConnectorCloseIsCleanAndIdempotent(t *testing.T) {
	fake := &fakePOP3Client{}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	if err := connector.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := connector.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if fake.quitCalls != 1 || fake.closeCalls != 1 {
		t.Fatalf("close lifecycle = QUIT %d, Close %d; want 1 each", fake.quitCalls, fake.closeCalls)
	}
	_, err := connector.ListMailboxes(context.Background())
	if err == nil {
		t.Fatal("ListMailboxes() after Close returned nil error")
	}
}

func TestPOP3ConnectorCloseJoinsQuitAndTransportErrors(t *testing.T) {
	quitErr := errors.New("quit failed")
	closeErr := errors.New("close failed")
	fake := &fakePOP3Client{quitErr: quitErr, closeErr: closeErr}
	connector := authenticatedTestPOP3Connector(t, testPOP3Config(), fake)
	err := connector.Close()
	if !errors.Is(err, quitErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Close() error = %v, want joined QUIT and close errors", err)
	}
}

func TestPOP3ConnectorConfigFromSource(t *testing.T) {
	source := DefaultSourceConfig()
	source.Connector.Provider = "pop3"
	source.Connector.Endpoint = "pop3s://mail.example.test:1995"
	source.Connector.TLS.ServerName = "tls.example.test"
	source.Connector.ProxyURL = "socks5://proxy.example.test:1080"
	source.Auth.Identity = "account-1"
	auth := POP3Auth{Username: "user", Password: "secret"}
	config, err := POP3ConnectorConfigFromSource(source, auth)
	if err != nil {
		t.Fatalf("POP3ConnectorConfigFromSource() error = %v", err)
	}
	if config.Host != "mail.example.test" || config.Port != 1995 || config.TLSPolicy != POP3TLSImplicit || config.Auth != auth || config.AccountID != "account-1" || config.MaxMessageBytes != source.Crawl.Limits.MaxMessageBytes || config.ProxyURL != source.Connector.ProxyURL {
		t.Fatalf("POP3 config = %#v", config)
	}
}

func pop3Versions(changes []Change) []string {
	versions := make([]string, len(changes))
	for i, change := range changes {
		versions[i] = change.Ref.Version
	}
	return versions
}

type fakePOP3Client struct {
	mu                  sync.Mutex
	auth                POP3Auth
	authenticateErr     error
	authenticateStarted chan struct{}
	blockAuthenticate   bool
	messages            []pop3Message
	bodies              map[int][]byte
	listStarted         chan struct{}
	blockList           bool
	listCalls           int
	retrieveNumber      int
	retrieveCalls       int
	retrieveMaxBytes    int64
	retrieveErr         map[int]error
	quitCalls           int
	closeCalls          int
	quitErr             error
	closeErr            error
}

func (f *fakePOP3Client) Authenticate(ctx context.Context, auth POP3Auth) error {
	f.auth = auth
	if f.authenticateStarted != nil {
		close(f.authenticateStarted)
	}
	if f.blockAuthenticate {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.authenticateErr
}

func (f *fakePOP3Client) List(ctx context.Context) ([]pop3Message, error) {
	f.mu.Lock()
	f.listCalls++
	started := f.listStarted
	if started != nil {
		f.listStarted = nil
	}
	block := f.blockList
	messages := append([]pop3Message(nil), f.messages...)
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return messages, nil
}

func (f *fakePOP3Client) Retrieve(_ context.Context, number int, maxBytes int64) ([]byte, error) {
	f.retrieveNumber = number
	f.retrieveCalls++
	f.retrieveMaxBytes = maxBytes
	if err := f.retrieveErr[number]; err != nil {
		return nil, err
	}
	data := append([]byte(nil), f.bodies[number]...)
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, &Error{Kind: ErrorOversized, Operation: "fetch message", Message: "message exceeds configured byte limit"}
	}
	return data, nil
}

func (f *fakePOP3Client) Quit(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quitCalls++
	return f.quitErr
}

func (f *fakePOP3Client) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}
