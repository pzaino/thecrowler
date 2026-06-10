package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// IdentityStrategy describes the strongest provider-neutral evidence used to
// construct a stable message identity.
type IdentityStrategy string

const (
	// IdentityProviderID uses an API provider's stable, account-scoped message
	// identifier. It intentionally excludes the mailbox so the identity survives
	// label changes and moves when the provider identifier does.
	IdentityProviderID IdentityStrategy = "provider_id"
	// IdentityIMAPUID uses the mailbox-scoped IMAP identity tuple. Both
	// UIDVALIDITY and UID are required because a UID can be reused after the
	// mailbox's UIDVALIDITY changes.
	IdentityIMAPUID IdentityStrategy = "imap_uid"
	// IdentityContentSHA256 is the last-resort identity when no stable provider
	// identity is available. Its mailbox scope prevents equal copies in separate
	// mailboxes from being collapsed; Fingerprint can still be compared as
	// deduplication evidence.
	IdentityContentSHA256 IdentityStrategy = "content_sha256"
)

// MessageIdentity contains the index identity selected for a message and its
// content fingerprint. Fingerprint is independent of Strategy and can be used
// as deduplication or change-recognition evidence even when a stronger
// provider identity is available.
type MessageIdentity struct {
	ID          string           `json:"id" yaml:"id"`
	Strategy    IdentityStrategy `json:"strategy" yaml:"strategy"`
	Fingerprint string           `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
}

// SHA256Content streams content into a SHA-256 digest and returns its lowercase
// hexadecimal representation.
func SHA256Content(content io.Reader) (string, error) {
	if content == nil {
		return "", errors.New("mail: cannot fingerprint nil content")
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, content); err != nil {
		return "", fmt.Errorf("mail: fingerprint content: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// StableMessageIdentity selects the strongest available identity in this
// order: stable provider message ID, IMAP UID tuple, then a SHA-256 content
// fingerprint. Provider IDs are account-scoped and survive mailbox moves;
// IMAP identities include mailbox and UIDVALIDITY because UIDs have no meaning
// outside that scope.
func StableMessageIdentity(sourceID string, ref MessageRef, fingerprint string) (MessageIdentity, error) {
	sourceID = strings.TrimSpace(sourceID)
	accountID := strings.TrimSpace(ref.AccountID)
	if sourceID == "" {
		return MessageIdentity{}, errors.New("mail: stable identity requires source ID")
	}
	if accountID == "" {
		return MessageIdentity{}, errors.New("mail: stable identity requires account ID")
	}

	provider := strings.TrimSpace(ref.Provider)
	providerMessageID := strings.TrimSpace(ref.ProviderMessageID)
	if providerMessageID != "" {
		return MessageIdentity{
			ID:          identityID(IdentityProviderID, sourceID, accountID, provider, providerMessageID),
			Strategy:    IdentityProviderID,
			Fingerprint: normalizedFingerprint(fingerprint),
		}, nil
	}

	mailboxID := stableMailboxID(ref.Mailbox)
	if ref.UID != 0 && ref.UIDValidity != 0 && mailboxID != "" {
		return MessageIdentity{
			ID: identityID(
				IdentityIMAPUID,
				sourceID,
				accountID,
				mailboxID,
				strconv.FormatUint(uint64(ref.UIDValidity), 10),
				strconv.FormatUint(uint64(ref.UID), 10),
			),
			Strategy:    IdentityIMAPUID,
			Fingerprint: normalizedFingerprint(fingerprint),
		}, nil
	}

	fingerprint = normalizedFingerprint(fingerprint)
	if fingerprint == "" {
		return MessageIdentity{}, errors.New("mail: stable identity requires a provider ID, an IMAP UID tuple, or a SHA-256 content fingerprint")
	}
	if mailboxID == "" {
		return MessageIdentity{}, errors.New("mail: content identity requires a mailbox ID or name")
	}
	return MessageIdentity{
		ID:          identityID(IdentityContentSHA256, sourceID, accountID, mailboxID, fingerprint),
		Strategy:    IdentityContentSHA256,
		Fingerprint: fingerprint,
	}, nil
}

func stableMailboxID(mailbox Mailbox) string {
	if id := strings.TrimSpace(mailbox.ID); id != "" {
		return id
	}
	return strings.TrimSpace(mailbox.Name)
}

func normalizedFingerprint(fingerprint string) string {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if len(fingerprint) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return ""
	}
	return fingerprint
}

func identityID(strategy IdentityStrategy, components ...string) string {
	encoded := make([]string, 0, len(components)+2)
	encoded = append(encoded, "mail", string(strategy))
	for _, component := range components {
		encoded = append(encoded, url.QueryEscape(component))
	}
	return strings.Join(encoded, ":")
}
