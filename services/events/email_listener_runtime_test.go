package main

import (
	"testing"
	"time"

	mail "github.com/pzaino/thecrowler/pkg/mail"
)

func TestEmailListenerMailboxesAreBoundToConfiguredSource(t *testing.T) {
	config := mail.DefaultSourceConfig()
	config.Connector.Provider = "imap"
	config.Auth.Identity = "account-1"
	config.Mailboxes.Include = []string{" INBOX ", "Archive", ""}

	mailboxes := emailListenerMailboxes(42, config)
	if len(mailboxes) != 2 {
		t.Fatalf("mailbox count = %d, want 2", len(mailboxes))
	}
	for index, want := range []string{"INBOX", "Archive"} {
		got := mailboxes[index]
		if got.SourceID != "42" || got.Provider != "imap" || got.AccountID != "account-1" || got.Mailbox.Name != want {
			t.Fatalf("mailbox[%d] = %#v", index, got)
		}
	}
}

func TestNonIMAPListenerUsesConfiguredSafetyNetInterval(t *testing.T) {
	manager := &emailListenerManager{}
	config := mail.DefaultSourceConfig()
	config.Connector.Provider = "gmail"
	config.Reconciliation.PollInterval = 17 * time.Minute

	listener, _, err := manager.listenerFor(emailListenerSource{ID: 42, Config: config})
	if err != nil {
		t.Fatalf("listenerFor() error = %v", err)
	}
	polling, ok := listener.(*mail.PollingListener)
	if !ok {
		t.Fatalf("listener type = %T, want *mail.PollingListener", listener)
	}
	if polling.Interval != 17*time.Minute {
		t.Fatalf("poll interval = %v, want 17m", polling.Interval)
	}
}
