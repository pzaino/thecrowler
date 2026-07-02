package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestStableMessageIdentityDuplicateProviderReferences(t *testing.T) {
	t.Parallel()

	fingerprint := strings.Repeat("a", 64)
	first, err := StableMessageIdentity("source", MessageRef{
		Provider:          "graph",
		AccountID:         "account",
		Mailbox:           Mailbox{ID: "inbox"},
		ProviderMessageID: "provider-message",
	}, fingerprint)
	if err != nil {
		t.Fatalf("StableMessageIdentity(first) error = %v", err)
	}
	duplicate, err := StableMessageIdentity("source", MessageRef{
		Provider:          "graph",
		AccountID:         "account",
		Mailbox:           Mailbox{ID: "inbox"},
		ProviderMessageID: "provider-message",
	}, fingerprint)
	if err != nil {
		t.Fatalf("StableMessageIdentity(duplicate) error = %v", err)
	}

	if first.ID != duplicate.ID {
		t.Fatalf("duplicate IDs differ: %q != %q", first.ID, duplicate.ID)
	}
	if first.Strategy != IdentityProviderID {
		t.Errorf("Strategy = %q, want %q", first.Strategy, IdentityProviderID)
	}
}

func TestStableProviderIdentitySurvivesMailboxMove(t *testing.T) {
	t.Parallel()

	ref := MessageRef{
		Provider:          "gmail",
		AccountID:         "account",
		Mailbox:           Mailbox{ID: "inbox"},
		ProviderMessageID: "stable-message",
	}
	before, err := StableMessageIdentity("source", ref, strings.Repeat("b", 64))
	if err != nil {
		t.Fatalf("StableMessageIdentity(before move) error = %v", err)
	}
	ref.Mailbox = Mailbox{ID: "archive"}
	after, err := StableMessageIdentity("source", ref, strings.Repeat("b", 64))
	if err != nil {
		t.Fatalf("StableMessageIdentity(after move) error = %v", err)
	}

	if before.ID != after.ID {
		t.Errorf("provider identity changed across move: %q != %q", before.ID, after.ID)
	}
}

func TestIMAPIdentityChangesAcrossMoveAndUIDValidityReset(t *testing.T) {
	t.Parallel()

	ref := MessageRef{
		Provider:    "imap",
		AccountID:   "account",
		Mailbox:     Mailbox{ID: "inbox"},
		UIDValidity: 10,
		UID:         42,
	}
	original, err := StableMessageIdentity("source", ref, strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("StableMessageIdentity(original) error = %v", err)
	}

	movedRef := ref
	movedRef.Mailbox = Mailbox{ID: "archive"}
	moved, err := StableMessageIdentity("source", movedRef, strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("StableMessageIdentity(moved) error = %v", err)
	}
	resetRef := ref
	resetRef.UIDValidity++
	reset, err := StableMessageIdentity("source", resetRef, strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("StableMessageIdentity(reset) error = %v", err)
	}

	if original.Strategy != IdentityIMAPUID {
		t.Errorf("Strategy = %q, want %q", original.Strategy, IdentityIMAPUID)
	}
	if original.ID == moved.ID {
		t.Error("IMAP identity did not change after mailbox move")
	}
	if original.ID == reset.ID {
		t.Error("IMAP identity did not change after UIDVALIDITY reset")
	}
	if original.Fingerprint != moved.Fingerprint || original.Fingerprint != reset.Fingerprint {
		t.Error("content fingerprint should remain usable as deduplication evidence")
	}
}

func TestContentIdentityFallbackScopesCopiesByMailbox(t *testing.T) {
	t.Parallel()

	fingerprint := strings.Repeat("d", 64)
	inbox, err := StableMessageIdentity("source", MessageRef{
		AccountID: "account",
		Mailbox:   Mailbox{ID: "inbox"},
	}, fingerprint)
	if err != nil {
		t.Fatalf("StableMessageIdentity(inbox) error = %v", err)
	}
	archive, err := StableMessageIdentity("source", MessageRef{
		AccountID: "account",
		Mailbox:   Mailbox{ID: "archive"},
	}, fingerprint)
	if err != nil {
		t.Fatalf("StableMessageIdentity(archive) error = %v", err)
	}

	if inbox.Strategy != IdentityContentSHA256 {
		t.Errorf("Strategy = %q, want %q", inbox.Strategy, IdentityContentSHA256)
	}
	if inbox.ID == archive.ID {
		t.Error("content fallback collapsed copies in separate mailboxes")
	}
	if inbox.Fingerprint != archive.Fingerprint {
		t.Error("equal content should retain equal deduplication fingerprints")
	}
}

func TestProcessorIdentifiesMessageWithoutMessageID(t *testing.T) {
	t.Parallel()

	raw := "From: sender@example.test\r\nTo: receiver@example.test\r\nSubject: no message id\r\n\r\nbody\r\n"
	document, err := NewProcessor("source").Process(context.Background(), RawMessage{
		Ref: MessageRef{
			Provider:    "imap",
			AccountID:   "account",
			Mailbox:     Mailbox{ID: "inbox"},
			UIDValidity: 77,
			UID:         8,
		},
		RFC822: io.NopCloser(strings.NewReader(raw)),
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	wantHash := sha256.Sum256([]byte(raw))
	if document.MessageID != "" {
		t.Errorf("MessageID = %q, want empty", document.MessageID)
	}
	if document.ID == "" || document.IdentityStrategy != IdentityIMAPUID {
		t.Errorf("identity = (%q, %q), want non-empty IMAP identity", document.ID, document.IdentityStrategy)
	}
	if document.ContentFingerprint != hex.EncodeToString(wantHash[:]) {
		t.Errorf("Fingerprint = %q, want %q", document.ContentFingerprint, hex.EncodeToString(wantHash[:]))
	}
}

func TestSHA256Content(t *testing.T) {
	t.Parallel()

	got, err := SHA256Content(strings.NewReader("same content"))
	if err != nil {
		t.Fatalf("SHA256Content() error = %v", err)
	}
	want := sha256.Sum256([]byte("same content"))
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("SHA256Content() = %q, want %q", got, hex.EncodeToString(want[:]))
	}
}
