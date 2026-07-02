package mail

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

var (
	_ GmailCredentialSource = (*fakeGmailCredentialSource)(nil)
	_ GmailTokenProvider    = (*fakeGmailTokenProvider)(nil)
	_ GmailClientFactory    = (*fakeGmailClientFactory)(nil)
	_ GmailClient           = (*fakeGmailClient)(nil)
)

func TestGmailConnectorFakeLifecycle(t *testing.T) {
	events := make([]string, 0, 8)
	credentials := &fakeGmailCredentialSource{
		credentials: GmailOAuthCredentials{JSON: []byte(`{"client_id":"test"}`)},
		events:      &events,
	}
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake-access-token"})
	tokens := &fakeGmailTokenProvider{tokens: tokenSource, events: &events}
	client := &fakeGmailClient{
		events:  &events,
		labels:  []GmailLabel{{ID: "TRASH", Name: "Trash"}, {ID: "INBOX", Name: "Inbox"}},
		profile: GmailProfile{HistoryID: 100},
		messagePages: map[string]GmailMessagePage{
			"": {
				Messages:      []GmailMessage{{ID: "m1", ThreadID: "t1", HistoryID: 90, Size: 12}},
				NextPageToken: "messages-page-2",
			},
			"messages-page-2": {Messages: []GmailMessage{{ID: "m2", ThreadID: "t2", HistoryID: 91, Size: 13}}},
		},
		historyPages: map[string]GmailHistoryPage{
			"": {
				Changes:       []GmailHistoryChange{{Kind: ChangeUpsert, Message: GmailMessage{ID: "m3", HistoryID: 101}}},
				HistoryID:     105,
				NextPageToken: "history-page-2",
			},
			"history-page-2": {
				Changes:   []GmailHistoryChange{{Kind: ChangeDelete, Message: GmailMessage{ID: "m1", HistoryID: 102}}},
				HistoryID: 105,
			},
		},
		rawMessages: map[string]GmailMessage{
			"m3": {
				ID:           "m3",
				ThreadID:     "t3",
				HistoryID:    105,
				InternalDate: time.Unix(1_700_000_000, 0),
				Size:         12,
				Raw:          base64.RawURLEncoding.EncodeToString([]byte("Subject: hi\r\n\r\nhello")),
			},
		},
	}
	factory := &fakeGmailClientFactory{client: client, events: &events}

	connector, err := NewGmailConnector(context.Background(), GmailConnectorConfig{
		AccountID:     "account-1",
		CredentialRef: "secret/gmail/account-1",
		Mailboxes:     MailboxSelector{Include: []string{"Inbox"}},
		Query:         "  after:2026/01/01 has:attachment  ",
	}, GmailDependencies{Credentials: credentials, Tokens: tokens, Clients: factory})
	if err != nil {
		t.Fatalf("NewGmailConnector() error = %v", err)
	}
	if credentials.reference != "secret/gmail/account-1" {
		t.Fatalf("credential reference = %q, want secret/gmail/account-1", credentials.reference)
	}
	if !reflect.DeepEqual(tokens.credentials, credentials.credentials) {
		t.Fatalf("token credentials = %#v, want %#v", tokens.credentials, credentials.credentials)
	}
	if !reflect.DeepEqual(tokens.scopes, []string{gmailScope}) {
		t.Fatalf("token scopes = %#v, want Gmail read-only scope", tokens.scopes)
	}
	if factory.tokens != tokenSource {
		t.Fatal("client factory did not receive fake token source")
	}

	mailboxes, err := connector.ListMailboxes(context.Background())
	if err != nil {
		t.Fatalf("ListMailboxes() error = %v", err)
	}
	wantMailboxes := []Mailbox{{ID: "INBOX", Name: "Inbox"}}
	if !reflect.DeepEqual(mailboxes, wantMailboxes) {
		t.Fatalf("ListMailboxes() = %#v, want %#v", mailboxes, wantMailboxes)
	}

	first, err := connector.ListChanges(context.Background(), mailboxes[0], Cursor{}, 1)
	if err != nil {
		t.Fatalf("first ListChanges() error = %v", err)
	}
	assertGmailChange(t, first, ChangeUpsert, "m1", true)
	second, err := connector.ListChanges(context.Background(), mailboxes[0], first.Next, 1)
	if err != nil {
		t.Fatalf("second ListChanges() error = %v", err)
	}
	assertGmailChange(t, second, ChangeUpsert, "m2", false)
	wantMessageCalls := []fakeGmailMessageCall{
		{UserID: gmailUserMe, LabelID: "INBOX", Query: "after:2026/01/01 has:attachment", Limit: 1},
		{UserID: gmailUserMe, LabelID: "INBOX", Query: "after:2026/01/01 has:attachment", PageToken: "messages-page-2", Limit: 1},
	}
	if !reflect.DeepEqual(client.messageCalls, wantMessageCalls) {
		t.Fatalf("message list calls = %#v, want %#v", client.messageCalls, wantMessageCalls)
	}

	historyFirst, err := connector.ListChanges(context.Background(), mailboxes[0], second.Next, 1)
	if err != nil {
		t.Fatalf("first history ListChanges() error = %v", err)
	}
	assertGmailChange(t, historyFirst, ChangeUpsert, "m3", true)
	historySecond, err := connector.ListChanges(context.Background(), mailboxes[0], historyFirst.Next, 1)
	if err != nil {
		t.Fatalf("second history ListChanges() error = %v", err)
	}
	assertGmailChange(t, historySecond, ChangeDelete, "m1", false)
	if !reflect.DeepEqual(client.historyStarts, []uint64{100, 100}) {
		t.Fatalf("history start IDs = %#v, want stable start ID across pages", client.historyStarts)
	}
	state, err := decodeGmailCursor(historySecond.Next.Token)
	if err != nil {
		t.Fatalf("decode final cursor: %v", err)
	}
	if historySecond.Next.HistoryID != 105 || state.PendingHistoryID != 0 || state.PageToken != "" {
		t.Fatalf("final history cursor = %#v with state %#v, want committed history 105", historySecond.Next, state)
	}

	raw, err := connector.OpenMessage(context.Background(), historyFirst.Changes[0].Ref, FetchOptions{IncludeBody: true, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("OpenMessage() error = %v", err)
	}
	data, err := io.ReadAll(raw.RFC822)
	if err != nil {
		t.Fatalf("read raw message: %v", err)
	}
	if err := raw.RFC822.Close(); err != nil {
		t.Fatalf("close raw message: %v", err)
	}
	if string(data) != "Subject: hi\r\n\r\nhello" {
		t.Fatalf("raw message = %q", data)
	}
	if raw.Ref.Provider != gmailProvider || raw.Ref.AccountID != "account-1" || raw.Ref.ProviderThreadID != "t3" {
		t.Fatalf("raw message ref = %#v", raw.Ref)
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

	wantPrefix := []string{"credentials", "tokens", "client", "labels", "profile", "messages:", "messages:messages-page-2"}
	if len(events) < len(wantPrefix) || !reflect.DeepEqual(events[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("lifecycle events = %#v, want prefix %#v", events, wantPrefix)
	}
}

func TestGmailConnectorResolvesDuplicateHistoryChangesAndDeletions(t *testing.T) {
	client := &fakeGmailClient{historyPages: map[string]GmailHistoryPage{
		"": {
			Changes: []GmailHistoryChange{
				{Kind: ChangeUpsert, Message: GmailMessage{ID: "m1", ThreadID: "old", HistoryID: 101}},
				{Kind: ChangeUpsert, Message: GmailMessage{ID: "m2", HistoryID: 102}},
				{Kind: ChangeUpsert, Message: GmailMessage{ID: "m1", ThreadID: "new", HistoryID: 103}},
				{Kind: ChangeDelete, Message: GmailMessage{ID: "m1", HistoryID: 104}},
				{Kind: ChangeDelete, Message: GmailMessage{ID: "m2", HistoryID: 105}},
				{Kind: ChangeDelete, Message: GmailMessage{ID: "m2", HistoryID: 105}},
			},
			HistoryID: 110,
		},
	}}
	connector := newFakeGmailConnector(t, client)

	page, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{HistoryID: 100}, 50)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if page.More || page.Next.HistoryID != 110 || len(page.Changes) != 2 {
		t.Fatalf("history page = %#v, want two resolved changes at history 110", page)
	}
	for i, messageID := range []string{"m1", "m2"} {
		change := page.Changes[i]
		if change.Kind != ChangeDelete || change.Ref.ProviderMessageID != messageID {
			t.Fatalf("change %d = %#v, want delete for %s", i, change, messageID)
		}
	}
}

func TestGmailConnectorExpiredHistoryCursorFallsBackToBootstrap(t *testing.T) {
	client := &fakeGmailClient{
		profile: GmailProfile{HistoryID: 200},
		historyErrors: map[string]error{
			"": &googleapi.Error{Code: http.StatusNotFound, Message: "Requested entity was not found"},
		},
		messagePages: map[string]GmailMessagePage{
			"": {Messages: []GmailMessage{{ID: "current"}}},
		},
	}
	connector := newFakeGmailConnector(t, client)
	mailbox := Mailbox{ID: "INBOX"}

	reset, err := connector.ListChanges(context.Background(), mailbox, Cursor{HistoryID: 100}, 25)
	if err != nil {
		t.Fatalf("expired ListChanges() error = %v", err)
	}
	if len(reset.Changes) != 1 || reset.Changes[0].Kind != ChangeReset || !reset.More || reset.Next.HistoryID != 200 {
		t.Fatalf("reset page = %#v", reset)
	}

	fallback, err := connector.ListChanges(context.Background(), mailbox, reset.Next, 25)
	if err != nil {
		t.Fatalf("fallback ListChanges() error = %v", err)
	}
	assertGmailChange(t, fallback, ChangeUpsert, "current", false)
	if fallback.Next.HistoryID != 200 {
		t.Fatalf("fallback history ID = %d, want 200", fallback.Next.HistoryID)
	}
}

func TestGmailConnectorRejectsInvalidHistoryPageID(t *testing.T) {
	connector := newFakeGmailConnector(t, &fakeGmailClient{historyPages: map[string]GmailHistoryPage{
		"": {Changes: []GmailHistoryChange{{Kind: ChangeUpsert, Message: GmailMessage{ID: "m1"}}}},
	}})

	_, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{HistoryID: 100}, 10)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorTransient || !strings.Contains(err.Error(), "invalid current history ID") {
		t.Fatalf("invalid history page error = %T %v, want transient invalid-history error", err, err)
	}
}

func TestGmailConnectorRejectsMissingCurrentHistoryID(t *testing.T) {
	connector := newFakeGmailConnector(t, &fakeGmailClient{profile: GmailProfile{}})

	_, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{}, 10)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorTransient || !strings.Contains(err.Error(), "valid history ID") {
		t.Fatalf("missing history ID error = %T %v, want transient invalid-history error", err, err)
	}
}

func TestNewGmailConnectorStopsAtFailedLifecycleStage(t *testing.T) {
	tests := []struct {
		name        string
		credentials *fakeGmailCredentialSource
		tokens      *fakeGmailTokenProvider
		factory     *fakeGmailClientFactory
		wantKind    ErrorKind
	}{
		{
			name:        "credentials",
			credentials: &fakeGmailCredentialSource{err: errors.New("vault unavailable")},
			tokens:      &fakeGmailTokenProvider{},
			factory:     &fakeGmailClientFactory{},
			wantKind:    ErrorAuthentication,
		},
		{
			name:        "token",
			credentials: &fakeGmailCredentialSource{credentials: GmailOAuthCredentials{JSON: []byte("fake")}},
			tokens:      &fakeGmailTokenProvider{err: errors.New("grant rejected")},
			factory:     &fakeGmailClientFactory{},
			wantKind:    ErrorAuthentication,
		},
		{
			name:        "client",
			credentials: &fakeGmailCredentialSource{credentials: GmailOAuthCredentials{JSON: []byte("fake")}},
			tokens:      &fakeGmailTokenProvider{tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake"})},
			factory:     &fakeGmailClientFactory{err: errors.New("API unavailable")},
			wantKind:    ErrorTransient,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewGmailConnector(context.Background(), GmailConnectorConfig{
				AccountID: "account", CredentialRef: "credential",
			}, GmailDependencies{Credentials: test.credentials, Tokens: test.tokens, Clients: test.factory})
			var mailErr *Error
			if !errors.As(err, &mailErr) || mailErr.Kind != test.wantKind {
				t.Fatalf("NewGmailConnector() error = %T %v, want kind %q", err, err, test.wantKind)
			}
		})
	}
}

func TestGoogleGmailClientListsAndRetrievesFakeResponses(t *testing.T) {
	var requests []struct {
		Path  string
		Query url.Values
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, struct {
			Path  string
			Query url.Values
		}{Path: r.URL.Path, Query: r.URL.Query()})
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			_, _ = io.WriteString(w, `{"messages":[{"id":"message-1","threadId":"thread-1"}],"nextPageToken":"next-page"}`)
		case "/gmail/v1/users/me/messages/message-1":
			_, _ = io.WriteString(w, `{"id":"message-1","threadId":"thread-1","historyId":"42","internalDate":"1767225600000","sizeEstimate":321,"raw":"U3ViamVjdDogdGVzdA0KDQpib2R5"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service, err := gmail.NewService(context.Background(), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("gmail.NewService() error = %v", err)
	}
	client := &googleGmailClient{service: service, httpClient: server.Client()}

	page, err := client.ListMessages(context.Background(), gmailUserMe, "IMPORTANT", "from:alerts@example.test", "page-2", 25)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	wantPage := GmailMessagePage{
		Messages:      []GmailMessage{{ID: "message-1", ThreadID: "thread-1"}},
		NextPageToken: "next-page",
	}
	if !reflect.DeepEqual(page, wantPage) {
		t.Fatalf("ListMessages() = %#v, want %#v", page, wantPage)
	}

	message, err := client.GetRawMessage(context.Background(), gmailUserMe, "message-1")
	if err != nil {
		t.Fatalf("GetRawMessage() error = %v", err)
	}
	if message.ID != "message-1" || message.ThreadID != "thread-1" || message.HistoryID != 42 || message.Size != 321 || message.Raw == "" {
		t.Fatalf("GetRawMessage() = %#v", message)
	}
	if wantDate := time.UnixMilli(1767225600000); !message.InternalDate.Equal(wantDate) {
		t.Fatalf("message internal date = %v, want %v", message.InternalDate, wantDate)
	}

	if len(requests) != 2 {
		t.Fatalf("requests = %#v, want two requests", requests)
	}
	listRequest := requests[0]
	if listRequest.Path != "/gmail/v1/users/me/messages" ||
		!reflect.DeepEqual(listRequest.Query["labelIds"], []string{"IMPORTANT"}) ||
		listRequest.Query.Get("q") != "from:alerts@example.test" ||
		listRequest.Query.Get("pageToken") != "page-2" ||
		listRequest.Query.Get("maxResults") != "25" {
		t.Fatalf("list request = %#v", listRequest)
	}
	getRequest := requests[1]
	if getRequest.Path != "/gmail/v1/users/me/messages/message-1" || getRequest.Query.Get("format") != "raw" {
		t.Fatalf("get request = %#v", getRequest)
	}
}

func TestGmailConnectorRenewsWatchAndReturnsExpiration(t *testing.T) {
	expiresAt := time.Date(2026, time.June, 18, 12, 0, 0, 0, time.UTC)
	client := &fakeGmailClient{watch: GmailWatch{HistoryID: 42, ExpiresAt: expiresAt}}
	connector := newFakeGmailConnector(t, client)

	watch, err := connector.RenewWatch(context.Background(), " projects/test/topics/mail ", []string{"INBOX", "IMPORTANT"})
	if err != nil {
		t.Fatalf("RenewWatch() error = %v", err)
	}
	if watch.HistoryID != 42 || watch.ExpiresAt != expiresAt {
		t.Fatalf("RenewWatch() = %#v", watch)
	}
	if client.watchUserID != gmailUserMe || client.watchTopic != "projects/test/topics/mail" || !reflect.DeepEqual(client.watchLabelIDs, []string{"INBOX", "IMPORTANT"}) {
		t.Fatalf("watch call = user %q topic %q labels %v", client.watchUserID, client.watchTopic, client.watchLabelIDs)
	}
}

func TestGmailConnectorRejectsInvalidCursorAndOversizedMessage(t *testing.T) {
	client := &fakeGmailClient{rawMessages: map[string]GmailMessage{
		"large": {ID: "large", Raw: base64.RawURLEncoding.EncodeToString([]byte("too large"))},
	}}
	connector := newFakeGmailConnector(t, client)

	_, err := connector.ListChanges(context.Background(), Mailbox{ID: "INBOX"}, Cursor{Token: "not-base64"}, 10)
	var mailErr *Error
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorCheckpointReset {
		t.Fatalf("invalid cursor error = %T %v, want checkpoint reset", err, err)
	}
	_, err = connector.OpenMessage(context.Background(), MessageRef{Provider: gmailProvider, ProviderMessageID: "large"}, FetchOptions{MaxBytes: 3})
	if !errors.As(err, &mailErr) || mailErr.Kind != ErrorOversized {
		t.Fatalf("oversized message error = %T %v, want oversized", err, err)
	}
}

func assertGmailChange(t *testing.T, page ChangePage, kind ChangeKind, messageID string, more bool) {
	t.Helper()
	if len(page.Changes) != 1 || page.Changes[0].Kind != kind || page.Changes[0].Ref.ProviderMessageID != messageID || page.More != more {
		t.Fatalf("change page = %#v, want one %s for %s and More=%v", page, kind, messageID, more)
	}
}

func newFakeGmailConnector(t *testing.T, client GmailClient) *GmailConnector {
	t.Helper()
	connector, err := NewGmailConnector(context.Background(), GmailConnectorConfig{
		AccountID: "account", CredentialRef: "credential",
	}, GmailDependencies{
		Credentials: &fakeGmailCredentialSource{credentials: GmailOAuthCredentials{JSON: []byte("fake")}},
		Tokens:      &fakeGmailTokenProvider{tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake"})},
		Clients:     &fakeGmailClientFactory{client: client},
	})
	if err != nil {
		t.Fatalf("NewGmailConnector() error = %v", err)
	}
	t.Cleanup(func() { _ = connector.Close() })
	return connector
}

type fakeGmailCredentialSource struct {
	credentials GmailOAuthCredentials
	reference   string
	err         error
	events      *[]string
}

func (f *fakeGmailCredentialSource) LoadCredentials(_ context.Context, reference string) (GmailOAuthCredentials, error) {
	f.reference = reference
	appendGmailEvent(f.events, "credentials")
	return f.credentials, f.err
}

type fakeGmailTokenProvider struct {
	credentials GmailOAuthCredentials
	scopes      []string
	tokens      oauth2.TokenSource
	err         error
	events      *[]string
}

func (f *fakeGmailTokenProvider) TokenSource(_ context.Context, credentials GmailOAuthCredentials, scopes ...string) (oauth2.TokenSource, error) {
	f.credentials = credentials
	f.scopes = append([]string(nil), scopes...)
	appendGmailEvent(f.events, "tokens")
	return f.tokens, f.err
}

type fakeGmailClientFactory struct {
	client GmailClient
	tokens oauth2.TokenSource
	err    error
	events *[]string
}

func (f *fakeGmailClientFactory) NewGmailClient(_ context.Context, tokens oauth2.TokenSource) (GmailClient, error) {
	f.tokens = tokens
	appendGmailEvent(f.events, "client")
	return f.client, f.err
}

type fakeGmailClient struct {
	labels        []GmailLabel
	profile       GmailProfile
	messagePages  map[string]GmailMessagePage
	historyPages  map[string]GmailHistoryPage
	historyErrors map[string]error
	rawMessages   map[string]GmailMessage
	watch         GmailWatch
	watchErr      error
	watchUserID   string
	watchTopic    string
	watchLabelIDs []string
	historyStarts []uint64
	messageCalls  []fakeGmailMessageCall
	closeCalls    int
	events        *[]string
}

func (f *fakeGmailClient) ListLabels(context.Context, string) ([]GmailLabel, error) {
	appendGmailEvent(f.events, "labels")
	return f.labels, nil
}

func (f *fakeGmailClient) GetProfile(context.Context, string) (GmailProfile, error) {
	appendGmailEvent(f.events, "profile")
	return f.profile, nil
}

func (f *fakeGmailClient) ListMessages(_ context.Context, userID, labelID, query, pageToken string, limit int) (GmailMessagePage, error) {
	f.messageCalls = append(f.messageCalls, fakeGmailMessageCall{
		UserID: userID, LabelID: labelID, Query: query, PageToken: pageToken, Limit: limit,
	})
	appendGmailEvent(f.events, "messages:"+pageToken)
	return f.messagePages[pageToken], nil
}

func (f *fakeGmailClient) ListHistory(_ context.Context, _, _ string, startHistoryID uint64, pageToken string, _ int) (GmailHistoryPage, error) {
	f.historyStarts = append(f.historyStarts, startHistoryID)
	appendGmailEvent(f.events, "history:"+pageToken)
	if err := f.historyErrors[pageToken]; err != nil {
		return GmailHistoryPage{}, err
	}
	return f.historyPages[pageToken], nil
}

func (f *fakeGmailClient) GetRawMessage(_ context.Context, _, messageID string) (GmailMessage, error) {
	appendGmailEvent(f.events, "raw:"+messageID)
	message, ok := f.rawMessages[messageID]
	if !ok {
		return GmailMessage{}, errors.New("missing fake message")
	}
	return message, nil
}

func (f *fakeGmailClient) Watch(_ context.Context, userID, topicName string, labelIDs []string) (GmailWatch, error) {
	f.watchUserID = userID
	f.watchTopic = topicName
	f.watchLabelIDs = append([]string(nil), labelIDs...)
	return f.watch, f.watchErr
}

func (f *fakeGmailClient) Close() error {
	f.closeCalls++
	appendGmailEvent(f.events, "close")
	return nil
}

type fakeGmailMessageCall struct {
	UserID    string
	LabelID   string
	Query     string
	PageToken string
	Limit     int
}

func appendGmailEvent(events *[]string, event string) {
	if events != nil {
		*events = append(*events, event)
	}
}
