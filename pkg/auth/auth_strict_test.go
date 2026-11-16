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

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func strictIdentityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open strict identity sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE Users (
			user_id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT,
			disabled BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at TIMESTAMP,
			external_subject TEXT,
			external_issuer TEXT,
			credential_version BIGINT NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE AuthRoles (role_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT)`,
		`CREATE TABLE AuthScopes (scope_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT)`,
		`CREATE TABLE UserRoles (user_id INTEGER NOT NULL, role_id INTEGER NOT NULL, PRIMARY KEY (user_id, role_id))`,
		`CREATE TABLE UserScopes (user_id INTEGER NOT NULL, scope_id INTEGER NOT NULL, PRIMARY KEY (user_id, scope_id))`,
		`CREATE TABLE RoleScopes (role_id INTEGER NOT NULL, scope_id INTEGER NOT NULL, PRIMARY KEY (role_id, scope_id))`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create strict auth table: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at, external_subject, external_issuer) VALUES
		(7, 'owner-1', FALSE, NULL, NULL, NULL),
		(8, 'disabled-user', TRUE, NULL, NULL, NULL),
		(9, 'deleted-user', FALSE, CURRENT_TIMESTAMP, NULL, NULL),
		(11, 'external-user', FALSE, NULL, 'ext-subject', 'https://issuer.example/')`); err != nil {
		t.Fatalf("insert strict users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AuthRoles(role_id, name) VALUES (1, 'admin');
		INSERT INTO AuthScopes(scope_id, name) VALUES (1, 'api:read');
		INSERT INTO UserRoles(user_id, role_id) VALUES (7, 1);
		INSERT INTO RoleScopes(role_id, scope_id) VALUES (1, 1);`); err != nil {
		t.Fatalf("insert strict grants: %v", err)
	}
	return db
}

func TestResolveActiveIdentityStrict(t *testing.T) {
	db := strictIdentityTestDB(t)
	id, err := ResolveActiveIdentityStrict(context.Background(), db, Identity{
		Subject: "7", Username: "owner-1", Issuer: "ignored-audit-issuer",
	})
	if err != nil {
		t.Fatalf("strict resolve: %v", err)
	}
	if id.Subject != "7" || id.Username != "owner-1" || id.Issuer != LocalIssuerName {
		t.Fatalf("strict identity = %#v", id)
	}
	if len(id.Roles) != 1 || id.Roles[0] != "admin" ||
		len(id.Scopes) != 1 || id.Scopes[0] != "api:read" {
		t.Fatalf("strict grants = %#v", id)
	}
}

func TestResolveActiveIdentityStrictErrors(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
		want     error
	}{
		{"disabled", Identity{Subject: "8", Username: "disabled-user"}, ErrIdentityInactive},
		{"deleted", Identity{Subject: "9", Username: "deleted-user"}, ErrIdentityInactive},
		{"missing", Identity{Subject: "10", Username: "missing"}, ErrIdentityInactive},
		{"renamed", Identity{Subject: "7", Username: "old-name"}, ErrIdentityMismatch},
		{"malformed subject", Identity{Subject: "external-subject"}, ErrIdentityInvalid},
		{"zero subject", Identity{Subject: "0"}, ErrIdentityInvalid},
		{"external mapping", Identity{Subject: "11", Username: "external-user"}, ErrIdentityExternalUnsupported},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			db := strictIdentityTestDB(t)
			if _, err := ResolveActiveIdentityStrict(context.Background(), db, test.identity); !errors.Is(err, test.want) {
				t.Fatalf("strict resolve error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolveActiveIdentityStrictUnavailable(t *testing.T) {
	t.Run("missing users table", func(t *testing.T) {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		db.SetMaxOpenConns(1)
		defer db.Close()
		if _, err := ResolveActiveIdentityStrict(context.Background(), db, Identity{Subject: "7"}); !errors.Is(err, ErrIdentityStoreUnavailable) {
			t.Fatalf("missing schema error = %v, want store unavailable", err)
		}
	})
	t.Run("missing grant table", func(t *testing.T) {
		db := strictIdentityTestDB(t)
		if _, err := db.Exec(`DROP TABLE AuthRoles`); err != nil {
			t.Fatalf("drop roles: %v", err)
		}
		if _, err := ResolveActiveIdentityStrict(context.Background(), db, Identity{Subject: "7", Username: "owner-1"}); !errors.Is(err, ErrIdentityStoreUnavailable) {
			t.Fatalf("missing grant table error = %v, want store unavailable", err)
		}
	})
	t.Run("closed database", func(t *testing.T) {
		db := strictIdentityTestDB(t)
		_ = db.Close()
		if _, err := ResolveActiveIdentityStrict(context.Background(), db, Identity{Subject: "7", Username: "owner-1"}); !errors.Is(err, ErrIdentityStoreUnavailable) {
			t.Fatalf("closed database error = %v, want store unavailable", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		db := strictIdentityTestDB(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ResolveActiveIdentityStrict(ctx, db, Identity{Subject: "7", Username: "owner-1"}); !errors.Is(err, ErrIdentityStoreUnavailable) {
			t.Fatalf("cancelled context error = %v, want store unavailable", err)
		}
	})
}

func TestResolveActiveIdentityLegacyContractUnchanged(t *testing.T) {
	db := authIdentityTestDB(t)
	if _, err := ResolveActiveIdentity(context.Background(), db, Identity{Subject: "8", Issuer: "crowler"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy disabled error = %v, want ErrUnauthorized", err)
	}
	_ = db.Close()
	if _, err := ResolveActiveIdentity(context.Background(), db, Identity{Subject: "7", Issuer: "crowler"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("legacy closed-db error = %v, want ErrUnauthorized", err)
	}
}
