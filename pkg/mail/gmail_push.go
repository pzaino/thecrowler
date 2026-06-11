package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	maxGmailPushPayloadBytes = 1 << 20
	maxGmailNotificationData = 64 << 10
)

// ErrMalformedGmailPushNotification identifies a push payload that cannot be
// trusted as a Gmail account change hint.
var ErrMalformedGmailPushNotification = errors.New("mail: malformed Gmail push notification")

// EmailChangeQueue accepts mailbox-account change hints for asynchronous delta
// reconciliation. Implementations may be in-memory, database-backed, or an
// adapter to another job system; GmailPushReceiver does not require Pub/Sub.
type EmailChangeQueue interface {
	Enqueue(ctx context.Context, event EmailChangeEvent) error
}

// GmailPushReceiver decodes Google Pub/Sub push envelopes and enqueues only
// monotonically newer Gmail history hints for each normalized account.
type GmailPushReceiver struct {
	mu       sync.Mutex
	queue    EmailChangeQueue
	latestID map[string]uint64
}

// NewGmailPushReceiver creates a transport-independent Gmail push receiver.
func NewGmailPushReceiver(queue EmailChangeQueue) (*GmailPushReceiver, error) {
	if queue == nil {
		return nil, errors.New("mail: Gmail push receiver requires an email change queue")
	}
	return &GmailPushReceiver{queue: queue, latestID: make(map[string]uint64)}, nil
}

// Handle decodes payload and enqueues a delta reconciliation hint. It returns
// false without an error for a duplicate or out-of-order history ID. The
// monotonic position advances only after the queue accepts the event, allowing
// a failed enqueue to be retried.
func (r *GmailPushReceiver) Handle(ctx context.Context, payload []byte) (bool, error) {
	if r == nil || r.queue == nil {
		return false, errors.New("mail: Gmail push receiver is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	event, err := DecodeGmailPushNotification(payload)
	if err != nil {
		return false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if event.Cursor.HistoryID <= r.latestID[event.AccountID] {
		return false, nil
	}
	if err := r.queue.Enqueue(ctx, event); err != nil {
		return false, fmt.Errorf("mail: enqueue Gmail delta reconciliation: %w", err)
	}
	r.latestID[event.AccountID] = event.Cursor.HistoryID
	return true, nil
}

// DecodeGmailPushNotification validates a Google Pub/Sub push envelope and its
// base64-encoded Gmail notification data without contacting Pub/Sub or Gmail.
func DecodeGmailPushNotification(payload []byte) (EmailChangeEvent, error) {
	if len(payload) == 0 {
		return EmailChangeEvent{}, malformedGmailPush("payload is empty", nil)
	}
	if len(payload) > maxGmailPushPayloadBytes {
		return EmailChangeEvent{}, malformedGmailPush("payload exceeds size limit", nil)
	}

	var envelope struct {
		Message struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	if err := decodeSingleJSON(payload, &envelope); err != nil {
		return EmailChangeEvent{}, malformedGmailPush("decode Pub/Sub envelope", err)
	}
	if envelope.Message.Data == "" {
		return EmailChangeEvent{}, malformedGmailPush("Pub/Sub message data is empty", nil)
	}

	data, err := decodeGmailPushData(envelope.Message.Data)
	if err != nil {
		return EmailChangeEvent{}, malformedGmailPush("decode Pub/Sub message data", err)
	}
	if len(data) > maxGmailNotificationData {
		return EmailChangeEvent{}, malformedGmailPush("decoded message data exceeds size limit", nil)
	}

	var notification struct {
		EmailAddress string `json:"emailAddress"`
		HistoryID    string `json:"historyId"`
	}
	if err := decodeSingleJSON(data, &notification); err != nil {
		return EmailChangeEvent{}, malformedGmailPush("decode Gmail notification", err)
	}

	accountID, err := safeGmailAccount(notification.EmailAddress)
	if err != nil {
		return EmailChangeEvent{}, malformedGmailPush("invalid Gmail account", err)
	}
	historyID, err := strconv.ParseUint(notification.HistoryID, 10, 64)
	if err != nil || historyID == 0 {
		if err == nil {
			err = errors.New("history ID must be greater than zero")
		}
		return EmailChangeEvent{}, malformedGmailPush("invalid Gmail history ID", err)
	}

	mailbox := Mailbox{ID: "*", Name: "All mailboxes"}
	return EmailChangeEvent{
		Provider:     gmailProvider,
		AccountID:    accountID,
		Mailbox:      mailbox,
		Cursor:       Cursor{HistoryID: historyID},
		SafeIdentity: SafeEmailEventIdentity(gmailProvider, accountID, mailbox),
		ChangeType:   ChangeUpsert,
		Metadata: EmailEventMetadata{
			ListenerMode:   ListenerModePush,
			ListenerStatus: ListenerStatusActive,
		},
	}, nil
}

func decodeGmailPushData(encoded string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(encoded)
}

func decodeSingleJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func safeGmailAccount(value string) (string, error) {
	account := strings.TrimSpace(value)
	if account == "" || len(account) > 254 || strings.Count(account, "@") != 1 {
		return "", errors.New("account must be a bounded email address")
	}
	for _, r := range account {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("account contains unsafe characters")
		}
	}
	parsed, err := mail.ParseAddress(account)
	if err != nil || parsed.Name != "" || parsed.Address != account {
		if err == nil {
			err = errors.New("account must be a bare email address")
		}
		return "", err
	}
	return strings.ToLower(account), nil
}

func malformedGmailPush(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrMalformedGmailPushNotification, message)
	}
	return fmt.Errorf("%w: %s: %v", ErrMalformedGmailPushNotification, message, cause)
}

// GmailWatchRenewer adapts Gmail watch registration to the provider-neutral
// SubscriptionRenewer boundary.
type GmailWatchRenewer struct {
	Connector *GmailConnector
	TopicName string
	LabelIDs  []string
}

// Renew registers a fresh Gmail watch and returns its provider expiration.
func (r GmailWatchRenewer) Renew(ctx context.Context, key MailboxKey) (RenewalResult, error) {
	if r.Connector == nil {
		return RenewalResult{}, errors.New("mail: Gmail watch renewer requires a connector")
	}
	if key.Provider != "" && !strings.EqualFold(strings.TrimSpace(key.Provider), gmailProvider) {
		return RenewalResult{}, fmt.Errorf("mail: Gmail watch renewer cannot renew provider %q", key.Provider)
	}
	watch, err := r.Connector.RenewWatch(ctx, r.TopicName, r.LabelIDs)
	if err != nil {
		return RenewalResult{}, err
	}
	return RenewalResult{ExpiresAt: watch.ExpiresAt}, nil
}

var _ SubscriptionRenewer = GmailWatchRenewer{}
