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
	content := "Subject: Graph\r\n\r\nhello"
	client := &fakeGraphClient{
		folders: []GraphFolder{{ID: "inbox-id", Name: "Inbox"}, {ID: "archive-id", Name: "Archive"}},
		pages: map[string]GraphMessagePage{"": {
			Messages: []GraphMessage{{ID: "message-id", ThreadID: "thread-id"}}, DeltaCursor: "delta-token",
		}},
		messages: map[string]GraphMessage{"message-id": {ID: "message-id", ThreadID: "thread-id", RawContent: []byte(content)}},
		events:   &events,
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
	if len(client.messageCalls) != 1 || client.messageCalls[0] != (fakeGraphMessageCall{UserID: "reader@example.com", FolderID: "inbox-id", Limit: 25}) {
		t.Fatalf("message calls = %#v", client.messageCalls)
	}
	if page.Next.Token != "delta-token" || page.More {
		t.Fatalf("ListChanges() page = %#v, want terminal delta cursor", page)
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
	if string(data) != content || raw.Ref.Size != int64(len(content)) {
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

	wantEvents := []string{"credentials", "token-source", "token", "client", "folders", "messages:", "message:message-id", "close"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("lifecycle events = %#v, want %#v", events, wantEvents)
	}
}

func TestGraphConnectorListsFolderFilteredMessagePages(t *testing.T) {
	modified := time.Date(2026, time.June, 11, 10, 30, 0, 0, time.UTC)
	client := &fakeGraphClient{pages: map[string]GraphMessagePage{
		"": {
			Messages: []GraphMessage{{
				ID: "message-1", ThreadID: "thread-1", InternalDate: modified.Add(-time.Hour),
				ModifiedAt: modified, Size: 321,
			}},
			NextPageToken: "page-2",
		},
		"page-2": {
			Messages:    []GraphMessage{{ID: "message-2", Removed: true}, {ThreadID: "missing-id"}},
			DeltaCursor: "delta-cursor",
		},
	}}
	connector := &GraphConnector{config: GraphConnectorConfig{AccountID: "account", UserID: "user"}, client: client}
	mailbox := Mailbox{ID: "inbox-id", Name: "Inbox"}

	first, err := connector.ListChanges(context.Background(), mailbox, Cursor{}, 10)
	if err != nil {
		t.Fatalf("first ListChanges() error = %v", err)
	}
	if !first.More || first.Next.Token != "page-2" || len(first.Changes) != 1 {
		t.Fatalf("first ListChanges() = %#v", first)
	}
	wantRef := MessageRef{
		Provider: graphProvider, AccountID: "account", Mailbox: mailbox,
		ProviderMessageID: "message-1", ProviderThreadID: "thread-1",
		Version: modified.Format(time.RFC3339Nano), InternalDate: modified.Add(-time.Hour), Size: 321,
	}
	if first.Changes[0].Kind != ChangeUpsert || !reflect.DeepEqual(first.Changes[0].Ref, wantRef) {
		t.Fatalf("first change = %#v, want ref %#v", first.Changes[0], wantRef)
	}

	second, err := connector.ListChanges(context.Background(), mailbox, first.Next, 10)
	if err != nil {
		t.Fatalf("second ListChanges() error = %v", err)
	}
	if second.More || second.Next.Token != "delta-cursor" || len(second.Changes) != 1 || second.Changes[0].Kind != ChangeDelete {
		t.Fatalf("second ListChanges() = %#v", second)
	}
	wantCalls := []fakeGraphMessageCall{
		{UserID: "user", FolderID: "inbox-id", Limit: 10},
		{UserID: "user", FolderID: "inbox-id", PageToken: "page-2", Limit: 10},
	}
	if !reflect.DeepEqual(client.messageCalls, wantCalls) {
		t.Fatalf("message calls = %#v, want %#v", client.messageCalls, wantCalls)
	}
}

func TestGraphConnectorFiltersFolders(t *testing.T) {
	client := &fakeGraphClient{folders: []GraphFolder{
		{ID: "inbox", Name: "Inbox"}, {ID: "archive", Name: "Archive"},
		{ID: "junk", Name: "Junk"}, {Name: "missing ID"},
	}}
	connector := &GraphConnector{config: GraphConnectorConfig{
		UserID: "user", Mailboxes: MailboxSelector{Include: []string{"inbox", "Archive"}, Exclude: []string{"archive"}},
	}, client: client}

	mailboxes, err := connector.ListMailboxes(context.Background())
	if err != nil {
		t.Fatalf("ListMailboxes() error = %v", err)
	}
	if want := []Mailbox{{ID: "inbox", Name: "Inbox"}}; !reflect.DeepEqual(mailboxes, want) {
		t.Fatalf("ListMailboxes() = %#v, want %#v", mailboxes, want)
	}
}

func TestGraphConnectorRetrievesBodyOrRawContent(t *testing.T) {
	client := &fakeGraphClient{messages: map[string]GraphMessage{
		"body":  {ID: "body", ThreadID: "thread", Body: []byte("body content")},
		"raw":   {ID: "raw", Body: []byte("fallback"), RawContent: []byte("raw content")},
		"empty": {ID: "empty"},
	}}
	connector := &GraphConnector{config: GraphConnectorConfig{AccountID: "account", UserID: "user"}, client: client}

	for _, test := range []struct{ id, want string }{{"body", "body content"}, {"raw", "raw content"}} {
		t.Run(test.id, func(t *testing.T) {
			raw, err := connector.OpenMessage(context.Background(), MessageRef{Mailbox: Mailbox{ID: "inbox"}, ProviderMessageID: test.id}, FetchOptions{IncludeBody: true})
			if err != nil {
				t.Fatalf("OpenMessage() error = %v", err)
			}
			defer raw.RFC822.Close()
			content, err := io.ReadAll(raw.RFC822)
			if err != nil {
				t.Fatalf("read content: %v", err)
			}
			if string(content) != test.want || raw.Ref.Provider != graphProvider || raw.Ref.AccountID != "account" {
				t.Fatalf("OpenMessage() = %q, %#v", content, raw.Ref)
			}
			if test.id == "body" && raw.Ref.ProviderThreadID != "thread" {
				t.Fatalf("body message ref = %#v, want provider thread ID", raw.Ref)
			}
		})
	}

	_, err := connector.OpenMessage(context.Background(), MessageRef{ProviderMessageID: "empty"}, FetchOptions{IncludeBody: true})
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorMalformed || !strings.Contains(err.Error(), "body or raw content") {
		t.Fatalf("missing content error = %T %v, want malformed", err, err)
	}
}

func TestGraphConnectorPropagatesInternalClientErrors(t *testing.T) {
	sentinel := errors.New("provider failed")
	tests := []struct {
		name   string
		client *fakeGraphClient
		call   func(*GraphConnector) error
		wantOp string
	}{
		{name: "folders", client: &fakeGraphClient{folderErr: sentinel}, call: func(c *GraphConnector) error { _, err := c.ListMailboxes(context.Background()); return err }, wantOp: "list Microsoft Graph mailboxes"},
		{name: "messages", client: &fakeGraphClient{pageErrors: map[string]error{"cursor": sentinel}}, call: func(c *GraphConnector) error {
			_, err := c.ListChanges(context.Background(), Mailbox{ID: "inbox"}, Cursor{Token: "cursor"}, 5)
			return err
		}, wantOp: "list Microsoft Graph messages"},
		{name: "content", client: &fakeGraphClient{messageErrors: map[string]error{"message": sentinel}}, call: func(c *GraphConnector) error {
			_, err := c.OpenMessage(context.Background(), MessageRef{ProviderMessageID: "message"}, FetchOptions{IncludeBody: true})
			return err
		}, wantOp: "get Microsoft Graph message"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := &GraphConnector{config: GraphConnectorConfig{AccountID: "account", UserID: "user"}, client: test.client}
			err := test.call(connector)
			var mailErr *Error
			if !errors.As(err, &mailErr) || mailErr.Kind != ErrorTransient || mailErr.Operation != test.wantOp || !errors.Is(err, sentinel) {
				t.Fatalf("error = %T %v, want transient operation %q", err, err, test.wantOp)
			}
		})
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
	folders       []GraphFolder
	pages         map[string]GraphMessagePage
	messages      map[string]GraphMessage
	folderErr     error
	pageErrors    map[string]error
	messageErrors map[string]error
	messageCalls  []fakeGraphMessageCall
	closeCalls    int
	events        *[]string
}

type fakeGraphMessageCall struct {
	UserID    string
	FolderID  string
	PageToken string
	Limit     int
}

func (f *fakeGraphClient) ListFolders(_ context.Context, _ string) ([]GraphFolder, error) {
	appendGraphEvent(f.events, "folders")
	return append([]GraphFolder(nil), f.folders...), f.folderErr
}

func (f *fakeGraphClient) ListMessages(_ context.Context, userID, folderID, pageToken string, limit int) (GraphMessagePage, error) {
	appendGraphEvent(f.events, "messages:"+pageToken)
	f.messageCalls = append(f.messageCalls, fakeGraphMessageCall{UserID: userID, FolderID: folderID, PageToken: pageToken, Limit: limit})
	if err := f.pageErrors[pageToken]; err != nil {
		return GraphMessagePage{}, err
	}
	return f.pages[pageToken], nil
}

func (f *fakeGraphClient) GetMessage(_ context.Context, _, messageID string) (GraphMessage, error) {
	appendGraphEvent(f.events, "message:"+messageID)
	if err := f.messageErrors[messageID]; err != nil {
		return GraphMessage{}, err
	}
	return f.messages[messageID], nil
}

func (f *fakeGraphClient) Close() error {
	appendGraphEvent(f.events, "close")
	f.closeCalls++
	return nil
}

func appendGraphEvent(events *[]string, event string) {
	if events != nil {
		*events = append(*events, event)
	}
}
