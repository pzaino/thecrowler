package mail

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type renewalTestRenewer struct {
	results []RenewalResult
	errors  []error
	calls   int
}

func (r *renewalTestRenewer) Renew(context.Context, MailboxKey) (RenewalResult, error) {
	index := r.calls
	r.calls++
	var result RenewalResult
	var err error
	if index < len(r.results) {
		result = r.results[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return result, err
}

type renewalTestScheduler struct {
	at []time.Time
}

func (s *renewalTestScheduler) ScheduleRenewal(_ context.Context, _ MailboxKey, at time.Time) error {
	s.at = append(s.at, at)
	return nil
}

func TestRenewalDueUsesSafetyMarginAndDetectsExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		expiresAt   time.Time
		margin      time.Duration
		wantDue     bool
		wantExpired bool
	}{
		{name: "missing expiration is due", margin: time.Hour, wantDue: true},
		{name: "outside margin is not due", expiresAt: now.Add(2 * time.Hour), margin: time.Hour},
		{name: "at margin is due", expiresAt: now.Add(time.Hour), margin: time.Hour, wantDue: true},
		{name: "inside margin is due", expiresAt: now.Add(30 * time.Minute), margin: time.Hour, wantDue: true},
		{name: "at expiration is expired", expiresAt: now, margin: time.Hour, wantDue: true, wantExpired: true},
		{name: "past expiration is expired", expiresAt: now.Add(-time.Second), margin: time.Hour, wantDue: true, wantExpired: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			due, expired := RenewalDue(now, RenewalMetadata{ExpiresAt: test.expiresAt}, test.margin)
			if due != test.wantDue || expired != test.wantExpired {
				t.Fatalf("RenewalDue() = (%t, %t), want (%t, %t)", due, expired, test.wantDue, test.wantExpired)
			}
		})
	}
}

func TestRenewalStatusAtCoversHealthyDueExpiredAndFailed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		metadata RenewalMetadata
		want     RenewalStatus
	}{
		{name: "healthy", metadata: RenewalMetadata{ExpiresAt: now.Add(2 * time.Hour)}, want: RenewalStatusHealthy},
		{name: "due", metadata: RenewalMetadata{ExpiresAt: now.Add(30 * time.Minute)}, want: RenewalStatusDue},
		{name: "expired", metadata: RenewalMetadata{ExpiresAt: now.Add(-time.Second)}, want: RenewalStatusExpired},
		{name: "failed", metadata: RenewalMetadata{ExpiresAt: now.Add(2 * time.Hour), FailureCount: 1, Status: RenewalStatusFailed}, want: RenewalStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RenewalStatusAt(now, test.metadata, time.Hour); got != test.want {
				t.Fatalf("RenewalStatusAt() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNextRenewalAtCapsMarginAtHalfProviderLifetime(t *testing.T) {
	t.Parallel()

	renewedAt := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	metadata := RenewalMetadata{LastRenewedAt: renewedAt, ExpiresAt: renewedAt.Add(30 * time.Minute)}
	if got, want := NextRenewalAt(metadata, time.Hour), renewedAt.Add(15*time.Minute); !got.Equal(want) {
		t.Fatalf("NextRenewalAt() = %v, want %v", got, want)
	}
}

func TestRenewIfDueSkipsAndSchedulesSafetyBoundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	key := renewalTestKey()
	store := NewMemoryStateStore()
	expiresAt := now.Add(8 * time.Hour)
	if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{Renewal: RenewalMetadata{ExpiresAt: expiresAt}}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	renewer := &renewalTestRenewer{}
	scheduler := &renewalTestScheduler{}
	coordinator := RenewalCoordinator{Store: store, Renewer: renewer, Scheduler: scheduler, SafetyMargin: 2 * time.Hour, Now: func() time.Time { return now }}

	decision, err := coordinator.RenewIfDue(context.Background(), key)
	if err != nil {
		t.Fatalf("RenewIfDue() error = %v", err)
	}
	if decision.Due || decision.Attempted || decision.Renewed || decision.Expired {
		t.Fatalf("RenewIfDue() decision = %#v, want not due", decision)
	}
	wantNext := expiresAt.Add(-2 * time.Hour)
	if renewer.calls != 0 || !reflect.DeepEqual(scheduler.at, []time.Time{wantNext}) || !decision.NextAttempt.Equal(wantNext) {
		t.Fatalf("calls = %d, scheduled = %v, decision = %#v", renewer.calls, scheduler.at, decision)
	}
}

func TestRenewIfDueRenewsDueAndExpiredSubscriptions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		expiresAt time.Time
		expired   bool
	}{
		{name: "due", expiresAt: now.Add(30 * time.Minute)},
		{name: "expired", expiresAt: now.Add(-time.Minute), expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := renewalTestKey()
			store := NewMemoryStateStore()
			if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{Renewal: RenewalMetadata{ExpiresAt: test.expiresAt}}); err != nil {
				t.Fatalf("seed checkpoint: %v", err)
			}
			newExpiration := now.Add(7 * 24 * time.Hour)
			renewer := &renewalTestRenewer{results: []RenewalResult{{SubscriptionID: "subscription-2", ResourcePath: "projects/test/topics/mail", ExpiresAt: newExpiration}}}
			scheduler := &renewalTestScheduler{}
			coordinator := RenewalCoordinator{Store: store, Renewer: renewer, Scheduler: scheduler, SafetyMargin: 24 * time.Hour, Now: func() time.Time { return now }}

			decision, err := coordinator.RenewIfDue(context.Background(), key)
			if err != nil {
				t.Fatalf("RenewIfDue() error = %v", err)
			}
			if !decision.Due || !decision.Attempted || !decision.Renewed || decision.Expired != test.expired {
				t.Fatalf("RenewIfDue() decision = %#v", decision)
			}
			stored, err := store.LoadCheckpoint(context.Background(), key)
			if err != nil {
				t.Fatalf("LoadCheckpoint() error = %v", err)
			}
			want := RenewalMetadata{SubscriptionID: "subscription-2", ResourcePath: "projects/test/topics/mail", Status: RenewalStatusHealthy, LastRenewedAt: now, ExpiresAt: newExpiration, LastAttemptAt: now}
			if stored.Renewal != want {
				t.Fatalf("stored renewal = %#v, want %#v", stored.Renewal, want)
			}
			wantNext := newExpiration.Add(-24 * time.Hour)
			if !reflect.DeepEqual(scheduler.at, []time.Time{wantNext}) {
				t.Fatalf("scheduled = %v, want [%v]", scheduler.at, wantNext)
			}
		})
	}
}

func TestRenewIfDuePersistsFailureAndRetrySuccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	currentNow := now
	key := renewalTestKey()
	store := NewMemoryStateStore()
	oldRenewal := RenewalMetadata{LastRenewedAt: now.Add(-6 * 24 * time.Hour), ExpiresAt: now.Add(30 * time.Minute)}
	if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{Renewal: oldRenewal}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	providerErr := errors.New("temporary Gmail watch failure")
	newExpiration := now.Add(7 * 24 * time.Hour)
	renewer := &renewalTestRenewer{results: []RenewalResult{{}, {SubscriptionID: "subscription-3", ResourcePath: "projects/test/topics/mail", ExpiresAt: newExpiration}}, errors: []error{providerErr, nil}}
	scheduler := &renewalTestScheduler{}
	coordinator := RenewalCoordinator{
		Store: store, Renewer: renewer, Scheduler: scheduler,
		SafetyMargin: time.Hour, RetryDelay: 10 * time.Minute,
		Now: func() time.Time { return currentNow },
	}

	failed, err := coordinator.RenewIfDue(context.Background(), key)
	if !errors.Is(err, providerErr) {
		t.Fatalf("failed RenewIfDue() error = %v, want provider error", err)
	}
	if !failed.Attempted || failed.Renewed || failed.Metadata.FailureCount != 1 || failed.Metadata.LastError != providerErr.Error() || failed.Status != RenewalStatusFailed || failed.Metadata.Status != RenewalStatusFailed {
		t.Fatalf("failed decision = %#v", failed)
	}
	if failed.Metadata.LastRenewedAt != oldRenewal.LastRenewedAt || failed.Metadata.ExpiresAt != oldRenewal.ExpiresAt || failed.Metadata.LastAttemptAt != now {
		t.Fatalf("failed metadata = %#v, want preserved watch metadata and attempted time", failed.Metadata)
	}
	wantRetry := now.Add(10 * time.Minute)
	if !reflect.DeepEqual(scheduler.at, []time.Time{wantRetry}) {
		t.Fatalf("failure scheduled = %v, want [%v]", scheduler.at, wantRetry)
	}

	currentNow = wantRetry
	retried, err := coordinator.RenewIfDue(context.Background(), key)
	if err != nil {
		t.Fatalf("retry RenewIfDue() error = %v", err)
	}
	if !retried.Renewed || retried.Metadata.FailureCount != 0 || retried.Metadata.LastError != "" || retried.Metadata.LastRenewedAt != currentNow || retried.Metadata.ExpiresAt != newExpiration || retried.Status != RenewalStatusHealthy || retried.Metadata.Status != RenewalStatusHealthy || retried.Metadata.SubscriptionID != "subscription-3" || retried.Metadata.ResourcePath != "projects/test/topics/mail" {
		t.Fatalf("retry decision = %#v", retried)
	}
	wantNext := newExpiration.Add(-time.Hour)
	if !reflect.DeepEqual(scheduler.at, []time.Time{wantRetry, wantNext}) || renewer.calls != 2 {
		t.Fatalf("retry calls = %d, scheduled = %v", renewer.calls, scheduler.at)
	}
}

func TestRenewIfDueSchedulesFailedRetryBeforeExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
	key := renewalTestKey()
	store := NewMemoryStateStore()
	expiresAt := now.Add(4 * time.Minute)
	if err := store.CommitCheckpoint(context.Background(), key, "", Checkpoint{Renewal: RenewalMetadata{ExpiresAt: expiresAt}}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	scheduler := &renewalTestScheduler{}
	coordinator := RenewalCoordinator{
		Store: store, Renewer: &renewalTestRenewer{errors: []error{errors.New("provider unavailable")}}, Scheduler: scheduler,
		SafetyMargin: 5 * time.Minute, RetryDelay: 10 * time.Minute, Now: func() time.Time { return now },
	}
	decision, err := coordinator.RenewIfDue(context.Background(), key)
	if err == nil {
		t.Fatal("RenewIfDue() error = nil, want provider failure")
	}
	wantRetry := now.Add(2 * time.Minute)
	if !decision.NextAttempt.Equal(wantRetry) || !decision.NextAttempt.Before(expiresAt) || !reflect.DeepEqual(scheduler.at, []time.Time{wantRetry}) {
		t.Fatalf("failed retry = %v, scheduled = %v, want safe retry %v before %v", decision.NextAttempt, scheduler.at, wantRetry, expiresAt)
	}
}

func renewalTestKey() MailboxKey {
	return MailboxKey{SourceID: "7", Provider: gmailProvider, AccountID: "user@example.com", Mailbox: Mailbox{ID: "INBOX"}}
}

var (
	_ SubscriptionRenewer = (*renewalTestRenewer)(nil)
	_ RenewalScheduler    = (*renewalTestScheduler)(nil)
)
