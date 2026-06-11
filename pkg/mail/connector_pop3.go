package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pop3Provider = "pop3"
	pop3Mailbox  = "INBOX"
)

// POP3TLSPolicy controls how a POP3 connection is protected.
type POP3TLSPolicy string

const (
	// POP3TLSImplicit establishes TLS before speaking POP3, as used by pop3s.
	POP3TLSImplicit POP3TLSPolicy = "implicit"
	// POP3TLSStartTLS connects in cleartext and requires an STLS upgrade before authentication.
	POP3TLSStartTLS POP3TLSPolicy = "starttls"
	// POP3TLSNone permits an unencrypted connection for explicitly trusted environments.
	POP3TLSNone POP3TLSPolicy = "none"
)

// POP3Auth contains resolved, in-memory USER/PASS authentication material.
type POP3Auth struct {
	Username string
	Password string
}

// POP3ConnectorConfig configures a legacy, polling-only POP3 connector.
type POP3ConnectorConfig struct {
	Host      string
	Port      int
	TLSPolicy POP3TLSPolicy
	TLS       TLSConfig
	Timeout   time.Duration
	AccountID string
	Auth      POP3Auth
}

// POP3Connector implements Connector for the single POP3 maildrop. POP3 has
// no mailbox hierarchy and message numbers are session-local, so operations
// are serialized and UIDL values are treated as locators rather than strong
// provider identities. Content hashing remains the stable identity fallback.
type POP3Connector struct {
	client    pop3Client
	config    POP3ConnectorConfig
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
	closed    bool
}

// pop3Client is the fakeable protocol boundary. Package-local values keep
// tests independent of the wire implementation.
type pop3Client interface {
	Authenticate(context.Context, POP3Auth) error
	List(context.Context) ([]pop3Message, error)
	Retrieve(context.Context, int, int64) ([]byte, error)
	Quit(context.Context) error
	Close() error
}

type pop3Message struct {
	Number int
	UIDL   string
	Size   int64
}

type pop3ClientFactory func(context.Context, POP3ConnectorConfig) (pop3Client, error)

var dialPOP3Client pop3ClientFactory = dialWirePOP3Client

// NewPOP3Connector validates config, connects, and authenticates. A client
// created before authentication failure or cancellation is always closed.
func NewPOP3Connector(ctx context.Context, config POP3ConnectorConfig) (*POP3Connector, error) {
	return newPOP3Connector(ctx, config, dialPOP3Client)
}

func newPOP3Connector(ctx context.Context, config POP3ConnectorConfig, factory pop3ClientFactory) (*POP3Connector, error) {
	config = normalizePOP3Config(config)
	if err := validatePOP3Config(config); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := factory(ctx, config)
	if err != nil {
		return nil, pop3Error("connect", err)
	}
	connector := &POP3Connector{client: client, config: config}
	if err := client.Authenticate(ctx, config.Auth); err != nil {
		_ = client.Close()
		return nil, pop3Error("authenticate", err)
	}
	return connector, nil
}

// POP3ConnectorConfigFromSource translates pop3/pop3s endpoints. auth must
// already contain secret material resolved from SourceConfig.Auth.
func POP3ConnectorConfigFromSource(config SourceConfig, auth POP3Auth) (POP3ConnectorConfig, error) {
	if strings.ToLower(strings.TrimSpace(config.Connector.Provider)) != pop3Provider {
		return POP3ConnectorConfig{}, fmt.Errorf("mail: source provider %q is not POP3", config.Connector.Provider)
	}
	endpoint, err := url.Parse(strings.TrimSpace(config.Connector.Endpoint))
	if err != nil {
		return POP3ConnectorConfig{}, fmt.Errorf("mail: parse POP3 endpoint: %w", err)
	}
	port := 0
	if endpoint.Port() != "" {
		port, err = strconv.Atoi(endpoint.Port())
		if err != nil {
			return POP3ConnectorConfig{}, fmt.Errorf("mail: parse POP3 port: %w", err)
		}
	}
	policy := POP3TLSNone
	if strings.EqualFold(endpoint.Scheme, "pop3s") {
		policy = POP3TLSImplicit
	}
	return POP3ConnectorConfig{
		Host: endpoint.Hostname(), Port: port, TLSPolicy: policy,
		TLS: config.Connector.TLS, Timeout: config.Connector.Timeout,
		AccountID: config.Auth.Identity, Auth: auth,
	}, nil
}

func normalizePOP3Config(config POP3ConnectorConfig) POP3ConnectorConfig {
	config.Host = strings.TrimSpace(config.Host)
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.Auth.Username = strings.TrimSpace(config.Auth.Username)
	if config.AccountID == "" {
		config.AccountID = config.Auth.Username
	}
	if config.TLSPolicy == "" {
		config.TLSPolicy = POP3TLSImplicit
	}
	if config.Port == 0 {
		if config.TLSPolicy == POP3TLSImplicit {
			config.Port = 995
		} else {
			config.Port = 110
		}
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	return config
}

func validatePOP3Config(config POP3ConnectorConfig) error {
	if config.Host == "" {
		return errors.New("mail: POP3 host is required")
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("mail: POP3 port %d is outside 1-65535", config.Port)
	}
	switch config.TLSPolicy {
	case POP3TLSImplicit, POP3TLSStartTLS, POP3TLSNone:
	default:
		return fmt.Errorf("mail: unsupported POP3 TLS policy %q", config.TLSPolicy)
	}
	if config.TLSPolicy == POP3TLSNone && (config.TLS.ServerName != "" || config.TLS.InsecureSkipVerify) {
		return errors.New("mail: POP3 TLS options require implicit TLS or STARTTLS")
	}
	if config.Auth.Username == "" || config.Auth.Password == "" {
		return errors.New("mail: POP3 USER/PASS authentication requires username and password")
	}
	if strings.ContainsAny(config.Auth.Username, "\r\n") || strings.ContainsAny(config.Auth.Password, "\r\n") {
		return errors.New("mail: POP3 credentials must not contain line breaks")
	}
	return nil
}

// ListMailboxes always returns the one POP3 maildrop as INBOX.
func (c *POP3Connector) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	return []Mailbox{{ID: pop3Mailbox, Name: pop3Mailbox}}, nil
}

type pop3Cursor struct {
	Known   []string           `json:"known,omitempty"`
	Pending []pop3CursorChange `json:"pending,omitempty"`
	Offset  int                `json:"offset,omitempty"`
}

type pop3CursorChange struct {
	Kind   ChangeKind `json:"kind"`
	Number int        `json:"number,omitempty"`
	UIDL   string     `json:"uidl"`
	Size   int64      `json:"size,omitempty"`
}

// ListChanges polls UIDL/LIST and compares the current maildrop with the prior
// opaque snapshot. The cursor can be larger than an IMAP cursor because POP3
// offers no change token. UIDLs are mailbox-scoped polling keys, not strong
// identities; MessageRef.Version carries UIDL while ProviderMessageID remains empty.
func (c *POP3Connector) ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return ChangePage{}, err
	}
	if !isPOP3Mailbox(mailbox) {
		return ChangePage{}, &Error{Kind: ErrorMailboxNotFound, Operation: "list changes", Message: "POP3 exposes only INBOX"}
	}
	if limit <= 0 {
		return ChangePage{}, errors.New("mail: POP3 change limit must be greater than zero")
	}
	state, err := decodePOP3Cursor(cursor.Token)
	if err != nil {
		return ChangePage{}, &Error{Kind: ErrorCheckpointReset, Operation: "list changes", Message: "POP3 cursor is invalid", Cause: err}
	}
	if len(state.Pending) == 0 {
		messages, err := c.client.List(ctx)
		if err != nil {
			return ChangePage{}, pop3Error("list messages", err)
		}
		state.Pending, state.Known = diffPOP3Messages(state.Known, messages)
		state.Offset = 0
	}
	end := state.Offset + limit
	if end > len(state.Pending) {
		end = len(state.Pending)
	}
	changes := make([]Change, 0, end-state.Offset)
	canonicalMailbox := Mailbox{ID: pop3Mailbox, Name: pop3Mailbox}
	for _, pending := range state.Pending[state.Offset:end] {
		changes = append(changes, Change{Kind: pending.Kind, Ref: c.messageRef(canonicalMailbox, pop3Message{
			Number: pending.Number, UIDL: pending.UIDL, Size: pending.Size,
		})})
	}
	state.Offset = end
	more := state.Offset < len(state.Pending)
	if !more {
		state.Pending = nil
		state.Offset = 0
	}
	token, err := encodePOP3Cursor(state)
	if err != nil {
		return ChangePage{}, fmt.Errorf("mail: encode POP3 cursor: %w", err)
	}
	return ChangePage{Changes: changes, Next: Cursor{Token: token}, More: more}, nil
}

// OpenMessage resolves the UIDL against the current session before RETR so a
// renumbered maildrop cannot silently return a different message.
func (c *POP3Connector) OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ready(ctx); err != nil {
		return RawMessage{}, err
	}
	if !isPOP3Mailbox(ref.Mailbox) {
		return RawMessage{}, &Error{Kind: ErrorMailboxNotFound, Operation: "open message", Message: "POP3 exposes only INBOX"}
	}
	messages, err := c.client.List(ctx)
	if err != nil {
		return RawMessage{}, pop3Error("list messages", err)
	}
	message, found := findPOP3Message(messages, ref)
	if !found {
		return RawMessage{}, &Error{Kind: ErrorMessageNotFound, Operation: "open message", Message: "POP3 message was not found"}
	}
	if options.MaxBytes > 0 && message.Size > options.MaxBytes {
		return RawMessage{}, &Error{Kind: ErrorOversized, Operation: "fetch message", Message: "message exceeds configured byte limit"}
	}
	data, err := c.client.Retrieve(ctx, message.Number, options.MaxBytes)
	if err != nil {
		return RawMessage{}, pop3Error("fetch message", err)
	}
	if options.MaxBytes > 0 && int64(len(data)) > options.MaxBytes {
		return RawMessage{}, &Error{Kind: ErrorOversized, Operation: "fetch message", Message: "message exceeds configured byte limit"}
	}
	return RawMessage{Ref: c.messageRef(Mailbox{ID: pop3Mailbox, Name: pop3Mailbox}, message), RFC822: io.NopCloser(bytes.NewReader(data))}, nil
}

// Close sends QUIT once and always closes the transport. It is idempotent.
func (c *POP3Connector) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
		defer cancel()
		quitErr := c.client.Quit(ctx)
		closeErr := c.client.Close()
		c.closeErr = errors.Join(quitErr, closeErr)
	})
	return c.closeErr
}

func (c *POP3Connector) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed {
		return errors.New("mail: POP3 connector is closed")
	}
	return nil
}

func (c *POP3Connector) messageRef(mailbox Mailbox, message pop3Message) MessageRef {
	return MessageRef{Provider: pop3Provider, AccountID: c.config.AccountID, Mailbox: mailbox,
		UID: uint32(message.Number), Version: message.UIDL, Size: message.Size}
}

func isPOP3Mailbox(mailbox Mailbox) bool {
	name := mailbox.ID
	if strings.TrimSpace(name) == "" {
		name = mailbox.Name
	}
	return strings.EqualFold(strings.TrimSpace(name), pop3Mailbox)
}

func findPOP3Message(messages []pop3Message, ref MessageRef) (pop3Message, bool) {
	if ref.Version != "" {
		for _, message := range messages {
			if message.UIDL == ref.Version {
				return message, true
			}
		}
		return pop3Message{}, false
	}
	for _, message := range messages {
		if message.Number > 0 && uint32(message.Number) == ref.UID {
			return message, true
		}
	}
	return pop3Message{}, false
}

func diffPOP3Messages(known []string, messages []pop3Message) ([]pop3CursorChange, []string) {
	sort.Slice(messages, func(i, j int) bool { return messages[i].Number < messages[j].Number })
	current := make(map[string]pop3Message, len(messages))
	currentIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		id := message.UIDL
		if id == "" {
			id = "number:" + strconv.Itoa(message.Number)
			message.UIDL = id
		}
		if _, duplicate := current[id]; duplicate {
			continue
		}
		current[id] = message
		currentIDs = append(currentIDs, id)
	}
	knownSet := make(map[string]struct{}, len(known))
	for _, id := range known {
		knownSet[id] = struct{}{}
	}
	pending := make([]pop3CursorChange, 0)
	for _, id := range currentIDs {
		if _, exists := knownSet[id]; !exists {
			message := current[id]
			pending = append(pending, pop3CursorChange{Kind: ChangeUpsert, Number: message.Number, UIDL: id, Size: message.Size})
		}
	}
	currentSet := make(map[string]struct{}, len(currentIDs))
	for _, id := range currentIDs {
		currentSet[id] = struct{}{}
	}
	for _, id := range known {
		if _, exists := currentSet[id]; !exists {
			pending = append(pending, pop3CursorChange{Kind: ChangeDelete, UIDL: id})
		}
	}
	return pending, currentIDs
}

func decodePOP3Cursor(token string) (pop3Cursor, error) {
	if token == "" {
		return pop3Cursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return pop3Cursor{}, err
	}
	var cursor pop3Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return pop3Cursor{}, err
	}
	if cursor.Offset < 0 || cursor.Offset > len(cursor.Pending) {
		return pop3Cursor{}, errors.New("POP3 cursor offset is out of range")
	}
	return cursor, nil
}

func encodePOP3Cursor(cursor pop3Cursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pop3Error(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: ErrorTimeout, Operation: operation, Message: "POP3 operation timed out", Cause: context.DeadlineExceeded}
	}
	var mailErr *Error
	if errors.As(err, &mailErr) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return &Error{Kind: ErrorNetwork, Operation: operation, Message: "POP3 network operation failed"}
	}
	kind := ErrorTransient
	message := "POP3 operation failed"
	if operation == "authenticate" {
		kind, message = ErrorAuthentication, "POP3 authentication failed"
	}
	return &Error{Kind: kind, Operation: operation, Message: message}
}

// wirePOP3Client is a small USER/PASS POP3 implementation kept behind
// pop3Client so connector behavior is testable without a server.
type wirePOP3Client struct {
	conn    net.Conn
	reader  *textproto.Reader
	writer  *textproto.Writer
	timeout time.Duration
}

func dialWirePOP3Client(ctx context.Context, config POP3ConnectorConfig) (pop3Client, error) {
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	dialer := &net.Dialer{Timeout: config.Timeout}
	var conn net.Conn
	var err error
	tlsConfig := &tls.Config{ServerName: config.TLS.ServerName, InsecureSkipVerify: config.TLS.InsecureSkipVerify, MinVersion: tls.VersionTLS12} //nolint:gosec
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = config.Host
	}
	if config.TLSPolicy == POP3TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}
	client := newWirePOP3Client(conn, config.Timeout)
	if _, err := client.command(ctx, false, ""); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if config.TLSPolicy == POP3TLSStartTLS {
		if _, err := client.command(ctx, false, "STLS"); err != nil {
			_ = conn.Close()
			return nil, err
		}
		tlsConn := tls.Client(conn, tlsConfig)
		if err := client.withContext(ctx, func() error { return tlsConn.HandshakeContext(ctx) }); err != nil {
			_ = conn.Close()
			return nil, err
		}
		client.conn = tlsConn
		client.reader = textproto.NewReader(bufio.NewReader(tlsConn))
		client.writer = textproto.NewWriter(bufio.NewWriter(tlsConn))
	}
	return client, nil
}

func newWirePOP3Client(conn net.Conn, timeout time.Duration) *wirePOP3Client {
	return &wirePOP3Client{conn: conn, reader: textproto.NewReader(bufio.NewReader(conn)), writer: textproto.NewWriter(bufio.NewWriter(conn)), timeout: timeout}
}

func (c *wirePOP3Client) Authenticate(ctx context.Context, auth POP3Auth) error {
	if _, err := c.command(ctx, false, "USER %s", auth.Username); err != nil {
		return err
	}
	_, err := c.command(ctx, false, "PASS %s", auth.Password)
	return err
}

func (c *wirePOP3Client) List(ctx context.Context) ([]pop3Message, error) {
	lines, err := c.command(ctx, true, "LIST")
	if err != nil {
		return nil, err
	}
	messages := make(map[int]pop3Message, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		number, numberErr := strconv.Atoi(fields[0])
		size, sizeErr := strconv.ParseInt(fields[1], 10, 64)
		if numberErr == nil && sizeErr == nil && number > 0 && size >= 0 {
			messages[number] = pop3Message{Number: number, Size: size}
		}
	}
	uidLines, err := c.command(ctx, true, "UIDL")
	if err != nil {
		return nil, err
	}
	for _, line := range uidLines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		number, numberErr := strconv.Atoi(fields[0])
		if message, ok := messages[number]; numberErr == nil && ok {
			message.UIDL = fields[1]
			messages[number] = message
		}
	}
	result := make([]pop3Message, 0, len(messages))
	for _, message := range messages {
		result = append(result, message)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result, nil
}

func (c *wirePOP3Client) Retrieve(ctx context.Context, number int, maxBytes int64) ([]byte, error) {
	var data []byte
	err := c.withContext(ctx, func() error {
		if err := c.writer.PrintfLine("RETR %d", number); err != nil {
			return err
		}
		line, err := c.reader.ReadLine()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "+OK") {
			return errors.New("POP3 negative response")
		}
		var buffer bytes.Buffer
		for {
			line, err = c.reader.ReadLine()
			if err != nil {
				return err
			}
			if line == "." {
				break
			}
			if strings.HasPrefix(line, "..") {
				line = line[1:]
			}
			if maxBytes > 0 && int64(buffer.Len()+len(line)+2) > maxBytes {
				return &Error{Kind: ErrorOversized, Operation: "fetch message", Message: "message exceeds configured byte limit"}
			}
			buffer.WriteString(line)
			buffer.WriteString("\r\n")
		}
		data = buffer.Bytes()
		return nil
	})
	return data, err
}

func (c *wirePOP3Client) Quit(ctx context.Context) error {
	_, err := c.command(ctx, false, "QUIT")
	return err
}

func (c *wirePOP3Client) Close() error { return c.conn.Close() }

func (c *wirePOP3Client) command(ctx context.Context, multiline bool, format string, args ...any) ([]string, error) {
	var lines []string
	err := c.withContext(ctx, func() error {
		if format != "" {
			if err := c.writer.PrintfLine(format, args...); err != nil {
				return err
			}
		}
		line, err := c.reader.ReadLine()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "+OK") {
			return errors.New("POP3 negative response")
		}
		if multiline {
			lines, err = c.reader.ReadDotLines()
			return err
		}
		return nil
	})
	return lines, err
}

func (c *wirePOP3Client) withContext(ctx context.Context, operation func() error) error {
	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return err
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = c.conn.SetDeadline(time.Now())
		close(canceled)
	})
	err := operation()
	if !stop() {
		<-canceled
	}
	_ = c.conn.SetDeadline(time.Time{})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
