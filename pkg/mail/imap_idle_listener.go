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

	mu      sync.Mutex
	running bool
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

// Listen implements Listener. It returns when ctx is cancelled or when any
// mailbox session fails; cancellation of one session shuts down all others.
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
	if !listener.start() {
		return ErrIMAPIdleListenerRunning
	}
	defer listener.stop()

	selected := priorityIMAPMailboxes(mailboxes, listener.config.Mailboxes)
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
	client, err := listener.factory(ctx, listener.config)
	if err != nil {
		return fmt.Errorf("mail: connect IMAP IDLE mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
	}
	defer shutdownIMAPIdleClient(client, listener.config.Timeout)

	if err := client.Authenticate(ctx, listener.config.Auth); err != nil {
		return imapError("authenticate IDLE client", err)
	}
	if _, err := client.SelectMailbox(ctx, imapMailboxName(mailbox.Mailbox)); err != nil {
		return imapError("select IDLE mailbox", err)
	}

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

		select {
		case <-ctx.Done():
			close(stop)
			<-idleDone
			return ctx.Err()
		case err := <-idleDone:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err == nil {
				err = errors.New("IDLE session ended without a change notification")
			}
			return fmt.Errorf("mail: IDLE mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
		case <-changes:
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
				return fmt.Errorf("mail: enqueue reconciliation for mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
			}
		}
	}
}

func (listener *IMAPIdleListener) start() bool {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.running {
		return false
	}
	listener.running = true
	return true
}

func (listener *IMAPIdleListener) stop() {
	listener.mu.Lock()
	listener.running = false
	listener.mu.Unlock()
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

func shutdownIMAPIdleClient(client imapIdleClient, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
