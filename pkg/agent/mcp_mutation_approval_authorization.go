package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// ErrMCPMutationApprovalForbidden means the current approver or proposal
// principal is no longer authorized for this exact approval.
var ErrMCPMutationApprovalForbidden = errors.New("MCP mutation approval is not authorized")

// MCPMutationApprovalAuthorizer revalidates approver and proposal-principal
// authority inside the approval transaction, after policy evaluation and before
// quota/proposal locks.
type MCPMutationApprovalAuthorizer interface {
	AuthorizeMCPMutationApproval(ctx context.Context, tx *sql.Tx, proposal cdb.MCPMutationProposalRow, approver MCPMutationPrincipal) error
}

// DatabaseMCPMutationApprovalAuthorizer is the production approval authority.
type DatabaseMCPMutationApprovalAuthorizer struct {
	DB *cdb.Handler
}

func (a DatabaseMCPMutationApprovalAuthorizer) AuthorizeMCPMutationApproval(ctx context.Context, tx *sql.Tx, proposal cdb.MCPMutationProposalRow, approver MCPMutationPrincipal) error {
	if a.DB == nil || *a.DB == nil || tx == nil {
		return ErrMCPMutationStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := approver.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrMCPMutationApprovalForbidden, err)
	}
	if proposal.PrincipalUserID == 0 || strings.TrimSpace(proposal.PrincipalUsername) == "" {
		return ErrMCPMutationApprovalForbidden
	}
	request := cdb.MCPMutationApprovalAuthorityRequest{
		SourceIDs:           append([]uint64(nil), proposal.SourceIDs...),
		ExternalResource:    strings.TrimSpace(proposal.ExternalResource) != "",
		ApproverUserID:      approver.UserID,
		ProposalPrincipalID: proposal.PrincipalUserID,
	}
	if err := cdb.AuthorizeMCPMutationApprovalTx(ctx, tx, *a.DB, request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		switch {
		case errors.Is(err, cdb.ErrMCPMutationApproverForbidden),
			errors.Is(err, cdb.ErrMCPMutationProposalPrincipalForbidden),
			errors.Is(err, cdb.ErrSourceMutationNotFound),
			errors.Is(err, cdb.ErrSourceMutationDeleted),
			errors.Is(err, cdb.ErrSourceMutationForbidden),
			errors.Is(err, cdb.ErrSourceMutationUnverified),
			errors.Is(err, cdb.ErrSourceOwnershipAdminRequired):
			return ErrMCPMutationApprovalForbidden
		case errors.Is(err, cdb.ErrMCPMutationApprovalAuthorityUnavailable):
			return ErrMCPMutationStoreUnavailable
		default:
			return ErrMCPMutationStoreUnavailable
		}
	}
	approverIdentity, err := resolveMCPApprovalIdentity(ctx, tx, approver)
	if err != nil {
		return err
	}
	if approverIdentity.Username != approver.Username || approverIdentity.Issuer != approver.Issuer {
		return ErrMCPMutationApprovalForbidden
	}
	principal := MCPMutationPrincipal{
		UserID:   proposal.PrincipalUserID,
		Username: proposal.PrincipalUsername,
		Issuer:   proposal.PrincipalIssuer,
	}
	principalIdentity, err := resolveMCPApprovalIdentity(ctx, tx, principal)
	if err != nil {
		return err
	}
	if principalIdentity.Username != proposal.PrincipalUsername ||
		principalIdentity.Issuer != proposal.PrincipalIssuer {
		return ErrMCPMutationApprovalForbidden
	}
	return nil
}

func resolveMCPApprovalIdentity(ctx context.Context, tx *sql.Tx, principal MCPMutationPrincipal) (auth.Identity, error) {
	resolved, err := auth.ResolveActiveIdentity(ctx, mcpTxAuthStore{tx: tx}, auth.Identity{
		Subject:  strconv.FormatUint(principal.UserID, 10),
		Username: principal.Username,
		Issuer:   principal.Issuer,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return auth.Identity{}, ctxErr
		}
		return auth.Identity{}, ErrMCPMutationApprovalForbidden
	}
	return resolved, nil
}
