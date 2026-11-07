// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"context"
	"time"
)

// WP-02 goal/acceptance contract. The public action is AgentGoalRun. Goal
// authority is parsed and frozen at manifest load; the action never reads
// criteria, scopes, budgets, allowlists or endpoint selection from runtime
// params. See artifacts/agentic/WP02_*.md for the approved contract.

// GoalTerminalStatus is the stable terminal outcome of one goal run.
type GoalTerminalStatus string

const (
	GoalCriteriaMet       GoalTerminalStatus = "criteria_met"
	GoalIncomplete        GoalTerminalStatus = "incomplete"
	GoalBudgetExhausted   GoalTerminalStatus = "budget_exhausted"
	GoalCancelled         GoalTerminalStatus = "cancelled"
	GoalDenied            GoalTerminalStatus = "denied"
	GoalProviderError     GoalTerminalStatus = "provider_error"
	GoalToolError         GoalTerminalStatus = "tool_error"
	GoalVerificationError GoalTerminalStatus = "verification_error"
)

// AcceptanceMode selects how criteria combine.
type AcceptanceMode string

const (
	AcceptanceAll AcceptanceMode = "all"
	AcceptanceAny AcceptanceMode = "any"
)

// PredicateKind is a closed, deterministic predicate vocabulary.
type PredicateKind string

const (
	PredicateAll            PredicateKind = "all"
	PredicateAny            PredicateKind = "any"
	PredicateEvidenceExists PredicateKind = "evidence_exists"
	PredicateEvidenceCount  PredicateKind = "evidence_count"
	PredicateFieldEquals    PredicateKind = "evidence_field_equals"
	PredicateFieldIn        PredicateKind = "evidence_field_in"
	PredicateFieldNumber    PredicateKind = "evidence_field_number"
	PredicateEvidenceScope  PredicateKind = "evidence_scope"
	PredicateResourceStatus PredicateKind = "resource_status"
	PredicateEventObserved  PredicateKind = "event_observed"
)

// CompareOp is a closed numeric/exact comparison operator.
type CompareOp string

const (
	CompareEq  CompareOp = "eq"
	CompareLt  CompareOp = "lt"
	CompareLte CompareOp = "lte"
	CompareGt  CompareOp = "gt"
	CompareGte CompareOp = "gte"
)

// EvidenceKind identifies a trusted evidence adapter.
type EvidenceKind string

const (
	EvidenceSourceStatus    EvidenceKind = "source_status"
	EvidenceSourceMetadata  EvidenceKind = "source_metadata"
	EvidenceIndexedPage     EvidenceKind = "indexed_page"
	EvidenceScrapedField    EvidenceKind = "scraped_field"
	EvidenceArtifact        EvidenceKind = "artifact"
	EvidenceToolObservation EvidenceKind = "tool_observation"
)

// TrustClass classifies provenance. Only system_verified and
// tool_observation can satisfy criteria; model_assertion and
// retrieved_content never do.
type TrustClass string

const (
	TrustModelAssertion   TrustClass = "model_assertion"
	TrustRetrievedContent TrustClass = "retrieved_content"
	TrustToolObservation  TrustClass = "tool_observation"
	TrustSystemVerified   TrustClass = "system_verified"
	TrustOperatorStatic   TrustClass = "operator_static"
)

// CriterionState is the per-criterion evaluation state.
type CriterionState string

const (
	CriterionPass        CriterionState = "pass"
	CriterionFail        CriterionState = "fail"
	CriterionUnavailable CriterionState = "unavailable"
	CriterionStale       CriterionState = "stale"
	CriterionDenied      CriterionState = "denied"
	CriterionError       CriterionState = "error"
)

// Goal run limits/defaults.
const (
	defaultGoalModelRounds        = 4
	defaultGoalToolCalls          = 8
	defaultGoalEvidenceRecords    = 16
	defaultGoalEvidenceBytes      = 32 << 10
	defaultGoalTotalResultBytes   = 64 << 10
	defaultGoalMaxElapsed         = 30 * time.Second
	defaultGoalConsecutiveDenials = 3
	defaultGoalEvidenceMaxAge     = 15 * time.Minute
	defaultGoalPayloadBytes       = 4 << 10

	hardGoalModelRounds        = 8
	hardGoalToolCalls          = 16
	hardGoalEvidenceRecords    = 32
	hardGoalEvidenceBytes      = 64 << 10
	hardGoalTotalResultBytes   = 64 << 10
	hardGoalMaxElapsed         = 2 * time.Minute
	hardGoalConsecutiveDenials = 5
	hardGoalEvidenceMaxAge     = 24 * time.Hour
	hardGoalPayloadBytes       = 16 << 10
)

// AgentGoalSpec is the immutable, validated authority for one goal run.
type AgentGoalSpec struct {
	GoalID          string
	Objective       string
	Provider        string
	URL             string
	Auth            string // excluded from the public SpecHash
	AuthPresent     bool
	Model           string
	Prompt          string
	Messages        []any
	Allowlist       []string
	AllowedSources  []uint64
	Evidence        EvidencePolicy
	Budgets         GoalBudgets
	Acceptance      AcceptanceSpec
	StopOnToolError bool
	SpecHash        string
}

// EvidencePolicy bounds evidence recording.
type EvidencePolicy struct {
	Allow           []EvidenceKind
	DefaultMaxAge   time.Duration
	MaxPayloadBytes int
}

// GoalBudgets bounds the whole logical goal run.
type GoalBudgets struct {
	MaxModelRounds        int
	MaxToolCalls          int
	MaxEvidenceRecords    int
	MaxEvidenceBytes      int
	MaxTotalResultBytes   int
	MaxElapsed            time.Duration
	MaxConsecutiveDenials int
}

// AcceptanceSpec is the frozen acceptance contract.
type AcceptanceSpec struct {
	Mode     AcceptanceMode
	Criteria []CriterionSpec
}

// CriterionSpec is one stable acceptance criterion.
type CriterionSpec struct {
	ID          string
	Description string
	Predicate   Predicate
	Freshness   *FreshnessSpec
}

// FreshnessSpec bounds evidence age for one criterion.
type FreshnessSpec struct {
	MaxAge time.Duration
}

// Predicate is a deterministic acceptance predicate.
type Predicate struct {
	Kind       PredicateKind
	Predicates []Predicate
	Evidence   EvidenceKind
	Where      *EvidenceSelector
	Path       string
	Quantifier string
	Op         CompareOp
	Value      any
	Values     []any
	Resource   string
	ResourceID uint64
	Status     string
	EventType  string
	SourceID   *uint64
}

// EvidenceSelector is the operator-defined evidence query. Selectors are
// canonicalized and hashed; the model cannot alter them.
type EvidenceSelector struct {
	SourceID        *uint64
	PageURL         string
	Status          string
	Field           string
	Value           string
	Tool            string
	ArgumentsSHA256 string
}

// EvidenceRecord is one run-scoped trusted observation.
type EvidenceRecord struct {
	EvidenceID      string
	LedgerID        string
	RunID           string
	RunNonceHash    string
	TraceID         string
	GoalID          string
	GoalSpecHash    string
	AgentID         string
	Owner           string
	Kind            EvidenceKind
	Producer        string
	TrustClass      TrustClass
	Tool            string
	ArgumentsSHA256 string
	SourceIDs       []uint64
	SourceUID       string
	Operation       string
	ObservedAt      time.Time
	DataUpdatedAt   time.Time
	ExpiresAt       time.Time
	Payload         map[string]any
	PayloadSHA256   string
	SelectorSHA256  string
	RecordSHA256    string
	Redacted        bool
}

// EvidenceObservation is what a provider/tool supplies to the recorder. It
// carries no run identity or trust class; the recorder stamps those.
type EvidenceObservation struct {
	Kind            EvidenceKind
	Producer        string
	SourceIDs       []uint64
	SourceUID       string
	Operation       string
	Payload         map[string]any
	Tool            string
	ArgumentsSHA256 string
	SelectorSHA256  string
	ObservedAt      time.Time
	DataUpdatedAt   time.Time
	MatchedValue    string // provider-side selector value, validated but not stored
}

// EvidenceRequest is one bounded provider read.
type EvidenceRequest struct {
	Selector  EvidenceSelector
	Limit     int
	MaxAge    time.Duration
	Now       time.Time
	SourceIDs []uint64
}

// EvidenceProvider is a trusted read-only adapter for one evidence kind.
type EvidenceProvider interface {
	Kind() EvidenceKind
	Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error)
}

// EvidenceSource is optionally implemented by tools that can produce
// evidence. The registry freezes this metadata at admission.
type EvidenceSource interface {
	EvidenceKind() EvidenceKind
	EvidenceTrustClass() TrustClass
	IsReadOnly() bool
}

// EvidenceMetadata is the registry's frozen evidence capability snapshot.
type EvidenceMetadata struct {
	Kind      EvidenceKind
	Trust     TrustClass
	ReadOnly  bool
	Supported bool
}

// EvidenceRecorder is the run-scoped ledger contract.
type EvidenceRecorder interface {
	Record(ctx context.Context, obs EvidenceObservation) (string, error)
	Snapshot() []EvidenceRecord
}

// ResourceObservation is a direct trusted resource-status check.
type ResourceObservation struct {
	Resource      string
	ID            uint64
	Status        string
	ObservedAt    time.Time // trusted read time
	DataUpdatedAt time.Time // underlying source/seed update time, if known
	RunID         string
	GoalID        string
	GoalSpecHash  string
	Source        string
	Err           string
}

// EvidenceQueryState describes an operator-defined evidence query.
type EvidenceQueryState string

const (
	EvidenceQueryCompleted   EvidenceQueryState = "completed"
	EvidenceQueryUnavailable EvidenceQueryState = "unavailable"
	EvidenceQueryFailed      EvidenceQueryState = "failed"
	EvidenceQueryTruncated   EvidenceQueryState = "truncated"
)

// EvidenceQueryResult is the explicit completion record for one evidence
// selector. It lets the evaluator distinguish a successful empty query from
// an unavailable, failed or truncated read.
type EvidenceQueryResult struct {
	Kind           EvidenceKind
	SelectorSHA256 string
	State          EvidenceQueryState
	Count          int
	Limit          int
	Reason         string
	LedgerID       string
	RunID          string
	GoalID         string
	GoalSpecHash   string
	AgentID        string
	Owner          string
	CompletedAt    time.Time
	QuerySHA256    string
}

// GoalEvalInput is the pure evaluator input. The identity fields bind the
// evaluated records to one trusted run/ledger/spec snapshot.
type GoalEvalInput struct {
	Spec         *AgentGoalSpec
	Records      []EvidenceRecord
	Resources    map[string]ResourceObservation
	Queries      map[string]EvidenceQueryResult
	Now          time.Time
	LedgerID     string
	RunID        string
	GoalID       string
	GoalSpecHash string
	RunNonceHash string
	AgentID      string
	Owner        string
}

// EvaluationResult is the deterministic acceptance evaluation.
type EvaluationResult struct {
	State       string            `json:"state"` // met | not_met | incomplete | error
	Criteria    []CriterionResult `json:"criteria"`
	EvaluatedAt time.Time         `json:"evaluated_at"`
}

// CriterionResult is one criterion's captured observation/result.
type CriterionResult struct {
	ID             string   `json:"id"`
	Result         string   `json:"result"` // pass | fail | unavailable | stale | denied | error
	ReasonCode     string   `json:"reason_code"`
	EvidenceIDs    []string `json:"evidence_ids,omitempty"`
	EvidenceSource string   `json:"evidence_source,omitempty"`
	ObservedValue  any      `json:"observed_value,omitempty"`
	ObservedAt     string   `json:"observed_at,omitempty"`
}

// GoalRunResult is the typed terminal result. requested_goal, evaluation,
// evidence and completion_decision remain separate.
type GoalRunResult struct {
	Status             GoalTerminalStatus `json:"status"`
	ReasonCode         string             `json:"reason_code"`
	Reason             string             `json:"reason"`
	RunID              string             `json:"run_id"`
	TraceID            string             `json:"trace_id"`
	CorrelationID      string             `json:"correlation_id,omitempty"`
	GoalID             string             `json:"goal_id"`
	GoalSpecHash       string             `json:"goal_spec_hash"`
	RequestedGoal      GoalSummary        `json:"requested_goal"`
	Evaluation         EvaluationResult   `json:"evaluation"`
	Evidence           []EvidenceSummary  `json:"evidence"`
	CompletionDecision CompletionDecision `json:"completion_decision"`
	ToolCalls          []ToolCallSummary  `json:"tool_calls"`
	ModelRounds        int                `json:"model_rounds"`
	ToolCallCount      int                `json:"tool_call_count"`
}

// GoalSummary is the frozen goal exposed in the result.
type GoalSummary struct {
	GoalID         string                 `json:"goal_id"`
	Objective      string                 `json:"objective"`
	AcceptanceMode AcceptanceMode         `json:"acceptance_mode"`
	Criteria       []GoalCriterionSummary `json:"criteria"`
}

// GoalCriterionSummary is the public criterion description.
type GoalCriterionSummary struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// EvidenceSummary is a public, payload-free evidence reference.
type EvidenceSummary struct {
	EvidenceID    string       `json:"evidence_id"`
	Kind          EvidenceKind `json:"kind"`
	TrustClass    TrustClass   `json:"trust_class"`
	Producer      string       `json:"producer"`
	SourceIDs     []uint64     `json:"source_ids,omitempty"`
	ObservedAt    time.Time    `json:"observed_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	PayloadSHA256 string       `json:"payload_sha256"`
}

// CompletionDecision records that the deterministic verifier, not the model,
// decided completion.
type CompletionDecision struct {
	DecidedBy          string `json:"decided_by"`
	ModelClaimObserved bool   `json:"model_claim_observed"`
	ModelClaimUsed     bool   `json:"model_claim_used"`
	CriteriaMet        bool   `json:"criteria_met"`
}
