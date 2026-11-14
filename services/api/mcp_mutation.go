package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	agentpkg "github.com/pzaino/thecrowler/pkg/agent"
	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	errMCPMutationAuthRequired = errors.New("MCP mutation operations require enabled API authentication")
	errMCPMutationForbidden    = errors.New("MCP mutation operation is not authorized")
)

type mcpMutationApprovalSummary struct {
	ApprovalID        string     `json:"approval_id"`
	State             string     `json:"state"`
	ApprovedBy        string     `json:"approved_by"`
	ApprovedAt        time.Time  `json:"approved_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	MaxExecutions     int        `json:"max_executions"`
	ExecutionsStarted int        `json:"executions_started"`
}

type mcpMutationProposalResponse struct {
	ProposalID       string                       `json:"proposal_id"`
	ProposalSHA256   string                       `json:"proposal_sha256"`
	IdentityVersion  int                          `json:"identity_version"`
	State            string                       `json:"state"`
	RunID            string                       `json:"run_id"`
	TraceID          string                       `json:"trace_id,omitempty"`
	AgentID          string                       `json:"agent_id"`
	Owner            string                       `json:"owner"`
	ServerID         string                       `json:"server_id"`
	RawToolName      string                       `json:"tool"`
	ToolAlias        string                       `json:"tool_alias"`
	Transport        string                       `json:"transport"`
	PolicyID         string                       `json:"policy_id"`
	PolicyVersion    string                       `json:"policy_version"`
	PolicySHA256     string                       `json:"policy_sha256"`
	ArgumentsSHA256  string                       `json:"arguments_sha256"`
	ArgumentKeys     []string                     `json:"argument_keys"`
	SourceIDs        []uint64                     `json:"source_ids,omitempty"`
	ExternalResource string                       `json:"external_resource,omitempty"`
	CreatedAt        time.Time                    `json:"created_at"`
	ExpiresAt        time.Time                    `json:"expires_at"`
	ApprovedAt       *time.Time                   `json:"approved_at,omitempty"`
	TerminalAt       *time.Time                   `json:"terminal_at,omitempty"`
	TerminalReason   string                       `json:"terminal_reason,omitempty"`
	ActiveApproval   *mcpMutationApprovalSummary  `json:"active_approval,omitempty"`
	Approvals        []mcpMutationApprovalSummary `json:"approvals,omitempty"`
}

type mcpMutationListResponse struct {
	Items      []mcpMutationProposalResponse `json:"items"`
	NextCursor string                        `json:"next_cursor,omitempty"`
}

type mcpMutationApproveRequest struct {
	ProposalSHA256  string    `json:"proposal_sha256"`
	IdentityVersion int       `json:"identity_version"`
	MaxExecutions   int       `json:"max_executions"`
	RequestedExpiry time.Time `json:"requested_expiry"`
	Reason          string    `json:"reason"`
}

type mcpMutationDenyRequest struct {
	ProposalSHA256 string `json:"proposal_sha256"`
	Reason         string `json:"reason"`
}

type mcpMutationRevokeRequest struct {
	ApprovalID     string `json:"approval_id"`
	ProposalSHA256 string `json:"proposal_sha256"`
	Reason         string `json:"reason"`
}

func resolveMCPMutationPrincipal(ctx context.Context) (agentpkg.MCPMutationPrincipal, auth.Identity, error) {
	if !auth.Enabled(config.API.Auth) {
		return agentpkg.MCPMutationPrincipal{}, auth.Identity{}, errMCPMutationAuthRequired
	}
	identity, ok := auth.FromContext(ctx)
	if !ok {
		return agentpkg.MCPMutationPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, dbHandler, identity)
	if err != nil {
		return agentpkg.MCPMutationPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	userID, err := strconv.ParseUint(strings.TrimSpace(resolved.Subject), 10, 64)
	if err != nil || userID == 0 {
		return agentpkg.MCPMutationPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	principal := agentpkg.MCPMutationPrincipal{
		UserID: userID, Username: resolved.Username, Issuer: resolved.Issuer,
	}
	if err := principal.Validate(); err != nil {
		return agentpkg.MCPMutationPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	return principal, resolved, nil
}

func mcpMutationIsAdmin(identity auth.Identity) bool {
	return hasRole(identity.Roles, roleAdmin, roleSuperAdmin)
}

func authorizeMCPMutationProposal(ctx context.Context, principal agentpkg.MCPMutationPrincipal, identity auth.Identity, row cdb.MCPMutationProposalRow) error {
	if len(row.SourceIDs) > 0 {
		authorizer := agentpkg.DatabaseCrawlSourceAuthorizer{
			Lookup:     agentpkg.DatabaseSourceOwnerLookup{DB: &dbHandler},
			AllowAdmin: true,
		}
		crawlPrincipal := agentpkg.CrawlPrincipal{
			UserID: principal.UserID, Username: principal.Username,
			Issuer: principal.Issuer, Roles: identity.Roles, Scopes: identity.Scopes,
		}
		for _, sourceID := range row.SourceIDs {
			if err := authorizer.AuthorizeCrawlSource(ctx, crawlPrincipal, sourceID); err != nil {
				return errMCPMutationForbidden
			}
		}
		return nil
	}
	if !mcpMutationIsAdmin(identity) {
		return errMCPMutationForbidden
	}
	return nil
}

func validateStoredMCPMutationProposal(row cdb.MCPMutationProposalRow) error {
	if _, err := agentpkg.ValidateStoredMCPMutationProposal(row); err != nil {
		return err
	}
	return nil
}

// authorizeMCPMutationAdministration allows an administrator to deny or revoke
// a proposal even when source ownership provenance changed after proposal
// creation. Approval separately requires current proposal-principal ownership.
func authorizeMCPMutationAdministration(ctx context.Context, principal agentpkg.MCPMutationPrincipal, identity auth.Identity, row cdb.MCPMutationProposalRow) error {
	if mcpMutationIsAdmin(identity) {
		return nil
	}
	return authorizeMCPMutationProposal(ctx, principal, identity, row)
}

// authorizeMCPMutationObservation allows administrators to observe every
// proposal, including source-scoped proposals whose ownership provenance has
// changed. Non-admin observation still requires verified ownership of every
// source; external-resource proposals remain administrator-only.
func authorizeMCPMutationObservation(ctx context.Context, principal agentpkg.MCPMutationPrincipal, identity auth.Identity, row cdb.MCPMutationProposalRow) error {
	if mcpMutationIsAdmin(identity) {
		return nil
	}
	return authorizeMCPMutationProposal(ctx, principal, identity, row)
}

func requireActiveMCPProposalPrincipal(ctx context.Context, proposal cdb.MCPMutationProposalRow) error {
	if proposal.PrincipalUserID == 0 {
		return errMCPMutationForbidden
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, dbHandler, auth.Identity{
		Subject: strconv.FormatUint(proposal.PrincipalUserID, 10),
	})
	if err != nil {
		return errMCPMutationForbidden
	}
	if len(proposal.SourceIDs) > 0 {
		authorizer := agentpkg.DatabaseCrawlSourceAuthorizer{
			Lookup:     agentpkg.DatabaseSourceOwnerLookup{DB: &dbHandler},
			AllowAdmin: true,
		}
		principal := agentpkg.CrawlPrincipal{
			UserID: proposal.PrincipalUserID, Username: resolved.Username,
			Issuer: resolved.Issuer, Roles: resolved.Roles, Scopes: resolved.Scopes,
		}
		for _, sourceID := range proposal.SourceIDs {
			if err := authorizer.AuthorizeCrawlSource(ctx, principal, sourceID); err != nil {
				return errMCPMutationForbidden
			}
		}
	}
	return nil
}

func mcpMutationErrorCode(err error) int {
	switch {
	case errors.Is(err, errMCPMutationAuthRequired), errors.Is(err, errSourceUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, errMCPMutationForbidden), errors.Is(err, errSourceForbidden),
		errors.Is(err, errSourceOwnerUnknown), errors.Is(err, agentpkg.ErrMCPMutationPrincipal),
		errors.Is(err, agentpkg.ErrMCPMutationApprovalForbidden):
		return http.StatusForbidden
	case errors.Is(err, cdb.ErrMCPMutationProposalNotFound), errors.Is(err, cdb.ErrMCPMutationApprovalNotFound):
		return http.StatusNotFound
	case errors.Is(err, agentpkg.ErrMCPMutationProposalExpired), errors.Is(err, cdb.ErrMCPMutationProposalExpired):
		return http.StatusGone
	case errors.Is(err, agentpkg.ErrMCPMutationQuota), errors.Is(err, cdb.ErrMCPMutationQuotaReached):
		return http.StatusTooManyRequests
	case errors.Is(err, agentpkg.ErrMCPMutationProposalConflict), errors.Is(err, agentpkg.ErrMCPMutationApproval),
		errors.Is(err, cdb.ErrMCPMutationProposalConflict), errors.Is(err, cdb.ErrMCPMutationApprovalConflict),
		errors.Is(err, cdb.ErrMCPMutationApprovalExpired), errors.Is(err, cdb.ErrMCPMutationApprovalRevoked),
		errors.Is(err, agentpkg.ErrMCPMutationPolicyMismatch), errors.Is(err, agentpkg.ErrMCPMutationOperationUnauthorized):
		return http.StatusConflict
	case errors.Is(err, agentpkg.ErrMCPMutationPolicyUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, agentpkg.ErrMCPMutationProposalCorrupt), errors.Is(err, agentpkg.ErrMCPMutationApprovalCorrupt),
		errors.Is(err, agentpkg.ErrMCPMutationStoreUnavailable), errors.Is(err, cdb.ErrMCPMutationSchemaUnavailable),
		errors.Is(err, cdb.ErrMCPMutationStoreUnsupported):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		return 499
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func mcpMutationApprovalSummaryFromRow(row cdb.MCPMutationApprovalRow) mcpMutationApprovalSummary {
	summary := mcpMutationApprovalSummary{
		ApprovalID: row.ApprovalID, State: row.State, ApprovedBy: row.ApprovedBy,
		ApprovedAt: row.ApprovedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		MaxExecutions: row.MaxExecutions, ExecutionsStarted: row.ExecutionsStarted,
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time.UTC()
		summary.RevokedAt = &t
	}
	return summary
}

func mcpMutationApprovalSummaryFromAgent(approval agentpkg.MCPMutationApproval) mcpMutationApprovalSummary {
	summary := mcpMutationApprovalSummary{
		ApprovalID: approval.ApprovalID, State: approval.State,
		ApprovedBy: approval.ApprovedBy, ApprovedAt: approval.ApprovedAt.UTC(),
		ExpiresAt: approval.ExpiresAt.UTC(), MaxExecutions: approval.MaxExecutions,
		ExecutionsStarted: approval.ExecutionsStarted,
	}
	if approval.RevokedAt != nil {
		t := approval.RevokedAt.UTC()
		summary.RevokedAt = &t
	}
	return summary
}

func mcpMutationArgumentKeys(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 32 {
		keys = keys[:32]
	}
	return keys
}

func mcpMutationSafeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		if builder.Len() >= limit {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func mcpMutationSafeResource(value string) string {
	value = mcpMutationSafeText(value, 512)
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return mcpMutationSafeText(parsed.String(), 256)
	}
	return mcpMutationSafeText(value, 256)
}

func mcpMutationProposalResponseFromRow(row cdb.MCPMutationProposalRow, active *cdb.MCPMutationApprovalRow, history []cdb.MCPMutationApprovalRow) mcpMutationProposalResponse {
	response := mcpMutationProposalResponse{
		ProposalID: row.ProposalID, ProposalSHA256: row.ProposalSHA256,
		IdentityVersion: row.IdentityVersion, State: row.State,
		RunID: row.RunID, TraceID: row.TraceID, AgentID: row.AgentID,
		Owner: row.Owner, ServerID: row.ServerID, RawToolName: row.RawToolName,
		ToolAlias: row.ToolAlias, Transport: row.Transport,
		PolicyID: row.PolicyID, PolicyVersion: row.PolicyVersion,
		PolicySHA256: row.PolicySHA256, ArgumentsSHA256: row.ArgumentsSHA256,
		ArgumentKeys:     mcpMutationArgumentKeys(row.CanonicalArguments),
		SourceIDs:        append([]uint64(nil), row.SourceIDs...),
		ExternalResource: mcpMutationSafeResource(row.ExternalResource),
		CreatedAt:        row.CreatedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		TerminalReason: mcpMutationSafeText(row.TerminalReason.String, 256),
	}
	if row.ApprovedAt.Valid {
		t := row.ApprovedAt.Time.UTC()
		response.ApprovedAt = &t
	}
	if row.TerminalAt.Valid {
		t := row.TerminalAt.Time.UTC()
		response.TerminalAt = &t
	}
	if row.State == cdb.MCPMutationStateApproved && active == nil {
		// The stored state is reconciled transactionally on the next approval
		// action. The read model must not present an approved proposal without a
		// currently usable approval.
		response.State = cdb.MCPMutationStateApprovalRequired
		response.ApprovedAt = nil
	}
	if active != nil {
		summary := mcpMutationApprovalSummaryFromRow(*active)
		response.ActiveApproval = &summary
	}
	if len(history) > 0 {
		response.Approvals = make([]mcpMutationApprovalSummary, 0, len(history))
		for _, approval := range history {
			response.Approvals = append(response.Approvals, mcpMutationApprovalSummaryFromRow(approval))
		}
	}
	return response
}

type mcpMutationCursor struct {
	CreatedAt  time.Time `json:"created_at"`
	ProposalID string    `json:"proposal_id"`
}

func encodeMCPMutationCursor(createdAt time.Time, proposalID string) string {
	encoded, _ := json.Marshal(mcpMutationCursor{CreatedAt: createdAt.UTC(), ProposalID: proposalID})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeMCPMutationCursor(raw string) (mcpMutationCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return mcpMutationCursor{}, err
	}
	var cursor mcpMutationCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return mcpMutationCursor{}, err
	}
	if cursor.CreatedAt.IsZero() || strings.TrimSpace(cursor.ProposalID) == "" {
		return mcpMutationCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func mcpMutationListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	principal, identity, err := resolveMCPMutationPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation authentication failed", mcpMutationErrorCode(err), 0)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			handleErrorAndRespond(w, errors.New("invalid limit"), nil, "Invalid MCP mutation query", http.StatusBadRequest, 0)
			return
		}
		limit = parsed
	}
	if limit > 100 {
		limit = 100
	}
	stateFilter := strings.TrimSpace(r.URL.Query().Get("state"))
	serverFilter := strings.TrimSpace(r.URL.Query().Get("server_id"))
	runFilter := strings.TrimSpace(r.URL.Query().Get("run_id"))
	if len(stateFilter) > 64 || len(serverFilter) > 256 || len(runFilter) > 256 {
		handleErrorAndRespond(w, errors.New("filter is too long"), nil, "Invalid MCP mutation query", http.StatusBadRequest, 0)
		return
	}
	filter := cdb.MCPMutationProposalFilter{
		State:        stateFilter,
		ServerID:     serverFilter,
		RunID:        runFilter,
		Limit:        limit,
		ViewerUserID: principal.UserID,
		ViewerAdmin:  mcpMutationIsAdmin(identity),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		if len(raw) > 512 {
			handleErrorAndRespond(w, errors.New("cursor is too long"), nil, "Invalid MCP mutation query", http.StatusBadRequest, 0)
			return
		}
		cursor, err := decodeMCPMutationCursor(raw)
		if err != nil {
			handleErrorAndRespond(w, errors.New("invalid cursor"), nil, "Invalid MCP mutation query", http.StatusBadRequest, 0)
			return
		}
		filter.CreatedBefore = cursor.CreatedAt
		filter.BeforeID = cursor.ProposalID
	} else if raw := strings.TrimSpace(r.URL.Query().Get("created_before")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			handleErrorAndRespond(w, errors.New("invalid created_before"), nil, "Invalid MCP mutation query", http.StatusBadRequest, 0)
			return
		}
		filter.CreatedBefore = parsed.UTC()
	}
	rows, err := cdb.ListMCPMutationProposals(r.Context(), &dbHandler, filter)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation listing failed", mcpMutationErrorCode(err), 0)
		return
	}
	response := mcpMutationListResponse{Items: make([]mcpMutationProposalResponse, 0, len(rows))}
	for _, row := range rows {
		if err := validateStoredMCPMutationProposal(row); err != nil {
			handleErrorAndRespond(w, err, nil, "MCP mutation proposal store is corrupt", mcpMutationErrorCode(err), 0)
			return
		}
		if err := authorizeMCPMutationObservation(r.Context(), principal, identity, row); err != nil {
			continue
		}
		active, found, err := cdb.GetActiveMCPMutationApprovalByProposal(r.Context(), &dbHandler, row.ProposalSHA256, time.Now().UTC())
		if err != nil {
			handleErrorAndRespond(w, err, nil, "MCP mutation approval lookup failed", mcpMutationErrorCode(err), 0)
			return
		}
		var activePtr *cdb.MCPMutationApprovalRow
		if found {
			activePtr = &active
		}
		response.Items = append(response.Items, mcpMutationProposalResponseFromRow(row, activePtr, nil))
	}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		response.NextCursor = encodeMCPMutationCursor(last.CreatedAt, last.ProposalID)
	}
	handleErrorAndRespond(w, nil, response, "", 0, http.StatusOK)
}

func mcpMutationDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	proposalID := strings.TrimSpace(r.PathValue("proposal_id"))
	if proposalID == "" {
		handleErrorAndRespond(w, errors.New("proposal_id is required"), nil, "Invalid MCP mutation request", http.StatusBadRequest, 0)
		return
	}
	principal, identity, err := resolveMCPMutationPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation authentication failed", mcpMutationErrorCode(err), 0)
		return
	}
	row, found, err := cdb.GetMCPMutationProposal(r.Context(), &dbHandler, proposalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil, "MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	if err := validateStoredMCPMutationProposal(row); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation proposal store is corrupt", mcpMutationErrorCode(err), 0)
		return
	}
	if err := authorizeMCPMutationObservation(r.Context(), principal, identity, row); err != nil {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil,
			"MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	active, found, err := cdb.GetActiveMCPMutationApprovalByProposal(r.Context(), &dbHandler, row.ProposalSHA256, time.Now().UTC())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation approval lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	var activePtr *cdb.MCPMutationApprovalRow
	if found {
		activePtr = &active
	}
	history, err := cdb.ListMCPMutationApprovalsByProposal(r.Context(), &dbHandler, row.ProposalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation approval history failed", mcpMutationErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, mcpMutationProposalResponseFromRow(row, activePtr, history), "", 0, http.StatusOK)
}

func mcpMutationAttemptsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	principal, identity, err := resolveMCPMutationPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation authentication failed", mcpMutationErrorCode(err), 0)
		return
	}
	proposalID := strings.TrimSpace(r.PathValue("proposal_id"))
	proposal, found, err := cdb.GetMCPMutationProposal(r.Context(), &dbHandler, proposalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil, "MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	if err := validateStoredMCPMutationProposal(proposal); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation proposal store is corrupt", mcpMutationErrorCode(err), 0)
		return
	}
	if err := authorizeMCPMutationObservation(r.Context(), principal, identity, proposal); err != nil {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil,
			"MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{
		"items":            []any{},
		"dispatch_enabled": false,
	}, "", 0, http.StatusOK)
}

func decodeMCPMutationJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	return decodeJSON(r, dst)
}

// mcpMutationApprovalStoreFactory is the test seam for the single
// authoritative approval service boundary. Production always uses the durable
// store, whose Issue method performs current-policy revalidation.
var mcpMutationApprovalStoreFactory = agentpkg.NewDatabaseMCPMutationApprovalStore

func mcpMutationApproveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request mcpMutationApproveRequest
	if err := decodeMCPMutationJSON(w, r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid MCP mutation approval request", http.StatusBadRequest, 0)
		return
	}
	request.ProposalSHA256 = strings.TrimSpace(request.ProposalSHA256)
	request.Reason = mcpMutationSafeText(request.Reason, 256)
	proposalID := strings.TrimSpace(r.PathValue("proposal_id"))
	if proposalID == "" || request.ProposalSHA256 == "" || request.MaxExecutions < 0 || request.MaxExecutions > 1 {
		handleErrorAndRespond(w, errors.New("proposal_id, proposal_sha256 and max_executions <= 1 are required"),
			nil, "Invalid MCP mutation approval request", http.StatusBadRequest, 0)
		return
	}
	_, identity, err := resolveMCPMutationPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation authentication failed", mcpMutationErrorCode(err), 0)
		return
	}
	proposal, found, err := cdb.GetMCPMutationProposal(r.Context(), &dbHandler, proposalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil, "MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	if err := validateStoredMCPMutationProposal(proposal); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation proposal store is corrupt", mcpMutationErrorCode(err), 0)
		return
	}
	if request.IdentityVersion == 0 || proposal.ProposalSHA256 != request.ProposalSHA256 ||
		proposal.IdentityVersion != request.IdentityVersion {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalConflict, nil,
			"MCP mutation proposal content changed", http.StatusConflict, 0)
		return
	}
	approvalStore, err := mcpMutationApprovalStoreFactory(&dbHandler, time.Hour)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation approval store unavailable", mcpMutationErrorCode(err), 0)
		return
	}
	approvalStore.SetSnapshot(agentpkg.MCPMutationApprovalSnapshot{
		Policy:    agentpkg.CurrentMCPMutationPolicy(),
		Inventory: agentpkg.CurrentMCPOperatorConfig(),
		Agent:     agentpkg.CurrentAgentIdentityForMCPApproval,
	})
	approval, inserted, err := approvalStore.Approve(r.Context(), agentpkg.MCPMutationApprovalCommand{
		Approver: identity, ProposalID: proposalID, ProposalSHA256: request.ProposalSHA256,
		IdentityVersion: request.IdentityVersion, ExpiresAt: request.RequestedExpiry,
		MaxExecutions: request.MaxExecutions, Reason: request.Reason,
	})
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation approval failed", mcpMutationErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{
		"approval":        mcpMutationApprovalSummaryFromAgent(approval),
		"proposal_id":     approval.ProposalID,
		"proposal_sha256": approval.ProposalSHA256,
		"reused":          !inserted,
	}, "", 0, http.StatusOK)
}

func mcpMutationDenyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request mcpMutationDenyRequest
	if err := decodeMCPMutationJSON(w, r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid MCP mutation denial request", http.StatusBadRequest, 0)
		return
	}
	request.ProposalSHA256 = strings.TrimSpace(request.ProposalSHA256)
	request.Reason = mcpMutationSafeText(request.Reason, 256)
	proposalID := strings.TrimSpace(r.PathValue("proposal_id"))
	if proposalID == "" || request.ProposalSHA256 == "" || request.Reason == "" {
		handleErrorAndRespond(w, errors.New("proposal_id, proposal_sha256 and reason are required"),
			nil, "Invalid MCP mutation denial request", http.StatusBadRequest, 0)
		return
	}
	principal, identity, err := resolveMCPMutationPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation authentication failed", mcpMutationErrorCode(err), 0)
		return
	}
	proposal, found, err := cdb.GetMCPMutationProposal(r.Context(), &dbHandler, proposalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil, "MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	if err := validateStoredMCPMutationProposal(proposal); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation proposal store is corrupt", mcpMutationErrorCode(err), 0)
		return
	}
	if proposal.ProposalSHA256 != request.ProposalSHA256 {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalConflict, nil,
			"MCP mutation proposal content changed", http.StatusConflict, 0)
		return
	}
	if err := authorizeMCPMutationAdministration(r.Context(), principal, identity, proposal); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation denial is not authorized", mcpMutationErrorCode(err), 0)
		return
	}
	denied, transitioned, err := cdb.DenyMCPMutationProposal(r.Context(), &dbHandler, proposalID,
		principal.UserID, principal.Username, request.Reason, time.Now().UTC())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation denial failed", mcpMutationErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{
		"proposal_id": denied.ProposalID, "state": denied.State,
		"terminal_reason": denied.TerminalReason.String, "reused": !transitioned,
	}, "", 0, http.StatusOK)
}

func mcpMutationRevokeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request mcpMutationRevokeRequest
	if err := decodeMCPMutationJSON(w, r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid MCP mutation revocation request", http.StatusBadRequest, 0)
		return
	}
	request.ApprovalID = strings.TrimSpace(request.ApprovalID)
	request.ProposalSHA256 = strings.TrimSpace(request.ProposalSHA256)
	request.Reason = mcpMutationSafeText(request.Reason, 256)
	proposalID := strings.TrimSpace(r.PathValue("proposal_id"))
	if proposalID == "" || request.ApprovalID == "" || request.ProposalSHA256 == "" || request.Reason == "" {
		handleErrorAndRespond(w, errors.New("proposal_id, approval_id and proposal_sha256 are required"),
			nil, "Invalid MCP mutation revocation request", http.StatusBadRequest, 0)
		return
	}
	principal, identity, err := resolveMCPMutationPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation authentication failed", mcpMutationErrorCode(err), 0)
		return
	}
	proposal, found, err := cdb.GetMCPMutationProposal(r.Context(), &dbHandler, proposalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalNotFound, nil, "MCP mutation proposal not found", http.StatusNotFound, 0)
		return
	}
	if err := validateStoredMCPMutationProposal(proposal); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation proposal store is corrupt", mcpMutationErrorCode(err), 0)
		return
	}
	if proposal.ProposalSHA256 != request.ProposalSHA256 {
		handleErrorAndRespond(w, cdb.ErrMCPMutationProposalConflict, nil,
			"MCP mutation proposal content changed", http.StatusConflict, 0)
		return
	}
	if err := authorizeMCPMutationAdministration(r.Context(), principal, identity, proposal); err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation revocation is not authorized", mcpMutationErrorCode(err), 0)
		return
	}
	approvalRow, found, err := cdb.GetMCPMutationApproval(r.Context(), &dbHandler, request.ApprovalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation approval lookup failed", mcpMutationErrorCode(err), 0)
		return
	}
	if !found || approvalRow.ProposalID != proposalID || approvalRow.ProposalSHA256 != proposal.ProposalSHA256 {
		handleErrorAndRespond(w, cdb.ErrMCPMutationApprovalNotFound, nil,
			"MCP mutation approval not found", http.StatusNotFound, 0)
		return
	}
	approvalStore, err := mcpMutationApprovalStoreFactory(&dbHandler, time.Hour)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation approval store unavailable", mcpMutationErrorCode(err), 0)
		return
	}
	revoked, transitioned, err := approvalStore.Revoke(r.Context(), request.ApprovalID, principal, request.Reason)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "MCP mutation revocation failed", mcpMutationErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{
		"approval":    mcpMutationApprovalSummaryFromAgent(revoked),
		"proposal_id": proposalID,
		"revoked":     transitioned,
	}, "", 0, http.StatusOK)
}
