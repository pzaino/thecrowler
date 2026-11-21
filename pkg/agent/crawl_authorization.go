package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// CrawlPrincipal is an authenticated operator identity supplied by the trusted
// runtime. It is never populated from model arguments. Username must match the
// agent owner for agent-originated requests; the agent cannot choose its own
// principal by changing request parameters.
type CrawlPrincipal struct {
	UserID   uint64
	Username string
	Issuer   string
	Roles    []string
	Scopes   []string
}

// Validate reports whether the principal carries enough trusted identity to
// participate in authorization.
func (p CrawlPrincipal) Validate() error {
	if p.UserID == 0 {
		return fmt.Errorf("principal user ID is required")
	}
	if err := validateCrawlRequestField("principal username", p.Username); err != nil {
		return err
	}
	if err := validateCrawlRequestField("principal issuer", p.Issuer); err != nil {
		return err
	}
	return nil
}

// HasRole reports whether the principal carries one of the exact roles.
func (p CrawlPrincipal) HasRole(roles ...string) bool {
	for _, held := range p.Roles {
		held = strings.ToLower(strings.TrimSpace(held))
		for _, wanted := range roles {
			if held == strings.ToLower(strings.TrimSpace(wanted)) {
				return true
			}
		}
	}
	return false
}

// HasScope reports whether the principal carries one of the exact scopes.
func (p CrawlPrincipal) HasScope(scopes ...string) bool {
	for _, held := range p.Scopes {
		held = strings.TrimSpace(held)
		for _, wanted := range scopes {
			if held == strings.TrimSpace(wanted) || held == "*" {
				return true
			}
		}
	}
	return false
}

// IsSourceAdministrator reports whether the principal has an established
// administrative role that permits managing any source.
func (p CrawlPrincipal) IsSourceAdministrator() bool {
	return p.HasRole("superadmin", "admin")
}

// CrawlPrincipalFromAuthIdentity adapts the authenticated HTTP identity to the
// crawl authorization principal. The auth subject must be the numeric users
// row identifier used by the repository's local authentication model.
func CrawlPrincipalFromAuthIdentity(identity auth.Identity) (CrawlPrincipal, error) {
	userID, err := strconv.ParseUint(strings.TrimSpace(identity.Subject), 10, 64)
	if err != nil || userID == 0 {
		return CrawlPrincipal{}, fmt.Errorf("authenticated subject is not a valid user ID")
	}
	principal := CrawlPrincipal{
		UserID:   userID,
		Username: strings.TrimSpace(identity.Username),
		Issuer:   strings.TrimSpace(identity.Issuer),
		Roles:    append([]string(nil), identity.Roles...),
		Scopes:   append([]string(nil), identity.Scopes...),
	}
	if err := principal.Validate(); err != nil {
		return CrawlPrincipal{}, err
	}
	return principal, nil
}

var (
	// ErrCrawlSourceOwnershipMissing means the source has no unambiguous owner.
	ErrCrawlSourceOwnershipMissing = errors.New("source ownership information is missing")
	// ErrCrawlSourceForbidden means the principal is not the source owner.
	ErrCrawlSourceForbidden = errors.New("principal is not authorized for the source")
	// ErrCrawlSourceAuthorizationUnavailable means the ownership check could not
	// be completed. It must never be interpreted as a permission grant.
	ErrCrawlSourceAuthorizationUnavailable = errors.New("source authorization is unavailable")
)

// CrawlSourceAuthorizer is the trusted source-ownership boundary. A nil
// authorizer fails closed. Implementations must use trusted principal data and
// authoritative ownership data, never model arguments.
type CrawlSourceAuthorizer interface {
	AuthorizeCrawlSource(ctx context.Context, principal CrawlPrincipal, sourceID uint64) error
}

// CrawlSourceOwnerLookup is the narrow database adapter used by the default
// source authorizer.
type CrawlSourceOwnerLookup interface {
	LookupCrawlSourceOwner(ctx context.Context, sourceID uint64) (cdb.SourceOwner, error)
}

// DatabaseSourceOwnerLookup reads verified source ownership from the v1.17
// SourceOwnership provenance table. A legacy nonzero Sources.usr_id without a
// matching provenance row is treated as unknown ownership.
type DatabaseSourceOwnerLookup struct {
	DB *cdb.Handler
}

// LookupCrawlSourceOwner implements CrawlSourceOwnerLookup.
func (l DatabaseSourceOwnerLookup) LookupCrawlSourceOwner(ctx context.Context, sourceID uint64) (cdb.SourceOwner, error) {
	return cdb.GetVerifiedSourceOwner(ctx, l.DB, sourceID)
}

// DatabaseCrawlSourceAuthorizer enforces source ownership using verified
// provenance returned by its lookup. Missing, zero, legacy and unknown
// ownership fail closed.
type DatabaseCrawlSourceAuthorizer struct {
	Lookup     CrawlSourceOwnerLookup
	AllowAdmin bool
}

// AuthorizeCrawlSource implements CrawlSourceAuthorizer.
func (a DatabaseCrawlSourceAuthorizer) AuthorizeCrawlSource(ctx context.Context, principal CrawlPrincipal, sourceID uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := principal.Validate(); err != nil {
		return ErrCrawlSourceAuthorizationUnavailable
	}
	if a.Lookup == nil {
		return ErrCrawlSourceAuthorizationUnavailable
	}
	owner, err := a.Lookup.LookupCrawlSourceOwner(ctx, sourceID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrCrawlSourceAuthorizationUnavailable
	}
	if !owner.Found || owner.UserID == 0 {
		return ErrCrawlSourceOwnershipMissing
	}
	if !owner.Verified && !(a.AllowAdmin && principal.IsSourceAdministrator()) {
		return ErrCrawlSourceOwnershipMissing
	}
	if owner.UserID != principal.UserID && !(a.AllowAdmin && principal.IsSourceAdministrator()) {
		return ErrCrawlSourceForbidden
	}
	return nil
}

var (
	// ErrCrawlApprovalUnknown means the approval ID is not known to the store.
	ErrCrawlApprovalUnknown = errors.New("crawl approval is unknown to the trusted authority")
	// ErrCrawlApprovalForged means the presented approval does not match the
	// authoritative record.
	ErrCrawlApprovalForged = errors.New("crawl approval does not match the trusted record")
	// ErrCrawlApprovalIssuerUntrusted means the approval issuer is not trusted.
	ErrCrawlApprovalIssuerUntrusted = errors.New("crawl approval issuer is not trusted")
	// ErrCrawlApprovalApproverUnauthorized means the approver is not authorized
	// for the approved source.
	ErrCrawlApprovalApproverUnauthorized = errors.New("crawl approval approver is not authorized for the source")
	// ErrCrawlApprovalApproverUnavailable means the approver authority could not
	// be checked.
	ErrCrawlApprovalApproverUnavailable = errors.New("crawl approval approver authorization is unavailable")
	// ErrCrawlApprovalStoreUnavailable means the authoritative store could not
	// be read. It must never be interpreted as an approval.
	ErrCrawlApprovalStoreUnavailable = errors.New("crawl approval store is unavailable")
	// ErrCrawlApprovalLifetime means the approval has an invalid or oversized
	// issuance-to-expiry interval.
	ErrCrawlApprovalLifetime = errors.New("crawl approval lifetime is invalid")
	// ErrCrawlApprovalConflict means a different logical request already owns
	// the same durable approval key.
	ErrCrawlApprovalConflict = errors.New("crawl approval conflicts with an existing logical request")
)

// CrawlApprovalRecord is an authoritative approval record returned by a
// trusted approval store. Approval is the record as issued; ApproverPrincipal
// is the authenticated operator who issued it.
type CrawlApprovalRecord struct {
	Approval          CrawlRequestApproval
	Proposal          CrawlRequestProposal
	PrincipalUserID   uint64
	IssuerPrincipal   CrawlPrincipal
	ApproverPrincipal CrawlPrincipal
	Revoked           bool
	RevokedAt         *time.Time
	RevokedByUserID   uint64
	RevokedBy         string
	RevocationReason  string
}

// CrawlApprovalStore is the authoritative lookup boundary. It must return only
// records issued by a trusted authority. It is deliberately separate from
// issuance and revocation.
type CrawlApprovalStore interface {
	LookupCrawlApproval(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error)
	LookupCrawlApprovalByRequest(ctx context.Context, runID, requestID string, principalUserID, sourceID uint64, operationType string) (CrawlApprovalRecord, bool, error)
}

// CrawlApprovalIssuanceRequest is the trusted input to an approval issuer. The
// issuer must validate the approver principal and source authority, assign an
// approval ID, persist the record durably and return the issued approval.
type CrawlApprovalIssuanceRequest struct {
	Proposal        CrawlRequestProposal
	Identity        AgentIdentity
	Principal       CrawlPrincipal
	RunID           string
	IssuerPrincipal CrawlPrincipal
	Approver        CrawlPrincipal
	Issuer          string
	ApprovedAt      time.Time
	ExpiresAt       time.Time
}

// CrawlApprovalIssuer is the trusted issuance boundary. No production
// implementation is provided because CROWler has no durable approval store or
// operator approval workflow yet.
type CrawlApprovalIssuer interface {
	IssueCrawlApproval(ctx context.Context, request CrawlApprovalIssuanceRequest) (CrawlRequestApproval, error)
}

// CrawlApprovalRevoker is the trusted revocation boundary.
type CrawlApprovalRevoker interface {
	RevokeCrawlApproval(ctx context.Context, principal CrawlPrincipal, approvalID, reason string, revokedAt time.Time) error
}

// CrawlApprovalVerification is the complete trusted context presented to an
// approval verifier.
type CrawlApprovalVerification struct {
	Approval  CrawlRequestApproval
	Proposal  CrawlRequestProposal
	Identity  AgentIdentity
	Principal CrawlPrincipal
	RunID     string
	Now       time.Time
}

// StoreBackedCrawlApprovalVerifier verifies a presented approval against an
// authoritative store and checks that the issuing operator is authorized for
// the source. A nil store or source authorizer fails closed.
type StoreBackedCrawlApprovalVerifier struct {
	Store            CrawlApprovalStore
	SourceAuthorizer CrawlSourceAuthorizer
	TrustedIssuers   map[string]bool
	MaxLifetime      time.Duration
}

// VerifyCrawlApproval implements CrawlApprovalVerifier.
func (v StoreBackedCrawlApprovalVerifier) VerifyCrawlApproval(ctx context.Context, verification CrawlApprovalVerification) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.Store == nil || v.SourceAuthorizer == nil {
		return ErrCrawlApprovalStoreUnavailable
	}
	presented := verification.Approval
	if len(v.TrustedIssuers) == 0 || !v.TrustedIssuers[presented.Issuer] {
		return ErrCrawlApprovalIssuerUntrusted
	}
	record, found, err := v.Store.LookupCrawlApproval(ctx, presented.ApprovalID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrCrawlApprovalStoreUnavailable
	}
	if !found {
		return ErrCrawlApprovalUnknown
	}
	if record.Revoked {
		return ErrCrawlApprovalRevoked
	}

	authoritative := record.Approval
	if err := verification.Principal.Validate(); err != nil {
		return ErrCrawlApprovalForged
	}
	if authoritative.Owner != verification.Principal.Username {
		return ErrCrawlApprovalForged
	}
	if authoritative.Fingerprint() != presented.Fingerprint() {
		return ErrCrawlApprovalForged
	}
	if authoritative.ProposalSHA256 != verification.Proposal.Fingerprint() ||
		authoritative.RunID != verification.RunID ||
		authoritative.AgentID != verification.Identity.AgentID ||
		authoritative.Owner != verification.Identity.Owner ||
		authoritative.SourceID != verification.Proposal.SourceID ||
		authoritative.OperationType != verification.Proposal.OperationType {
		return ErrCrawlApprovalForged
	}

	now := verification.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if authoritative.ApprovedAt.IsZero() || authoritative.ExpiresAt.IsZero() ||
		authoritative.ApprovedAt.After(now) || !authoritative.ExpiresAt.After(now) ||
		!authoritative.ExpiresAt.After(authoritative.ApprovedAt) {
		return ErrCrawlApprovalLifetime
	}
	maxLifetime := v.MaxLifetime
	if maxLifetime <= 0 || maxLifetime > maxCrawlApprovalLifetime {
		maxLifetime = maxCrawlApprovalLifetime
	}
	if authoritative.ExpiresAt.Sub(authoritative.ApprovedAt) > maxLifetime {
		return ErrCrawlApprovalLifetime
	}

	approver := record.ApproverPrincipal
	if err := approver.Validate(); err != nil {
		return ErrCrawlApprovalApproverUnauthorized
	}
	if approver.UserID == 0 || approver.UserID != authoritative.ApproverUserID {
		return ErrCrawlApprovalApproverUnauthorized
	}
	if err := v.SourceAuthorizer.AuthorizeCrawlSource(ctx, approver, authoritative.SourceID); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		switch {
		case errors.Is(err, ErrCrawlSourceOwnershipMissing), errors.Is(err, ErrCrawlSourceForbidden):
			return ErrCrawlApprovalApproverUnauthorized
		default:
			return ErrCrawlApprovalApproverUnavailable
		}
	}
	return nil
}
