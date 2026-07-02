package mail

import (
	"container/list"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	maxGraphNotificationPayloadBytes = 1 << 20
	maxGraphClientStateBytes         = 1024
	defaultGraphNotificationDedup    = 4096
)

// ErrMalformedGraphChangeNotification identifies a Graph webhook payload that
// is malformed, unauthenticated, unsupported, or inconsistent with its
// configured subscription.
var ErrMalformedGraphChangeNotification = errors.New("mail: malformed Microsoft Graph change notification")

// GraphNotificationConfig supplies the trusted routing and authentication data
// that must not be inferred from a webhook body. SubscriptionID is optional;
// when set, notifications for any other subscription are rejected.
type GraphNotificationConfig struct {
	AccountID      string
	Mailbox        Mailbox
	ClientState    string
	SubscriptionID string
}

// GraphChangeNotificationReceiver validates Graph webhook batches, converts
// them to provider-neutral events, and suppresses bounded redeliveries after
// they have been accepted by the queue.
type GraphChangeNotificationReceiver struct {
	queue  EmailChangeQueue
	config GraphNotificationConfig

	mu       sync.Mutex
	seen     map[string]*list.Element
	order    *list.List
	capacity int
}

type graphNotificationDedupEntry struct {
	id string
}

// NewGraphChangeNotificationReceiver creates a receiver for one configured
// Graph account and subscription client-state secret.
func NewGraphChangeNotificationReceiver(queue EmailChangeQueue, config GraphNotificationConfig) (*GraphChangeNotificationReceiver, error) {
	if queue == nil {
		return nil, errors.New("mail: Graph change notification receiver requires an email change queue")
	}
	if err := validateGraphNotificationConfig(config); err != nil {
		return nil, err
	}
	return &GraphChangeNotificationReceiver{
		queue: queue, config: config, capacity: defaultGraphNotificationDedup,
		seen: make(map[string]*list.Element, defaultGraphNotificationDedup), order: list.New(),
	}, nil
}

// Handle validates and enqueues every unique notification in a Graph batch.
// The returned count includes only newly accepted events. Event identities are
// remembered only after a successful enqueue, so failed deliveries can retry.
func (r *GraphChangeNotificationReceiver) Handle(ctx context.Context, payload []byte) (int, error) {
	if r == nil || r.queue == nil {
		return 0, errors.New("mail: Graph change notification receiver is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	events, err := DecodeGraphChangeNotifications(payload, r.config)
	if err != nil {
		return 0, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	accepted := 0
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return accepted, err
		}
		if _, duplicate := r.seen[event.Metadata.EventID]; duplicate {
			continue
		}
		if err := r.queue.Enqueue(ctx, event); err != nil {
			return accepted, fmt.Errorf("mail: enqueue Microsoft Graph delta reconciliation: %w", err)
		}
		r.remember(event.Metadata.EventID)
		accepted++
	}
	return accepted, nil
}

// DecodeGraphChangeNotifications validates a complete Graph change-notification
// collection and converts its unique values to closed EmailChangeEvent values.
func DecodeGraphChangeNotifications(payload []byte, config GraphNotificationConfig) ([]EmailChangeEvent, error) {
	if err := validateGraphNotificationConfig(config); err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, malformedGraphNotification("payload is empty", nil)
	}
	if len(payload) > maxGraphNotificationPayloadBytes {
		return nil, malformedGraphNotification("payload exceeds size limit", nil)
	}

	var collection struct {
		Value []graphChangeNotification `json:"value"`
	}
	if err := decodeSingleJSON(payload, &collection); err != nil {
		return nil, malformedGraphNotification("decode notification collection", err)
	}
	if len(collection.Value) == 0 {
		return nil, malformedGraphNotification("notification collection is empty", nil)
	}

	events := make([]EmailChangeEvent, 0, len(collection.Value))
	seen := make(map[string]struct{}, len(collection.Value))
	for index, notification := range collection.Value {
		event, err := graphNotificationEvent(notification, config)
		if err != nil {
			return nil, malformedGraphNotification(fmt.Sprintf("value[%d]", index), err)
		}
		if _, duplicate := seen[event.Metadata.EventID]; duplicate {
			continue
		}
		seen[event.Metadata.EventID] = struct{}{}
		events = append(events, event)
	}
	return events, nil
}

type graphChangeNotification struct {
	ID                             string `json:"id"`
	SubscriptionID                 string `json:"subscriptionId"`
	SubscriptionExpirationDateTime string `json:"subscriptionExpirationDateTime"`
	ClientState                    string `json:"clientState"`
	ChangeType                     string `json:"changeType"`
	LifecycleEvent                 string `json:"lifecycleEvent"`
	Resource                       string `json:"resource"`
	TenantID                       string `json:"tenantId"`
	ResourceData                   struct {
		ODataType string `json:"@odata.type"`
		ODataID   string `json:"@odata.id"`
		ODataETag string `json:"@odata.etag"`
		ID        string `json:"id"`
	} `json:"resourceData"`
}

func graphNotificationEvent(notification graphChangeNotification, config GraphNotificationConfig) (EmailChangeEvent, error) {
	if strings.TrimSpace(notification.SubscriptionID) == "" {
		return EmailChangeEvent{}, errors.New("subscriptionId is required")
	}
	if config.SubscriptionID != "" && notification.SubscriptionID != config.SubscriptionID {
		return EmailChangeEvent{}, errors.New("subscriptionId does not match configured subscription")
	}
	if !constantTimeStringEqual(notification.ClientState, config.ClientState) {
		return EmailChangeEvent{}, errors.New("clientState does not match configured secret")
	}

	changeType := strings.TrimSpace(notification.ChangeType)
	lifecycleEvent := strings.TrimSpace(notification.LifecycleEvent)
	if (changeType == "") == (lifecycleEvent == "") {
		return EmailChangeEvent{}, errors.New("exactly one of changeType or lifecycleEvent is required")
	}

	kind := ChangeUpsert
	status := ListenerStatusActive
	if changeType != "" {
		if strings.TrimSpace(notification.Resource) == "" {
			return EmailChangeEvent{}, errors.New("resource is required for a change notification")
		}
		switch changeType {
		case "created", "updated":
			kind = ChangeUpsert
		case "deleted":
			kind = ChangeDelete
		default:
			return EmailChangeEvent{}, fmt.Errorf("unsupported changeType %q", changeType)
		}
	} else {
		kind = ChangeReset
		switch lifecycleEvent {
		case "reauthorizationRequired", "missed":
			status = ListenerStatusDegraded
		case "subscriptionRemoved":
			status = ListenerStatusStopped
		default:
			return EmailChangeEvent{}, fmt.Errorf("unsupported lifecycleEvent %q", lifecycleEvent)
		}
	}

	eventID, err := graphNotificationIdentity(notification)
	if err != nil {
		return EmailChangeEvent{}, err
	}
	event := EmailChangeEvent{
		Provider:     graphProvider,
		AccountID:    strings.TrimSpace(config.AccountID),
		Mailbox:      config.Mailbox,
		Cursor:       Cursor{Token: eventID},
		SafeIdentity: SafeEmailEventIdentity(graphProvider, config.AccountID, config.Mailbox),
		ChangeType:   kind,
		Metadata: EmailEventMetadata{
			EventID:        eventID,
			ListenerMode:   ListenerModeWebhook,
			ListenerStatus: status,
		},
	}
	if err := event.Validate(); err != nil {
		return EmailChangeEvent{}, err
	}
	return event, nil
}

func graphNotificationIdentity(notification graphChangeNotification) (string, error) {
	encoded, err := json.Marshal(notification)
	if err != nil {
		return "", fmt.Errorf("encode notification identity: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func validateGraphNotificationConfig(config GraphNotificationConfig) error {
	if strings.TrimSpace(config.AccountID) == "" {
		return malformedGraphNotification("configured account ID is required", nil)
	}
	if strings.TrimSpace(config.Mailbox.ID) == "" && strings.TrimSpace(config.Mailbox.Name) == "" {
		return malformedGraphNotification("configured mailbox requires an ID or name", nil)
	}
	if config.ClientState == "" {
		return malformedGraphNotification("configured clientState is required", nil)
	}
	if len(config.ClientState) > maxGraphClientStateBytes {
		return malformedGraphNotification("configured clientState exceeds size limit", nil)
	}
	if len(config.SubscriptionID) > maxEmailEventStringBytes {
		return malformedGraphNotification("configured subscription ID exceeds size limit", nil)
	}
	return nil
}

func constantTimeStringEqual(actual, expected string) bool {
	actualHash := sha256.Sum256([]byte(actual))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(actualHash[:], expectedHash[:]) == 1
}

func (r *GraphChangeNotificationReceiver) remember(id string) {
	if r.capacity <= 0 {
		return
	}
	if len(r.seen) >= r.capacity {
		oldest := r.order.Front()
		if oldest != nil {
			delete(r.seen, oldest.Value.(graphNotificationDedupEntry).id)
			r.order.Remove(oldest)
		}
	}
	element := r.order.PushBack(graphNotificationDedupEntry{id: id})
	r.seen[id] = element
}

func malformedGraphNotification(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrMalformedGraphChangeNotification, message)
	}
	return fmt.Errorf("%w: %s: %v", ErrMalformedGraphChangeNotification, message, cause)
}
