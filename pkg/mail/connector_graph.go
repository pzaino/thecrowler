package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	graphProvider   = "graph"
	graphDefaultURL = "https://graph.microsoft.com/v1.0"
	graphScope      = "https://graph.microsoft.com/.default"
)

// GraphConnectorConfig configures read-only Microsoft Graph mail access.
// CredentialRef identifies application credentials in GraphCredentialSource;
// source configuration must not contain client secrets or access tokens.
type GraphConnectorConfig struct {
	AccountID     string
	TenantID      string
	UserID        string
	CredentialRef string
	Mailboxes     MailboxSelector
	BaseURL       string
}

// GraphOAuthCredentials contains the application identity loaded from a secret
// store. ClientSecret must never be written to source configuration or logs.
type GraphOAuthCredentials struct {
	ClientID     string
	ClientSecret string
}

// GraphCredentialSource resolves an external Microsoft identity credential.
type GraphCredentialSource interface {
	LoadCredentials(ctx context.Context, reference string) (GraphOAuthCredentials, error)
}

// GraphTokenProvider creates a refreshing OAuth token source. Keeping token
// acquisition behind this interface makes authentication independently fakeable.
type GraphTokenProvider interface {
	TokenSource(ctx context.Context, tenantID string, credentials GraphOAuthCredentials, scopes ...string) (oauth2.TokenSource, error)
}

// GraphClientFactory creates the replaceable Microsoft Graph operation boundary.
type GraphClientFactory interface {
	NewGraphClient(ctx context.Context, tokens oauth2.TokenSource, baseURL string) (GraphClient, error)
}

// GraphClient is the provider-operation boundary used by GraphConnector. It
// exposes a small, SDK-independent subset of folders, messages, paging, and
// message content so connector logic can return provider-neutral mail types.
type GraphClient interface {
	ListFolders(ctx context.Context, userID string) ([]GraphFolder, error)
	ListMessages(ctx context.Context, userID, folderID, pageToken string, limit int) (GraphMessagePage, error)
	GetMessage(ctx context.Context, userID, messageID string) (GraphMessage, error)
	Close() error
}

// GraphFolder is the SDK-independent identity of one Microsoft Graph mail folder.
type GraphFolder struct {
	ID   string
	Name string
}

// GraphMessage contains the provider metadata needed to build a MessageRef.
// RawContent is preferred for RFC 5322 retrieval; Body is a fallback for
// internal clients that expose the same content under a body field.
type GraphMessage struct {
	ID           string
	ThreadID     string
	InternalDate time.Time
	ModifiedAt   time.Time
	Size         int64
	Removed      bool
	Body         []byte
	RawContent   []byte
}

// GraphMessagePage is one folder-filtered page. NextPageToken continues the
// current response; DeltaCursor is the durable placeholder returned when the
// provider has completed the current delta traversal.
type GraphMessagePage struct {
	Messages      []GraphMessage
	NextPageToken string
	DeltaCursor   string
}

// GraphDependencies contains replaceable secret, token, and provider operation
// boundaries used while constructing a GraphConnector.
type GraphDependencies struct {
	Credentials GraphCredentialSource
	Tokens      GraphTokenProvider
	Clients     GraphClientFactory
}

// MicrosoftGraphTokenProvider uses the Microsoft identity platform's OAuth 2.0
// client-credentials flow.
type MicrosoftGraphTokenProvider struct{}

// TokenSource implements GraphTokenProvider.
func (MicrosoftGraphTokenProvider) TokenSource(ctx context.Context, tenantID string, credentials GraphOAuthCredentials, scopes ...string) (oauth2.TokenSource, error) {
	tenantID = strings.TrimSpace(tenantID)
	credentials.ClientID = strings.TrimSpace(credentials.ClientID)
	if tenantID == "" || credentials.ClientID == "" || credentials.ClientSecret == "" {
		return nil, errors.New("mail: Microsoft Graph tenant, client ID, and client secret are required")
	}
	if len(scopes) == 0 {
		scopes = []string{graphScope}
	}
	config := clientcredentials.Config{
		ClientID:     credentials.ClientID,
		ClientSecret: credentials.ClientSecret,
		TokenURL:     "https://login.microsoftonline.com/" + url.PathEscape(tenantID) + "/oauth2/v2.0/token",
		Scopes:       append([]string(nil), scopes...),
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	return config.TokenSource(ctx), nil
}

// MicrosoftGraphClientFactory creates a GraphClient backed by net/http.
type MicrosoftGraphClientFactory struct{}

// NewGraphClient implements GraphClientFactory.
func (MicrosoftGraphClientFactory) NewGraphClient(ctx context.Context, tokens oauth2.TokenSource, baseURL string) (GraphClient, error) {
	if tokens == nil {
		return nil, errors.New("mail: Microsoft Graph OAuth token source is nil")
	}
	base, err := parseGraphBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &microsoftGraphHTTPClient{httpClient: oauth2.NewClient(ctx, tokens), baseURL: base}, nil
}

// GraphConnector implements Connector with Microsoft Graph mail folders,
// message delta queries, and MIME message retrieval.
type GraphConnector struct {
	mu        sync.Mutex
	closeOnce sync.Once
	config    GraphConnectorConfig
	client    GraphClient
	closed    bool
	closeErr  error
}

var (
	_ Connector          = (*GraphConnector)(nil)
	_ io.Closer          = (*GraphConnector)(nil)
	_ GraphTokenProvider = MicrosoftGraphTokenProvider{}
	_ GraphClientFactory = MicrosoftGraphClientFactory{}
	_ GraphClient        = (*microsoftGraphHTTPClient)(nil)
)

// NewGraphConnector validates configuration, resolves credentials, verifies
// token acquisition, and creates the Microsoft Graph operation client.
func NewGraphConnector(ctx context.Context, config GraphConnectorConfig, dependencies GraphDependencies) (*GraphConnector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.TenantID = strings.TrimSpace(config.TenantID)
	config.UserID = strings.TrimSpace(config.UserID)
	config.CredentialRef = strings.TrimSpace(config.CredentialRef)
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if config.AccountID == "" {
		return nil, errors.New("mail: Microsoft Graph account ID is required")
	}
	if config.TenantID == "" {
		return nil, errors.New("mail: Microsoft Graph tenant ID is required")
	}
	if config.UserID == "" {
		return nil, errors.New("mail: Microsoft Graph user ID is required")
	}
	if config.CredentialRef == "" {
		return nil, errors.New("mail: Microsoft Graph credential reference is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = graphDefaultURL
	}
	if _, err := parseGraphBaseURL(config.BaseURL); err != nil {
		return nil, err
	}
	if dependencies.Credentials == nil {
		return nil, errors.New("mail: Microsoft Graph credential source is required")
	}
	if dependencies.Tokens == nil {
		dependencies.Tokens = MicrosoftGraphTokenProvider{}
	}
	if dependencies.Clients == nil {
		dependencies.Clients = MicrosoftGraphClientFactory{}
	}

	credentials, err := dependencies.Credentials.LoadCredentials(ctx, config.CredentialRef)
	if err != nil {
		return nil, &Error{Kind: ErrorAuthentication, Operation: "load Microsoft Graph credentials", Message: "OAuth credentials are unavailable", Cause: err}
	}
	tokens, err := dependencies.Tokens.TokenSource(ctx, config.TenantID, credentials, graphScope)
	if err != nil {
		return nil, &Error{Kind: ErrorAuthentication, Operation: "acquire Microsoft Graph token", Message: "OAuth token acquisition failed", Cause: err}
	}
	if tokens == nil {
		return nil, &Error{Kind: ErrorAuthentication, Operation: "acquire Microsoft Graph token", Message: "OAuth token source is unavailable"}
	}
	// Force the first token exchange during construction. This makes invalid
	// credentials fail before a connector enters the ingestion lifecycle.
	initialToken, err := tokens.Token()
	if err != nil {
		return nil, &Error{Kind: ErrorAuthentication, Operation: "authenticate Microsoft Graph", Message: "Microsoft identity platform rejected OAuth credentials", Cause: err}
	}

	client, err := dependencies.Clients.NewGraphClient(ctx, oauth2.ReuseTokenSource(initialToken, tokens), config.BaseURL)
	if err != nil {
		if client != nil {
			_ = client.Close()
		}
		return nil, graphError("create Microsoft Graph client", err)
	}
	if client == nil {
		return nil, errors.New("mail: Microsoft Graph client factory returned nil")
	}
	return &GraphConnector{config: config, client: client}, nil
}

// ListMailboxes returns selected Microsoft Graph mail folders.
func (c *GraphConnector) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	folders, err := c.client.ListFolders(ctx, c.config.UserID)
	if err != nil {
		return nil, graphError("list Microsoft Graph mailboxes", err)
	}
	selected := make([]Mailbox, 0, len(folders))
	for _, folder := range folders {
		mailbox := Mailbox{ID: folder.ID, Name: folder.Name}
		if mailbox.ID != "" && graphMailboxSelected(mailbox, c.config.Mailboxes) {
			selected = append(selected, mailbox)
		}
	}
	return selected, nil
}

// ListChanges returns one Microsoft Graph delta page for a mail folder.
func (c *GraphConnector) ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return ChangePage{}, err
	}
	if mailbox.ID == "" {
		return ChangePage{}, errors.New("mail: Microsoft Graph mailbox ID is required")
	}
	if limit <= 0 {
		return ChangePage{}, errors.New("mail: Microsoft Graph change limit must be positive")
	}
	page, err := c.client.ListMessages(ctx, c.config.UserID, mailbox.ID, cursor.Token, limit)
	if err != nil {
		return ChangePage{}, graphError("list Microsoft Graph messages", err)
	}
	changes := make([]Change, 0, len(page.Messages))
	for _, message := range page.Messages {
		if message.ID == "" {
			continue
		}
		kind := ChangeUpsert
		if message.Removed {
			kind = ChangeDelete
		}
		changes = append(changes, Change{Kind: kind, Ref: c.messageRef(mailbox, message)})
	}
	next := page.DeltaCursor
	if page.NextPageToken != "" {
		next = page.NextPageToken
	}
	return ChangePage{Changes: changes, Next: Cursor{Token: next}, More: page.NextPageToken != ""}, nil
}

// OpenMessage retrieves a complete RFC 5322 MIME message from Microsoft Graph.
func (c *GraphConnector) OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return RawMessage{}, err
	}
	if ref.Provider != "" && ref.Provider != graphProvider {
		return RawMessage{}, errors.New("mail: message reference belongs to another provider")
	}
	if ref.AccountID != "" && ref.AccountID != c.config.AccountID {
		return RawMessage{}, errors.New("mail: message reference belongs to another account")
	}
	if ref.ProviderMessageID == "" {
		return RawMessage{}, errors.New("mail: Microsoft Graph message ID is required")
	}
	if !options.IncludeBody {
		return RawMessage{}, &Error{Kind: ErrorUnsupported, Operation: "open Microsoft Graph message", Message: "Microsoft Graph MIME retrieval requires IncludeBody"}
	}
	message, err := c.client.GetMessage(ctx, c.config.UserID, ref.ProviderMessageID)
	if err != nil {
		return RawMessage{}, graphError("get Microsoft Graph message", err)
	}
	content := message.RawContent
	if len(content) == 0 {
		content = message.Body
	}
	if len(content) == 0 {
		return RawMessage{}, &Error{Kind: ErrorMalformed, Operation: "get Microsoft Graph message", Message: "Microsoft Graph message did not include body or raw content"}
	}
	if options.MaxBytes > 0 && int64(len(content)) > options.MaxBytes {
		return RawMessage{}, &Error{Kind: ErrorOversized, Operation: "get Microsoft Graph message", Message: "message exceeds configured byte limit"}
	}
	normalized := ref
	normalized.Provider = graphProvider
	normalized.AccountID = c.config.AccountID
	if message.ID != "" {
		normalized.ProviderMessageID = message.ID
	}
	if message.ThreadID != "" {
		normalized.ProviderThreadID = message.ThreadID
	}
	if !message.InternalDate.IsZero() {
		normalized.InternalDate = message.InternalDate
	}
	if !message.ModifiedAt.IsZero() {
		normalized.Version = message.ModifiedAt.UTC().Format(time.RFC3339Nano)
	}
	if message.Size > 0 {
		normalized.Size = message.Size
	} else {
		normalized.Size = int64(len(content))
	}
	return RawMessage{Ref: normalized, RFC822: io.NopCloser(bytes.NewReader(content))}, nil
}

func (c *GraphConnector) messageRef(mailbox Mailbox, message GraphMessage) MessageRef {
	version := ""
	if !message.ModifiedAt.IsZero() {
		version = message.ModifiedAt.UTC().Format(time.RFC3339Nano)
	}
	return MessageRef{
		Provider:          graphProvider,
		AccountID:         c.config.AccountID,
		Mailbox:           mailbox,
		ProviderMessageID: message.ID,
		ProviderThreadID:  message.ThreadID,
		Version:           version,
		InternalDate:      message.InternalDate,
		Size:              message.Size,
	}
}

// Close releases provider HTTP resources. It is safe to call repeatedly.
func (c *GraphConnector) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		c.closeErr = c.client.Close()
	})
	return c.closeErr
}

func (c *GraphConnector) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed {
		return errors.New("mail: Microsoft Graph connector is closed")
	}
	return nil
}

func graphMailboxSelected(mailbox Mailbox, selector MailboxSelector) bool {
	matches := func(values []string) bool {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" && (value == mailbox.ID || value == mailbox.Name) {
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

// graphHTTPError is private so Graph transport details do not leak through the
// connector API.
type graphHTTPError struct {
	status     int
	retryAfter time.Duration
	cause      error
}

func (e *graphHTTPError) Error() string { return http.StatusText(e.status) }
func (e *graphHTTPError) Unwrap() error { return e.cause }

func graphError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	kind := ErrorTransient
	message := "Microsoft Graph request failed"
	var responseErr *graphHTTPError
	if errors.As(err, &responseErr) {
		switch responseErr.status {
		case http.StatusUnauthorized:
			kind, message = ErrorAuthentication, "Microsoft Graph rejected OAuth credentials"
		case http.StatusForbidden:
			kind, message = ErrorPermission, "Microsoft Graph denied mail access"
		case http.StatusNotFound:
			kind, message = ErrorMessageNotFound, "Microsoft Graph resource was not found"
		case http.StatusGone:
			kind, message = ErrorCheckpointReset, "Microsoft Graph delta cursor expired"
		case http.StatusTooManyRequests:
			kind, message = ErrorRateLimit, "Microsoft Graph rate limit exceeded"
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			kind, message = ErrorTimeout, "Microsoft Graph request timed out"
		case http.StatusBadRequest:
			kind, message = ErrorConfiguration, "Microsoft Graph rejected the request"
		}
		return &Error{Kind: kind, Operation: operation, Message: message, RetryAfter: responseErr.retryAfter, Cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			kind, message = ErrorTimeout, "Microsoft Graph request timed out"
		} else {
			kind, message = ErrorNetwork, "Microsoft Graph network request failed"
		}
	}
	return &Error{Kind: kind, Operation: operation, Message: message, Cause: err}
}

// All Microsoft Graph wire representations are intentionally private.
type graphFolderCollection struct {
	Value    []graphFolder `json:"value"`
	NextLink string        `json:"@odata.nextLink"`
}

type graphFolder struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type graphMessageCollection struct {
	Value     []graphMessage `json:"value"`
	NextLink  string         `json:"@odata.nextLink"`
	DeltaLink string         `json:"@odata.deltaLink"`
}

type graphMessage struct {
	ID               string          `json:"id"`
	ConversationID   string          `json:"conversationId"`
	ReceivedDateTime time.Time       `json:"receivedDateTime"`
	LastModified     time.Time       `json:"lastModifiedDateTime"`
	Size             int64           `json:"size"`
	Removed          json.RawMessage `json:"@removed"`
}

type microsoftGraphHTTPClient struct {
	httpClient *http.Client
	baseURL    *url.URL
	closeOnce  sync.Once
}

func (c *microsoftGraphHTTPClient) ListFolders(ctx context.Context, userID string) ([]GraphFolder, error) {
	next := c.endpoint("users", userID, "mailFolders")
	query := next.Query()
	query.Set("$select", "id,displayName")
	query.Set("$top", "100")
	next.RawQuery = query.Encode()
	var folders []GraphFolder
	for next != nil {
		var response graphFolderCollection
		if err := c.getJSON(ctx, next, &response); err != nil {
			return nil, err
		}
		for _, folder := range response.Value {
			folders = append(folders, GraphFolder{ID: folder.ID, Name: folder.DisplayName})
		}
		var err error
		next, err = c.followLink(response.NextLink)
		if err != nil {
			return nil, err
		}
	}
	return folders, nil
}

func (c *microsoftGraphHTTPClient) ListMessages(ctx context.Context, userID, folderID, pageToken string, limit int) (GraphMessagePage, error) {
	var endpoint *url.URL
	var err error
	if pageToken != "" {
		endpoint, err = c.followLink(pageToken)
		if err != nil {
			return GraphMessagePage{}, err
		}
	} else {
		endpoint = c.endpoint("users", userID, "mailFolders", folderID, "messages", "delta")
		query := endpoint.Query()
		query.Set("$select", "id,conversationId,receivedDateTime,lastModifiedDateTime,size")
		query.Set("$top", strconv.Itoa(limit))
		endpoint.RawQuery = query.Encode()
	}
	var response graphMessageCollection
	if err := c.getJSON(ctx, endpoint, &response); err != nil {
		return GraphMessagePage{}, err
	}
	messages := make([]GraphMessage, 0, len(response.Value))
	for _, message := range response.Value {
		messages = append(messages, GraphMessage{
			ID: message.ID, ThreadID: message.ConversationID,
			InternalDate: message.ReceivedDateTime, ModifiedAt: message.LastModified,
			Size: message.Size, Removed: len(message.Removed) != 0 && string(message.Removed) != "null",
		})
	}
	return GraphMessagePage{Messages: messages, NextPageToken: response.NextLink, DeltaCursor: response.DeltaLink}, nil
}

func (c *microsoftGraphHTTPClient) GetMessage(ctx context.Context, userID, messageID string) (GraphMessage, error) {
	endpoint := c.endpoint("users", userID, "messages", messageID, "$value")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return GraphMessage{}, err
	}
	request.Header.Set("Accept", "message/rfc822")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return GraphMessage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return GraphMessage{}, graphResponseError(response)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return GraphMessage{}, err
	}
	return GraphMessage{ID: messageID, Size: int64(len(data)), RawContent: data}, nil
}

func (c *microsoftGraphHTTPClient) Close() error {
	c.closeOnce.Do(func() {
		if c.httpClient != nil {
			c.httpClient.CloseIdleConnections()
		}
	})
	return nil
}

func (c *microsoftGraphHTTPClient) getJSON(ctx context.Context, endpoint *url.URL, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return graphResponseError(response)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Microsoft Graph response: %w", err)
	}
	return nil
}

func (c *microsoftGraphHTTPClient) endpoint(parts ...string) *url.URL {
	result := *c.baseURL
	path := strings.TrimSuffix(result.Path, "/")
	rawPath := strings.TrimSuffix(result.EscapedPath(), "/")
	for _, part := range parts {
		path += "/" + part
		rawPath += "/" + url.PathEscape(part)
	}
	result.Path = path
	result.RawPath = rawPath
	result.RawQuery = ""
	return &result
}

func (c *microsoftGraphHTTPClient) followLink(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	candidate, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid Microsoft Graph continuation link: %w", err)
	}
	if !candidate.IsAbs() {
		candidate = c.baseURL.ResolveReference(candidate)
	}
	if !strings.EqualFold(candidate.Scheme, c.baseURL.Scheme) || !strings.EqualFold(candidate.Host, c.baseURL.Host) ||
		!strings.HasPrefix(strings.TrimSuffix(candidate.Path, "/")+"/", strings.TrimSuffix(c.baseURL.Path, "/")+"/") {
		return nil, errors.New("mail: Microsoft Graph continuation link is outside the configured endpoint")
	}
	return candidate, nil
}

func graphResponseError(response *http.Response) error {
	retryAfter := time.Duration(0)
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		retryAfter = time.Duration(seconds) * time.Second
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return &graphHTTPError{status: response.StatusCode, retryAfter: retryAfter}
}

func parseGraphBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("mail: invalid Microsoft Graph base URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("mail: Microsoft Graph base URL must be an HTTPS URL without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed, nil
}
