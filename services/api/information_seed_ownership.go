package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	errInformationSeedUnauthenticated = errors.New("information seed operation requires an authenticated user")
	errInformationSeedForbidden       = errors.New("information seed operation is not authorized")
	errInformationSeedNotFound        = errors.New("information seed not found")
)

func resolveInformationSeedAccess(ctx context.Context, db cdb.Handler) (sourceMutationAccess, error) {
	if !auth.Enabled(config.API.Auth) {
		return sourceMutationAccess{}, nil
	}
	identity, authenticated, err := resolveSourceMutationIdentity(ctx, db)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, errInformationSeedUnauthenticated
	}
	if !authenticated {
		return sourceMutationAccess{}, nil
	}
	access, err := sourceMutationAccessFromIdentity(identity)
	if err != nil {
		return sourceMutationAccess{Authenticated: true}, errInformationSeedUnauthenticated
	}
	return access, nil
}

func informationSeedAccessOrRespond(w http.ResponseWriter, r *http.Request) (sourceMutationAccess, bool) {
	access, err := resolveInformationSeedAccess(r.Context(), dbHandler)
	if err != nil {
		handleErrorAndRespond(w, informationSeedAccessError(err), nil, "Information seed authorization failed", informationSeedAccessErrorCode(err), 0)
		return access, false
	}
	return access, true
}

func authorizeInformationSeedAccess(seed cdb.InformationSeed, access sourceMutationAccess) error {
	if !access.Authenticated || access.Admin {
		return nil
	}
	if seed.UsrID != 0 && seed.UsrID == access.UserID {
		return nil
	}
	return errInformationSeedForbidden
}

func loadAuthorizedInformationSeedContext(ctx context.Context, db *cdb.Handler, id uint64, access sourceMutationAccess) (*cdb.InformationSeed, error) {
	if id == 0 {
		return nil, errInformationSeedNotFound
	}
	seeds, err := cdb.ListInformationSeedsWithStatsContext(ctx, db, cdb.InformationSeedFilter{ID: id, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return nil, errInformationSeedNotFound
	}
	seed := seeds[0].InformationSeed
	if err := authorizeInformationSeedAccess(seed, access); err != nil {
		return nil, err
	}
	return &seed, nil
}

func requireInformationSeedAdmin(access sourceMutationAccess) error {
	if !access.Authenticated {
		// Authentication-disabled installations preserve the existing console
		// contract; operators are expected to expose only trusted networks.
		return nil
	}
	if !access.Admin {
		return errInformationSeedForbidden
	}
	return nil
}

func informationSeedAccessErrorCode(err error) int {
	switch {
	case errors.Is(err, errInformationSeedUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, errInformationSeedForbidden):
		return http.StatusForbidden
	case errors.Is(err, errInformationSeedNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func informationSeedAccessError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errInformationSeedUnauthenticated),
		errors.Is(err, errInformationSeedForbidden),
		errors.Is(err, errInformationSeedNotFound):
		return err
	default:
		return fmt.Errorf("information seed authorization failed: %w", err)
	}
}

func informationSeedRequestValuesWithOwner(values url.Values, access sourceMutationAccess) url.Values {
	if !access.Authenticated || access.Admin {
		return values
	}
	filtered := make(url.Values, len(values)+1)
	for key, value := range values {
		switch strings.ToLower(key) {
		case "user", "user_id", "usr_id":
			continue
		default:
			filtered[key] = append([]string(nil), value...)
		}
	}
	filtered["usr_id"] = []string{strconv.FormatUint(access.UserID, 10)}
	return filtered
}
