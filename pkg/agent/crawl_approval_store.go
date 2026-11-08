package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// DatabaseCrawlApprovalStore is the production-shaped durable approval
// adapter. It implements issuance, lookup and revocation against the
// CrawlApprovals table and uses the trusted source authorizer for operator
// permission checks.
type DatabaseCrawlApprovalStore struct {
	DB               *cdb.Handler
	SourceAuthorizer CrawlSourceAuthorizer
	Issuer           string
	MaxLifetime      time.Duration
}

func newCrawlApprovalID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *DatabaseCrawlApprovalStore) maxLifetime() time.Duration {
	if s.MaxLifetime <= 0 || s.MaxLifetime > maxCrawlApprovalLifetime {
		return maxCrawlApprovalLifetime
	}
	return s.MaxLifetime
}

// IssueCrawlApproval validates and durably persists one operator approval.
func (s *DatabaseCrawlApprovalStore) IssueCrawlApproval(ctx context.Context, request CrawlApprovalIssuanceRequest) (CrawlRequestApproval, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return CrawlRequestApproval{}, err
	}
	if s.DB == nil || *s.DB == nil || s.SourceAuthorizer == nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalStoreUnavailable
	}
	if s.Issuer == "" || request.Issuer != s.Issuer {
		return CrawlRequestApproval{}, ErrCrawlApprovalIssuerUntrusted
	}
	if err := request.IssuerPrincipal.Validate(); err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	if err := request.Approver.Validate(); err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	if err := request.Principal.Validate(); err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	if strings.TrimSpace(request.Identity.AgentID) == "" ||
		strings.TrimSpace(request.Identity.Owner) == "" ||
		strings.TrimSpace(request.RunID) == "" {
		return CrawlRequestApproval{}, ErrCrawlApprovalForged
	}
	if request.Principal.Username != request.Identity.Owner {
		return CrawlRequestApproval{}, ErrCrawlApprovalForged
	}
	principal, err := s.enrichPrincipal(ctx, request.Principal)
	if err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	request.Principal = principal
	approver, err := s.enrichPrincipal(ctx, request.Approver)
	if err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	request.Approver = approver
	issuerPrincipal, err := s.enrichPrincipal(ctx, request.IssuerPrincipal)
	if err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	request.IssuerPrincipal = issuerPrincipal
	if request.Principal.Username != request.Identity.Owner {
		return CrawlRequestApproval{}, ErrCrawlApprovalForged
	}
	proposal := request.Proposal
	if err := validateCrawlProposal(proposal, request.ApprovedAt); err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalForged
	}
	if proposal.SourceID == 0 || proposal.OperationType != cdb.CrawlOperationType {
		return CrawlRequestApproval{}, ErrCrawlApprovalForged
	}
	if request.ApprovedAt.IsZero() || request.ApprovedAt.After(time.Now().UTC().Add(time.Minute)) {
		return CrawlRequestApproval{}, ErrCrawlApprovalLifetime
	}
	if !proposal.ExpiresAt.After(request.ApprovedAt) {
		return CrawlRequestApproval{}, ErrCrawlApprovalForged
	}
	if request.ExpiresAt.IsZero() || !request.ExpiresAt.After(request.ApprovedAt) ||
		request.ExpiresAt.Sub(request.ApprovedAt) > s.maxLifetime() ||
		!request.ExpiresAt.After(time.Now().UTC()) {
		return CrawlRequestApproval{}, ErrCrawlApprovalLifetime
	}
	if err := s.SourceAuthorizer.AuthorizeCrawlSource(ctx, request.Approver, proposal.SourceID); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return CrawlRequestApproval{}, ctxErr
		}
		return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
	}
	if request.IssuerPrincipal.UserID != request.Approver.UserID {
		if err := s.SourceAuthorizer.AuthorizeCrawlSource(ctx, request.IssuerPrincipal, proposal.SourceID); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return CrawlRequestApproval{}, ctxErr
			}
			return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
		}
	}

	approvalID, err := newCrawlApprovalID()
	if err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalStoreUnavailable
	}
	approvedAt := request.ApprovedAt.UTC().Truncate(time.Microsecond)
	expiresAt := request.ExpiresAt.UTC().Truncate(time.Microsecond)
	approval := CrawlRequestApproval{
		ApprovalID:     approvalID,
		ProposalSHA256: proposal.Fingerprint(),
		RunID:          request.RunID,
		AgentID:        request.Identity.AgentID,
		Owner:          request.Identity.Owner,
		SourceID:       proposal.SourceID,
		OperationType:  proposal.OperationType,
		Issuer:         request.Issuer,
		ApprovedBy:     request.Approver.Username,
		ApproverUserID: request.Approver.UserID,
		ApprovedAt:     approvedAt,
		ExpiresAt:      expiresAt,
	}
	row := cdb.CrawlApprovalRow{
		ApprovalID:        approval.ApprovalID,
		ProposalSHA256:    approval.ProposalSHA256,
		RequestID:         proposal.RequestID,
		RunID:             approval.RunID,
		AgentID:           approval.AgentID,
		Owner:             approval.Owner,
		PrincipalUserID:   request.Principal.UserID,
		SourceID:          approval.SourceID,
		OperationType:     approval.OperationType,
		Issuer:            approval.Issuer,
		IssuedByUserID:    request.IssuerPrincipal.UserID,
		IssuedBy:          request.IssuerPrincipal.Username,
		IssuedByIssuer:    request.IssuerPrincipal.Issuer,
		ApprovedByUserID:  request.Approver.UserID,
		ApprovedBy:        request.Approver.Username,
		ApprovedByIssuer:  request.Approver.Issuer,
		ApprovalSHA256:    approval.Fingerprint(),
		ProposalExpiresAt: proposal.ExpiresAt.UTC().Truncate(time.Microsecond),
		ApprovedAt:        approval.ApprovedAt,
		ExpiresAt:         approval.ExpiresAt,
	}
	inserted, err := cdb.InsertCrawlApprovalAuthorized(ctx, s.DB, row)
	if err != nil {
		if errors.Is(err, cdb.ErrSourceMutationNotFound) ||
			errors.Is(err, cdb.ErrSourceMutationDeleted) ||
			errors.Is(err, cdb.ErrSourceMutationUnverified) ||
			errors.Is(err, cdb.ErrSourceMutationForbidden) {
			return CrawlRequestApproval{}, ErrCrawlApprovalApproverUnauthorized
		}
		return CrawlRequestApproval{}, ErrCrawlApprovalStoreUnavailable
	}
	if inserted {
		return approval, nil
	}

	existing, found, err := cdb.GetCrawlApprovalByRequest(ctx, s.DB, request.RunID, proposal.RequestID,
		request.Principal.UserID, proposal.SourceID, proposal.OperationType)
	if err != nil {
		return CrawlRequestApproval{}, ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return CrawlRequestApproval{}, ErrCrawlApprovalStoreUnavailable
	}
	if existing.ProposalSHA256 != approval.ProposalSHA256 ||
		existing.RunID != approval.RunID ||
		existing.AgentID != approval.AgentID ||
		existing.Owner != approval.Owner ||
		existing.PrincipalUserID != request.Principal.UserID ||
		existing.SourceID != approval.SourceID ||
		existing.OperationType != approval.OperationType ||
		existing.Issuer != approval.Issuer ||
		existing.IssuedByUserID != request.IssuerPrincipal.UserID ||
		existing.ApprovedByUserID != request.Approver.UserID {
		return CrawlRequestApproval{}, ErrCrawlApprovalConflict
	}
	if existing.RevokedAt.Valid {
		return CrawlRequestApproval{}, ErrCrawlApprovalRevoked
	}
	return approvalFromRow(existing), nil
}

// LookupCrawlApproval returns the authoritative approval by approval ID.
func (s *DatabaseCrawlApprovalStore) LookupCrawlApproval(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return CrawlApprovalRecord{}, false, ErrCrawlApprovalStoreUnavailable
	}
	row, found, err := cdb.GetCrawlApprovalByID(ctx, s.DB, approvalID)
	if err != nil {
		return CrawlApprovalRecord{}, false, ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return CrawlApprovalRecord{}, false, nil
	}
	record := approvalRecordFromRow(row)
	if err := s.enrichApprover(ctx, &record); err != nil {
		return CrawlApprovalRecord{}, false, err
	}
	s.enrichIssuer(ctx, &record)
	return record, true, nil
}

// LookupCrawlApprovalRaw returns the stored approval without re-resolving the
// operator's current account state. Operators may inspect a historical
// approval even after the approver account is disabled.
func (s *DatabaseCrawlApprovalStore) LookupCrawlApprovalRaw(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return CrawlApprovalRecord{}, false, ErrCrawlApprovalStoreUnavailable
	}
	row, found, err := cdb.GetCrawlApprovalByID(ctx, s.DB, approvalID)
	if err != nil {
		return CrawlApprovalRecord{}, false, ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return CrawlApprovalRecord{}, false, nil
	}
	return approvalRecordFromRow(row), true, nil
}

// LookupCrawlApprovalByRequest returns the durable approval for one logical
// request. This is the restart-safe proposal anchor after approval.
func (s *DatabaseCrawlApprovalStore) LookupCrawlApprovalByRequest(ctx context.Context, runID, requestID string, principalUserID, sourceID uint64, operationType string) (CrawlApprovalRecord, bool, error) {
	if s == nil || s.DB == nil || *s.DB == nil {
		return CrawlApprovalRecord{}, false, ErrCrawlApprovalStoreUnavailable
	}
	row, found, err := cdb.GetCrawlApprovalByRequest(ctx, s.DB, runID, requestID, principalUserID, sourceID, operationType)
	if err != nil {
		return CrawlApprovalRecord{}, false, ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return CrawlApprovalRecord{}, false, nil
	}
	record := approvalRecordFromRow(row)
	if err := s.enrichApprover(ctx, &record); err != nil {
		return CrawlApprovalRecord{}, false, err
	}
	s.enrichIssuer(ctx, &record)
	return record, true, nil
}

// RevokeCrawlApproval persists revocation after checking the principal's
// source authority. Repeating a revocation is idempotent.
func (s *DatabaseCrawlApprovalStore) RevokeCrawlApproval(ctx context.Context, principal CrawlPrincipal, approvalID, reason string, revokedAt time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := principal.Validate(); err != nil {
		return ErrCrawlApprovalApproverUnauthorized
	}
	row, found, err := cdb.GetCrawlApprovalByID(ctx, s.DB, approvalID)
	if err != nil {
		return ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return ErrCrawlApprovalUnknown
	}
	record := approvalRecordFromRow(row)
	if record.Revoked {
		return nil
	}
	if s.SourceAuthorizer == nil {
		return ErrCrawlApprovalApproverUnavailable
	}
	if err := s.SourceAuthorizer.AuthorizeCrawlSource(ctx, principal, record.Approval.SourceID); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, ErrCrawlSourceOwnershipMissing) || errors.Is(err, ErrCrawlSourceForbidden) {
			return ErrCrawlApprovalApproverUnauthorized
		}
		return ErrCrawlApprovalApproverUnavailable
	}
	revoked, exists, err := cdb.RevokeCrawlApproval(ctx, s.DB, approvalID, principal.UserID, principal.Username, reason, revokedAt)
	if err != nil {
		return ErrCrawlApprovalStoreUnavailable
	}
	if !exists {
		return ErrCrawlApprovalUnknown
	}
	_ = revoked
	return nil
}

func approvalFromRow(row cdb.CrawlApprovalRow) CrawlRequestApproval {
	return CrawlRequestApproval{
		ApprovalID:     row.ApprovalID,
		ProposalSHA256: row.ProposalSHA256,
		RunID:          row.RunID,
		AgentID:        row.AgentID,
		Owner:          row.Owner,
		SourceID:       row.SourceID,
		OperationType:  row.OperationType,
		Issuer:         row.Issuer,
		ApprovedBy:     row.ApprovedBy,
		ApproverUserID: row.ApprovedByUserID,
		ApprovedAt:     row.ApprovedAt.UTC(),
		ExpiresAt:      row.ExpiresAt.UTC(),
	}
}

func (s *DatabaseCrawlApprovalStore) enrichApprover(ctx context.Context, record *CrawlApprovalRecord) error {
	principal, err := s.enrichPrincipal(ctx, record.ApproverPrincipal)
	if err != nil {
		return ErrCrawlApprovalApproverUnauthorized
	}
	record.ApproverPrincipal = principal
	return nil
}

func (s *DatabaseCrawlApprovalStore) enrichIssuer(ctx context.Context, record *CrawlApprovalRecord) {
	if principal, err := s.enrichPrincipal(ctx, record.IssuerPrincipal); err == nil {
		record.IssuerPrincipal = principal
	}
}

func (s *DatabaseCrawlApprovalStore) enrichPrincipal(ctx context.Context, principal CrawlPrincipal) (CrawlPrincipal, error) {
	if principal.UserID == 0 {
		return principal, nil
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, *s.DB, auth.Identity{
		Subject:  strconv.FormatUint(principal.UserID, 10),
		Username: principal.Username, Issuer: principal.Issuer,
	})
	if err != nil {
		return CrawlPrincipal{}, err
	}
	principal.Username = resolved.Username
	principal.Issuer = resolved.Issuer
	principal.Roles = append([]string(nil), resolved.Roles...)
	principal.Scopes = append([]string(nil), resolved.Scopes...)
	return principal, nil
}

func approvalRecordFromRow(row cdb.CrawlApprovalRow) CrawlApprovalRecord {
	record := CrawlApprovalRecord{
		Approval: approvalFromRow(row),
		Proposal: CrawlRequestProposal{
			RequestID: row.RequestID, SourceID: row.SourceID,
			OperationType: row.OperationType, ExpiresAt: row.ProposalExpiresAt.UTC(),
		},
		IssuerPrincipal: CrawlPrincipal{
			UserID: row.IssuedByUserID, Username: row.IssuedBy, Issuer: row.IssuedByIssuer,
		},
		ApproverPrincipal: CrawlPrincipal{
			UserID: row.ApprovedByUserID, Username: row.ApprovedBy, Issuer: row.ApprovedByIssuer,
		},
		Revoked: row.RevokedAt.Valid,
	}
	if row.RevokedAt.Valid {
		revokedAt := row.RevokedAt.Time.UTC()
		record.RevokedAt = &revokedAt
	}
	if row.RevokedByUserID.Valid {
		record.RevokedByUserID = uint64(row.RevokedByUserID.Int64)
	}
	if row.RevokedBy.Valid {
		record.RevokedBy = row.RevokedBy.String
	}
	if row.RevocationReason.Valid {
		record.RevocationReason = row.RevocationReason.String
	}
	return record
}

var _ CrawlApprovalStore = (*DatabaseCrawlApprovalStore)(nil)
var _ CrawlApprovalIssuer = (*DatabaseCrawlApprovalStore)(nil)
var _ CrawlApprovalRevoker = (*DatabaseCrawlApprovalStore)(nil)
