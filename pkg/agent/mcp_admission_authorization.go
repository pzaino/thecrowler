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

// MCPMutationAdmissionAuthorizer revalidates the trusted principal and current
// source ownership inside the durable admission transaction. A nil authorizer
// fails closed in the durable store.
type MCPMutationAdmissionAuthorizer interface {
	AuthorizeMCPMutationAdmission(ctx context.Context, tx *sql.Tx, proposal MCPMutationProposal) error
}

// DatabaseMCPMutationAdmissionAuthorizer is the production admission boundary.
// It re-resolves the current account and locks the relevant ownership rows in
// the same transaction that inserts the proposal.
type DatabaseMCPMutationAdmissionAuthorizer struct {
	DB *cdb.Handler
}

func (a DatabaseMCPMutationAdmissionAuthorizer) AuthorizeMCPMutationAdmission(ctx context.Context, tx *sql.Tx, proposal MCPMutationProposal) error {
	if a.DB == nil || *a.DB == nil || tx == nil {
		return ErrMCPMutationStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if proposal.Principal.Validate() != nil {
		return ErrMCPMutationPrincipal
	}
	if len(proposal.SourceIDs) == 0 && strings.TrimSpace(proposal.ExternalResource) == "" {
		return fmt.Errorf("%w: proposal has no approved scope", ErrMCPMutationOperationUnauthorized)
	}
	if len(proposal.SourceIDs) > 0 && strings.TrimSpace(proposal.ExternalResource) != "" {
		return fmt.Errorf("%w: proposal has conflicting scope", ErrMCPMutationOperationUnauthorized)
	}
	if len(proposal.SourceIDs) == 0 {
		// External-resource proposals have no source row to lock. Lock the
		// account directly and resolve its current grants.
		if err := lockMCPPrincipalTx(ctx, tx, *a.DB, proposal.Principal.UserID); err != nil {
			return err
		}
		_, err := resolveMCPAdmissionIdentity(ctx, tx, proposal.Principal)
		return err
	}

	// Source-scoped proposals follow the application's transfer lock order:
	// source first, then user. Resolve the principal once to obtain current
	// roles for the administrative override, then let
	// AuthorizeSourceMutationTx acquire the source and user locks. Re-resolve
	// after the locks to reject a username or issuer change that committed
	// before the locks were taken.
	resolved, err := resolveMCPAdmissionIdentity(ctx, tx, proposal.Principal)
	if err != nil {
		return err
	}
	principal := CrawlPrincipal{
		UserID:   proposal.Principal.UserID,
		Username: resolved.Username,
		Issuer:   resolved.Issuer,
		Roles:    append([]string(nil), resolved.Roles...),
		Scopes:   append([]string(nil), resolved.Scopes...),
	}
	for _, sourceID := range proposal.SourceIDs {
		if _, err := cdb.AuthorizeSourceMutationTx(ctx, tx, *a.DB, sourceID,
			principal.UserID, principal.IsSourceAdministrator()); err != nil {
			return mapMCPAdmissionSourceError(ctx, sourceID, err)
		}
	}
	locked, err := resolveMCPAdmissionIdentity(ctx, tx, proposal.Principal)
	if err != nil {
		return err
	}
	if locked.Username != principal.Username || locked.Issuer != principal.Issuer {
		return ErrMCPMutationPrincipal
	}
	return nil
}

func resolveMCPAdmissionIdentity(ctx context.Context, tx *sql.Tx, principal MCPMutationPrincipal) (auth.Identity, error) {
	resolved, err := auth.ResolveActiveIdentity(ctx, mcpTxAuthStore{tx: tx}, auth.Identity{
		Subject:  strconv.FormatUint(principal.UserID, 10),
		Username: principal.Username,
		Issuer:   principal.Issuer,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return auth.Identity{}, ctxErr
		}
		return auth.Identity{}, fmt.Errorf("%w: %v", ErrMCPMutationPrincipal, err)
	}
	if resolved.Subject != strconv.FormatUint(principal.UserID, 10) ||
		resolved.Username != principal.Username ||
		resolved.Issuer != principal.Issuer {
		return auth.Identity{}, ErrMCPMutationPrincipal
	}
	return resolved, nil
}

func mapMCPAdmissionSourceError(ctx context.Context, sourceID uint64, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	switch {
	case errors.Is(err, cdb.ErrSourceMutationNotFound),
		errors.Is(err, cdb.ErrSourceMutationDeleted),
		errors.Is(err, cdb.ErrSourceMutationForbidden),
		errors.Is(err, cdb.ErrSourceMutationUnverified),
		errors.Is(err, cdb.ErrSourceOwnershipAdminRequired):
		return fmt.Errorf("%w: source %d: %v", ErrMCPMutationOperationUnauthorized, sourceID, err)
	default:
		return fmt.Errorf("%w: source %d authorization unavailable: %v", ErrMCPMutationStoreUnavailable, sourceID, err)
	}
}

func lockMCPPrincipalTx(ctx context.Context, tx *sql.Tx, db cdb.Handler, userID uint64) error {
	if userID == 0 {
		return ErrMCPMutationPrincipal
	}
	placeholder := "$1"
	if strings.Contains(strings.ToLower(db.DBMS()), "mysql") {
		placeholder = "?"
	}
	query := `SELECT user_id FROM Users WHERE user_id = ` + placeholder +
		` AND disabled = FALSE AND deleted_at IS NULL`
	if strings.Contains(strings.ToLower(db.DBMS()), "postgres") {
		query += ` FOR SHARE`
	}
	var locked uint64
	if err := tx.QueryRowContext(ctx, query, userID).Scan(&locked); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMCPMutationPrincipal
		}
		return fmt.Errorf("%w: lock principal: %v", ErrMCPMutationStoreUnavailable, err)
	}
	return nil
}

// mcpTxAuthStore adapts the admission transaction to the auth resolver's
// structural Store interfaces, keeping account and grant reads in the same
// transaction as the proposal insert.
type mcpTxAuthStore struct {
	tx *sql.Tx
}

func (s mcpTxAuthStore) QueryRow(query string, args ...any) *sql.Row {
	return s.tx.QueryRow(query, args...)
}

func (s mcpTxAuthStore) Exec(query string, args ...any) (sql.Result, error) {
	return s.tx.Exec(query, args...)
}

func (s mcpTxAuthStore) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.tx.QueryRowContext(ctx, query, args...)
}

func (s mcpTxAuthStore) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.tx.QueryContext(ctx, query, args...)
}
