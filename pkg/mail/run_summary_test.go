package mail

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunSummaryAddAggregatesCountsCheckpointsAndTimings(t *testing.T) {
	firstStart := time.Date(2026, time.June, 12, 10, 0, 0, 0, time.UTC)
	secondStart := firstStart.Add(2 * time.Second)
	summary := RunSummary{
		Counts:      RunCounts{Mailboxes: 1, Pages: 2, Completed: 3, Warnings: 1},
		Checkpoints: CheckpointOutcomes{Loaded: 2, Committed: 1, Advanced: 1},
		Timing:      RunTiming{StartedAt: firstStart, FinishedAt: firstStart.Add(3 * time.Second), Duration: 3 * time.Second},
	}
	summary.Add(RunSummary{
		Counts:      RunCounts{Mailboxes: 1, Pages: 1, Completed: 1, Failures: 1},
		Checkpoints: CheckpointOutcomes{Loaded: 2, Committed: 1, Retained: 1},
		Timing:      RunTiming{StartedAt: secondStart, FinishedAt: secondStart.Add(4 * time.Second), Duration: 4 * time.Second},
	})

	if summary.Counts.Mailboxes != 2 || summary.Counts.Pages != 3 || summary.Counts.Completed != 4 || summary.Counts.Warnings != 1 || summary.Counts.Failures != 1 {
		t.Fatalf("aggregated counts = %#v", summary.Counts)
	}
	if summary.Checkpoints != (CheckpointOutcomes{Loaded: 4, Committed: 2, Advanced: 1, Retained: 1}) {
		t.Fatalf("aggregated checkpoints = %#v", summary.Checkpoints)
	}
	if summary.Timing.StartedAt != firstStart || summary.Timing.FinishedAt != secondStart.Add(4*time.Second) || summary.Timing.Duration != 7*time.Second {
		t.Fatalf("aggregated timing = %#v", summary.Timing)
	}
}

func TestPipelineRunWithSummaryReportsEmptyRunAndTiming(t *testing.T) {
	started := time.Date(2026, time.June, 12, 11, 0, 0, 0, time.UTC)
	times := []time.Time{started, started.Add(250 * time.Millisecond)}
	pipeline := NewPipeline(&pipelineFakeConnector{}, NewMemoryStateStore(), &pipelineFakeProcessor{}, &pipelineFakeEmitter{})
	pipeline.Now = func() time.Time {
		next := times[0]
		times = times[1:]
		return next
	}

	summary, err := pipeline.RunWithSummary(context.Background())
	if err != nil {
		t.Fatalf("RunWithSummary() error = %v", err)
	}
	if summary.Counts != (RunCounts{}) || summary.Checkpoints != (CheckpointOutcomes{}) {
		t.Fatalf("empty run summary = %#v", summary)
	}
	if summary.Timing.StartedAt != started || summary.Timing.FinishedAt != started.Add(250*time.Millisecond) || summary.Timing.Duration != 250*time.Millisecond {
		t.Fatalf("empty run timing = %#v", summary.Timing)
	}
}

func TestPipelineRunWithSummaryReportsPartialFailureWarningsAndCheckpointRetention(t *testing.T) {
	mailbox := Mailbox{ID: "inbox"}
	good := pipelineTestRef(mailbox, "good")
	failed := pipelineTestRef(mailbox, "failed")
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{{}: {
			Changes: []Change{{Kind: ChangeUpsert, Ref: good}, {Kind: ChangeUpsert, Ref: failed}},
			Next:    Cursor{Token: "unsafe"},
		}},
	}
	processor := lifecycleProcessorFunc(func(_ context.Context, raw RawMessage) (Document, error) {
		warnings := []ParserWarning(nil)
		if raw.Ref.ProviderMessageID == "good" {
			warnings = []ParserWarning{{Category: WarningMalformedHeader}, {Category: WarningAttachmentSkipped}}
		}
		return Document{ID: raw.Ref.ProviderMessageID, Ref: raw.Ref, Warnings: warnings}, nil
	})
	pipeline := NewPipeline(connector, NewMemoryStateStore(), processor, &pipelineFakeEmitter{
		failures: map[string]error{"failed": errors.New("index unavailable")},
	})
	pipeline.RetryPolicy = RetryPolicy{MaxAttempts: 1}

	summary, err := pipeline.RunWithSummary(context.Background())
	if err == nil {
		t.Fatal("RunWithSummary() error = nil, want partial failure")
	}
	wantCounts := RunCounts{
		Mailboxes: 1, Pages: 1, Discovered: 2, Fetched: 2, Parsed: 2,
		Completed: 1, Warnings: 2, Failures: 1,
	}
	if summary.Counts != wantCounts {
		t.Fatalf("partial failure counts = %#v, want %#v", summary.Counts, wantCounts)
	}
	wantCheckpoints := CheckpointOutcomes{Loaded: 2, Committed: 1, Retained: 1}
	if summary.Checkpoints != wantCheckpoints {
		t.Fatalf("partial failure checkpoints = %#v, want %#v", summary.Checkpoints, wantCheckpoints)
	}
	if summary.Timing.StartedAt.IsZero() || summary.Timing.FinishedAt.IsZero() || summary.Timing.Duration < 0 {
		t.Fatalf("partial failure timing = %#v", summary.Timing)
	}
}
