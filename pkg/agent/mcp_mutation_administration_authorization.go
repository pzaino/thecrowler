package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// ErrMCPMutationAdministrationForbidden means the current actor may not deny
// or revoke this proposal/approval.
var ErrMCPMutationAdministrationForbidden = errors.New("MCP mutation administration is not authorized")

// MCPMutationAdministrationAuthorizer revalidates deny/revoke actor authority
// inside the terminal transaction.
type MCPMutationAdministrationAuthorizer interface {
	AuthorizeMCPMutationAdministration(ctx context.Context, tx *sql.Tx, proposal cdb.MCPMutationProposalRow, actor MCPMutationPrincipal) error
}

type DatabaseMCPMutationAdministrationAuthorizer struct {
	DB *cdb.Handler
}

func (a DatabaseMCPMutationAdministrationAuthorizer) AuthorizeMCPMutationAdministration(ctx context.Context, tx *sql.Tx, proposal cdb.MCPMutationProposalRow, actor MCPMutationPrincipal) error {
	if a.DB == nil || *a.DB == nil || tx == nil {
		return ErrMCPMutationStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := actor.Validate(); err != nil {
		return ErrMCPMutationAdministrationForbidden
	}
	request := cdb.MCPMutationAdministrationAuthorityRequest{
		SourceIDs:        append([]uint64(nil), proposal.SourceIDs...),
		ExternalResource: proposal.ExternalResource != "",
		ActorUserID:      actor.UserID,
	}
	if err := cdb.AuthorizeMCPMutationAdministrationTx(ctx, tx, *a.DB, request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		switch {
		case errors.Is(err, cdb.ErrMCPMutationAdministrationForbidden),
			errors.Is(err, cdb.ErrSourceMutationNotFound),
			errors.Is(err, cdb.ErrSourceMutationDeleted),
			errors.Is(err, cdb.ErrSourceMutationForbidden),
			errors.Is(err, cdb.ErrSourceMutationUnverified):
			return ErrMCPMutationAdministrationForbidden
		case errors.Is(err, cdb.ErrMCPMutationAdministrationAuthorityUnavailable):
			return ErrMCPMutationStoreUnavailable
		default:
			return ErrMCPMutationStoreUnavailable
		}
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, mcpTxAuthStore{tx: tx}, auth.Identity{
		Subject: strconv.FormatUint(actor.UserID, 10), Username: actor.Username, Issuer: actor.Issuer,
	})
	if err != nil || resolved.Username != actor.Username || resolved.Issuer != actor.Issuer {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: actor identity changed", ErrMCPMutationAdministrationForbidden)
	}
	return nil
}
