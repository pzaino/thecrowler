package mail

import (
	"context"
	"encoding/json"
	"fmt"
)

// LifecycleEvent is the validated, serialized boundary passed to a
// LifecycleEventSink. Details contains one of the closed email lifecycle
// payloads from event_payloads.go; raw messages, headers, addresses, subjects,
// credentials, provider errors, and attachment content are never included.
type LifecycleEvent struct {
	Type    string          `json:"type"`
	Details json.RawMessage `json:"details"`
}

// LifecycleEventSink accepts operational email lifecycle events. Delivery is
// observational: Pipeline retries transient sink failures according to
// LifecycleEventRetryPolicy, then drops the event. Sink failures and panics do
// not fail message processing or prevent checkpoint commits.
type LifecycleEventSink interface {
	EmitLifecycleEvent(ctx context.Context, event LifecycleEvent) error
}

func newLifecycleEvent(eventType string, payload any) (LifecycleEvent, error) {
	if !validEmailLifecycleEventType(eventType) {
		return LifecycleEvent{}, fmt.Errorf("mail: invalid lifecycle event type %q", eventType)
	}
	details, err := json.Marshal(payload)
	if err != nil {
		return LifecycleEvent{}, fmt.Errorf("mail: marshal lifecycle event %q: %w", eventType, err)
	}
	return LifecycleEvent{Type: eventType, Details: details}, nil
}

func validEmailLifecycleEventType(eventType string) bool {
	switch eventType {
	case EmailEventMessageDiscovered,
		EmailEventMessageFetched,
		EmailEventMessageParsed,
		EmailEventMessageFailed,
		EmailEventMessageCompleted,
		EmailEventListenerStarted,
		EmailEventListenerStopped,
		EmailEventReconciliationCompleted:
		return true
	default:
		return false
	}
}

func (p *Pipeline) lifecycleMessageRef(ref MessageRef) MessageRef {
	if ref.Provider == "" {
		ref.Provider = p.Provider
	}
	if ref.AccountID == "" {
		ref.AccountID = p.AccountID
	}
	return ref
}

func (p *Pipeline) emitLifecycleEvent(ctx context.Context, eventType string, payload any) {
	if p == nil || p.LifecycleEventSink == nil {
		return
	}
	event, err := newLifecycleEvent(eventType, payload)
	if err != nil {
		return
	}

	policy := p.lifecycleEventRetryPolicy()
	for attempt := 1; ; attempt++ {
		err = callLifecycleEventSink(ctx, p.LifecycleEventSink, event)
		if err == nil {
			return
		}
		decision := DecideRetry(err, attempt, policy)
		if decision.Action != RetryActionRetry {
			return
		}
		if err := p.sleep(ctx, decision.Delay); err != nil {
			return
		}
	}
}

func (p *Pipeline) lifecycleEventRetryPolicy() RetryPolicy {
	policy := p.LifecycleEventRetryPolicy
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	return policy.normalized()
}

func callLifecycleEventSink(ctx context.Context, sink LifecycleEventSink, event LifecycleEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("mail: lifecycle event sink panic")
		}
	}()
	return sink.EmitLifecycleEvent(ctx, event)
}
