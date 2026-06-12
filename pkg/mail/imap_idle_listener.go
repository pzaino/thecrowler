package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	imapclient "github.com/emersion/go-imap/client"
	"golang.org/x/sync/errgroup"
)

var (
	// ErrInvalidIMAPIdleListener identifies an IDLE listener with incomplete
	// connection configuration or no client factory.
	ErrInvalidIMAPIdleListener = errors.New("mail: invalid IMAP IDLE listener")
	// ErrIMAPIdleListenerRunning prevents one listener instance from owning two
	// overlapping sets of long-lived mailbox sessions.
	ErrIMAPIdleListenerRunning = errors.New("mail: IMAP IDLE listener is already running")
)

// IMAPIdleListenerStatus is a concurrency-safe snapshot of listener health.
// A running listener is degraded while any selected mailbox is disconnected or
// retrying. LastError is cleared after every mailbox has recovered.
type IMAPIdleListenerStatus struct {
	Running          bool   `json:"running" yaml:"running"`
	Degraded         bool   `json:"degraded" yaml:"degraded"`
	ActiveSessions   int    `json:"active_sessions" yaml:"active_sessions"`
	ExpectedSessions int    `json:"expected_sessions" yaml:"expected_sessions"`
	ReconnectCount   uint64 `json:"reconnect_count" yaml:"reconnect_count"`
	LastError        string `json:"last_error,omitempty" yaml:"last_error,omitempty"`
}

type imapIdleMailboxState struct {
	active    bool
	lastError string
}

// imapIdleClient is the fakeable protocol boundary used by IMAPIdleListener.
// Each client owns exactly one selected mailbox and one IDLE session at a time.
type imapIdleClient interface {
	Authenticate(context.Context, IMAPAuth) error
	SelectMailbox(context.Context, string) (imapMailboxStatus, error)
	Idle(context.Context, <-chan struct{}, chan<- struct{}) error
	Logout(context.Context) error
	Close() error
}

type imapIdleClientFactory func(context.Context, IMAPConnectorConfig) (imapIdleClient, error)

// IMAPIdleListener keeps one authenticated IMAP connection in IDLE for each
// configured, selected priority mailbox. A server update is only a hint: the
// listener first leaves IDLE, submits the mailbox to EventSink, and then
// resumes IDLE. Durable progress remains the reconciler's responsibility.
type IMAPIdleListener struct {
	config  IMAPConnectorConfig
	factory imapIdleClientFactory

	mu             sync.Mutex
	running        bool
	mailboxStates  map[string]imapIdleMailboxState
	reconnectCount uint64
	lastError      string
}

// NewIMAPIdleListener constructs a listener using the mailbox include order as
// its priority order. If Include is empty, all mailboxes passed to Listen are
// watched in caller order. Excluded mailboxes are never watched.
func NewIMAPIdleListener(config IMAPConnectorConfig) (*IMAPIdleListener, error) {
	return newIMAPIdleListener(config, dialIMAPIdleClient)
}

func newIMAPIdleListener(config IMAPConnectorConfig, factory imapIdleClientFactory) (*IMAPIdleListener, error) {
	config = normalizeIMAPConfig(config)
	if err := validateIMAPConfig(config); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, fmt.Errorf("%w: client factory is required", ErrInvalidIMAPIdleListener)
	}
	return &IMAPIdleListener{config: config, factory: factory}, nil
}

// Status returns a point-in-time health snapshot without exposing credentials
// or protocol client objects.
func (listener *IMAPIdleListener) Status() IMAPIdleListenerStatus {
	if listener == nil {
		return IMAPIdleListenerStatus{}
	}
	listener.mu.Lock()
	defer listener.mu.Unlock()

	status := IMAPIdleListenerStatus{
		Running:          listener.running,
		ExpectedSessions: len(listener.mailboxStates),
		ReconnectCount:   listener.reconnectCount,
		LastError:        listener.lastError,
	}
	for _, state := range listener.mailboxStates {
		if state.active {
			status.ActiveSessions++
		}
	}
	status.Degraded = status.Running && (status.ActiveSessions < status.ExpectedSessions || status.LastError != "")
	return status
}

// Listen implements Listener. Each mailbox reconnects independently after
// connection, authentication, selection, or IDLE failures. A fatal sink error
// still cancels all sessions because hints can no longer be delivered.
func (listener *IMAPIdleListener) Listen(ctx context.Context, mailboxes []MailboxKey, sink EventSink) error {
	if listener == nil || listener.factory == nil {
		return fmt.Errorf("%w: listener is not initialized", ErrInvalidIMAPIdleListener)
	}
	if sink == nil {
		return fmt.Errorf("%w: event sink is required", ErrInvalidIMAPIdleListener)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	selected := priorityIMAPMailboxes(mailboxes, listener.config.Mailboxes)
	if !listener.start(selected) {
		return ErrIMAPIdleListenerRunning
	}
	defer listener.stop()

	group, groupCtx := errgroup.WithContext(ctx)
	for _, mailbox := range selected {
		mailbox := mailbox
		group.Go(func() error {
			return listener.listenMailbox(groupCtx, mailbox, sink)
		})
	}

	err := group.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (listener *IMAPIdleListener) listenMailbox(ctx context.Context, mailbox MailboxKey, sink EventSink) error {
	key := imapMailboxStateKey(mailbox)
	backoff := listener.config.ReconnectBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		client, err := listener.connectMailbox(ctx, mailbox)
		if err == nil {
			listener.markConnected(key)
			err = listener.runIdleSession(ctx, client, mailbox, sink)
			listener.markDisconnected(key, err)
			shutdownIMAPIdleClient(ctx, client, listener.config.Timeout)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			var sinkErr *imapIdleSinkError
			if errors.As(err, &sinkErr) {
				return sinkErr.err
			}
		} else {
			listener.markDisconnected(key, err)
		}

		listener.recordReconnect()
		if err := waitForIMAPIdleReconnect(ctx, backoff); err != nil {
			return err
		}
		backoff = nextIMAPIdleBackoff(backoff, listener.config.MaxReconnectBackoff)
	}
}

func (listener *IMAPIdleListener) connectMailbox(ctx context.Context, mailbox MailboxKey) (imapIdleClient, error) {
	client, err := listener.factory(ctx, listener.config)
	if err != nil {
		return nil, fmt.Errorf("mail: connect IMAP IDLE mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
	}
	if err := client.Authenticate(ctx, listener.config.Auth); err != nil {
		shutdownIMAPIdleClient(ctx, client, listener.config.Timeout)
		return nil, imapError("authenticate IDLE client", err)
	}
	if _, err := client.SelectMailbox(ctx, imapMailboxName(mailbox.Mailbox)); err != nil {
		shutdownIMAPIdleClient(ctx, client, listener.config.Timeout)
		return nil, imapError("select IDLE mailbox", err)
	}
	return client, nil
}

type imapIdleSinkError struct{ err error }

func (err *imapIdleSinkError) Error() string { return err.err.Error() }
func (err *imapIdleSinkError) Unwrap() error { return err.err }

func (listener *IMAPIdleListener) runIdleSession(ctx context.Context, client imapIdleClient, mailbox MailboxKey, sink EventSink) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stop := make(chan struct{})
		changes := make(chan struct{}, 1)
		idleDone := make(chan error, 1)
		go func() {
			idleDone <- client.Idle(ctx, stop, changes)
		}()

		timer := time.NewTimer(listener.config.IdleReissueInterval)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			close(stop)
			<-idleDone
			return ctx.Err()
		case err := <-idleDone:
			stopTimer(timer)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err == nil {
				err = errors.New("IDLE session ended without a change notification")
			}
			return fmt.Errorf("mail: IDLE mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
		case <-timer.C:
			close(stop)
			if err := <-idleDone; err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("mail: reissue IDLE mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
			}
		case <-changes:
			stopTimer(timer)
			close(stop)
			if err := <-idleDone; err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("mail: leave IDLE mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
			}
			if err := sink.Notify(ctx, mailbox); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return &imapIdleSinkError{err: fmt.Errorf("mail: enqueue reconciliation for mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)}
			}
		}
	}
}

func waitForIMAPIdleReconnect(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextIMAPIdleBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (listener *IMAPIdleListener) start(mailboxes []MailboxKey) bool {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.running {
		return false
	}
	listener.running = true
	listener.reconnectCount = 0
	listener.lastError = ""
	listener.mailboxStates = make(map[string]imapIdleMailboxState, len(mailboxes))
	for _, mailbox := range mailboxes {
		listener.mailboxStates[imapMailboxStateKey(mailbox)] = imapIdleMailboxState{}
	}
	return true
}

func (listener *IMAPIdleListener) stop() {
	listener.mu.Lock()
	listener.running = false
	listener.mu.Unlock()
}

func (listener *IMAPIdleListener) markConnected(key string) {
	listener.mu.Lock()
	state := listener.mailboxStates[key]
	state.active = true
	state.lastError = ""
	listener.mailboxStates[key] = state
	listener.refreshLastErrorLocked()
	listener.mu.Unlock()
}

func (listener *IMAPIdleListener) markDisconnected(key string, err error) {
	listener.mu.Lock()
	state := listener.mailboxStates[key]
	state.active = false
	if err != nil && !errors.Is(err, context.Canceled) {
		state.lastError = safeIMAPIdleStatusError(err)
	}
	listener.mailboxStates[key] = state
	listener.refreshLastErrorLocked()
	listener.mu.Unlock()
}

func safeIMAPIdleStatusError(err error) string {
	var mailErr *Error
	if errors.As(err, &mailErr) {
		return mailErr.Error()
	}
	return "mail: IMAP listener session unavailable"
}

func (listener *IMAPIdleListener) recordReconnect() {
	recordMailListenerReconnect()
	listener.mu.Lock()
	listener.reconnectCount++
	listener.mu.Unlock()
}

func (listener *IMAPIdleListener) refreshLastErrorLocked() {
	listener.lastError = ""
	for _, state := range listener.mailboxStates {
		if state.lastError != "" {
			listener.lastError = state.lastError
			return
		}
	}
}

func imapMailboxStateKey(mailbox MailboxKey) string {
	return strings.ToLower(strings.TrimSpace(imapMailboxName(mailbox.Mailbox)))
}

func priorityIMAPMailboxes(mailboxes []MailboxKey, selector MailboxSelector) []MailboxKey {
	byName := make(map[string]MailboxKey, len(mailboxes)*2)
	for _, mailbox := range mailboxes {
		for _, name := range []string{mailbox.Mailbox.ID, mailbox.Mailbox.Name} {
			name = strings.TrimSpace(name)
			if name != "" {
				byName[strings.ToLower(name)] = mailbox
			}
		}
	}

	selected := make([]MailboxKey, 0, len(mailboxes))
	seen := make(map[string]struct{}, len(mailboxes))
	appendMailbox := func(mailbox MailboxKey) {
		identity := strings.ToLower(strings.TrimSpace(imapMailboxName(mailbox.Mailbox)))
		if identity == "" {
			return
		}
		if _, duplicate := seen[identity]; duplicate {
			return
		}
		if imapMailboxExcluded(mailbox.Mailbox, selector.Exclude) {
			return
		}
		seen[identity] = struct{}{}
		selected = append(selected, mailbox)
	}

	if len(selector.Include) != 0 {
		for _, configured := range selector.Include {
			if mailbox, ok := byName[strings.ToLower(strings.TrimSpace(configured))]; ok {
				appendMailbox(mailbox)
			}
		}
		return selected
	}
	for _, mailbox := range mailboxes {
		appendMailbox(mailbox)
	}
	return selected
}

func imapMailboxExcluded(mailbox Mailbox, excluded []string) bool {
	identities := map[string]struct{}{
		strings.ToLower(strings.TrimSpace(mailbox.ID)):   {},
		strings.ToLower(strings.TrimSpace(mailbox.Name)): {},
	}
	for _, name := range excluded {
		if _, found := identities[strings.ToLower(strings.TrimSpace(name))]; found {
			return true
		}
	}
	return false
}

func dialIMAPIdleClient(ctx context.Context, config IMAPConnectorConfig) (imapIdleClient, error) {
	client, err := dialGoIMAPClient(ctx, config)
	if err != nil {
		return nil, err
	}
	idleClient, ok := client.(imapIdleClient)
	if !ok {
		_ = client.Close()
		return nil, errors.New("mail: IMAP client does not support IDLE")
	}
	return idleClient, nil
}

func shutdownIMAPIdleClient(parent context.Context, client imapIdleClient, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	_ = client.Logout(ctx)
	_ = client.Close()
}

// Idle runs one IDLE command and reports the first unilateral update. The
// caller closes stop before issuing any other command on this connection.
func (c *goIMAPClient) Idle(ctx context.Context, stop <-chan struct{}, changes chan<- struct{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("IMAP client is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	updates := make(chan imapclient.Update, 16)
	c.client.Updates = updates
	defer func() { c.client.Updates = nil }()

	forwardStop := make(chan struct{})
	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-forwardStop:
				return
			case _, ok := <-updates:
				if !ok {
					return
				}
				select {
				case changes <- struct{}{}:
				default:
				}
			}
		}
	}()

	combinedStop := make(chan struct{})
	var stopOnce sync.Once
	stopIdle := func() { stopOnce.Do(func() { close(combinedStop) }) }
	idleReturned := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_ = c.conn.SetDeadline(time.Now())
			stopIdle()
		case <-stop:
			stopIdle()
		case <-idleReturned:
		}
	}()

	err := c.client.Idle(combinedStop, &imapclient.IdleOptions{PollInterval: -1})
	close(idleReturned)
	close(forwardStop)
	<-forwardDone
	<-watchDone
	_ = c.conn.SetDeadline(time.Time{})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

var _ Listener = (*IMAPIdleListener)(nil)
