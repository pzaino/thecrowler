// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package auth

// Strict current-identity resolution for trusted internal eligibility checks.
//
// ResolveActiveIdentity intentionally preserves the legacy authentication
// contract: almost every failure is ErrUnauthorized and grant query errors are
// ignored. That is appropriate for login and middleware, but it cannot
// distinguish a disabled user from an unavailable auth database. This strict
// resolver is a separate entry point used only by explicit internal authority
// checks; it does not change login, token validation or middleware behavior.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LocalIssuerName is the canonical issuer of a local CROWler account. The
// strict resolver returns this value instead of echoing a caller-supplied
// issuer string.
const LocalIssuerName = "crowler"

var (
	// ErrIdentityInvalid means the supplied subject is not a valid local user
	// identifier.
	ErrIdentityInvalid = errors.New("authenticated identity is invalid")
	// ErrIdentityInactive means the local account does not exist, is disabled
	// or is soft-deleted.
	ErrIdentityInactive = errors.New("authenticated identity is inactive")
	// ErrIdentityMismatch means the current account username does not match
	// the supplied identity.
	ErrIdentityMismatch = errors.New("authenticated identity does not match the current account")
	// ErrIdentityExternalUnsupported means the account carries an external
	// issuer mapping that this read-only local-principal path cannot verify.
	ErrIdentityExternalUnsupported = errors.New("external identity issuer is unsupported for this operation")
	// ErrIdentityStoreUnavailable means the user or grant store could not be
	// read. It is never an authorization denial.
	ErrIdentityStoreUnavailable = errors.New("identity store is unavailable")
)

// ResolveActiveIdentityStrict resolves one numeric local account and its
// current grants. It distinguishes inactive identities from unavailable auth
// storage and returns a canonical local issuer rather than echoing input.
//
// This is a local-principal policy: the numeric local user ID and current
// username are authoritative. Accounts carrying an external subject/issuer
// mapping fail closed because this path has no verified external-issuer
// mapping. Existing ResolveActiveIdentity behavior is unchanged.
func ResolveActiveIdentityStrict(ctx context.Context, db Store, identity Identity) (Identity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	userID, err := strconv.ParseUint(strings.TrimSpace(identity.Subject), 10, 64)
	if err != nil || userID == 0 {
		return Identity{}, ErrIdentityInvalid
	}
	qs, ok := db.(queryRowStore)
	if !ok || qs == nil {
		return Identity{}, ErrIdentityStoreUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var (
		username       string
		disabled       bool
		deletedAt      sql.NullTime
		externalSub    sql.NullString
		externalIssuer sql.NullString
	)
	err = qs.QueryRowContext(ctx,
		`SELECT username, disabled, deleted_at, external_subject, external_issuer
		 FROM Users WHERE user_id = $1`, userID).Scan(
		&username, &disabled, &deletedAt, &externalSub, &externalIssuer,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrIdentityInactive
	}
	if err != nil {
		if ctx.Err() != nil {
			return Identity{}, fmt.Errorf("%w: %v", ErrIdentityStoreUnavailable, ctx.Err())
		}
		return Identity{}, ErrIdentityStoreUnavailable
	}
	if disabled || deletedAt.Valid {
		return Identity{}, ErrIdentityInactive
	}
	if strings.TrimSpace(externalSub.String) != "" ||
		strings.TrimSpace(externalIssuer.String) != "" {
		return Identity{}, ErrIdentityExternalUnsupported
	}
	if want := strings.TrimSpace(identity.Username); want != "" && want != username {
		return Identity{}, ErrIdentityMismatch
	}

	subject := strconv.FormatUint(userID, 10)
	roles, err := strictLoadRoles(ctx, db, subject)
	if err != nil {
		return Identity{}, err
	}
	scopes, err := strictLoadScopes(ctx, db, subject)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Subject:   subject,
		Username:  username,
		Issuer:    LocalIssuerName,
		TokenID:   identity.TokenID,
		Roles:     roles,
		Scopes:    scopes,
		ExpiresAt: identity.ExpiresAt,
	}, nil
}

func strictLoadRoles(ctx context.Context, db Store, subject string) ([]string, error) {
	qs, ok := db.(queryStore)
	if !ok || qs == nil {
		return nil, ErrIdentityStoreUnavailable
	}
	rows, err := qs.QueryContext(ctx,
		`SELECT r.name FROM AuthRoles r
		 JOIN UserRoles ur ON ur.role_id = r.role_id
		 WHERE ur.user_id = $1 ORDER BY r.name`, subject)
	if err != nil {
		return nil, strictIdentityStoreError(ctx)
	}
	defer rows.Close() //nolint:errcheck
	roles := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, strictIdentityStoreError(ctx)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, strictIdentityStoreError(ctx)
	}
	return roles, nil
}

func strictLoadScopes(ctx context.Context, db Store, subject string) ([]string, error) {
	qs, ok := db.(queryStore)
	if !ok || qs == nil {
		return nil, ErrIdentityStoreUnavailable
	}
	rows, err := qs.QueryContext(ctx, `
		SELECT DISTINCT s.name
		FROM AuthScopes s
		LEFT JOIN UserScopes us ON us.scope_id = s.scope_id AND us.user_id = $1
		LEFT JOIN RoleScopes rs ON rs.scope_id = s.scope_id
		LEFT JOIN UserRoles ur ON ur.role_id = rs.role_id AND ur.user_id = $1
		WHERE us.user_id IS NOT NULL OR ur.user_id IS NOT NULL
		ORDER BY s.name`, subject)
	if err != nil {
		return nil, strictIdentityStoreError(ctx)
	}
	defer rows.Close() //nolint:errcheck
	scopes := []string{}
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, strictIdentityStoreError(ctx)
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, strictIdentityStoreError(ctx)
	}
	return scopes, nil
}

func strictIdentityStoreError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return fmt.Errorf("%w: %v", ErrIdentityStoreUnavailable, ctx.Err())
	}
	return ErrIdentityStoreUnavailable
}
