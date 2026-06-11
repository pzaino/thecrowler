package mail

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestNewGraphConnectorAuthenticatesAndRunsLifecycle(t *testing.T) {
	events := make([]string, 0)
	credentials := &fakeGraphCredentialSource{
		credentials: GraphOAuthCredentials{ClientID: "client-id", ClientSecret: "client-secret"},
		events:      &events,
	}
	tokenSource := &fakeGraphTokenSource{token: &oauth2.Token{AccessToken: "access-token", Expiry: time.Now().Add(time.Hour)}, events: &events}
	tokens := &fakeGraphTokenProvider{source: tokenSource, events: &events}
	client := &fakeGraphClient{
		mailboxes: []Mailbox{{ID: "inbox-id", Name: "Inbox"}, {ID: "archive-id", Name: "Archive"}},
		page: ChangePage{Changes: []Change{{Kind: ChangeUpsert, Ref: MessageRef{
			ProviderMessageID: "message-id", ProviderThreadID: "thread-id",
		}}}, Next: Cursor{Token: "delta-token"}},
		message: "Subject: Graph\r\n\r\nhello",
		events:  &events,
	}
	factory := &fakeGraphClientFactory{client: client, events: &events}

	connector, err := NewGraphConnector(context.Background(), GraphConnectorConfig{
		AccountID:     "account-1",
		TenantID:      "tenant-1",
		UserID:        "reader@example.com",
		CredentialRef: "secret/graph/account-1",
		Mailboxes:     MailboxSelector{Include: []string{"Inbox"}},
	}, GraphDependencies{Credentials: credentials, Tokens: tokens, Clients: factory})
	if err != nil {
		t.Fatalf("NewGraphConnector() error = %v", err)
	}
	if credentials.reference != "secret/graph/account-1" {
		t.Fatalf("credential reference = %q", credentials.reference)
	}
	if tokens.tenantID != "tenant-1" || !reflect.DeepEqual(tokens.credentials, credentials.credentials) {
		t.Fatalf("token request = tenant %q, credentials %#v", tokens.tenantID, tokens.credentials)
	}
	if !reflect.DeepEqual(tokens.scopes, []string{graphScope}) {
		t.Fatalf("token scopes = %#v, want Graph default scope", tokens.scopes)
	}
	if tokenSource.calls != 1 {
		t.Fatalf("token calls during authentication = %d, want 1", tokenSource.calls)
	}
	if factory.tokens == nil || factory.baseURL != graphDefaultURL {
		t.Fatalf("factory arguments = tokens %v, base URL %q", factory.tokens != nil, factory.baseURL)
	}

	mailboxes, err := connector.ListMailboxes(context.Background())
	if err != nil {
		t.Fatalf("ListMailboxes() error = %v", err)
	}
	if want := []Mailbox{{ID: "inbox-id", Name: "Inbox"}}; !reflect.DeepEqual(mailboxes, want) {
		t.Fatalf("ListMailboxes() = %#v, want %#v", mailboxes, want)
	}

	page, err := connector.ListChanges(context.Background(), mailboxes[0], Cursor{}, 25)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(page.Changes) != 1 {
		t.Fatalf("ListChanges() changes = %#v", page.Changes)
	}
	ref := page.Changes[0].Ref
	if ref.Provider != graphProvider || ref.AccountID != "account-1" || ref.Mailbox != mailboxes[0] {
		t.Fatalf("normalized message reference = %#v", ref)
	}
	if client.changeUserID != "reader@example.com" || client.changeLimit != 25 {
		t.Fatalf("change call user = %q, limit = %d", client.changeUserID, client.changeLimit)
	}

	raw, err := connector.OpenMessage(context.Background(), ref, FetchOptions{IncludeBody: true, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("OpenMessage() error = %v", err)
	}
	data, err := io.ReadAll(raw.RFC822)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	if err := raw.RFC822.Close(); err != nil {
		t.Fatalf("close message: %v", err)
	}
	if string(data) != client.message || raw.Ref.Size != int64(len(client.message)) {
		t.Fatalf("raw message = %q with ref %#v", data, raw.Ref)
	}

	if err := connector.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := connector.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("client Close calls = %d, want 1", client.closeCalls)
	}
	if _, err := connector.ListMailboxes(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("ListMailboxes() after Close error = %v, want closed error", err)
	}

	wantEvents := []string{"credentials", "token-source", "token", "client", "mailboxes", "changes", "message", "close"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("lifecycle events = %#v, want %#v", events, wantEvents)
	}
}

func TestNewGraphConnectorAuthenticationFailuresStopLifecycle(t *testing.T) {
	sentinel := errors.New("sentinel")
	tests := []struct {
		name       string
		credential error
		token      error
		exchange   error
		wantOp     string
		wantEvents []string
	}{
		{name: "credential load", credential: sentinel, wantOp: "load Microsoft Graph credentials", wantEvents: []string{"credentials"}},
		{name: "token source", token: sentinel, wantOp: "acquire Microsoft Graph token", wantEvents: []string{"credentials", "token-source"}},
		{name: "token exchange", exchange: sentinel, wantOp: "authenticate Microsoft Graph", wantEvents: []string{"credentials", "token-source", "token"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make([]string, 0)
			credentials := &fakeGraphCredentialSource{credentials: GraphOAuthCredentials{ClientID: "id", ClientSecret: "secret"}, err: test.credential, events: &events}
			tokenSource := &fakeGraphTokenSource{token: &oauth2.Token{AccessToken: "token"}, err: test.exchange, events: &events}
			tokens := &fakeGraphTokenProvider{source: tokenSource, err: test.token, events: &events}
			factory := &fakeGraphClientFactory{client: &fakeGraphClient{events: &events}, events: &events}

			_, err := NewGraphConnector(context.Background(), validGraphConfig(), GraphDependencies{
				Credentials: credentials, Tokens: tokens, Clients: factory,
			})
			var mailErr *Error
			if !errors.As(err, &mailErr) || mailErr.Kind != ErrorAuthentication || mailErr.Operation != test.wantOp {
				t.Fatalf("NewGraphConnector() error = %T %v, want authentication operation %q", err, err, test.wantOp)
			}
			if !reflect.DeepEqual(events, test.wantEvents) {
				t.Fatalf("events = %#v, want %#v", events, test.wantEvents)
			}
		})
	}
}

func TestNewGraphConnectorClosesClientReturnedWithFactoryError(t *testing.T) {
	events := make([]string, 0)
	client := &fakeGraphClient{events: &events}
	factory := &fakeGraphClientFactory{client: client, err: errors.New("factory failed"), events: &events}
	_, err := NewGraphConnector(context.Background(), validGraphConfig(), GraphDependencies{
		Credentials: &fakeGraphCredentialSource{credentials: GraphOAuthCredentials{ClientID: "id", ClientSecret: "secret"}, events: &events},
		Tokens: &fakeGraphTokenProvider{source: &fakeGraphTokenSource{
			token: &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(time.Hour)}, events: &events,
		}, events: &events},
		Clients: factory,
	})
	if err == nil {
		t.Fatal("NewGraphConnector() error = nil, want factory failure")
	}
	if client.closeCalls != 1 {
		t.Fatalf("client Close calls = %d, want 1", client.closeCalls)
	}
	want := []string{"credentials", "token-source", "token", "client", "close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestNewGraphConnectorHonorsCanceledContextBeforeAuthentication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make([]string, 0)
	_, err := NewGraphConnector(ctx, validGraphConfig(), GraphDependencies{
		Credentials: &fakeGraphCredentialSource{events: &events},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewGraphConnector() error = %v, want context.Canceled", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no lifecycle activity", events)
	}
}

func validGraphConfig() GraphConnectorConfig {
	return GraphConnectorConfig{
		AccountID: "account", TenantID: "tenant", UserID: "user@example.com", CredentialRef: "secret/graph",
	}
}

type fakeGraphCredentialSource struct {
	credentials GraphOAuthCredentials
	reference   string
	err         error
	events      *[]string
}

func (f *fakeGraphCredentialSource) LoadCredentials(_ context.Context, reference string) (GraphOAuthCredentials, error) {
	*f.events = append(*f.events, "credentials")
	f.reference = reference
	return f.credentials, f.err
}

type fakeGraphTokenProvider struct {
	source      oauth2.TokenSource
	tenantID    string
	credentials GraphOAuthCredentials
	scopes      []string
	err         error
	events      *[]string
}

func (f *fakeGraphTokenProvider) TokenSource(_ context.Context, tenantID string, credentials GraphOAuthCredentials, scopes ...string) (oauth2.TokenSource, error) {
	*f.events = append(*f.events, "token-source")
	f.tenantID = tenantID
	f.credentials = credentials
	f.scopes = append([]string(nil), scopes...)
	return f.source, f.err
}

type fakeGraphTokenSource struct {
	token  *oauth2.Token
	err    error
	calls  int
	events *[]string
}

func (f *fakeGraphTokenSource) Token() (*oauth2.Token, error) {
	*f.events = append(*f.events, "token")
	f.calls++
	return f.token, f.err
}

type fakeGraphClientFactory struct {
	client  GraphClient
	tokens  oauth2.TokenSource
	baseURL string
	err     error
	events  *[]string
}

func (f *fakeGraphClientFactory) NewGraphClient(_ context.Context, tokens oauth2.TokenSource, baseURL string) (GraphClient, error) {
	*f.events = append(*f.events, "client")
	f.tokens = tokens
	f.baseURL = baseURL
	return f.client, f.err
}

type fakeGraphClient struct {
	mailboxes    []Mailbox
	page         ChangePage
	message      string
	changeUserID string
	changeLimit  int
	closeCalls   int
	events       *[]string
}

func (f *fakeGraphClient) ListMailboxes(_ context.Context, _ string) ([]Mailbox, error) {
	*f.events = append(*f.events, "mailboxes")
	return append([]Mailbox(nil), f.mailboxes...), nil
}

func (f *fakeGraphClient) ListChanges(_ context.Context, userID string, _ Mailbox, _ Cursor, limit int) (ChangePage, error) {
	*f.events = append(*f.events, "changes")
	f.changeUserID = userID
	f.changeLimit = limit
	return f.page, nil
}

func (f *fakeGraphClient) OpenMessage(_ context.Context, _, _ string, _ int64) (io.ReadCloser, int64, error) {
	*f.events = append(*f.events, "message")
	return io.NopCloser(strings.NewReader(f.message)), int64(len(f.message)), nil
}

func (f *fakeGraphClient) Close() error {
	*f.events = append(*f.events, "close")
	f.closeCalls++
	return nil
}
