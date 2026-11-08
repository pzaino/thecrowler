package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	agentpkg "github.com/pzaino/thecrowler/pkg/agent"
	auth "github.com/pzaino/thecrowler/pkg/auth"
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
	case errors.Is(err, agentpkg.ErrCrawlApprovalUnknown):
		return http.StatusNotFound
	case errors.Is(err, agentpkg.ErrCrawlApprovalConflict),
		errors.Is(err, agentpkg.ErrCrawlApprovalRevoked):
		return http.StatusConflict
	case errors.Is(err, agentpkg.ErrCrawlApprovalStoreUnavailable),
		errors.Is(err, agentpkg.ErrCrawlApprovalApproverUnavailable):
		return http.StatusServiceUnavailable
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
	if request.OperationType == "" {
		request.OperationType = "crawl"
	}
	principal, _, err := resolveCrawlApprovalPrincipal(r.Context())
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval authentication failed", crawlApprovalErrorCode(err), 0)
		return
	}
	now := time.Now().UTC()
	proposal := agentpkg.CrawlRequestProposal{
		RequestID: request.RequestID, SourceID: request.SourceID,
		OperationType: request.OperationType, ExpiresAt: request.ProposalExpiresAt,
	}
	store := newDatabaseCrawlApprovalStore()
	approval, err := store.IssueCrawlApproval(r.Context(), agentpkg.CrawlApprovalIssuanceRequest{
		Proposal: proposal,
		Identity: agentpkg.AgentIdentity{
			AgentID: request.AgentID, Owner: principal.Username, TrustLevel: "trusted",
		},
		Principal: principal, RunID: request.RunID,
		IssuerPrincipal: principal, Approver: principal,
		Issuer: crawlApprovalIssuerName, ApprovedAt: now,
		ExpiresAt: request.ApprovalExpiresAt,
	})
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Crawl approval issuance failed", crawlApprovalErrorCode(err), 0)
		return
	}
	handleErrorAndRespond(w, nil, crawlApprovalResponseFromApproval(approval, request.RequestID, false), "", 0, http.StatusOK)
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
