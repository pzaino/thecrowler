package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// The model-facing crawl request is the only mutation-capable agent tool in
// this work package. It is admitted through AgentToolRegistry.RegisterMutation
// and never through the read-only registration path.

var (
	// ErrCrawlMutationDisabled means the operator has not enabled model-driven
	// crawl requests.
	ErrCrawlMutationDisabled = errors.New("model-driven crawl requests are disabled")
	// ErrCrawlMutationUnsupported means the configured database backend cannot
	// provide the durable proposal and approval stores.
	ErrCrawlMutationUnsupported = errors.New("model-driven crawl requests are unsupported by this database backend")
	// ErrCrawlMutationOperationRequired means request_source_crawl is missing
	// from the explicit operation allowlist.
	ErrCrawlMutationOperationRequired = errors.New("request_source_crawl must be explicitly allowlisted")
	// ErrCrawlMutationSourceScopeRequired means the run has no source scope.
	ErrCrawlMutationSourceScopeRequired = errors.New("crawl request source scope is required")
	// ErrCrawlMutationQuotaRequired means the request quota is missing or
	// outside the supported range.
	ErrCrawlMutationQuotaRequired = errors.New("crawl request quota is required")
	// ErrCrawlPrincipalUnavailable means the trusted owner name could not be
	// mapped to an active local account.
	ErrCrawlPrincipalUnavailable = errors.New("crawl request principal is unavailable")
	// ErrCrawlProposalNotFound means a referenced durable proposal does not
	// exist.
	ErrCrawlProposalNotFound = errors.New("crawl proposal not found")
	// ErrCrawlProposalConflict means durable proposal identity does not match
	// the trusted invocation or a stored row.
	ErrCrawlProposalConflict = errors.New("crawl proposal conflicts with the durable record")
	// ErrCrawlProposalExpired means the proposal or approval has expired.
	ErrCrawlProposalExpired = errors.New("crawl proposal has expired")
	// ErrCrawlProposalStoreUnavailable means the durable proposal store could
	// not be read or written.
	ErrCrawlProposalStoreUnavailable = errors.New("crawl proposal store is unavailable")
	// ErrCrawlSubmissionDenied means the approved proposal failed the trusted
	// authorization policy at submission time.
	ErrCrawlSubmissionDenied = errors.New("approved crawl proposal is not authorized for submission")
	// ErrCrawlSchedulingUncertain means the scheduler commit outcome could not
	// be determined. Callers must retry and reconcile against the durable
	// operation record.
	ErrCrawlSchedulingUncertain = errors.New("crawl scheduling commit outcome is uncertain")
)

// CrawlRequestOperationName is the stable operation/tool name used by trusted
// configuration, approval APIs and audit records.
const CrawlRequestOperationName = crawlRequestToolName

// CrawlRequestToolRuntimeConfig is the trusted operator configuration for the
// request_source_crawl tool. Every field is authority-bearing and must come
// from the manifest or deployment configuration, never from model output.
type CrawlRequestToolRuntimeConfig struct {
	Enabled           bool
	DB                cdb.Handler
	Identity          AgentIdentity
	AllowedOperations []string
	AllowedSources    []uint64
	MaxRequests       int
	BudgetWindow      time.Duration
	Issuer            string
	ProposalTTL       time.Duration
	SourceAuthorizer  CrawlSourceAuthorizer
	ApprovalStore     CrawlApprovalStore
	ApprovalVerifier  CrawlApprovalVerifier
	Now               func() time.Time
}

// CrawlRequestToolRuntime binds the model-facing proposal tool to the trusted
// policy, durable proposal store and authoritative approval store.
type CrawlRequestToolRuntime struct {
	DB       cdb.Handler
	Identity AgentIdentity
	Policy   CrawlRequestPolicy

	ApprovalStore CrawlApprovalStore
	Issuer        string
	ProposalTTL   time.Duration
	Now           func() time.Time
}

func crawlRuntimeNow(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

func (r *CrawlRequestToolRuntime) proposalTTL() time.Duration {
	if r == nil || r.ProposalTTL <= 0 || r.ProposalTTL > maxCrawlProposalLifetime {
		return 30 * time.Minute
	}
	return r.ProposalTTL
}

func (r *CrawlRequestToolRuntime) now() time.Time {
	if r == nil {
		return time.Now().UTC()
	}
	return crawlRuntimeNow(r.Now)
}

func supportedCrawlMutationBackend(db cdb.Handler) bool {
	if db == nil {
		return false
	}
	dbms := strings.ToLower(strings.TrimSpace(db.DBMS()))
	return strings.Contains(dbms, "postgres") || strings.Contains(dbms, "sqlite")
}

// BuildCrawlRequestToolRuntime validates the trusted mutation configuration
// and resolves the agent owner to an active local user ID. Any missing or
// ambiguous authority fails closed.
func BuildCrawlRequestToolRuntime(ctx context.Context, config CrawlRequestToolRuntimeConfig) (*CrawlRequestToolRuntime, error) {
	if !config.Enabled {
		return nil, ErrCrawlMutationDisabled
	}
	if config.DB == nil || !supportedCrawlMutationBackend(config.DB) {
		return nil, ErrCrawlMutationUnsupported
	}
	if strings.TrimSpace(config.Identity.AgentID) == "" || strings.TrimSpace(config.Identity.Owner) == "" {
		return nil, ErrCrawlPrincipalUnavailable
	}
	if strings.TrimSpace(config.Issuer) == "" {
		return nil, ErrCrawlMutationOperationRequired
	}
	operations := map[string]bool{}
	for _, name := range config.AllowedOperations {
		name = strings.TrimSpace(name)
		if name != "" {
			operations[name] = true
		}
	}
	if !operations[crawlRequestToolName] {
		return nil, ErrCrawlMutationOperationRequired
	}
	if len(config.AllowedSources) == 0 {
		return nil, ErrCrawlMutationSourceScopeRequired
	}
	sources := map[uint64]bool{}
	for _, sourceID := range config.AllowedSources {
		if sourceID == 0 {
			return nil, ErrCrawlMutationSourceScopeRequired
		}
		sources[sourceID] = true
	}
	if config.MaxRequests <= 0 || config.MaxRequests > maxCrawlPolicyRequests ||
		config.BudgetWindow <= 0 || config.BudgetWindow > maxCrawlPolicyBudget {
		return nil, ErrCrawlMutationQuotaRequired
	}

	resolveCtx := ctx
	if resolveCtx == nil {
		resolveCtx = context.Background()
	}
	if _, hasDeadline := resolveCtx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		resolveCtx, cancel = context.WithTimeout(resolveCtx, 5*time.Second)
		defer cancel()
	}
	db := config.DB
	principalUserID, err := cdb.ResolveActiveUserIDByUsername(resolveCtx, &db, config.Identity.Owner)
	if err != nil {
		return nil, ErrCrawlPrincipalUnavailable
	}
	principal := CrawlPrincipal{
		UserID:   principalUserID,
		Username: strings.TrimSpace(config.Identity.Owner),
		Issuer:   strings.TrimSpace(config.Issuer),
	}

	authorizer := config.SourceAuthorizer
	if authorizer == nil {
		authorizer = DatabaseCrawlSourceAuthorizer{
			Lookup:     DatabaseSourceOwnerLookup{DB: &db},
			AllowAdmin: false,
		}
	}
	approvalStore := config.ApprovalStore
	if approvalStore == nil {
		approvalStore = &DatabaseCrawlApprovalStore{
			DB:               &db,
			SourceAuthorizer: authorizer,
			Issuer:           strings.TrimSpace(config.Issuer),
			MaxLifetime:      maxCrawlApprovalLifetime,
		}
	}
	verifier := config.ApprovalVerifier
	if verifier == nil {
		verifier = StoreBackedCrawlApprovalVerifier{
			Store:            approvalStore,
			SourceAuthorizer: authorizer,
			TrustedIssuers:   map[string]bool{strings.TrimSpace(config.Issuer): true},
			MaxLifetime:      maxCrawlApprovalLifetime,
		}
	}

	return &CrawlRequestToolRuntime{
		DB:       db,
		Identity: config.Identity,
		Policy: CrawlRequestPolicy{
			AllowedOperations: operations,
			AllowedSources:    sources,
			Principal:         principal,
			SourceAuthorizer:  authorizer,
			ApprovalVerifier:  verifier,
			MaxRequests:       config.MaxRequests,
			BudgetWindow:      config.BudgetWindow,
		},
		ApprovalStore: approvalStore,
		Issuer:        strings.TrimSpace(config.Issuer),
		ProposalTTL:   config.ProposalTTL,
		Now:           config.Now,
	}, nil
}

// RegisterCrawlRequestTool registers the mutation tool only through the
// explicit mutation admission path.
func RegisterCrawlRequestTool(registry *AgentToolRegistry, runtime *CrawlRequestToolRuntime) error {
	if registry == nil || runtime == nil {
		return ErrCrawlMutationDisabled
	}
	if _, exists := registry.Get(crawlRequestToolName); exists {
		return fmt.Errorf("crawl request tool is already registered")
	}
	return registry.RegisterMutation(&crawlRequestTool{runtime: runtime})
}

type crawlRequestTool struct {
	runtime *CrawlRequestToolRuntime
}

func (t *crawlRequestTool) Name() string { return crawlRequestToolName }

func (t *crawlRequestTool) Description() string {
	return "Request an operator-approved crawl of one explicitly scoped CROWler source. " +
		"This records a bounded proposal and never schedules the crawl itself. " +
		"An authorized operator must approve and submit the exact proposal."
}

func (t *crawlRequestTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "The CROWler source identifier to propose for crawling.",
			},
		},
		"required":             []any{"source_id"},
		"additionalProperties": false,
	}
}

func (t *crawlRequestTool) RequiredCapabilities() []string {
	return []string{"tool_execution", "db_write"}
}

// Effect marks the tool as mutation-capable so read-only registry admission
// and model-facing execution remain separate.
func (t *crawlRequestTool) Effect() ToolEffect { return ToolEffectMutation }

func (t *crawlRequestTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	if t == nil || t.runtime == nil {
		return nil, ErrCrawlMutationDisabled
	}
	if t.runtime.ApprovalStore == nil || t.runtime.Policy.SourceAuthorizer == nil {
		return nil, ErrCrawlProposalStoreUnavailable
	}
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || runtime.Crawl == nil || runtime.Crawl != t.runtime {
		return nil, ErrCrawlMutationDisabled
	}
	if strings.TrimSpace(runtime.CallID) == "" {
		return nil, denyTool(ToolDenyArguments, "trusted tool call ID is missing")
	}
	if strings.TrimSpace(runtime.Auth.RunID) == "" {
		return nil, denyTool(ToolDenyNoOptIn, "crawl request run ID is missing")
	}
	if len(args) != 1 {
		return nil, denyTool(ToolDenyArguments, "source_id is the only accepted argument")
	}
	sourceID, err := toSourceIDArg(args["source_id"])
	if err != nil {
		return nil, denyTool(ToolDenyArguments, "%s", err.Error())
	}
	now := t.runtime.now()
	db := t.runtime.DB
	requestID, err := CrawlToolRequestID(runtime.Auth.RunID, runtime.CallID, sourceID)
	if err != nil {
		return nil, denyTool(ToolDenyArguments, "crawl request identity could not be derived")
	}
	proposal := CrawlRequestProposal{
		RequestID: requestID, SourceID: sourceID, OperationType: cdb.CrawlOperationType,
		ExpiresAt: now.Add(t.runtime.proposalTTL()).UTC().Truncate(time.Microsecond),
	}
	// A retry after a process restart may have a different local clock. The
	// durable row, not the new clock reading, owns the proposal expiry and
	// fingerprint for an already-recorded logical invocation.
	existing, found, err := cdb.GetCrawlProposalByRequest(ctx, &db, requestID)
	if err != nil {
		return nil, ErrCrawlProposalStoreUnavailable
	}
	if found {
		if err := validateCrawlProposalBinding(runtime, existing, sourceID); err != nil {
			return nil, err
		}
		proposal = crawlProposalFromRow(existing)
		if existing.State == cdb.CrawlProposalStateSubmitted {
			t.emitAudit(runtime, proposal, CrawlRequestDecision{reasonCode: CrawlReasonApprovalRecorded}, auditOutcomeAllowed)
			return crawlProposalRowMap(existing, existing.State, CrawlReasonApprovalRecorded, now, true), nil
		}
		if existing.State == cdb.CrawlProposalStateExpired || !existing.ExpiresAt.After(now) {
			t.emitAudit(runtime, proposal, CrawlRequestDecision{reasonCode: CrawlReasonProposalExpired}, auditOutcomeDenied)
			return t.expireProposal(ctx, &db, existing, now)
		}
	}

	decision := AuthorizeCrawlRequest(ctx, runtime.Auth.Identity, t.runtime.Policy, proposal, nil, runtime.Auth.RunID, runtime.Auth.TraceID)
	if decision.Stage() != CrawlStageApprovalRequired {
		t.emitAudit(runtime, proposal, decision, auditOutcomeDenied)
		return nil, crawlRequestDeniedError(decision)
	}

	row, err := t.persistProposal(ctx, runtime, proposal)
	if err != nil {
		t.emitAudit(runtime, proposal, decision, auditOutcomeError)
		return nil, err
	}
	if row.State == cdb.CrawlProposalStateSubmitted {
		t.emitAudit(runtime, proposal, decision, auditOutcomeAllowed)
		return crawlProposalRowMap(row, row.State, CrawlReasonApprovalRecorded, now, true), nil
	}
	if !row.ExpiresAt.After(now) || row.State == cdb.CrawlProposalStateExpired {
		t.emitAudit(runtime, proposal, decision, auditOutcomeDenied)
		return t.expireProposal(ctx, &db, row, now)
	}
	// A concurrent duplicate may have inserted the row between the lookup and
	// the insert. Re-authorize against the durable expiry if it differs.
	storedProposal := crawlProposalFromRow(row)
	if storedProposal.Fingerprint() != proposal.Fingerprint() {
		proposal = storedProposal
		decision = AuthorizeCrawlRequest(ctx, runtime.Auth.Identity, t.runtime.Policy, proposal, nil, runtime.Auth.RunID, runtime.Auth.TraceID)
		if decision.Stage() != CrawlStageApprovalRequired {
			t.emitAudit(runtime, proposal, decision, auditOutcomeDenied)
			return nil, crawlRequestDeniedError(decision)
		}
	}

	state := "approval_required"
	reason := CrawlReasonApprovalRequired
	record, approvalFound, lookupErr := t.runtime.ApprovalStore.LookupCrawlApprovalByRequest(
		ctx, row.RunID, row.RequestID, row.PrincipalUserID, row.SourceID, row.OperationType,
	)
	if lookupErr != nil {
		t.emitAudit(runtime, proposal, decision, auditOutcomeError)
		return nil, denyTool(CrawlReasonApprovalStoreUnavailable, "authoritative approval lookup failed")
	}
	if approvalFound {
		switch {
		case record.Revoked:
			state, reason = "approval_revoked", CrawlReasonApprovalRevoked
		case !record.Approval.ExpiresAt.After(now):
			state, reason = "approval_expired", CrawlReasonApprovalExpired
		default:
			state, reason = "approval_recorded", CrawlReasonApprovalRecorded
		}
	}
	t.emitAudit(runtime, proposal, decision, auditOutcomeAllowed)
	return crawlRequestToolMap(CrawlRequestToolResult{
		RequestID: row.RequestID, SourceID: row.SourceID,
		State: state, ReasonCode: reason, ApprovalRequired: state == "approval_required",
		ObservedAt: now, ExpiresAt: row.ExpiresAt,
	}), nil
}

func crawlProposalRowMap(row cdb.CrawlProposalRow, state, reason string, now time.Time, reused bool) map[string]any {
	return crawlRequestToolMap(CrawlRequestToolResult{
		OperationID: row.OperationID.String, RequestID: row.RequestID,
		SourceID: row.SourceID, State: state, ReasonCode: reason, Reused: reused,
		ObservedAt: now, ExpiresAt: row.ExpiresAt,
	})
}

// expireProposal applies the strict pending -> expired transition and does not
// hide a conflicting submitted state.
func (t *crawlRequestTool) expireProposal(ctx context.Context, db *cdb.Handler, row cdb.CrawlProposalRow, now time.Time) (map[string]any, error) {
	// A committed operation may exist even though the proposal looks expired
	// (legacy divergence or crash). Surface that authoritative operation rather
	// than reporting the proposal as unscheduled.
	if reconciled, found, reconcileErr := cdb.ReconcileCrawlProposalOperation(ctx, db, row); reconcileErr == nil && found {
		return crawlRequestToolMap(CrawlRequestToolResult{
			OperationID: reconciled.OperationID, RequestID: reconciled.RequestID,
			SourceID: reconciled.SourceID, State: string(reconciled.State),
			ReasonCode: reconciled.ReasonCode, Reused: true,
			ObservedAt: now, ExpiresAt: reconciled.ExpiresAt,
		}), nil
	}
	transition, err := cdb.MarkCrawlProposalExpired(ctx, db, row.RequestID)
	if err == nil {
		return crawlProposalRowMap(transition.Row, cdb.CrawlProposalStateExpired, CrawlReasonProposalExpired, now, false), nil
	}
	switch {
	case errors.Is(err, cdb.ErrCrawlProposalStateConflict):
		current, found, readErr := cdb.GetCrawlProposalByRequest(ctx, db, row.RequestID)
		if readErr != nil {
			return nil, ErrCrawlProposalStoreUnavailable
		}
		if !found {
			return nil, ErrCrawlProposalNotFound
		}
		if current.State == cdb.CrawlProposalStateSubmitted {
			return crawlProposalRowMap(current, current.State, CrawlReasonApprovalRecorded, now, true), nil
		}
		if current.State == cdb.CrawlProposalStateExpired {
			return crawlProposalRowMap(current, current.State, CrawlReasonProposalExpired, now, false), nil
		}
		return nil, ErrCrawlProposalConflict
	case errors.Is(err, cdb.ErrCrawlProposalRowNotFound):
		return nil, ErrCrawlProposalNotFound
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil, err
	default:
		return nil, ErrCrawlProposalStoreUnavailable
	}
}

func crawlProposalFromRow(row cdb.CrawlProposalRow) CrawlRequestProposal {
	return CrawlRequestProposal{
		RequestID: row.RequestID, SourceID: row.SourceID,
		OperationType: row.OperationType, ExpiresAt: row.ExpiresAt.UTC(),
	}
}

func validateCrawlProposalBinding(runtime ToolRuntime, row cdb.CrawlProposalRow, sourceID uint64) error {
	if row.RunID != runtime.Auth.RunID || row.ToolCallID != runtime.CallID ||
		row.AgentID != runtime.Auth.Identity.AgentID || row.Owner != runtime.Auth.Identity.Owner ||
		row.SourceID != sourceID || row.OperationType != cdb.CrawlOperationType {
		return ErrCrawlProposalConflict
	}
	return nil
}

type crawlProposalPolicySnapshot struct {
	Identity            AgentIdentity `json:"identity"`
	AllowedOperations   []string      `json:"allowed_operations"`
	AllowedSources      []uint64      `json:"allowed_sources"`
	MaxRequests         int           `json:"max_requests"`
	BudgetWindowSeconds int64         `json:"budget_window_seconds"`
	Issuer              string        `json:"issuer"`
}

func (r *CrawlRequestToolRuntime) policySnapshot() (string, error) {
	if r == nil {
		return "", ErrCrawlMutationDisabled
	}
	operations := make([]string, 0, len(r.Policy.AllowedOperations))
	for name := range r.Policy.AllowedOperations {
		operations = append(operations, name)
	}
	sources := make([]uint64, 0, len(r.Policy.AllowedSources))
	for sourceID := range r.Policy.AllowedSources {
		sources = append(sources, sourceID)
	}
	encoded, err := json.Marshal(crawlProposalPolicySnapshot{
		Identity: r.Identity, AllowedOperations: operations, AllowedSources: sources,
		MaxRequests:         r.Policy.MaxRequests,
		BudgetWindowSeconds: int64(r.Policy.BudgetWindow / time.Second), Issuer: r.Issuer,
	})
	if err != nil {
		return "", ErrCrawlProposalConflict
	}
	return string(encoded), nil
}

func (t *crawlRequestTool) persistProposal(ctx context.Context, runtime ToolRuntime, proposal CrawlRequestProposal) (cdb.CrawlProposalRow, error) {
	policyJSON, err := t.runtime.policySnapshot()
	if err != nil {
		return cdb.CrawlProposalRow{}, err
	}
	row := cdb.CrawlProposalRow{
		RequestID:           proposal.RequestID,
		RunID:               runtime.Auth.RunID,
		ToolCallID:          runtime.CallID,
		AgentID:             runtime.Auth.Identity.AgentID,
		Owner:               runtime.Auth.Identity.Owner,
		PrincipalUserID:     t.runtime.Policy.Principal.UserID,
		SourceID:            proposal.SourceID,
		OperationType:       proposal.OperationType,
		ProposalSHA256:      proposal.Fingerprint(),
		TraceID:             runtime.Auth.TraceID,
		MaxRequests:         t.runtime.Policy.MaxRequests,
		BudgetWindowSeconds: int64(t.runtime.Policy.BudgetWindow / time.Second),
		PolicyJSON:          policyJSON,
		State:               cdb.CrawlProposalStatePending,
		ExpiresAt:           proposal.ExpiresAt,
	}
	db := t.runtime.DB
	if _, err := cdb.InsertCrawlProposal(ctx, &db, row); err != nil {
		return cdb.CrawlProposalRow{}, ErrCrawlProposalStoreUnavailable
	}
	stored, found, err := cdb.GetCrawlProposalByRequest(ctx, &db, proposal.RequestID)
	if err != nil {
		return cdb.CrawlProposalRow{}, ErrCrawlProposalStoreUnavailable
	}
	if !found {
		return cdb.CrawlProposalRow{}, ErrCrawlProposalStoreUnavailable
	}
	if stored.RunID != row.RunID || stored.ToolCallID != row.ToolCallID ||
		stored.AgentID != row.AgentID || stored.Owner != row.Owner ||
		stored.PrincipalUserID != row.PrincipalUserID || stored.SourceID != row.SourceID ||
		stored.OperationType != row.OperationType {
		return cdb.CrawlProposalRow{}, ErrCrawlProposalConflict
	}
	return stored, nil
}

func (t *crawlRequestTool) emitAudit(runtime ToolRuntime, proposal CrawlRequestProposal, decision CrawlRequestDecision, outcome string) {
	emitCrawlRequestAudit(runtime.Auth.RunID, runtime.Auth.TraceID, runtime.Auth.Identity.AgentID,
		runtime.Auth.Identity.Name, runtime.Auth.Identity.Owner, runtime.CallID,
		proposal, decision.PrincipalUserID(), outcome, decision.ReasonCode(), decision.Stage(),
		decision.ApprovalOutcome(), decision.SourceOwnershipOutcome(), "")
}

func crawlRequestDeniedError(decision CrawlRequestDecision) error {
	return &ToolAuthError{Code: decision.ReasonCode(), Reason: "crawl request denied by trusted policy"}
}

func crawlRequestToolMap(result CrawlRequestToolResult) map[string]any {
	return map[string]any{
		"request_id":        result.RequestID,
		"source_id":         result.SourceID,
		"state":             result.State,
		"reason_code":       result.ReasonCode,
		"approval_required": result.ApprovalRequired,
		"operation_id":      result.OperationID,
		"reused":            result.Reused,
		"expires_at":        result.ExpiresAt.UTC(),
		"observed_at":       result.ObservedAt.UTC(),
	}
}

// SubmitApprovedProposal performs the operator-controlled second phase. It
// loads the durable proposal, resolves the exact durable approval, re-runs the
// authorization policy and delegates to the existing scheduler. The agent
// tool never calls this method.
func (r *CrawlRequestToolRuntime) SubmitApprovedProposal(ctx context.Context, requestID, approvalID string, operator CrawlPrincipal) (cdb.CrawlScheduleResult, error) {
	if r == nil {
		return cdb.CrawlScheduleResult{}, ErrCrawlMutationDisabled
	}
	if r.DB == nil || r.ApprovalStore == nil || r.Policy.SourceAuthorizer == nil ||
		r.Policy.ApprovalVerifier == nil || len(r.Policy.AllowedSources) == 0 {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalStoreUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestID = strings.TrimSpace(requestID)
	approvalID = strings.TrimSpace(approvalID)
	if requestID == "" || approvalID == "" {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalNotFound
	}
	if err := operator.Validate(); err != nil {
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalApproverUnauthorized
	}
	db := r.DB
	row, found, err := cdb.GetCrawlProposalByRequest(ctx, &db, requestID)
	if err != nil {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalStoreUnavailable
	}
	if !found {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalNotFound
	}
	if row.AgentID != r.Identity.AgentID || row.Owner != r.Identity.Owner ||
		row.PrincipalUserID != r.Policy.Principal.UserID || row.OperationType != cdb.CrawlOperationType {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalConflict
	}
	if !r.Policy.AllowedSources[row.SourceID] {
		return cdb.CrawlScheduleResult{}, ErrCrawlSubmissionDenied
	}
	now := r.now()
	if row.MaxRequests <= 0 || row.BudgetWindowSeconds <= 0 || strings.TrimSpace(row.PolicyJSON) == "" {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalConflict
	}
	var snapshot crawlProposalPolicySnapshot
	if err := json.Unmarshal([]byte(row.PolicyJSON), &snapshot); err != nil {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalConflict
	}
	if snapshot.Identity.AgentID != row.AgentID || snapshot.Identity.Owner != row.Owner ||
		snapshot.MaxRequests != row.MaxRequests ||
		snapshot.BudgetWindowSeconds != row.BudgetWindowSeconds ||
		!containsUint64(snapshot.AllowedSources, row.SourceID) ||
		!containsString(snapshot.AllowedOperations, crawlRequestToolName) {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalConflict
	}
	if row.State == cdb.CrawlProposalStateDenied {
		return cdb.CrawlScheduleResult{}, ErrCrawlSubmissionDenied
	}
	if err := r.Policy.SourceAuthorizer.AuthorizeCrawlSource(ctx, operator, row.SourceID); err != nil {
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalApproverUnauthorized
	}
	proposal := CrawlRequestProposal{
		RequestID: row.RequestID, SourceID: row.SourceID,
		OperationType: row.OperationType, ExpiresAt: row.ExpiresAt.UTC(),
	}
	if proposal.Fingerprint() != row.ProposalSHA256 {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalConflict
	}
	// Reconcile an already committed operation before any expiry decision. A
	// crash or the pre-atomic divergence window may have left the proposal
	// pending or expired while the authoritative operation exists.
	reconciled, operationFound, reconcileErr := cdb.ReconcileCrawlProposalOperation(ctx, &db, row)
	if reconcileErr != nil {
		if errors.Is(reconcileErr, cdb.ErrCrawlSchedulingUncertain) {
			return reconciled, ErrCrawlSchedulingUncertain
		}
		if errors.Is(reconcileErr, cdb.ErrCrawlProposalRowNotFound) {
			return cdb.CrawlScheduleResult{}, ErrCrawlProposalNotFound
		}
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalStoreUnavailable
	}
	if operationFound {
		emitCrawlRequestAudit(row.RunID, row.TraceID, row.AgentID, r.Identity.Name, row.Owner,
			row.ToolCallID, proposal, row.PrincipalUserID, auditOutcomeAllowed,
			reconciled.ReasonCode, CrawlStageReused, CrawlApprovalOutcomeVerified,
			CrawlSourceOutcomeVerified, string(reconciled.State))
		return reconciled, nil
	}
	if row.State == cdb.CrawlProposalStateSubmitted {
		return cdb.CrawlScheduleResult{
			RequestID: row.RequestID, SourceID: row.SourceID,
			State: cdb.CrawlOperationUncertain, ReasonCode: cdb.CrawlReasonProposalInconsistent,
		}, ErrCrawlProposalConflict
	}
	record, found, err := r.ApprovalStore.LookupCrawlApproval(ctx, approvalID)
	if err != nil {
		if errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
			return cdb.CrawlScheduleResult{}, ErrCrawlApprovalApproverUnauthorized
		}
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalUnknown
	}
	if record.Revoked {
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalRevoked
	}
	approval := record.Approval
	if record.PrincipalUserID != row.PrincipalUserID ||
		approval.ProposalSHA256 != row.ProposalSHA256 ||
		approval.RunID != row.RunID || approval.AgentID != row.AgentID ||
		approval.Owner != row.Owner || approval.SourceID != row.SourceID ||
		approval.OperationType != row.OperationType {
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalForged
	}
	if record.ApproverPrincipal.UserID != operator.UserID &&
		record.IssuerPrincipal.UserID != operator.UserID {
		return cdb.CrawlScheduleResult{}, ErrCrawlApprovalApproverUnauthorized
	}
	if row.State == cdb.CrawlProposalStateExpired || !row.ExpiresAt.After(now) {
		_, expireErr := cdb.MarkCrawlProposalExpired(ctx, &db, row.RequestID)
		if expireErr != nil {
			if errors.Is(expireErr, cdb.ErrCrawlProposalStateConflict) {
				return cdb.CrawlScheduleResult{
					RequestID: row.RequestID, SourceID: row.SourceID,
					State: cdb.CrawlOperationUncertain, ReasonCode: cdb.CrawlReasonProposalInconsistent,
				}, ErrCrawlProposalConflict
			}
			return cdb.CrawlScheduleResult{}, ErrCrawlProposalStoreUnavailable
		}
		return cdb.CrawlScheduleResult{
			RequestID: row.RequestID, SourceID: row.SourceID,
			State: cdb.CrawlOperationExpired, ReasonCode: cdb.CrawlReasonExpired,
		}, ErrCrawlProposalExpired
	}
	policy := r.Policy
	policy.AllowedOperations = stringSet(snapshot.AllowedOperations)
	policy.AllowedSources = sliceToSourceSet(snapshot.AllowedSources)
	policy.MaxRequests = row.MaxRequests
	policy.BudgetWindow = time.Duration(row.BudgetWindowSeconds) * time.Second
	decision := AuthorizeCrawlRequest(ctx, snapshot.Identity, policy, proposal, &approval, row.RunID, row.TraceID)
	if !decision.Allowed() {
		emitCrawlRequestAudit(row.RunID, row.TraceID, row.AgentID, r.Identity.Name, row.Owner,
			row.ToolCallID, proposal, row.PrincipalUserID, auditOutcomeDenied,
			decision.ReasonCode(), decision.Stage(), decision.ApprovalOutcome(),
			decision.SourceOwnershipOutcome(), "")
		return cdb.CrawlScheduleResult{
			RequestID: row.RequestID, SourceID: row.SourceID,
			State: cdb.CrawlOperationDenied, ReasonCode: decision.ReasonCode(),
		}, fmt.Errorf("%w: %s", ErrCrawlSubmissionDenied, decision.ReasonCode())
	}
	scheduleRequest, err := BuildCrawlScheduleRequest(snapshot.Identity, proposal, approval, decision, row.RunID)
	if err != nil {
		return cdb.CrawlScheduleResult{}, ErrCrawlProposalConflict
	}
	result, err := cdb.ScheduleSourceCrawlRequestForProposal(ctx, &db, scheduleRequest, row.RequestID, row.ProposalSHA256)
	if err != nil {
		emitCrawlRequestAudit(row.RunID, row.TraceID, row.AgentID, r.Identity.Name, row.Owner,
			row.ToolCallID, proposal, row.PrincipalUserID, auditOutcomeError,
			result.ReasonCode, decision.Stage(), decision.ApprovalOutcome(),
			decision.SourceOwnershipOutcome(), string(result.State))
		if errors.Is(err, cdb.ErrCrawlSchedulingUncertain) {
			return result, ErrCrawlSchedulingUncertain
		}
		return result, err
	}
	emitCrawlRequestAudit(row.RunID, row.TraceID, row.AgentID, r.Identity.Name, row.Owner,
		row.ToolCallID, proposal, row.PrincipalUserID, auditOutcomeAllowed,
		result.ReasonCode, decision.Stage(), decision.ApprovalOutcome(),
		decision.SourceOwnershipOutcome(), string(result.State))
	return result, nil
}

func containsUint64(values []uint64, wanted uint64) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func emitCrawlRequestAudit(runID, traceID, agentID, agentName, owner, callID string,
	proposal CrawlRequestProposal, principalUserID uint64, outcome, reasonCode, stage,
	approvalOutcome, sourceOutcome, schedulingState string,
) {
	if AgentsEngine == nil {
		return
	}
	AgentsEngine.appendAudit(AuditEvent{
		RunID: runID, TraceID: traceID, AgentID: agentID, AgentName: agentName, Owner: owner,
		Action: crawlRequestToolName, RequiredCapability: "tool_execution/db_write",
		CapabilitiesUsed: []string{"tool_execution", "db_write"},
		Outcome:          outcome, Reason: reasonCode, ReasonCode: reasonCode,
		PrincipalUserID: principalUserID, SourceID: proposal.SourceID,
		RequestID: proposal.RequestID, OperationType: proposal.OperationType,
		IdempotencyKey:       proposal.RequestID,
		AuthorizationOutcome: outcome, AuthorizationReasonCode: reasonCode,
		AuthorizationStage: stage, ApprovalOutcome: approvalOutcome,
		SourceOwnershipOutcome: sourceOutcome, SchedulingState: schedulingState,
	})
}

var _ AgentTool = (*crawlRequestTool)(nil)
var _ AgentToolEffect = (*crawlRequestTool)(nil)
