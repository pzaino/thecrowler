package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
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

// CrawlProposalRegistry keeps one logical proposal identity stable across
// duplicate tool-call delivery inside a process. It is not durable; after an
// approval is issued, DatabaseCrawlApprovalStore.LookupCrawlApprovalByRequest
// provides the restart-safe proposal anchor.
type CrawlProposalRegistry interface {
	ResolveCrawlProposal(ctx context.Context, key string, create func() (CrawlRequestProposal, error)) (CrawlRequestProposal, error)
}

// MemoryCrawlProposalRegistry is a bounded process-local proposal registry for
// the disabled model-facing tool. Entries are pruned when their proposal
// expiry passes, and the least-recently-expiring entry is evicted when the
// configured capacity is reached. It is not durable and must not be presented
// as restart-safe.
type MemoryCrawlProposalRegistry struct {
	mu        sync.Mutex
	proposals map[string]crawlProposalEntry
	capacity  int
	ttl       time.Duration
	now       func() time.Time
}

type crawlProposalEntry struct {
	proposal  CrawlRequestProposal
	expiresAt time.Time
}

// DefaultCrawlProposalRegistryCapacity bounds process-local proposal retention
// for a long-running agent process. Durable approval lookup remains the
// restart-safe anchor once an approval has been issued.
const DefaultCrawlProposalRegistryCapacity = 1024

// NewMemoryCrawlProposalRegistry creates an empty process-local registry with
// bounded capacity and proposal-lifetime-aware eviction.
func NewMemoryCrawlProposalRegistry() *MemoryCrawlProposalRegistry {
	return NewMemoryCrawlProposalRegistryWithOptions(DefaultCrawlProposalRegistryCapacity, 0, nil)
}

// NewMemoryCrawlProposalRegistryWithOptions creates a registry with explicit
// capacity, fallback TTL and clock. Invalid capacity or TTL values fall back to
// safe bounded defaults; the clock is injectable for deterministic tests.
func NewMemoryCrawlProposalRegistryWithOptions(capacity int, ttl time.Duration, now func() time.Time) *MemoryCrawlProposalRegistry {
	if capacity <= 0 {
		capacity = DefaultCrawlProposalRegistryCapacity
	}
	if ttl <= 0 || ttl > maxCrawlProposalLifetime {
		ttl = maxCrawlProposalLifetime
	}
	if now == nil {
		now = time.Now
	}
	return &MemoryCrawlProposalRegistry{
		proposals: make(map[string]crawlProposalEntry),
		capacity:  capacity,
		ttl:       ttl,
		now:       now,
	}
}

// Len returns the number of retained proposals. It is intended for bounded
// retention tests and diagnostics.
func (r *MemoryCrawlProposalRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.proposals)
}

// ResolveCrawlProposal returns the first live proposal for key or creates it.
func (r *MemoryCrawlProposalRegistry) ResolveCrawlProposal(ctx context.Context, key string, create func() (CrawlRequestProposal, error)) (CrawlRequestProposal, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CrawlRequestProposal{}, err
	}
	if r == nil {
		return CrawlRequestProposal{}, fmt.Errorf("proposal registry is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	r.pruneExpiredLocked(now)
	if entry, ok := r.proposals[key]; ok {
		if entry.expiresAt.After(now) {
			return entry.proposal, nil
		}
		delete(r.proposals, key)
	}
	proposal, err := create()
	if err != nil {
		return CrawlRequestProposal{}, err
	}
	expiresAt := proposal.ExpiresAt.UTC()
	if expiresAt.IsZero() || !expiresAt.After(now) {
		expiresAt = now.Add(r.ttl)
	}
	if expiresAt.Sub(now) > maxCrawlProposalLifetime {
		expiresAt = now.Add(maxCrawlProposalLifetime)
	}
	if !proposal.ExpiresAt.Equal(expiresAt) {
		proposal.ExpiresAt = expiresAt
	}
	if len(r.proposals) >= r.capacity {
		r.evictEarliestLocked()
	}
	r.proposals[key] = crawlProposalEntry{proposal: proposal, expiresAt: expiresAt}
	return proposal, nil
}

func (r *MemoryCrawlProposalRegistry) pruneExpiredLocked(now time.Time) {
	for key, entry := range r.proposals {
		if !entry.expiresAt.After(now) {
			delete(r.proposals, key)
		}
	}
}

func (r *MemoryCrawlProposalRegistry) evictEarliestLocked() {
	var victimKey string
	var victimExpiry time.Time
	for key, entry := range r.proposals {
		if victimKey == "" || entry.expiresAt.Before(victimExpiry) ||
			(entry.expiresAt.Equal(victimExpiry) && key < victimKey) {
			victimKey = key
			victimExpiry = entry.expiresAt
		}
	}
	if victimKey != "" {
		delete(r.proposals, victimKey)
	}
}

// CrawlProposalFactory recreates the same proposal identity and expiry for a
// duplicate tool call. The model never chooses the request ID or expiry.
type CrawlProposalFactory struct {
	Registry CrawlProposalRegistry
	TTL      time.Duration
	Now      func() time.Time
}

// Proposal returns the stable proposal for one logical tool invocation.
func (f CrawlProposalFactory) Proposal(ctx context.Context, runID, toolCallID string, sourceID uint64) (CrawlRequestProposal, error) {
	requestID, err := CrawlToolRequestID(runID, toolCallID, sourceID)
	if err != nil {
		return CrawlRequestProposal{}, err
	}
	if f.Registry == nil {
		return CrawlRequestProposal{}, fmt.Errorf("proposal registry is required")
	}
	ttl := f.TTL
	if ttl <= 0 || ttl > maxCrawlProposalLifetime {
		ttl = maxCrawlProposalLifetime
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	return f.Registry.ResolveCrawlProposal(ctx, requestID, func() (CrawlRequestProposal, error) {
		return CrawlRequestProposal{
			RequestID: requestID, SourceID: sourceID,
			OperationType: cdb.CrawlOperationType,
			ExpiresAt:     now().UTC().Add(ttl),
		}, nil
	})
}
