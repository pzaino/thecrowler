package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	// MCPAuditEventAdmissionDenied records a proposal/tool-call admission that
	// was denied before any durable proposal row existed.
	MCPAuditEventAdmissionDenied = cdb.MCPMutationAuditEventAdmissionDenied
	// MCPAuditEventRegistrationDenied records a production proposal-tool
	// registration that failed a trusted gate.
	MCPAuditEventRegistrationDenied = cdb.MCPMutationAuditEventRegistrationDenied

	MCPAuditStageProposal     = "proposal"
	MCPAuditStageRegistration = "registration"
	MCPAuditStageToolLoop     = "tool_loop"
	MCPAuditStageGoal         = "goal"

	mcpAuditWriteTimeout  = 2 * time.Second
	mcpAuditMaxTextBytes  = 256
	mcpAuditMaxDetailSize = 2048
)

// MCPAdmissionDeniedEvent is the trusted, already-redacted description of a
// denied admission. It intentionally has no argument map: denied admissions
// must never copy raw model arguments into the durable audit.
type MCPAdmissionDeniedEvent struct {
	OccurredAt     time.Time
	EventType      string
	Stage          string
	RunID          string
	TraceID        string
	CallID         string
	Identity       AgentIdentity
	Principal      MCPMutationPrincipal
	ServerID       string
	ToolAlias      string
	PolicyID       string
	ReasonCode     string
	OperationCount int
}

// MCPMutationAuditSink is implemented by the durable proposal store. Callers
// must treat a sink error as an audit failure, never as a change to the
// admission decision.
type MCPMutationAuditSink interface {
	RecordAdmissionDenied(ctx context.Context, event MCPAdmissionDeniedEvent) error
}

// DatabaseMCPMutationAudit writes denied-admission events outside a proposal
// transaction. It is safe to use when no transaction exists.
type DatabaseMCPMutationAudit struct {
	DB  *cdb.Handler
	Now func() time.Time
}

func (a DatabaseMCPMutationAudit) RecordAdmissionDenied(ctx context.Context, event MCPAdmissionDeniedEvent) error {
	if a.DB == nil || *a.DB == nil {
		return cdb.ErrMCPMutationStoreUnsupported
	}
	record := buildMCPMutationAuditRecord(event)
	if a.Now != nil && record.OccurredAt.IsZero() {
		record.OccurredAt = a.Now().UTC()
	}
	return cdb.InsertMCPMutationAudit(ctx, a.DB, record)
}

// RecordAdmissionDenied lets the durable proposal store act as the audit sink
// for proposal-stage denials.
func (s *DatabaseMCPMutationProposalStore) RecordAdmissionDenied(ctx context.Context, event MCPAdmissionDeniedEvent) error {
	if s == nil || s.DB == nil {
		return ErrMCPMutationStoreUnavailable
	}
	return (DatabaseMCPMutationAudit{DB: s.DB, Now: s.Now}).RecordAdmissionDenied(ctx, event)
}

// recordMCPAdmissionDenied never changes the caller's denial. A write failure
// is logged with bounded fields and the original denial is returned unchanged.
func recordMCPAdmissionDenied(ctx context.Context, sink any, event MCPAdmissionDeniedEvent) {
	writer, ok := sink.(MCPMutationAuditSink)
	if !ok || writer == nil {
		return
	}
	auditCtx := context.Background()
	if ctx != nil {
		auditCtx = context.WithoutCancel(ctx)
	}
	auditCtx, cancel := context.WithTimeout(auditCtx, mcpAuditWriteTimeout)
	defer cancel()
	if err := writer.RecordAdmissionDenied(auditCtx, event); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError,
			"MCP mutation denied-admission audit write failed (stage=%s reason=%s error_type=%T)",
			normalizeMCPAuditStage(event.Stage), normalizeMCPAuditReasonCode(event.ReasonCode), err)
	}
}

func recordMCPAdmissionDeniedDB(ctx context.Context, db cdb.Handler, event MCPAdmissionDeniedEvent) {
	if db == nil {
		return
	}
	recordMCPAdmissionDenied(ctx, DatabaseMCPMutationAudit{DB: &db}, event)
}

func buildMCPMutationAuditRecord(event MCPAdmissionDeniedEvent) cdb.MCPMutationAuditEvent {
	stage := normalizeMCPAuditStage(event.Stage)
	reason := normalizeMCPAuditReasonCode(event.ReasonCode)
	eventType := normalizeMCPAuditEventType(event.EventType, stage)
	actorID := mcpAuditText(event.Identity.AgentID, mcpAuditMaxTextBytes)
	if actorID == "" {
		actorID = "unattributed"
	}
	var principal MCPMutationPrincipal
	if event.Principal.Validate() == nil {
		principal = event.Principal
	}
	detail := map[string]any{"stage": stage}
	if callID := sanitizeMCPAuditCallID(event.CallID); callID != "" {
		detail["call_id"] = callID
	}
	if policyID := mcpAuditText(event.PolicyID, mcpAuditMaxTextBytes); policyID != "" {
		detail["policy_id"] = policyID
	}
	if event.OperationCount > 0 {
		detail["operation_count"] = event.OperationCount
	}
	rawDetail, err := json.Marshal(detail)
	if err != nil || len(rawDetail) > mcpAuditMaxDetailSize {
		rawDetail = []byte(`{"stage":"` + stage + `"}`)
	}
	return cdb.MCPMutationAuditEvent{
		OccurredAt: event.OccurredAt, EventType: eventType,
		ActorType: "agent", ActorID: actorID,
		ActorIssuer:     mcpAuditText(principal.Issuer, mcpAuditMaxTextBytes),
		RunID:           mcpAuditText(event.RunID, mcpAuditMaxTextBytes),
		TraceID:         mcpAuditText(event.TraceID, mcpAuditMaxTextBytes),
		AgentID:         mcpAuditText(event.Identity.AgentID, mcpAuditMaxTextBytes),
		PrincipalUserID: principal.UserID,
		ServerID:        mcpAuditText(event.ServerID, mcpAuditMaxTextBytes),
		ToolAlias:       mcpAuditText(event.ToolAlias, mcpAuditMaxTextBytes),
		ReasonCode:      reason,
		RedactedDetail:  rawDetail,
	}
}

func normalizeMCPAuditStage(stage string) string {
	switch strings.TrimSpace(stage) {
	case MCPAuditStageRegistration:
		return MCPAuditStageRegistration
	case MCPAuditStageToolLoop:
		return MCPAuditStageToolLoop
	case MCPAuditStageGoal:
		return MCPAuditStageGoal
	default:
		return MCPAuditStageProposal
	}
}

func normalizeMCPAuditEventType(eventType, stage string) string {
	switch strings.TrimSpace(eventType) {
	case MCPAuditEventAdmissionDenied:
		return MCPAuditEventAdmissionDenied
	case MCPAuditEventRegistrationDenied:
		return MCPAuditEventRegistrationDenied
	}
	if stage == MCPAuditStageRegistration {
		return MCPAuditEventRegistrationDenied
	}
	return MCPAuditEventAdmissionDenied
}

var mcpAuditReasonCodes = map[string]bool{
	ToolDenyNoOptIn:          true,
	ToolDenyNoIdentity:       true,
	ToolDenyEnforcementOff:   true,
	ToolDenyUnknownTool:      true,
	ToolDenyNotAllowlisted:   true,
	ToolDenyCapability:       true,
	ToolDenyTrust:            true,
	ToolDenyContract:         true,
	ToolDenyArguments:        true,
	ToolDenyScope:            true,
	ToolDenyBudget:           true,
	ToolDenyContext:          true,
	ToolDenyDuplicateCallID:  true,
	ToolDenyMutationDisabled: true,
	ToolDenyOperation:        true,
	ToolDenyEffect:           true,

	"missing_principal":       true,
	"missing_owner":           true,
	"missing_run_identity":    true,
	"not_approved":            true,
	"descriptor_unavailable":  true,
	"descriptor_mismatch":     true,
	"agent_not_approved":      true,
	"capability_missing":      true,
	"trust_insufficient":      true,
	"contract_forbidden":      true,
	"transport_not_approved":  true,
	"source_scope_denied":     true,
	"resource_scope_conflict": true,
	"manager_unavailable":     true,
	"registry_denied":         true,
	"policy_invalid":          true,
	"store_unavailable":       true,
	"proposal_conflict":       true,
	"quota_exceeded":          true,
	"proposal_expired":        true,
	"proposal_corrupt":        true,
	"approval_invalid":        true,
	"admission_denied":        true,
}

func normalizeMCPAuditReasonCode(code string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	if mcpAuditReasonCodes[code] {
		return code
	}
	return "admission_denied"
}

// mcpAdmissionReasonCode maps a denial to a stable bounded code. Raw error
// text is never stored or returned as the audit reason.
func mcpAdmissionReasonCode(err error) string {
	if err == nil {
		return ""
	}
	var authErr *ToolAuthError
	if errors.As(err, &authErr) && authErr.Code != "" {
		return normalizeMCPAuditReasonCode(authErr.Code)
	}
	switch {
	case errors.Is(err, ErrMCPMutationDisabled):
		return ToolDenyMutationDisabled
	case errors.Is(err, ErrMCPMutationPolicyInvalid):
		return "policy_invalid"
	case errors.Is(err, ErrMCPMutationNotApproved):
		return "not_approved"
	case errors.Is(err, ErrMCPMutationArguments):
		return ToolDenyArguments
	case errors.Is(err, ErrMCPMutationRunIdentity):
		return "missing_run_identity"
	case errors.Is(err, ErrMCPMutationPrincipal):
		return "missing_principal"
	case errors.Is(err, ErrMCPMutationStoreUnavailable):
		return "store_unavailable"
	case errors.Is(err, ErrMCPMutationProposalConflict):
		return "proposal_conflict"
	case errors.Is(err, ErrMCPMutationQuota):
		return "quota_exceeded"
	case errors.Is(err, ErrMCPMutationProposalExpired):
		return "proposal_expired"
	case errors.Is(err, ErrMCPMutationProposalCorrupt):
		return "proposal_corrupt"
	case errors.Is(err, ErrMCPMutationApproval):
		return "approval_invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ToolDenyContext
	default:
		return "admission_denied"
	}
}

func sanitizeMCPAuditCallID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	safe := len(value) <= 64
	if safe {
		for _, r := range value {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' {
				continue
			}
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

func mcpAuditText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var out strings.Builder
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > maxBytes {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

// MCPMutationAuditIdentity is the trusted descriptor identity attached to a
// denied proposal-tool admission.
type MCPMutationAuditIdentity struct {
	ServerID  string
	ToolAlias string
	PolicyID  string
}

// MCPMutationProposalAuditInfo exposes descriptor identity without exposing
// the descriptor or the MCP transport.
type MCPMutationProposalAuditInfo interface {
	MCPMutationAuditIdentity() MCPMutationAuditIdentity
}

func mcpMutationProposalAuditIdentity(tool AgentTool) (MCPMutationAuditIdentity, bool) {
	marker, ok := tool.(MCPMutationProposalMarker)
	if !ok || !marker.IsMCPMutationProposal() {
		return MCPMutationAuditIdentity{}, false
	}
	if info, ok := tool.(MCPMutationProposalAuditInfo); ok {
		return info.MCPMutationAuditIdentity(), true
	}
	return MCPMutationAuditIdentity{}, true
}
