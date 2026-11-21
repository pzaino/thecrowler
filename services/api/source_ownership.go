package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	errSourceUnauthenticated          = errors.New("source mutation requires an authenticated user")
	errSourceForbidden                = errors.New("source mutation is not authorized")
	errSourceOwnerUnknown             = errors.New("source ownership is unknown")
	errSourceNotFound                 = errors.New("source not found")
	errSourceDeleted                  = errors.New("source is deleted")
	errSourceAuthorizationUnavailable = errors.New("source authorization is unavailable")
)

type sourceMutationAccess struct {
	Authenticated bool
	UserID        uint64
	Username      string
	Admin         bool
}

func resolveSourceMutationIdentity(ctx context.Context, db cdb.Handler) (auth.Identity, bool, error) {
	if !auth.Enabled(config.API.Auth) {
		return auth.Identity{}, false, nil
	}
	id, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Identity{}, true, errSourceUnauthenticated
	}
	resolved, err := auth.ResolveActiveIdentity(ctx, db, id)
	if err != nil {
		return auth.Identity{}, true, errSourceUnauthenticated
	}
	return resolved, true, nil
}

func sourceMutationUserID(id auth.Identity) (uint64, error) {
	userID, err := strconv.ParseUint(strings.TrimSpace(id.Subject), 10, 64)
	if err != nil || userID == 0 {
		return 0, errSourceUnauthenticated
	}
	return userID, nil
}

func sourceMutationPageAdmin(id auth.Identity) bool {
	return hasRole(id.Roles, roleAdmin, roleSuperAdmin)
}

func sourceMutationAccessFromIdentity(id auth.Identity) (sourceMutationAccess, error) {
	userID, err := sourceMutationUserID(id)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, err
	}
	return sourceMutationAccess{
		Authenticated: true,
		UserID:        userID,
		Username:      id.Username,
		Admin:         sourceMutationPageAdmin(id),
	}, nil
}

// authorizeSourceMutationTx locks the source row and rechecks verified
// ownership and, for administrators, the current administrative grant inside
// the same transaction that mutates the source.
func authorizeSourceMutationTx(ctx context.Context, tx *sql.Tx, db cdb.Handler, sourceID uint64, access sourceMutationAccess) (cdb.SourceOwner, error) {
	if !access.Authenticated {
		return cdb.SourceOwner{}, nil
	}
	owner, err := cdb.AuthorizeSourceMutationTx(ctx, tx, db, sourceID, access.UserID, access.Admin)
	if err != nil {
		return owner, sourceMutationAuthorizationError(err)
	}
	return owner, nil
}

func authorizeSourceMutationByURLTx(ctx context.Context, tx *sql.Tx, db cdb.Handler, sourceURL string, access sourceMutationAccess) (cdb.SourceOwner, error) {
	if !access.Authenticated {
		return cdb.SourceOwner{}, nil
	}
	owner, err := cdb.AuthorizeSourceMutationByURLTx(ctx, tx, db, sourceURL, access.UserID, access.Admin)
	if err != nil {
		return owner, sourceMutationAuthorizationError(err)
	}
	return owner, nil
}

func sourceMutationAuthorizationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cdb.ErrSourceMutationNotFound):
		return errSourceNotFound
	case errors.Is(err, cdb.ErrSourceMutationDeleted):
		return errSourceDeleted
	case errors.Is(err, cdb.ErrSourceMutationUnverified):
		return fmt.Errorf("%w: verified source ownership is required", errSourceForbidden)
	case errors.Is(err, cdb.ErrSourceMutationForbidden):
		return errSourceForbidden
	case errors.Is(err, cdb.ErrSourceOwnershipAdminRequired):
		return errSourceForbidden
	default:
		return errSourceAuthorizationUnavailable
	}
}

func sourceMutationErrorCode(err error) int {
	switch {
	case errors.Is(err, errSourceUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, errSourceForbidden), errors.Is(err, errSourceOwnerUnknown),
		errors.Is(err, cdb.ErrSourceMutationForbidden),
		errors.Is(err, cdb.ErrSourceMutationUnverified):
		return http.StatusForbidden
	case errors.Is(err, errSourceNotFound), errors.Is(err, cdb.ErrSourceMutationNotFound):
		return http.StatusNotFound
	case errors.Is(err, errSourceDeleted), errors.Is(err, cdb.ErrSourceMutationDeleted):
		return http.StatusConflict
	case errors.Is(err, cdb.ErrAuthenticatedSourceConflict):
		return http.StatusForbidden
	case errors.Is(err, cdb.ErrAuthenticatedSourceUnsupported):
		return http.StatusServiceUnavailable
	case errors.Is(err, errSourceAuthorizationUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func sourceMutationError(err error) error {
	if err == nil {
		return nil
	}
	if code := sourceMutationErrorCode(err); code != http.StatusInternalServerError {
		return err
	}
	return fmt.Errorf("source mutation failed: %w", err)
}

type sourceOwnershipVerifyRequest struct {
	SourceID uint64 `json:"source_id"`
	UserID   uint64 `json:"user_id"`
	Reason   string `json:"reason,omitempty"`
}

func sourceOwnershipVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !auth.Enabled(config.API.Auth) {
		handleErrorAndRespond(w, errSourceUnauthenticated, nil, "Source ownership verification failed", http.StatusUnauthorized, 0)
		return
	}
	identity, authenticated, err := resolveSourceMutationIdentity(r.Context(), dbHandler)
	if err != nil || !authenticated {
		handleErrorAndRespond(w, errSourceUnauthenticated, nil, "Source ownership verification failed", http.StatusUnauthorized, 0)
		return
	}
	if !sourceMutationPageAdmin(identity) {
		handleErrorAndRespond(w, errSourceForbidden, nil, "Source ownership verification failed", http.StatusForbidden, 0)
		return
	}
	var request sourceOwnershipVerifyRequest
	if err := decodeJSON(r, &request); err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid source ownership verification request", http.StatusBadRequest, 0)
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.SourceID == 0 || request.UserID == 0 {
		handleErrorAndRespond(w, errors.New("source_id and user_id are required"), nil, "Invalid source ownership verification request", http.StatusBadRequest, 0)
		return
	}
	if len(request.Reason) > 512 {
		handleErrorAndRespond(w, errors.New("reason is too long"), nil, "Invalid source ownership verification request", http.StatusBadRequest, 0)
		return
	}
	verifierID, err := sourceMutationUserID(identity)
	if err != nil {
		handleErrorAndRespond(w, errSourceUnauthenticated, nil, "Source ownership verification failed", http.StatusUnauthorized, 0)
		return
	}
	owner, err := cdb.VerifySourceOwnershipContext(r.Context(), &dbHandler, cdb.SourceOwnershipVerification{
		SourceID:           request.SourceID,
		UserID:             request.UserID,
		VerifiedByUserID:   verifierID,
		VerifiedBy:         identity.Username,
		VerificationMethod: "admin_verification",
		Reason:             request.Reason,
	})
	if err != nil {
		handleErrorAndRespond(w, sourceMutationAuthorizationError(err), nil, "Source ownership verification failed", sourceMutationErrorCode(sourceMutationAuthorizationError(err)), 0)
		return
	}
	handleErrorAndRespond(w, nil, map[string]any{
		"source_id": owner.SourceID,
		"user_id":   owner.UserID,
		"verified":  owner.Verified,
	}, "", 0, http.StatusOK)
}
