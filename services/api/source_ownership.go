package main

import (
	"context"
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
	errSourceAuthorizationUnavailable = errors.New("source authorization is unavailable")
)

type sourceMutationAccess struct {
	Authenticated bool
	UserID        uint64
	Username      string
	Admin         bool
	Existing      bool
	OwnerID       uint64
	Deleted       bool
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

func authorizeSourceID(ctx context.Context, db cdb.Handler, sourceID uint64) (sourceMutationAccess, error) {
	id, authenticated, err := resolveSourceMutationIdentity(ctx, db)
	if err != nil || !authenticated {
		return sourceMutationAccess{Authenticated: authenticated}, err
	}
	userID, err := sourceMutationUserID(id)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, err
	}
	owner, err := cdb.GetSourceOwner(ctx, &db, sourceID)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, errSourceAuthorizationUnavailable
	}
	if !owner.Found {
		return sourceMutationAccess{Authenticated: true}, errSourceNotFound
	}
	access := sourceMutationAccess{
		Authenticated: true, UserID: userID, Username: id.Username,
		Admin: sourceMutationPageAdmin(id), Existing: true, OwnerID: owner.UserID,
	}
	if owner.UserID == 0 && !access.Admin {
		return access, errSourceOwnerUnknown
	}
	if owner.UserID != 0 && owner.UserID != userID && !access.Admin {
		return access, errSourceForbidden
	}
	return access, nil
}

func authorizeSourceURL(ctx context.Context, db cdb.Handler, sourceURL string, includeDeleted bool) (sourceMutationAccess, error) {
	id, authenticated, err := resolveSourceMutationIdentity(ctx, db)
	if err != nil || !authenticated {
		return sourceMutationAccess{Authenticated: authenticated}, err
	}
	userID, err := sourceMutationUserID(id)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, err
	}
	owner, err := cdb.GetSourceOwnerByURL(ctx, &db, sourceURL, includeDeleted)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, errSourceAuthorizationUnavailable
	}
	access := sourceMutationAccess{
		Authenticated: true, UserID: userID, Username: id.Username,
		Admin: sourceMutationPageAdmin(id),
	}
	if !owner.Found {
		return access, nil
	}
	access.Existing = true
	access.OwnerID = owner.UserID
	access.Deleted = owner.Deleted
	if owner.UserID == 0 && !access.Admin {
		return access, errSourceOwnerUnknown
	}
	if owner.UserID != 0 && owner.UserID != userID && !access.Admin {
		return access, errSourceForbidden
	}
	return access, nil
}

func sourceMutationErrorCode(err error) int {
	switch {
	case errors.Is(err, errSourceUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, errSourceForbidden), errors.Is(err, errSourceOwnerUnknown):
		return http.StatusForbidden
	case errors.Is(err, errSourceNotFound):
		return http.StatusNotFound
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
