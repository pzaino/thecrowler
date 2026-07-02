package mail

import "time"

// RunSummary is the provider-neutral outcome of a pipeline or mailbox
// reconciliation run. It contains aggregate operational data only: no mailbox
// names, message identifiers, cursor values, errors, content, or credentials.
type RunSummary struct {
	Counts      RunCounts          `json:"counts" yaml:"counts"`
	Checkpoints CheckpointOutcomes `json:"checkpoints" yaml:"checkpoints"`
	Timing      RunTiming          `json:"timing" yaml:"timing"`
}

// RunCounts records work and outcomes observed during a run.
type RunCounts struct {
	Mailboxes   uint64 `json:"mailboxes" yaml:"mailboxes"`
	Pages       uint64 `json:"pages" yaml:"pages"`
	Discovered  uint64 `json:"discovered" yaml:"discovered"`
	Fetched     uint64 `json:"fetched" yaml:"fetched"`
	Parsed      uint64 `json:"parsed" yaml:"parsed"`
	Completed   uint64 `json:"completed" yaml:"completed"`
	Skipped     uint64 `json:"skipped" yaml:"skipped"`
	Quarantined uint64 `json:"quarantined" yaml:"quarantined"`
	Retries     uint64 `json:"retries" yaml:"retries"`
	Warnings    uint64 `json:"warnings" yaml:"warnings"`
	Failures    uint64 `json:"failures" yaml:"failures"`
}

// CheckpointOutcomes records durable checkpoint operations without exposing
// provider cursor values or mailbox identities.
type CheckpointOutcomes struct {
	Loaded    uint64 `json:"loaded" yaml:"loaded"`
	Committed uint64 `json:"committed" yaml:"committed"`
	Advanced  uint64 `json:"advanced" yaml:"advanced"`
	Retained  uint64 `json:"retained" yaml:"retained"`
	Reset     uint64 `json:"reset" yaml:"reset"`
	Failed    uint64 `json:"failed" yaml:"failed"`
}

// RunTiming describes the wall-clock bounds and elapsed duration of a run.
type RunTiming struct {
	StartedAt  time.Time     `json:"started_at" yaml:"started_at"`
	FinishedAt time.Time     `json:"finished_at" yaml:"finished_at"`
	Duration   time.Duration `json:"duration" yaml:"duration"`
}

// Add aggregates another summary into summary. Counts and checkpoint outcomes
// are summed. Timing bounds span the earliest start and latest finish, while
// Duration sums the measured work durations so callers can distinguish total
// work from those wall-clock bounds.
func (summary *RunSummary) Add(other RunSummary) {
	if summary == nil {
		return
	}
	summary.Counts.Mailboxes += other.Counts.Mailboxes
	summary.Counts.Pages += other.Counts.Pages
	summary.Counts.Discovered += other.Counts.Discovered
	summary.Counts.Fetched += other.Counts.Fetched
	summary.Counts.Parsed += other.Counts.Parsed
	summary.Counts.Completed += other.Counts.Completed
	summary.Counts.Skipped += other.Counts.Skipped
	summary.Counts.Quarantined += other.Counts.Quarantined
	summary.Counts.Retries += other.Counts.Retries
	summary.Counts.Warnings += other.Counts.Warnings
	summary.Counts.Failures += other.Counts.Failures

	summary.Checkpoints.Loaded += other.Checkpoints.Loaded
	summary.Checkpoints.Committed += other.Checkpoints.Committed
	summary.Checkpoints.Advanced += other.Checkpoints.Advanced
	summary.Checkpoints.Retained += other.Checkpoints.Retained
	summary.Checkpoints.Reset += other.Checkpoints.Reset
	summary.Checkpoints.Failed += other.Checkpoints.Failed

	if summary.Timing.StartedAt.IsZero() || (!other.Timing.StartedAt.IsZero() && other.Timing.StartedAt.Before(summary.Timing.StartedAt)) {
		summary.Timing.StartedAt = other.Timing.StartedAt
	}
	if other.Timing.FinishedAt.After(summary.Timing.FinishedAt) {
		summary.Timing.FinishedAt = other.Timing.FinishedAt
	}
	summary.Timing.Duration += other.Timing.Duration
}

func startRunSummary(started time.Time) RunSummary {
	return RunSummary{Timing: RunTiming{StartedAt: started}}
}

func finishRunSummary(summary *RunSummary, finished time.Time) {
	if summary == nil {
		return
	}
	summary.Timing.FinishedAt = finished
	summary.Timing.Duration = elapsedSince(finished, summary.Timing.StartedAt)
}
