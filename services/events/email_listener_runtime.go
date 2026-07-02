package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

const emailListenerSourceRefreshInterval = time.Minute

type emailListenerSource struct {
	ID     uint64
	Config mail.SourceConfig
}

type runningEmailListener struct {
	fingerprint string
	cancel      context.CancelFunc
}

// emailListenerManager owns provider listeners for all listen-mode email
// sources. It is started only by the master Events Manager replica.
type emailListenerManager struct {
	db          *cdb.Handler
	credentials map[string]cfg.EmailCredentialConfig
	refresh     time.Duration

	mu      sync.Mutex
	running map[uint64]runningEmailListener
}

func newEmailListenerManager(db *cdb.Handler, emailConfig cfg.EmailConfig) *emailListenerManager {
	credentials := make(map[string]cfg.EmailCredentialConfig, len(emailConfig.Credentials))
	for reference, credential := range emailConfig.Credentials {
		credentials[reference] = credential
	}
	return &emailListenerManager{
		db: db, credentials: credentials, refresh: emailListenerSourceRefreshInterval,
		running: make(map[uint64]runningEmailListener),
	}
}

func (manager *emailListenerManager) Run(ctx context.Context) {
	if manager == nil || manager.db == nil || *manager.db == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Email event listener cannot start without a database handler")
		return
	}
	if manager.refresh <= 0 {
		manager.refresh = emailListenerSourceRefreshInterval
	}
	manager.reconcile(ctx)
	ticker := time.NewTicker(manager.refresh)
	defer ticker.Stop()
	defer manager.stopAll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			manager.reconcile(ctx)
		}
	}
}

func (manager *emailListenerManager) reconcile(ctx context.Context) {
	sources, err := loadEmailListenerSources(ctx, manager.db)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Loading email listener sources: %v", err)
		return
	}

	desired := make(map[uint64]emailListenerSource, len(sources))
	for _, source := range sources {
		desired[source.ID] = source
		fingerprint := emailListenerFingerprint(source.Config)

		manager.mu.Lock()
		current, exists := manager.running[source.ID]
		manager.mu.Unlock()
		if exists && current.fingerprint == fingerprint {
			continue
		}
		if exists {
			current.cancel()
			manager.remove(source.ID, current.fingerprint)
		}
		manager.start(ctx, source, fingerprint)
	}

	manager.mu.Lock()
	stale := make([]runningEmailListener, 0)
	for sourceID, listener := range manager.running {
		if _, ok := desired[sourceID]; !ok {
			stale = append(stale, listener)
			delete(manager.running, sourceID)
		}
	}
	manager.mu.Unlock()
	for _, listener := range stale {
		listener.cancel()
	}
}

func (manager *emailListenerManager) start(parent context.Context, source emailListenerSource, fingerprint string) {
	listener, mailboxes, err := manager.listenerFor(source)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Configuring email listener for source %d: %v", source.ID, err)
		return
	}
	ctx, cancel := context.WithCancel(parent)
	manager.mu.Lock()
	manager.running[source.ID] = runningEmailListener{fingerprint: fingerprint, cancel: cancel}
	manager.mu.Unlock()

	go func() {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Starting email event listener for source %d (%s)", source.ID, source.Config.Connector.Provider)
		err := listener.Listen(ctx, mailboxes, sourceCrawlScheduleSink{db: manager.db, sourceID: source.ID})
		if err != nil && ctx.Err() == nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Email event listener for source %d stopped: %v", source.ID, err)
		}
		manager.remove(source.ID, fingerprint)
	}()
}

func (manager *emailListenerManager) listenerFor(source emailListenerSource) (mail.Listener, []mail.MailboxKey, error) {
	provider := strings.ToLower(strings.TrimSpace(source.Config.Connector.Provider))
	mailboxes := emailListenerMailboxes(source.ID, source.Config)
	if len(mailboxes) == 0 {
		return nil, nil, fmt.Errorf("no included mailboxes are configured")
	}
	if provider == "imap" {
		credential, ok := manager.credentials[strings.TrimSpace(source.Config.Auth.CredentialRef)]
		if !ok {
			return nil, nil, fmt.Errorf("credential reference %q is not configured", mail.RedactedValue)
		}
		imapConfig, err := mail.IMAPConnectorConfigFromSource(source.Config, mail.IMAPAuth{
			Username: credential.Username,
			Password: credential.Password,
		})
		if err != nil {
			return nil, nil, err
		}
		listener, err := mail.NewIMAPIdleListener(imapConfig)
		return listener, mailboxes, err
	}

	// Gmail and Graph notification receivers require externally reachable
	// webhook routes and provider subscription lifecycle management. Until those
	// routes are configured, keep the authoritative safety-net reconciliation on
	// the master listener owner rather than opening duplicate loops on engines.
	listener, err := mail.NewPollingListener(sourcePollingScheduler{db: manager.db, sourceID: source.ID}, source.Config.Reconciliation.PollInterval)
	return listener, mailboxes, err
}

func (manager *emailListenerManager) remove(sourceID uint64, fingerprint string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current, ok := manager.running[sourceID]; ok && current.fingerprint == fingerprint {
		delete(manager.running, sourceID)
	}
}

func (manager *emailListenerManager) stopAll() {
	manager.mu.Lock()
	listeners := make([]runningEmailListener, 0, len(manager.running))
	for _, listener := range manager.running {
		listeners = append(listeners, listener)
	}
	manager.running = make(map[uint64]runningEmailListener)
	manager.mu.Unlock()
	for _, listener := range listeners {
		listener.cancel()
	}
}

func loadEmailListenerSources(ctx context.Context, db *cdb.Handler) ([]emailListenerSource, error) {
	rows, err := (*db).QueryContext(ctx, `SELECT source_id, config
		FROM Sources
		WHERE disabled = FALSE
		  AND (
			LOWER(url) LIKE 'email://%'
			OR LOWER(url) LIKE 'imap://%'
			OR LOWER(url) LIKE 'imaps://%'
			OR LOWER(url) LIKE 'gmail://%'
			OR LOWER(url) LIKE 'graph-mail://%'
		  )`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []emailListenerSource
	for rows.Next() {
		var sourceID uint64
		var raw json.RawMessage
		if err := rows.Scan(&sourceID, &raw); err != nil {
			return nil, err
		}
		config, err := mail.ResolveSourceConfig(nil, raw)
		if err != nil {
			continue
		}
		if config.Crawl.Mode != mail.ModeListen || !config.Listener.Enabled {
			continue
		}
		if err := mail.ValidateSourceConfig(config); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Ignoring invalid email listener source %d: %v", sourceID, err)
			continue
		}
		sources = append(sources, emailListenerSource{ID: sourceID, Config: config})
	}
	return sources, rows.Err()
}

func emailListenerMailboxes(sourceID uint64, config mail.SourceConfig) []mail.MailboxKey {
	keys := make([]mail.MailboxKey, 0, len(config.Mailboxes.Include))
	for _, name := range config.Mailboxes.Include {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		keys = append(keys, mail.MailboxKey{
			SourceID:  strconv.FormatUint(sourceID, 10),
			Provider:  config.Connector.Provider,
			AccountID: config.Auth.Identity,
			Mailbox:   mail.Mailbox{ID: name, Name: name},
		})
	}
	return keys
}

func emailListenerFingerprint(config mail.SourceConfig) string {
	encoded, _ := json.Marshal(config)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type sourceCrawlScheduleSink struct {
	db       *cdb.Handler
	sourceID uint64
}

func (sink sourceCrawlScheduleSink) Notify(ctx context.Context, _ mail.MailboxKey) error {
	return cdb.ScheduleSourceCrawl(ctx, sink.db, sink.sourceID)
}

type sourcePollingScheduler sourceCrawlScheduleSink

func (reconciler sourcePollingScheduler) Reconcile(ctx context.Context, mailbox mail.MailboxKey) error {
	return sourceCrawlScheduleSink(reconciler).Notify(ctx, mailbox)
}
