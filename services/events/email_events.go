package main

import (
	"encoding/json"
	"fmt"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

type emailLifecyclePayload interface {
	Validate() error
}

// validateEmailLifecycleEvent validates the closed email lifecycle contracts.
// Other event types remain schema-agnostic for backward compatibility.
func validateEmailLifecycleEvent(event cdb.Event) error {
	var payload emailLifecyclePayload
	switch event.Type {
	case mail.EmailEventMessageDiscovered:
		payload = &mail.MessageDiscoveredEventPayload{}
	case mail.EmailEventMessageFetched:
		payload = &mail.MessageFetchedEventPayload{}
	case mail.EmailEventMessageParsed:
		payload = &mail.MessageParsedEventPayload{}
	case mail.EmailEventMessageFailed:
		payload = &mail.MessageFailedEventPayload{}
	case mail.EmailEventMessageCompleted:
		payload = &mail.MessageCompletedEventPayload{}
	case mail.EmailEventListenerStarted:
		payload = &mail.ListenerStartedEventPayload{}
	case mail.EmailEventListenerStopped:
		payload = &mail.ListenerStoppedEventPayload{}
	case mail.EmailEventReconciliationCompleted:
		payload = &mail.ReconciliationCompletedEventPayload{}
	default:
		return nil
	}

	details, err := json.Marshal(event.Details)
	if err != nil {
		return fmt.Errorf("invalid %s details: %w", event.Type, err)
	}
	if err := json.Unmarshal(details, payload); err != nil {
		return fmt.Errorf("invalid %s details: %w", event.Type, err)
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("invalid %s details: %w", event.Type, err)
	}
	return nil
}
