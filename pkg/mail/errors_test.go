package mail

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDecideRetryFailureClasses(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxAttempts: 4, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 2 * time.Second}
	tests := []struct {
		name string
		err  error
		want RetryDecision
	}{
		{name: "transient connector", err: &Error{Kind: ErrorTransient}, want: RetryDecision{Action: RetryActionRetry, Reason: RetryReasonTransient, Delay: 100 * time.Millisecond}},
		{name: "typed timeout", err: &Error{Kind: ErrorTimeout}, want: RetryDecision{Action: RetryActionRetry, Reason: RetryReasonTimeout, Delay: 100 * time.Millisecond}},
		{name: "context timeout", err: fmt.Errorf("connector: %w", context.DeadlineExceeded), want: RetryDecision{Action: RetryActionRetry, Reason: RetryReasonTimeout, Delay: 100 * time.Millisecond}},
		{name: "rate limit", err: &Error{Kind: ErrorRateLimit, RetryAfter: 750 * time.Millisecond}, want: RetryDecision{Action: RetryActionRetry, Reason: RetryReasonRateLimit, Delay: 750 * time.Millisecond}},
		{name: "parser partial", err: &Error{Kind: ErrorPartial}, want: RetryDecision{Action: RetryActionRetry, Reason: RetryReasonParserPartial, Delay: 100 * time.Millisecond}},
		{name: "oversized", err: &Error{Kind: ErrorOversized}, want: RetryDecision{Action: RetryActionDiscard, Reason: RetryReasonOversized}},
		{name: "policy limit", err: &Error{Kind: ErrorPolicy}, want: RetryDecision{Action: RetryActionDiscard, Reason: RetryReasonOversized}},
		{name: "authentication", err: &Error{Kind: ErrorAuthentication}, want: RetryDecision{Action: RetryActionFail, Reason: RetryReasonAuthentication}},
		{name: "malformed", err: &Error{Kind: ErrorMalformed}, want: RetryDecision{Action: RetryActionDiscard, Reason: RetryReasonMalformed}},
		{name: "permission", err: &Error{Kind: ErrorPermission}, want: RetryDecision{Action: RetryActionFail, Reason: RetryReasonPermanent}},
		{name: "canceled", err: fmt.Errorf("connector: %w", context.Canceled), want: RetryDecision{Action: RetryActionFail, Reason: RetryReasonCanceled}},
		{name: "unknown", err: errors.New("unknown"), want: RetryDecision{Action: RetryActionFail, Reason: RetryReasonPermanent}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := DecideRetry(test.err, 1, policy); got != test.want {
				t.Fatalf("DecideRetry() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRetryPolicyBackoffIsExponentialAndBounded(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxAttempts: 10, InitialBackoff: 125 * time.Millisecond, MaxBackoff: time.Second}
	want := []time.Duration{125 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, time.Second, time.Second}
	for i, expected := range want {
		if got := policy.Backoff(i + 1); got != expected {
			t.Errorf("Backoff(%d) = %s, want %s", i+1, got, expected)
		}
	}
}

func TestDecideRetryCapsRetryAfterAndStopsAtAttemptBudget(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Second, MaxBackoff: 5 * time.Second}
	err := &Error{Kind: ErrorRateLimit, RetryAfter: time.Minute}
	if got, want := DecideRetry(err, 1, policy), (RetryDecision{Action: RetryActionRetry, Reason: RetryReasonRateLimit, Delay: 5 * time.Second}); got != want {
		t.Fatalf("DecideRetry(first failure) = %#v, want %#v", got, want)
	}
	if got, want := DecideRetry(err, 3, policy), (RetryDecision{Action: RetryActionFail, Reason: RetryReasonExhausted}); got != want {
		t.Fatalf("DecideRetry(exhausted) = %#v, want %#v", got, want)
	}
}

func TestRetryableRecognizesAllRetryClasses(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		&Error{Kind: ErrorTransient},
		&Error{Kind: ErrorTimeout},
		&Error{Kind: ErrorRateLimit},
		&Error{Kind: ErrorPartial},
		context.DeadlineExceeded,
	} {
		if !Retryable(err) {
			t.Errorf("Retryable(%#v) = false, want true", err)
		}
	}
	for _, err := range []error{
		&Error{Kind: ErrorAuthentication},
		&Error{Kind: ErrorOversized},
		&Error{Kind: ErrorMalformed},
		context.Canceled,
	} {
		if Retryable(err) {
			t.Errorf("Retryable(%#v) = true, want false", err)
		}
	}
}
