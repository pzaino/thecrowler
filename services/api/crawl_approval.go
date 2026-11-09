package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	agentpkg "github.com/pzaino/thecrowler/pkg/agent"
	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const crawlApprovalIssuerName = "crowler-api"

var errCrawlApprovalAuthRequired = errors.New("crawl approval operations require enabled API authentication")

type crawlApprovalIssueRequest struct {
	RequestID         string    `json:"request_id"`
	SourceID          uint64    `json:"source_id"`
	AgentID           string    `json:"agent_id"`
	RunID             string    `json:"run_id"`
	OperationType     string    `json:"operation_type,omitempty"`
	ProposalExpiresAt time.Time `json:"proposal_expires_at"`
	ApprovalExpiresAt time.Time `json:"approval_expires_at"`
}

type crawlApprovalResponse struct {
	ApprovalID     string    `json:"approval_id"`
	ProposalSHA256 string    `json:"proposal_sha256"`
	RequestID      string    `json:"request_id"`
	SourceID       uint64    `json:"source_id"`
	AgentID        string    `json:"agent_id"`
	RunID          string    `json:"run_id"`
	OperationType  string    `json:"operation_type"`
	ApprovedBy     string    `json:"approved_by"`
	ApprovedAt     time.Time `json:"approved_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Revoked        bool      `json:"revoked"`
}

type crawlApprovalRevokeRequest struct {
	ApprovalID string `json:"approval_id"`
	Reason     string `json:"reason,omitempty"`
}

type crawlApprovalSubmitRequest struct {
	RequestID  string `json:"request_id"`
	ApprovalID string `json:"approval_id"`
}

func resolveCrawlApprovalPrincipal(ctx context.Context) (agentpkg.CrawlPrincipal, auth.Identity, error) {
	if !auth.Enabled(config.API.Auth) {
		return agentpkg.CrawlPrincipal{}, auth.Identity{}, errCrawlApprovalAuthRequired
	}
	identity, ok := auth.FromContext(ctx)
	if !ok {
		return agentpkg.CrawlPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, dbHandler, identity)
	if err != nil {
		return agentpkg.CrawlPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	principal, err := agentpkg.CrawlPrincipalFromAuthIdentity(resolved)
	if err != nil {
		return agentpkg.CrawlPrincipal{}, auth.Identity{}, errSourceUnauthenticated
	}
	return principal, resolved, nil
}

func newDatabaseCrawlApprovalStore() *agentpkg.DatabaseCrawlApprovalStore {
	authorizer := agentpkg.DatabaseCrawlSourceAuthorizer{
		Lookup:     agentpkg.DatabaseSourceOwnerLookup{DB: &dbHandler},
		AllowAdmin: true,
	}
	return &agentpkg.DatabaseCrawlApprovalStore{
		DB:               &dbHandler,
		SourceAuthorizer: authorizer,
		Issuer:           crawlApprovalIssuerName,
		MaxLifetime:      24 * time.Hour,
	}
}

func crawlApprovalErrorCode(err error) int {
	switch {
	case errors.Is(err, errCrawlApprovalAuthRequired), errors.Is(err, errSourceUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, errSourceForbidden), errors.Is(err, errSourceOwnerUnknown),
		errors.Is(err, agentpkg.ErrCrawlApprovalApproverUnauthorized),
		errors.Is(err, agentpkg.ErrCrawlApprovalIssuerUntrusted):
		return http.StatusForbidden
	case errors.Is(err, agentpkg.ErrCrawlApprovalLifetime),
		errors.Is(err, agentpkg.ErrCrawlApprovalForged):
		return http.StatusBadRequest
	case errors.Is(err, agentpkg.ErrCrawlApprovalUnknown), errors.Is(err, agentpkg.ErrCrawlProposalNotFound):
		return http.StatusNotFound
	case errors.Is(err, agentpkg.ErrCrawlProposalExpired):
		return http.StatusGone
	case errors.Is(err, agentpkg.ErrCrawlSubmissionDenied), errors.Is(err, agentpkg.ErrCrawlApprovalForged):
		return http.StatusForbidden
	case errors.Is(err, agentpkg.ErrCrawlApprovalConflict),
		errors.Is(err, agentpkg.ErrCrawlApprovalRevoked),
		errors.Is(err, agentpkg.ErrCrawlProposalConflict):
		return http.StatusConflict
	case errors.Is(err, agentpkg.ErrCrawlApprovalStoreUnavailable),
		errors.Is(err, agentpkg.ErrCrawlApprovalApproverUnavailable),
		errors.Is(err, agentpkg.ErrCrawlProposalStoreUnavailable),
		errors.Is(err, agentpkg.ErrCrawlMutationUnsupported):
		return http.StatusServiceUnavailable
	case errors.Is(err, agentpkg.ErrCrawlMutationDisabled),
		errors.Is(err, agentpkg.ErrCrawlMutationOperationRequired),
		errors.Is(err, agentpkg.ErrCrawlMutationSourceScopeRequired),
		errors.Is(err, agentpkg.ErrCrawlMutationQuotaRequired),
		errors.Is(err, agentpkg.ErrCrawlPrincipalUnavailable):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func crawlApprovalIssueHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request crawlApprovalIssueRequest
	if err := decodeJSON(r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid crawl approval request", http.StatusBadRequest, 0)
		return
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.RunID = strings.TrimSpace(request.RunID)
	request.OperationType = strings.TrimSpace(request.OperationType)
	if request.RequestID == "" {
		handleErrorAndRespond(w, errors.New("request_id is required"), nil, "Invalid crawl approval request", http.StatusBadRequest, 0)
		return
	}
	principal, _, err := resolveCrawlApprovalPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval authentication failed", crawlApprovalErrorCode(err), 0)
		return
	}
	row, found, err := cdb.GetCrawlProposalByRequest(r.Context(), &dbHandler, request.RequestID)
	if err != nil {
		handleErrorAndRespond(w, agentpkg.ErrCrawlProposalStoreUnavailable, nil, "Crawl proposal lookup failed", http.StatusServiceUnavailable, 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, agentpkg.ErrCrawlProposalNotFound, nil, "Crawl proposal not found", http.StatusNotFound, 0)
		return
	}
	if err := validateCrawlApprovalIssueAgainstProposal(request, row, time.Now().UTC()); err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval does not match the recorded proposal", http.StatusBadRequest, 0)
		return
	}
	proposal := agentpkg.CrawlRequestProposal{
		RequestID: row.RequestID, SourceID: row.SourceID,
		OperationType: row.OperationType, ExpiresAt: row.ExpiresAt.UTC(),
	}
	identity := agentpkg.AgentIdentity{
		AgentID: row.AgentID, Owner: row.Owner, TrustLevel: "trusted",
	}
	proposalPrincipal := agentpkg.CrawlPrincipal{
		UserID: row.PrincipalUserID, Username: row.Owner, Issuer: crawlApprovalIssuerName,
	}
	approvalExpiry := request.ApprovalExpiresAt.UTC()
	now := time.Now().UTC()
	if approvalExpiry.IsZero() {
		approvalExpiry = now.Add(time.Hour)
		if row.ExpiresAt.Before(approvalExpiry) {
			approvalExpiry = row.ExpiresAt.UTC()
		}
	}
	store := newDatabaseCrawlApprovalStore()
	approval, err := store.IssueCrawlApproval(r.Context(), agentpkg.CrawlApprovalIssuanceRequest{
		Proposal: proposal, Identity: identity,
		Principal: proposalPrincipal, RunID: row.RunID,
		IssuerPrincipal: principal, Approver: principal,
		Issuer: crawlApprovalIssuerName, ApprovedAt: now, ExpiresAt: approvalExpiry,
	})
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval issuance failed", crawlApprovalErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, crawlApprovalResponseFromApproval(approval, row.RequestID, false), "", 0, http.StatusOK)
}

func validateCrawlApprovalIssueAgainstProposal(request crawlApprovalIssueRequest, row cdb.CrawlProposalRow, now time.Time) error {
	if row.State != cdb.CrawlProposalStatePending {
		return agentpkg.ErrCrawlProposalConflict
	}
	if !row.ExpiresAt.After(now) {
		return agentpkg.ErrCrawlProposalExpired
	}
	expected := agentpkg.CrawlRequestProposal{
		RequestID: row.RequestID, SourceID: row.SourceID,
		OperationType: row.OperationType, ExpiresAt: row.ExpiresAt.UTC().Truncate(time.Microsecond),
	}.Fingerprint()
	if row.ProposalSHA256 != expected {
		return agentpkg.ErrCrawlProposalConflict
	}
	if request.SourceID != 0 && request.SourceID != row.SourceID {
		return agentpkg.ErrCrawlProposalConflict
	}
	if request.AgentID != "" && request.AgentID != row.AgentID {
		return agentpkg.ErrCrawlProposalConflict
	}
	if request.RunID != "" && request.RunID != row.RunID {
		return agentpkg.ErrCrawlProposalConflict
	}
	if request.OperationType != "" && request.OperationType != row.OperationType {
		return agentpkg.ErrCrawlProposalConflict
	}
	return nil
}

func crawlApprovalSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request crawlApprovalSubmitRequest
	if err := decodeJSON(r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid crawl submission request", http.StatusBadRequest, 0)
		return
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.ApprovalID = strings.TrimSpace(request.ApprovalID)
	if request.RequestID == "" || request.ApprovalID == "" {
		handleErrorAndRespond(w, errors.New("request_id and approval_id are required"), nil, "Invalid crawl submission request", http.StatusBadRequest, 0)
		return
	}
	principal, _, err := resolveCrawlApprovalPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl submission authentication failed", crawlApprovalErrorCode(err), 0)
		return
	}
	row, found, err := cdb.GetCrawlProposalByRequest(r.Context(), &dbHandler, request.RequestID)
	if err != nil {
		handleErrorAndRespond(w, agentpkg.ErrCrawlProposalStoreUnavailable, nil, "Crawl proposal lookup failed", http.StatusServiceUnavailable, 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, agentpkg.ErrCrawlProposalNotFound, nil, "Crawl proposal not found", http.StatusNotFound, 0)
		return
	}
	runtime, err := agentpkg.BuildCrawlRequestToolRuntime(r.Context(), agentpkg.CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: dbHandler,
		Identity:          agentpkg.AgentIdentity{AgentID: row.AgentID, Owner: row.Owner, TrustLevel: "trusted"},
		AllowedOperations: []string{agentpkg.CrawlRequestOperationName},
		AllowedSources:    []uint64{row.SourceID},
		MaxRequests:       row.MaxRequests,
		BudgetWindow:      time.Duration(row.BudgetWindowSeconds) * time.Second,
		Issuer:            crawlApprovalIssuerName,
		ProposalTTL:       time.Minute,
		SourceAuthorizer: agentpkg.DatabaseCrawlSourceAuthorizer{
			Lookup:     agentpkg.DatabaseSourceOwnerLookup{DB: &dbHandler},
			AllowAdmin: true,
		},
	})
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl submission authorization unavailable", http.StatusServiceUnavailable, 0)
		return
	}
	result, err := runtime.SubmitApprovedProposal(r.Context(), request.RequestID, request.ApprovalID, principal)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl submission failed", crawlApprovalErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{
		"request_id":   result.RequestID,
		"operation_id": result.OperationID,
		"state":        result.State,
		"reason_code":  result.ReasonCode,
		"source_state": result.SourceState,
		"reused":       result.Reused,
		"observed_at":  result.SourceObservedAt,
		"expires_at":   result.ExpiresAt,
	}, "", 0, http.StatusOK)
}

func crawlApprovalRevokeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request crawlApprovalRevokeRequest
	if err := decodeJSON(r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid crawl approval revocation request", http.StatusBadRequest, 0)
		return
	}
	request.ApprovalID = strings.TrimSpace(request.ApprovalID)
	request.Reason = strings.TrimSpace(request.Reason)
	if len(request.Reason) > 256 {
		handleErrorAndRespond(w, errors.New("reason is too long"), nil, "Invalid crawl approval revocation request", http.StatusBadRequest, 0)
		return
	}
	principal, _, err := resolveCrawlApprovalPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval authentication failed", crawlApprovalErrorCode(err), 0)
		return
	}
	store := newDatabaseCrawlApprovalStore()
	if err := store.RevokeCrawlApproval(r.Context(), principal, request.ApprovalID, request.Reason, time.Now().UTC()); err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval revocation failed", crawlApprovalErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{"approval_id": request.ApprovalID, "revoked": true}, "", 0, http.StatusOK)
}

func crawlApprovalStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	approvalID := strings.TrimSpace(r.URL.Query().Get("approval_id"))
	if approvalID == "" {
		handleErrorAndRespond(w, errors.New("approval_id is required"), nil, "Invalid crawl approval status request", http.StatusBadRequest, 0)
		return
	}
	principal, _, err := resolveCrawlApprovalPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval authentication failed", crawlApprovalErrorCode(err), 0)
		return
	}
	store := newDatabaseCrawlApprovalStore()
	record, found, err := store.LookupCrawlApprovalRaw(r.Context(), approvalID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval lookup failed", crawlApprovalErrorCode(err), 0)
		return
	}
	if !found {
		handleErrorAndRespond(w, agentpkg.ErrCrawlApprovalUnknown, nil, "Crawl approval not found", http.StatusNotFound, 0)
		return
	}
	if err := store.SourceAuthorizer.AuthorizeCrawlSource(r.Context(), principal, record.Approval.SourceID); err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval is not visible to this principal", http.StatusForbidden, 0)
		return
	}
	handleErrorAndRespond(w, nil, crawlApprovalResponseFromApproval(record.Approval, record.Proposal.RequestID, record.Revoked), "", 0, http.StatusOK)
}

func crawlApprovalResponseFromApproval(approval agentpkg.CrawlRequestApproval, requestID string, revoked bool) crawlApprovalResponse {
	return crawlApprovalResponse{
		ApprovalID: approval.ApprovalID, ProposalSHA256: approval.ProposalSHA256,
		RequestID: requestID, SourceID: approval.SourceID, AgentID: approval.AgentID,
		RunID: approval.RunID, OperationType: approval.OperationType,
		ApprovedBy: approval.ApprovedBy, ApprovedAt: approval.ApprovedAt,
		ExpiresAt: approval.ExpiresAt, Revoked: revoked,
	}
}
