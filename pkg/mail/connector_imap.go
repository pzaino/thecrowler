package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	imap "github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
	"github.com/emersion/go-sasl"
)

const imapProvider = "imap"

// IMAPTLSPolicy controls how an IMAP connection is protected.
type IMAPTLSPolicy string

const (
	// IMAPTLSImplicit establishes TLS before speaking IMAP, as used by imaps.
	IMAPTLSImplicit IMAPTLSPolicy = "implicit"
	// IMAPTLSStartTLS connects in cleartext and requires a STARTTLS upgrade
	// before authentication.
	IMAPTLSStartTLS IMAPTLSPolicy = "starttls"
	// IMAPTLSNone permits an unencrypted connection. It should only be used for
	// explicitly trusted development environments.
	IMAPTLSNone IMAPTLSPolicy = "none"
)

// IMAPAuth contains resolved, in-memory authentication material. Exactly one
// of Password or Token must be set. Token is intentionally protocol-neutral;
// the production client currently presents it with SASL OAUTHBEARER.
type IMAPAuth struct {
	Username string
	Password string
	Token    string
}

// IMAPConnectorConfig configures an IMAPConnector. Secrets belong in Auth only
// after resolving SourceConfig.Auth.CredentialRef and must not be logged.
type IMAPConnectorConfig struct {
	Host      string
	Port      int
	TLSPolicy IMAPTLSPolicy
	TLS       TLSConfig
	Timeout   time.Duration
	AccountID string
	Auth      IMAPAuth
	Mailboxes MailboxSelector
}

// IMAPConnector is a read-only, UID-based implementation of Connector. IMAP
// has connection-scoped mailbox selection, so operations are serialized.
type IMAPConnector struct {
	client    imapClient
	config    IMAPConnectorConfig
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
	closed    bool
}

// imapClient is the protocol boundary used by IMAPConnector. It deliberately
// uses package-local value types so unit tests can supply fakes without
// depending on an IMAP SDK.
type imapClient interface {
	Authenticate(context.Context, IMAPAuth) error
	ListMailboxes(context.Context) ([]imapMailbox, error)
	SelectMailbox(context.Context, string) (imapMailboxStatus, error)
	SearchUIDs(context.Context, uint32) ([]uint32, error)
	FetchMetadata(context.Context, []uint32) ([]imapMessage, error)
	FetchMessage(context.Context, uint32, FetchOptions) (imapMessage, []byte, error)
	Logout(context.Context) error
	Close() error
}

type imapMailbox struct {
	Name       string
	Selectable bool
}

type imapMailboxStatus struct {
	UIDValidity uint32
}

type imapMessage struct {
	UID          uint32
	InternalDate time.Time
	Flags        []string
	Size         int64
	Envelope     *MessageEnvelope
}

type imapClientFactory func(context.Context, IMAPConnectorConfig) (imapClient, error)

var dialIMAPClient imapClientFactory = dialGoIMAPClient

// NewIMAPConnector validates config, connects, and authenticates. If ctx is
// canceled during setup, the partially-created client is closed.
func NewIMAPConnector(ctx context.Context, config IMAPConnectorConfig) (*IMAPConnector, error) {
	return newIMAPConnector(ctx, config, dialIMAPClient)
}

func newIMAPConnector(ctx context.Context, config IMAPConnectorConfig, factory imapClientFactory) (*IMAPConnector, error) {
	config = normalizeIMAPConfig(config)
	if err := validateIMAPConfig(config); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	client, err := factory(ctx, config)
	if err != nil {
		return nil, imapError("connect", err)
	}
	connector := &IMAPConnector{client: client, config: config}
	if err := client.Authenticate(ctx, config.Auth); err != nil {
		_ = client.Close()
		return nil, imapError("authenticate", err)
	}
	return connector, nil
}

// IMAPConnectorConfigFromSource translates the typed endpoint and connector
// policy. auth must contain already-resolved secret material.
func IMAPConnectorConfigFromSource(config SourceConfig, auth IMAPAuth) (IMAPConnectorConfig, error) {
	if strings.ToLower(strings.TrimSpace(config.Connector.Provider)) != imapProvider {
		return IMAPConnectorConfig{}, fmt.Errorf("mail: source provider %q is not IMAP", config.Connector.Provider)
	}
	endpoint, err := url.Parse(strings.TrimSpace(config.Connector.Endpoint))
	if err != nil {
		return IMAPConnectorConfig{}, fmt.Errorf("mail: parse IMAP endpoint: %w", err)
	}
	port := 0
	if endpoint.Port() != "" {
		port, err = strconv.Atoi(endpoint.Port())
		if err != nil {
			return IMAPConnectorConfig{}, fmt.Errorf("mail: parse IMAP port: %w", err)
		}
	}
	policy := IMAPTLSNone
	if strings.EqualFold(endpoint.Scheme, "imaps") {
		policy = IMAPTLSImplicit
	}
	return IMAPConnectorConfig{
		Host:      endpoint.Hostname(),
		Port:      port,
		TLSPolicy: policy,
		TLS:       config.Connector.TLS,
		Timeout:   config.Connector.Timeout,
		AccountID: config.Auth.Identity,
		Auth:      auth,
		Mailboxes: MailboxSelector{Include: config.Mailboxes.Include, Exclude: config.Mailboxes.Exclude},
	}, nil
}

func normalizeIMAPConfig(config IMAPConnectorConfig) IMAPConnectorConfig {
	config.Host = strings.TrimSpace(config.Host)
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.Auth.Username = strings.TrimSpace(config.Auth.Username)
	if config.AccountID == "" {
		config.AccountID = config.Auth.Username
	}
	if config.TLSPolicy == "" {
		config.TLSPolicy = IMAPTLSImplicit
	}
	if config.Port == 0 {
		if config.TLSPolicy == IMAPTLSImplicit {
			config.Port = 993
		} else {
			config.Port = 143
		}
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	return config
}

func validateIMAPConfig(config IMAPConnectorConfig) error {
	if config.Host == "" {
		return errors.New("mail: IMAP host is required")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("mail: IMAP port %d is outside 1-65535", config.Port)
	}
	switch config.TLSPolicy {
	case IMAPTLSImplicit, IMAPTLSStartTLS, IMAPTLSNone:
	default:
		return fmt.Errorf("mail: unsupported IMAP TLS policy %q", config.TLSPolicy)
	}
	if config.TLSPolicy == IMAPTLSNone && (config.TLS.ServerName != "" || config.TLS.InsecureSkipVerify) {
		return errors.New("mail: IMAP TLS options require implicit TLS or STARTTLS")
	}
	if config.Auth.Username == "" {
		return errors.New("mail: IMAP username is required")
	}
	if (config.Auth.Password == "") == (config.Auth.Token == "") {
		return errors.New("mail: IMAP authentication requires exactly one of password or token")
	}
	return nil
}

// ListMailboxes lists selectable mailboxes and applies the configured exact
// include/exclude names. Exclusions take precedence.
func (c *IMAPConnector) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	mailboxes, err := c.client.ListMailboxes(ctx)
	if err != nil {
		return nil, imapError("list mailboxes", err)
	}
	result := make([]Mailbox, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if mailbox.Selectable && mailboxSelected(mailbox.Name, c.config.Mailboxes) {
			result = append(result, Mailbox{ID: mailbox.Name, Name: mailbox.Name})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// SelectMailbox selects mailbox read-only and returns its current UIDVALIDITY.
// It is exposed for lifecycle/setup callers; Connector operations select the
// required mailbox automatically.
func (c *IMAPConnector) SelectMailbox(ctx context.Context, mailbox Mailbox) (uint32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return 0, err
	}
	status, err := c.client.SelectMailbox(ctx, imapMailboxName(mailbox))
	if err != nil {
		return 0, imapError("select mailbox", err)
	}
	return status.UIDValidity, nil
}

// ListChanges returns messages with UIDs after cursor. A UIDVALIDITY mismatch
// is represented as a reset page so callers do not reuse stale UID state.
func (c *IMAPConnector) ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return ChangePage{}, err
	}
	if limit <= 0 {
		return ChangePage{}, errors.New("mail: IMAP change limit must be greater than zero")
	}
	status, err := c.client.SelectMailbox(ctx, imapMailboxName(mailbox))
	if err != nil {
		return ChangePage{}, imapError("select mailbox", err)
	}
	if cursor.UIDValidity != 0 && cursor.UIDValidity != status.UIDValidity {
		return ChangePage{
			Changes: []Change{{Kind: ChangeReset}},
			Next:    Cursor{UIDValidity: status.UIDValidity},
		}, nil
	}
	if cursor.UID == ^uint32(0) {
		return ChangePage{Next: Cursor{UID: cursor.UID, UIDValidity: status.UIDValidity}}, nil
	}
	firstUID := cursor.UID + 1
	uids, err := c.client.SearchUIDs(ctx, firstUID)
	if err != nil {
		return ChangePage{}, imapError("search messages", err)
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	uids = validUIDs(uids, firstUID)
	more := len(uids) > limit
	if more {
		uids = uids[:limit]
	}
	if len(uids) == 0 {
		return ChangePage{Next: Cursor{UID: cursor.UID, UIDValidity: status.UIDValidity}}, nil
	}
	messages, err := c.client.FetchMetadata(ctx, uids)
	if err != nil {
		return ChangePage{}, imapError("fetch message metadata", err)
	}
	byUID := make(map[uint32]imapMessage, len(messages))
	for _, message := range messages {
		byUID[message.UID] = message
	}
	changes := make([]Change, 0, len(uids))
	next := Cursor{UID: cursor.UID, UIDValidity: status.UIDValidity}
	for _, uid := range uids {
		message, found := byUID[uid]
		if found {
			changes = append(changes, Change{Kind: ChangeUpsert, Ref: c.messageRef(mailbox, status.UIDValidity, message)})
		} else {
			changes = append(changes, Change{Kind: ChangeDelete, Ref: c.messageRef(mailbox, status.UIDValidity, imapMessage{UID: uid})})
		}
		next.UID = uid
	}
	return ChangePage{Changes: changes, Next: next, More: more}, nil
}

// OpenMessage fetches one message by UID. The returned stream is memory-backed
// and independent of the IMAP command lifecycle.
func (c *IMAPConnector) OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return RawMessage{}, err
	}
	if ref.UID == 0 {
		return RawMessage{}, errors.New("mail: IMAP message UID must be greater than zero")
	}
	status, err := c.client.SelectMailbox(ctx, imapMailboxName(ref.Mailbox))
	if err != nil {
		return RawMessage{}, imapError("select mailbox", err)
	}
	if ref.UIDValidity != 0 && ref.UIDValidity != status.UIDValidity {
		return RawMessage{}, &Error{Kind: ErrorCheckpointReset, Operation: "open message", Message: "mailbox UIDVALIDITY changed"}
	}
	message, data, err := c.client.FetchMessage(ctx, ref.UID, options)
	if err != nil {
		return RawMessage{}, imapError("fetch message", err)
	}
	if options.MaxBytes > 0 && int64(len(data)) > options.MaxBytes {
		return RawMessage{}, &Error{Kind: ErrorOversized, Operation: "fetch message", Message: "message exceeds configured byte limit"}
	}
	fetchedRef := c.messageRef(ref.Mailbox, status.UIDValidity, message)
	return RawMessage{Ref: fetchedRef, RFC822: io.NopCloser(bytes.NewReader(data))}, nil
}

// Close logs out once and always closes the underlying connection. It is safe
// to call repeatedly.
func (c *IMAPConnector) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
		defer cancel()
		logoutErr := c.client.Logout(ctx)
		closeErr := c.client.Close()
		if logoutErr != nil && closeErr != nil {
			c.closeErr = errors.Join(logoutErr, closeErr)
		} else if logoutErr != nil {
			c.closeErr = logoutErr
		} else {
			c.closeErr = closeErr
		}
	})
	return c.closeErr
}

func (c *IMAPConnector) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed {
		return errors.New("mail: IMAP connector is closed")
	}
	return nil
}

func (c *IMAPConnector) messageRef(mailbox Mailbox, uidValidity uint32, message imapMessage) MessageRef {
	return MessageRef{
		Provider:     imapProvider,
		AccountID:    c.config.AccountID,
		Mailbox:      mailbox,
		UID:          message.UID,
		UIDValidity:  uidValidity,
		InternalDate: message.InternalDate,
		Flags:        append([]string(nil), message.Flags...),
		Size:         message.Size,
		Envelope:     cloneMessageEnvelope(message.Envelope),
	}
}

func validUIDs(uids []uint32, first uint32) []uint32 {
	filtered := uids[:0]
	var previous uint32
	for _, uid := range uids {
		if uid < first || uid == previous {
			continue
		}
		filtered = append(filtered, uid)
		previous = uid
	}
	return filtered
}

func cloneMessageEnvelope(envelope *MessageEnvelope) *MessageEnvelope {
	if envelope == nil {
		return nil
	}
	cloned := *envelope
	cloned.From = append([]Address(nil), envelope.From...)
	cloned.Sender = append([]Address(nil), envelope.Sender...)
	cloned.ReplyTo = append([]Address(nil), envelope.ReplyTo...)
	cloned.To = append([]Address(nil), envelope.To...)
	cloned.CC = append([]Address(nil), envelope.CC...)
	cloned.BCC = append([]Address(nil), envelope.BCC...)
	return &cloned
}

func mailboxSelected(name string, selector MailboxSelector) bool {
	name = strings.TrimSpace(name)
	for _, excluded := range selector.Exclude {
		if name == strings.TrimSpace(excluded) {
			return false
		}
	}
	if len(selector.Include) == 0 {
		return true
	}
	for _, included := range selector.Include {
		if name == strings.TrimSpace(included) {
			return true
		}
	}
	return false
}

func imapMailboxName(mailbox Mailbox) string {
	if strings.TrimSpace(mailbox.ID) != "" {
		return mailbox.ID
	}
	return mailbox.Name
}

func imapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var mailErr *Error
	if errors.As(err, &mailErr) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Kind: ErrorTimeout, Operation: operation, Message: "IMAP operation timed out", Cause: err}
	}
	kind := ErrorTransient
	if operation == "authenticate" {
		kind = ErrorAuthentication
	}
	return &Error{Kind: kind, Operation: operation, Message: "IMAP operation failed", Cause: err}
}

// goIMAPClient adapts emersion/go-imap behind the package-local protocol
// boundary. The net.Conn is retained to enforce context deadlines.
type goIMAPClient struct {
	client *imapclient.Client
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

func dialGoIMAPClient(ctx context.Context, config IMAPConnectorConfig) (imapClient, error) {
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	dialer := &net.Dialer{Timeout: config.Timeout}
	var conn net.Conn
	var err error
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         config.TLS.ServerName,
		InsecureSkipVerify: config.TLS.InsecureSkipVerify, //nolint:gosec // explicit configuration field
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = config.Host
	}
	if config.TLSPolicy == IMAPTLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}
	setupDeadline := time.Now().Add(config.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(setupDeadline) {
		setupDeadline = ctxDeadline
	}
	if err := conn.SetDeadline(setupDeadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	cancelSetup := make(chan struct{})
	setupWatcherDone := make(chan struct{})
	go func() {
		defer close(setupWatcherDone)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-cancelSetup:
		}
	}()
	client, err := imapclient.New(conn)
	close(cancelSetup)
	<-setupWatcherDone
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	wrapped := &goIMAPClient{client: client, conn: conn}
	if config.TLSPolicy == IMAPTLSStartTLS {
		if err := wrapped.run(ctx, func() error { return client.StartTLS(tlsConfig) }); err != nil {
			_ = wrapped.Close()
			return nil, err
		}
	}
	return wrapped, nil
}

func (c *goIMAPClient) Authenticate(ctx context.Context, auth IMAPAuth) error {
	return c.run(ctx, func() error {
		if auth.Token != "" {
			return c.client.Authenticate(sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{Username: auth.Username, Token: auth.Token}))
		}
		return c.client.Login(auth.Username, auth.Password)
	})
}

func (c *goIMAPClient) ListMailboxes(ctx context.Context) ([]imapMailbox, error) {
	var result []imapMailbox
	err := c.run(ctx, func() error {
		ch := make(chan *imap.MailboxInfo, 16)
		done := make(chan error, 1)
		go func() { done <- c.client.List("", "*", ch) }()
		for mailbox := range ch {
			selectable := true
			for _, attribute := range mailbox.Attributes {
				if strings.EqualFold(attribute, imap.NoSelectAttr) {
					selectable = false
				}
			}
			result = append(result, imapMailbox{Name: mailbox.Name, Selectable: selectable})
		}
		return <-done
	})
	return result, err
}

func (c *goIMAPClient) SelectMailbox(ctx context.Context, name string) (imapMailboxStatus, error) {
	var result imapMailboxStatus
	err := c.run(ctx, func() error {
		status, err := c.client.Select(name, true)
		if err == nil {
			result.UIDValidity = status.UidValidity
		}
		return err
	})
	return result, err
}

func (c *goIMAPClient) SearchUIDs(ctx context.Context, first uint32) ([]uint32, error) {
	var uids []uint32
	err := c.run(ctx, func() error {
		criteria := imap.NewSearchCriteria()
		criteria.Uid = new(imap.SeqSet)
		criteria.Uid.AddRange(first, 0)
		var err error
		uids, err = c.client.UidSearch(criteria)
		return err
	})
	return uids, err
}

func (c *goIMAPClient) FetchMetadata(ctx context.Context, uids []uint32) ([]imapMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	var result []imapMessage
	err := c.run(ctx, func() error {
		set := new(imap.SeqSet)
		set.AddNum(uids...)
		ch := make(chan *imap.Message, len(uids))
		done := make(chan error, 1)
		go func() {
			done <- c.client.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imap.FetchInternalDate, imap.FetchRFC822Size, imap.FetchEnvelope}, ch)
		}()
		for message := range ch {
			result = append(result, convertIMAPMessage(message))
		}
		return <-done
	})
	return result, err
}

func (c *goIMAPClient) FetchMessage(ctx context.Context, uid uint32, options FetchOptions) (imapMessage, []byte, error) {
	var metadata imapMessage
	var body []byte
	err := c.run(ctx, func() error {
		set := new(imap.SeqSet)
		set.AddNum(uid)

		preflight, err := c.fetchOne(set, []imap.FetchItem{imap.FetchUid, imap.FetchRFC822Size})
		if err != nil {
			return err
		}
		if preflight == nil || preflight.Uid != uid {
			return errors.New("IMAP message not found")
		}
		metadata = convertIMAPMessage(preflight)
		if options.MaxBytes > 0 && metadata.Size > options.MaxBytes {
			return oversizedMessageError()
		}

		section := &imap.BodySectionName{Peek: true}
		if !options.IncludeBody {
			section.Specifier = imap.HeaderSpecifier
			section.Fields = append([]string(nil), options.Headers...)
		}
		var fetchedMetadata imapMessage
		fetchedMetadata, body, err = c.fetchBody(ctx, set, uid, section, metadata.Size, options)
		if err != nil {
			return err
		}
		metadata = fetchedMetadata
		return nil
	})
	return metadata, body, err
}

func (c *goIMAPClient) fetchOne(set *imap.SeqSet, items []imap.FetchItem) (*imap.Message, error) {
	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.client.UidFetch(set, items, ch)
	}()
	message, ok := <-ch
	for range ch {
	}
	if err := <-done; err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return message, nil
}

func (c *goIMAPClient) fetchBody(ctx context.Context, set *imap.SeqSet, uid uint32, section *imap.BodySectionName, preflightSize int64, options FetchOptions) (imapMessage, []byte, error) {
	items := []imap.FetchItem{
		imap.FetchUid,
		imap.FetchFlags,
		imap.FetchInternalDate,
		imap.FetchRFC822Size,
		section.FetchItem(),
	}
	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.client.UidFetch(set, items, ch)
	}()

	message, ok := <-ch
	if !ok || message == nil || message.Uid != uid {
		if err := <-done; err != nil {
			return imapMessage{}, nil, err
		}
		return imapMessage{}, nil, errors.New("IMAP message not found")
	}
	metadata := convertIMAPMessage(message)
	if metadata.Size == 0 {
		metadata.Size = preflightSize
	}
	literal := message.GetBody(section)
	if literal == nil {
		for range ch {
		}
		_ = <-done
		return imapMessage{}, nil, errors.New("IMAP server omitted requested message body")
	}
	declaredSize := int64(0)
	if options.IncludeBody {
		declaredSize = metadata.Size
	}
	body, readErr := readIMAPLiteral(ctx, literal, declaredSize, options.MaxBytes)
	var mailErr *Error
	if errors.As(readErr, &mailErr) && mailErr.Kind == ErrorOversized {
		c.abortTransfer()
	}
	for range ch {
	}
	commandErr := <-done
	if readErr != nil {
		return imapMessage{}, nil, readErr
	}
	if commandErr != nil {
		return imapMessage{}, nil, commandErr
	}
	return metadata, body, nil
}

func (c *goIMAPClient) abortTransfer() {
	c.closed = true
	_ = c.conn.Close()
}

func readIMAPLiteral(ctx context.Context, literal io.Reader, declaredSize, maxBytes int64) ([]byte, error) {
	if maxBytes > 0 && declaredSize > maxBytes {
		return nil, oversizedMessageError()
	}

	reader := literal
	if maxBytes > 0 && maxBytes < int64(^uint64(0)>>1) {
		reader = io.LimitReader(literal, maxBytes+1)
	}
	var body bytes.Buffer
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			_, _ = body.Write(buffer[:n])
			if maxBytes > 0 && int64(body.Len()) > maxBytes {
				return nil, oversizedMessageError()
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	if declaredSize > 0 && int64(body.Len()) < declaredSize {
		return nil, io.ErrUnexpectedEOF
	}
	return body.Bytes(), nil
}

func oversizedMessageError() error {
	return &Error{Kind: ErrorOversized, Operation: "fetch message", Message: "message exceeds configured byte limit"}
}

func convertIMAPMessage(message *imap.Message) imapMessage {
	return imapMessage{
		UID:          message.Uid,
		InternalDate: message.InternalDate,
		Flags:        append([]string(nil), message.Flags...),
		Size:         int64(message.Size),
		Envelope:     convertIMAPEnvelope(message.Envelope),
	}
}

func convertIMAPEnvelope(envelope *imap.Envelope) *MessageEnvelope {
	if envelope == nil {
		return nil
	}
	return &MessageEnvelope{
		Date:      envelope.Date,
		Subject:   envelope.Subject,
		From:      convertIMAPAddresses(envelope.From),
		Sender:    convertIMAPAddresses(envelope.Sender),
		ReplyTo:   convertIMAPAddresses(envelope.ReplyTo),
		To:        convertIMAPAddresses(envelope.To),
		CC:        convertIMAPAddresses(envelope.Cc),
		BCC:       convertIMAPAddresses(envelope.Bcc),
		InReplyTo: envelope.InReplyTo,
		MessageID: envelope.MessageId,
	}
}

func convertIMAPAddresses(addresses []*imap.Address) []Address {
	converted := make([]Address, 0, len(addresses))
	for _, address := range addresses {
		if address == nil {
			continue
		}
		mailbox := address.Address()
		converted = append(converted, Address{
			Name:       address.PersonalName,
			Address:    mailbox,
			Normalized: strings.ToLower(mailbox),
		})
	}
	return converted
}

func (c *goIMAPClient) Logout(ctx context.Context) error {
	return c.run(ctx, c.client.Logout)
}

func (c *goIMAPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.client.Terminate()
}

func (c *goIMAPClient) run(ctx context.Context, operation func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("IMAP client is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return err
	}
	cancelDone := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = c.conn.SetDeadline(time.Now())
		case <-cancelDone:
		}
	}()
	err := operation()
	close(cancelDone)
	<-watcherDone
	_ = c.conn.SetDeadline(time.Time{})
	if ctxErr := ctx.Err(); ctxErr != nil {
		c.closed = true
		_ = c.client.Terminate()
		return ctxErr
	}
	return err
}

var _ Connector = (*IMAPConnector)(nil)
var _ io.Closer = (*IMAPConnector)(nil)
