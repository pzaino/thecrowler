package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	gmailProvider = "gmail"
	gmailScope    = gmail.GmailReadonlyScope
	gmailUserMe   = "me"
)

// GmailConnectorConfig configures a read-only Gmail connector. CredentialRef
// identifies secret material in the configured GmailCredentialSource; it must
// not contain OAuth credentials or tokens itself. Query uses Gmail search
// syntax to filter bootstrap message listings within each selected label.
type GmailConnectorConfig struct {
	AccountID     string
	UserID        string
	CredentialRef string
	ProxyURL      string
	Mailboxes     MailboxSelector
	Query         string
}

// GmailOAuthCredentials contains opaque OAuth credential JSON loaded from a
// secret store. JSON is intentionally interpreted only by the token provider.
type GmailOAuthCredentials struct {
	JSON []byte
}

// GmailCredentialSource resolves an external credential reference. Production
// implementations should read from the application's secret manager rather
// than source configuration.
type GmailCredentialSource interface {
	LoadCredentials(ctx context.Context, reference string) (GmailOAuthCredentials, error)
}

// GmailTokenProvider converts OAuth credentials into a refreshing token
// source. The connector never handles access or refresh token strings itself.
type GmailTokenProvider interface {
	TokenSource(ctx context.Context, credentials GmailOAuthCredentials, scopes ...string) (oauth2.TokenSource, error)
}

// GmailClientFactory creates the provider-call boundary from an authorized
// token source.
type GmailClientFactory interface {
	NewGmailClient(ctx context.Context, tokens oauth2.TokenSource) (GmailClient, error)
}

// GmailClient is the small subset of Gmail operations needed by Connector.
// It prevents Gmail SDK objects from leaking into connector logic or tests and
// keeps label, query, and page-token handling behind one internal boundary.
type GmailClient interface {
	ListLabels(ctx context.Context, userID string) ([]GmailLabel, error)
	GetProfile(ctx context.Context, userID string) (GmailProfile, error)
	ListMessages(ctx context.Context, userID, labelID, query, pageToken string, limit int) (GmailMessagePage, error)
	ListHistory(ctx context.Context, userID, labelID string, startHistoryID uint64, pageToken string, limit int) (GmailHistoryPage, error)
	GetRawMessage(ctx context.Context, userID, messageID string) (GmailMessage, error)
	Watch(ctx context.Context, userID, topicName string, labelIDs []string) (GmailWatch, error)
	Close() error
}

// GmailLabel is the provider-neutral subset of a Gmail label.
type GmailLabel struct {
	ID   string
	Name string
}

// GmailProfile contains the mailbox history position needed for bootstrapping.
type GmailProfile struct {
	HistoryID uint64
}

// GmailWatch contains the provider values returned when a Gmail push watch is registered.
type GmailWatch struct {
	HistoryID uint64
	ExpiresAt time.Time
}

// GmailMessage contains Gmail identity and raw-message metadata.
type GmailMessage struct {
	ID           string
	ThreadID     string
	HistoryID    uint64
	InternalDate time.Time
	Size         int64
	Raw          string
}

// GmailMessagePage is one page of label membership.
type GmailMessagePage struct {
	Messages      []GmailMessage
	NextPageToken string
}

// GmailHistoryChange describes one message mutation in Gmail history.
type GmailHistoryChange struct {
	Kind    ChangeKind
	Message GmailMessage
}

// GmailHistoryPage is one page of Gmail history records.
type GmailHistoryPage struct {
	Changes       []GmailHistoryChange
	HistoryID     uint64
	NextPageToken string
}

// GmailDependencies contains the replaceable credential, OAuth, and provider
// boundaries used during connector construction.
type GmailDependencies struct {
	Credentials GmailCredentialSource
	Tokens      GmailTokenProvider
	Clients     GmailClientFactory
}

// GoogleGmailTokenProvider acquires refreshing OAuth tokens using Google's
// credential JSON support.
type GoogleGmailTokenProvider struct{}

// TokenSource implements GmailTokenProvider.
func (GoogleGmailTokenProvider) TokenSource(ctx context.Context, credentials GmailOAuthCredentials, scopes ...string) (oauth2.TokenSource, error) {
	if len(credentials.JSON) == 0 {
		return nil, errors.New("mail: Gmail OAuth credential JSON is empty")
	}
	config, err := google.CredentialsFromJSON(ctx, credentials.JSON, scopes...)
	if err != nil {
		return nil, fmt.Errorf("mail: parse Gmail OAuth credentials: %w", err)
	}
	return config.TokenSource, nil
}

// GoogleGmailClientFactory creates a GmailClient backed by the official Google
// API client.
type GoogleGmailClientFactory struct{}

// NewGmailClient implements GmailClientFactory.
func (GoogleGmailClientFactory) NewGmailClient(ctx context.Context, tokens oauth2.TokenSource) (GmailClient, error) {
	if tokens == nil {
		return nil, errors.New("mail: Gmail OAuth token source is nil")
	}
	httpClient := oauth2.NewClient(ctx, tokens)
	service, err := gmail.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("mail: create Gmail API client: %w", err)
	}
	return &googleGmailClient{service: service, httpClient: httpClient}, nil
}

// GmailConnector implements Connector with the Gmail labels, messages, and
// history APIs.
type GmailConnector struct {
	mu        sync.Mutex
	closeOnce sync.Once
	config    GmailConnectorConfig
	client    GmailClient
	closed    bool
	closeErr  error
}

var (
	_ Connector          = (*GmailConnector)(nil)
	_ io.Closer          = (*GmailConnector)(nil)
	_ GmailTokenProvider = GoogleGmailTokenProvider{}
	_ GmailClientFactory = GoogleGmailClientFactory{}
	_ GmailClient        = (*googleGmailClient)(nil)
)

// NewGmailConnector validates configuration, resolves OAuth credentials,
// acquires a refreshing token source, and creates the Gmail API client.
func NewGmailConnector(ctx context.Context, config GmailConnectorConfig, dependencies GmailDependencies) (*GmailConnector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.UserID = strings.TrimSpace(config.UserID)
	config.CredentialRef = strings.TrimSpace(config.CredentialRef)
	config.Query = strings.TrimSpace(config.Query)
	config.ProxyURL = strings.TrimSpace(config.ProxyURL)
	var err error
	ctx, err = contextWithMailProxy(ctx, config.ProxyURL)
	if err != nil {
		return nil, err
	}
	if config.AccountID == "" {
		return nil, errors.New("mail: Gmail account ID is required")
	}
	if config.UserID == "" {
		config.UserID = gmailUserMe
	}
	if config.CredentialRef == "" {
		return nil, errors.New("mail: Gmail credential reference is required")
	}
	if dependencies.Credentials == nil {
		return nil, errors.New("mail: Gmail credential source is required")
	}
	if dependencies.Tokens == nil {
		dependencies.Tokens = GoogleGmailTokenProvider{}
	}
	if dependencies.Clients == nil {
		dependencies.Clients = GoogleGmailClientFactory{}
	}
	credentials, err := dependencies.Credentials.LoadCredentials(ctx, config.CredentialRef)
	if err != nil {
		return nil, &Error{Kind: ErrorAuthentication, Operation: "load Gmail credentials", Message: "OAuth credentials are unavailable", Cause: err}
	}
	tokens, err := dependencies.Tokens.TokenSource(ctx, credentials, gmailScope)
	if err != nil {
		return nil, &Error{Kind: ErrorAuthentication, Operation: "acquire Gmail token", Message: "OAuth token acquisition failed", Cause: err}
	}
	client, err := dependencies.Clients.NewGmailClient(ctx, tokens)
	if err != nil {
		return nil, gmailError("create Gmail client", err)
	}
	if client == nil {
		return nil, errors.New("mail: Gmail client factory returned nil")
	}
	return &GmailConnector{config: config, client: client}, nil
}

// ListMailboxes lists Gmail labels selected by the configured exact names or
// IDs. Exclusions take precedence.
func (c *GmailConnector) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	labels, err := c.client.ListLabels(ctx, c.config.UserID)
	if err != nil {
		return nil, gmailError("list Gmail labels", err)
	}
	mailboxes := make([]Mailbox, 0, len(labels))
	for _, label := range labels {
		if label.ID == "" || !gmailMailboxSelected(label, c.config.Mailboxes) {
			continue
		}
		name := label.Name
		if name == "" {
			name = label.ID
		}
		mailboxes = append(mailboxes, Mailbox{ID: label.ID, Name: name})
	}
	sort.Slice(mailboxes, func(i, j int) bool {
		if mailboxes[i].Name == mailboxes[j].Name {
			return mailboxes[i].ID < mailboxes[j].ID
		}
		return mailboxes[i].Name < mailboxes[j].Name
	})
	return mailboxes, nil
}

// ListChanges bootstraps a label from messages.list, then advances it with the
// Gmail history API. The opaque cursor retains bootstrap pagination and the
// stable history position captured before bootstrap began.
func (c *GmailConnector) ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return ChangePage{}, err
	}
	if strings.TrimSpace(mailbox.ID) == "" {
		return ChangePage{}, &Error{Kind: ErrorMailboxNotFound, Operation: "list Gmail changes", Message: "Gmail label ID is required"}
	}
	if limit <= 0 {
		return ChangePage{}, errors.New("mail: Gmail change limit must be greater than zero")
	}
	state, err := decodeGmailCursor(cursor.Token)
	if err != nil {
		return ChangePage{}, &Error{Kind: ErrorCheckpointReset, Operation: "list Gmail changes", Message: "Gmail cursor is invalid", Cause: err}
	}

	historyID := cursor.HistoryID
	// Read cursors written by the original Gmail implementation, which embedded
	// the durable history ID in Token. New cursors persist it explicitly.
	if historyID == 0 && state.LegacyHistoryID != 0 {
		historyID = state.LegacyHistoryID
	}
	state.LegacyHistoryID = 0
	if historyID == 0 && cursor.Token != "" {
		return ChangePage{}, &Error{Kind: ErrorCheckpointReset, Operation: "list Gmail changes", Message: "Gmail cursor does not include a history ID"}
	}
	if historyID == 0 {
		profile, profileErr := c.currentProfile(ctx)
		if profileErr != nil {
			return ChangePage{}, profileErr
		}
		historyID = profile.HistoryID
		state.Bootstrap = true
	}
	if state.Bootstrap {
		return c.listBootstrapPage(ctx, mailbox, historyID, state, limit)
	}

	page, err := c.client.ListHistory(ctx, c.config.UserID, mailbox.ID, historyID, state.PageToken, limit)
	if err != nil {
		if gmailHistoryCursorExpired(err) {
			profile, profileErr := c.currentProfile(ctx)
			if profileErr != nil {
				return ChangePage{}, profileErr
			}
			resetState := gmailCursor{Bootstrap: true}
			token, encodeErr := encodeGmailCursor(resetState)
			if encodeErr != nil {
				return ChangePage{}, encodeErr
			}
			return ChangePage{
				Changes: []Change{{Kind: ChangeReset}},
				Next:    Cursor{Token: token, HistoryID: profile.HistoryID},
				More:    true,
			}, nil
		}
		return ChangePage{}, gmailError("list Gmail history", err)
	}

	if page.HistoryID == 0 {
		return ChangePage{}, &Error{Kind: ErrorTransient, Operation: "list Gmail history", Message: "Gmail returned an invalid current history ID"}
	}
	changes := c.resolveHistoryChanges(mailbox, page.Changes)
	if page.HistoryID > state.PendingHistoryID {
		state.PendingHistoryID = page.HistoryID
	}
	state.PageToken = page.NextPageToken
	nextHistoryID := historyID
	if state.PageToken == "" {
		if state.PendingHistoryID > nextHistoryID {
			nextHistoryID = state.PendingHistoryID
		}
		state.PendingHistoryID = 0
	}
	next, err := encodeGmailCursor(state)
	if err != nil {
		return ChangePage{}, err
	}
	return ChangePage{Changes: changes, Next: Cursor{Token: next, HistoryID: nextHistoryID}, More: page.NextPageToken != ""}, nil
}

func (c *GmailConnector) currentProfile(ctx context.Context) (GmailProfile, error) {
	profile, err := c.client.GetProfile(ctx, c.config.UserID)
	if err != nil {
		return GmailProfile{}, gmailError("get Gmail profile", err)
	}
	if profile.HistoryID == 0 {
		return GmailProfile{}, &Error{Kind: ErrorTransient, Operation: "get Gmail profile", Message: "Gmail profile did not include a valid history ID"}
	}
	return profile, nil
}

func (c *GmailConnector) listBootstrapPage(ctx context.Context, mailbox Mailbox, historyID uint64, state gmailCursor, limit int) (ChangePage, error) {
	page, err := c.client.ListMessages(ctx, c.config.UserID, mailbox.ID, c.config.Query, state.PageToken, limit)
	if err != nil {
		return ChangePage{}, gmailError("list Gmail messages", err)
	}
	changes := make([]Change, 0, len(page.Messages))
	seen := make(map[string]struct{}, len(page.Messages))
	for _, message := range page.Messages {
		if message.ID == "" {
			continue
		}
		if _, duplicate := seen[message.ID]; duplicate {
			continue
		}
		seen[message.ID] = struct{}{}
		changes = append(changes, Change{Kind: ChangeUpsert, Ref: c.messageRef(mailbox, message)})
	}
	state.PageToken = page.NextPageToken
	state.Bootstrap = page.NextPageToken != ""
	next, encodeErr := encodeGmailCursor(state)
	if encodeErr != nil {
		return ChangePage{}, encodeErr
	}
	return ChangePage{Changes: changes, Next: Cursor{Token: next, HistoryID: historyID}, More: state.Bootstrap}, nil
}

func (c *GmailConnector) resolveHistoryChanges(mailbox Mailbox, historyChanges []GmailHistoryChange) []Change {
	changes := make([]Change, 0, len(historyChanges))
	positions := make(map[string]int, len(historyChanges))
	for _, historyChange := range historyChanges {
		messageID := historyChange.Message.ID
		if messageID == "" || (historyChange.Kind != ChangeUpsert && historyChange.Kind != ChangeDelete) {
			continue
		}
		change := Change{Kind: historyChange.Kind, Ref: c.messageRef(mailbox, historyChange.Message)}
		if position, duplicate := positions[messageID]; duplicate {
			// Gmail can report the same message in messagesAdded/labelsAdded (or
			// their deletion counterparts). Preserve one reference and let the
			// last mutation in history order determine its final kind.
			changes[position] = change
			continue
		}
		positions[messageID] = len(changes)
		changes = append(changes, change)
	}
	return changes
}

// OpenMessage retrieves one complete RFC 5322 message with Gmail's raw format.
func (c *GmailConnector) OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return RawMessage{}, err
	}
	if ref.Provider != "" && ref.Provider != gmailProvider {
		return RawMessage{}, errors.New("mail: message reference is not a Gmail message")
	}
	if ref.ProviderMessageID == "" {
		return RawMessage{}, errors.New("mail: Gmail message ID is required")
	}
	message, err := c.client.GetRawMessage(ctx, c.config.UserID, ref.ProviderMessageID)
	if err != nil {
		return RawMessage{}, gmailError("get Gmail message", err)
	}
	data, err := base64.RawURLEncoding.DecodeString(message.Raw)
	if err != nil {
		return RawMessage{}, &Error{Kind: ErrorMalformed, Operation: "decode Gmail message", Message: "Gmail returned invalid raw message data", Cause: err}
	}
	if options.MaxBytes > 0 && int64(len(data)) > options.MaxBytes {
		return RawMessage{}, &Error{Kind: ErrorOversized, Operation: "get Gmail message", Message: "message exceeds configured byte limit"}
	}
	return RawMessage{Ref: c.messageRef(ref.Mailbox, message), RFC822: io.NopCloser(bytes.NewReader(data))}, nil
}

// RenewWatch registers or renews the Gmail push watch for the configured user.
// The returned expiration is authoritative and should be persisted before the
// next renewal is scheduled.
func (c *GmailConnector) RenewWatch(ctx context.Context, topicName string, labelIDs []string) (GmailWatch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return GmailWatch{}, err
	}
	topicName = strings.TrimSpace(topicName)
	if topicName == "" {
		return GmailWatch{}, errors.New("mail: Gmail watch topic name is required")
	}
	watch, err := c.client.Watch(ctx, c.config.UserID, topicName, append([]string(nil), labelIDs...))
	if err != nil {
		return GmailWatch{}, gmailError("renew Gmail watch", err)
	}
	if watch.ExpiresAt.IsZero() {
		return GmailWatch{}, errors.New("mail: Gmail watch response has no expiration")
	}
	watch.ExpiresAt = watch.ExpiresAt.UTC()
	return watch, nil
}

// Close releases provider HTTP resources. It is safe to call repeatedly.
func (c *GmailConnector) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		c.closeErr = c.client.Close()
	})
	return c.closeErr
}

func (c *GmailConnector) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed {
		return errors.New("mail: Gmail connector is closed")
	}
	return nil
}

func (c *GmailConnector) messageRef(mailbox Mailbox, message GmailMessage) MessageRef {
	return MessageRef{
		Provider:          gmailProvider,
		AccountID:         c.config.AccountID,
		Mailbox:           mailbox,
		ProviderMessageID: message.ID,
		ProviderThreadID:  message.ThreadID,
		Version:           strconv.FormatUint(message.HistoryID, 10),
		InternalDate:      message.InternalDate,
		Size:              message.Size,
	}
}

type gmailCursor struct {
	LegacyHistoryID  uint64 `json:"history_id,omitempty"`
	PendingHistoryID uint64 `json:"pending_history_id,omitempty"`
	PageToken        string `json:"page_token,omitempty"`
	Bootstrap        bool   `json:"bootstrap,omitempty"`
}

func encodeGmailCursor(cursor gmailCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("mail: encode Gmail cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeGmailCursor(token string) (gmailCursor, error) {
	if token == "" {
		return gmailCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return gmailCursor{}, err
	}
	var cursor gmailCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return gmailCursor{}, err
	}
	return cursor, nil
}

func gmailMailboxSelected(label GmailLabel, selector MailboxSelector) bool {
	matches := func(values []string) bool {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" && (value == label.ID || value == label.Name) {
				return true
			}
		}
		return false
	}
	if matches(selector.Exclude) {
		return false
	}
	return len(selector.Include) == 0 || matches(selector.Include)
}

func gmailHistoryCursorExpired(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound
}

func gmailError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	kind := ErrorTransient
	message := "Gmail request failed"
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusUnauthorized:
			kind, message = ErrorAuthentication, "Gmail rejected OAuth credentials"
		case http.StatusForbidden:
			kind, message = ErrorPermission, "Gmail denied the requested operation"
		case http.StatusNotFound:
			if operation == "list Gmail history" {
				kind, message = ErrorCheckpointReset, "Gmail history cursor is no longer available"
			} else {
				kind, message = ErrorMessageNotFound, "Gmail message was not found"
			}
		case http.StatusTooManyRequests:
			kind, message = ErrorRateLimit, "Gmail rate limit exceeded"
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			kind, message = ErrorTimeout, "Gmail request timed out"
		default:
			if apiErr.Code >= 500 {
				kind, message = ErrorTransient, "Gmail service is temporarily unavailable"
			}
		}
	} else {
		var networkErr net.Error
		if errors.As(err, &networkErr) {
			kind = ErrorNetwork
			message = "Gmail network request failed"
			if networkErr.Timeout() {
				kind, message = ErrorTimeout, "Gmail request timed out"
			}
		}
	}
	return &Error{Kind: kind, Operation: operation, Message: message, Cause: err}
}

type googleGmailClient struct {
	service    *gmail.Service
	httpClient *http.Client
}

func (c *googleGmailClient) ListLabels(ctx context.Context, userID string) ([]GmailLabel, error) {
	response, err := c.service.Users.Labels.List(userID).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	labels := make([]GmailLabel, 0, len(response.Labels))
	for _, label := range response.Labels {
		if label != nil {
			labels = append(labels, GmailLabel{ID: label.Id, Name: label.Name})
		}
	}
	return labels, nil
}

func (c *googleGmailClient) GetProfile(ctx context.Context, userID string) (GmailProfile, error) {
	profile, err := c.service.Users.GetProfile(userID).Context(ctx).Do()
	if err != nil {
		return GmailProfile{}, err
	}
	return GmailProfile{HistoryID: profile.HistoryId}, nil
}

func (c *googleGmailClient) ListMessages(ctx context.Context, userID, labelID, query, pageToken string, limit int) (GmailMessagePage, error) {
	call := c.service.Users.Messages.List(userID).LabelIds(labelID).MaxResults(int64(limit)).Context(ctx)
	if query != "" {
		call = call.Q(query)
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	response, err := call.Do()
	if err != nil {
		return GmailMessagePage{}, err
	}
	messages := make([]GmailMessage, 0, len(response.Messages))
	for _, message := range response.Messages {
		if message != nil {
			messages = append(messages, gmailMessageFromAPI(message))
		}
	}
	return GmailMessagePage{Messages: messages, NextPageToken: response.NextPageToken}, nil
}

func (c *googleGmailClient) ListHistory(ctx context.Context, userID, labelID string, startHistoryID uint64, pageToken string, limit int) (GmailHistoryPage, error) {
	call := c.service.Users.History.List(userID).StartHistoryId(startHistoryID).LabelId(labelID).MaxResults(int64(limit)).Context(ctx)
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	response, err := call.Do()
	if err != nil {
		return GmailHistoryPage{}, err
	}
	changes := make([]GmailHistoryChange, 0)
	for _, history := range response.History {
		if history == nil {
			continue
		}
		for _, added := range history.MessagesAdded {
			if added != nil && added.Message != nil {
				changes = append(changes, GmailHistoryChange{Kind: ChangeUpsert, Message: gmailMessageFromAPI(added.Message)})
			}
		}
		for _, deleted := range history.MessagesDeleted {
			if deleted != nil && deleted.Message != nil {
				changes = append(changes, GmailHistoryChange{Kind: ChangeDelete, Message: gmailMessageFromAPI(deleted.Message)})
			}
		}
		for _, added := range history.LabelsAdded {
			if added != nil && added.Message != nil && containsString(added.LabelIds, labelID) {
				changes = append(changes, GmailHistoryChange{Kind: ChangeUpsert, Message: gmailMessageFromAPI(added.Message)})
			}
		}
		for _, removed := range history.LabelsRemoved {
			if removed != nil && removed.Message != nil && containsString(removed.LabelIds, labelID) {
				changes = append(changes, GmailHistoryChange{Kind: ChangeDelete, Message: gmailMessageFromAPI(removed.Message)})
			}
		}
	}
	return GmailHistoryPage{Changes: changes, HistoryID: response.HistoryId, NextPageToken: response.NextPageToken}, nil
}

func (c *googleGmailClient) GetRawMessage(ctx context.Context, userID, messageID string) (GmailMessage, error) {
	message, err := c.service.Users.Messages.Get(userID, messageID).Format("raw").Context(ctx).Do()
	if err != nil {
		return GmailMessage{}, err
	}
	return gmailMessageFromAPI(message), nil
}

func (c *googleGmailClient) Watch(ctx context.Context, userID, topicName string, labelIDs []string) (GmailWatch, error) {
	request := &gmail.WatchRequest{TopicName: topicName, LabelIds: append([]string(nil), labelIDs...)}
	response, err := c.service.Users.Watch(userID, request).Context(ctx).Do()
	if err != nil {
		return GmailWatch{}, err
	}
	watch := GmailWatch{HistoryID: response.HistoryId}
	if response.Expiration > 0 {
		watch.ExpiresAt = time.UnixMilli(response.Expiration).UTC()
	}
	return watch, nil
}

func (c *googleGmailClient) Close() error {
	if c.httpClient != nil {
		if closer, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
	return nil
}

func gmailMessageFromAPI(message *gmail.Message) GmailMessage {
	if message == nil {
		return GmailMessage{}
	}
	var internalDate time.Time
	if message.InternalDate > 0 {
		internalDate = time.UnixMilli(message.InternalDate)
	}
	return GmailMessage{
		ID:           message.Id,
		ThreadID:     message.ThreadId,
		HistoryID:    message.HistoryId,
		InternalDate: internalDate,
		Size:         message.SizeEstimate,
		Raw:          message.Raw,
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
