package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// CrawlRequestToolInput is the proposed model-facing input for the future
// request_source_crawl tool. It deliberately contains only the source the
// model is allowed to propose. Request identity, approval, principal, run,
// quota, clock and allowlist data are supplied by the trusted runtime.
type CrawlRequestToolInput struct {
	SourceID uint64 `json:"source_id"`
}

// Validate checks the bounded model proposal.
func (in CrawlRequestToolInput) Validate() error {
	if in.SourceID == 0 {
		return fmt.Errorf("source_id is required")
	}
	return nil
}

// CrawlToolRequestID derives a stable logical request ID from trusted run and
// tool-call correlation data. The model never chooses this value. Repeated
// execution of the same tool call preserves the same ID so the durable
// scheduler can apply its idempotency rules.
func CrawlToolRequestID(runID, toolCallID string, sourceID uint64) (string, error) {
	if runID == "" || toolCallID == "" || sourceID == 0 {
		return "", fmt.Errorf("run_id, tool_call_id and source_id are required")
	}
	sum := sha256.Sum256([]byte(runID + "\x00" + toolCallID + "\x00" + strconv.FormatUint(sourceID, 10)))
	return hex.EncodeToString(sum[:]), nil
}

// CrawlRequestToolResult is the proposed safe model-facing result. It contains
// no approval, principal, owner, quota or source-configuration data. A
// scheduling acknowledgment is not proof that a crawl completed.
type CrawlRequestToolResult struct {
	OperationID string    `json:"operation_id"`
	RequestID   string    `json:"request_id"`
	SourceID    uint64    `json:"source_id"`
	State       string    `json:"state"`
	ReasonCode  string    `json:"reason_code"`
	Reused      bool      `json:"reused"`
	ObservedAt  time.Time `json:"observed_at"`
}

// NewCrawlRequestToolResult converts a trusted scheduling result to the safe
// proposed tool result. The caller supplies the trusted observation time; if
// zero, the scheduler's observed time is used.
func NewCrawlRequestToolResult(result cdb.CrawlScheduleResult, observedAt time.Time) CrawlRequestToolResult {
	if observedAt.IsZero() {
		observedAt = result.SourceObservedAt
	}
	return CrawlRequestToolResult{
		OperationID: result.OperationID,
		RequestID:   result.RequestID,
		SourceID:    result.SourceID,
		State:       string(result.State),
		ReasonCode:  result.ReasonCode,
		Reused:      result.Reused,
		ObservedAt:  observedAt.UTC(),
	}
}
