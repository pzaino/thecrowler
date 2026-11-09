//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func newAuthenticatedSourceRaceFixture(t *testing.T) (*Handler, *sql.DB, uint64, uint64, uint64, string, string) {
	t.Helper()
	db, sqlDB := openPostgresIntegrationTestDB(t)
	ownerID := uint64(810000 + time.Now().UnixNano()%10000)
	adminID := ownerID + 1
	ownerName := fmt.Sprintf("race-owner-%d", ownerID)
	adminName := fmt.Sprintf("race-admin-%d", adminID)
	if _, err := sqlDB.Exec(`INSERT INTO AuthRoles(name, description) VALUES
		('admin', 'test administrator'), ('user', 'test user')
		ON CONFLICT (name) DO NOTHING`); err != nil {
		t.Fatalf("ensure race roles: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES
		($1, $2, FALSE, NULL), ($3, $4, FALSE, NULL)
		ON CONFLICT (user_id) DO UPDATE SET username=EXCLUDED.username, disabled=FALSE, deleted_at=NULL`,
		ownerID, ownerName, adminID, adminName); err != nil {
		t.Fatalf("insert race users: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='user'
		ON CONFLICT DO NOTHING`, ownerID); err != nil {
		t.Fatalf("grant owner role: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='admin'
		ON CONFLICT DO NOTHING`, adminID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}
	sourceURL := fmt.Sprintf("https://race-%d.example/", time.Now().UnixNano())
	source := &Source{URL: sourceURL, Name: "race-source", Priority: "high", Restricted: 1, UsrID: ownerID}
	sourceID, err := CreateSourceContext(context.Background(), db, source, cfg.SourceConfig{})
	if err != nil {
		t.Fatalf("create race source: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE Sources SET status='completed', last_updated_at=NOW() WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("complete race source: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method)
		VALUES ($1, $2, NOW(), $2, 'race-fixture', 'admin_verification')
		ON CONFLICT (source_id) DO UPDATE SET user_id=EXCLUDED.user_id, verified_at=EXCLUDED.verified_at`, sourceID, ownerID); err != nil {
		t.Fatalf("record race ownership: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM Status WHERE source_id=$1`, sourceID)
		_, _ = sqlDB.Exec(`DELETE FROM SourceOwnership WHERE source_id=$1`, sourceID)
		_, _ = sqlDB.Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID)
		_, _ = sqlDB.Exec(`DELETE FROM UserRoles WHERE user_id IN ($1,$2)`, ownerID, adminID)
		_, _ = sqlDB.Exec(`DELETE FROM UserScopes WHERE user_id IN ($1,$2)`, ownerID, adminID)
		_, _ = sqlDB.Exec(`DELETE FROM Users WHERE user_id IN ($1,$2)`, ownerID, adminID)
	})
	return db, sqlDB, sourceID, ownerID, adminID, ownerName, sourceURL
}

// waitForPostgresLockWait waits for a session running the requested statement
// to become blocked on a row lock. It observes database state rather than
// relying on a fixed sleep to establish ordering.
func waitForPostgresLockWait(t *testing.T, db *sql.DB, queryFragment string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database()
			  AND state = 'active'
			  AND wait_event_type = 'Lock'
			  AND query ILIKE $1`, queryFragment).Scan(&count)
		if err != nil {
			t.Fatalf("observe blocked PostgreSQL query: %v", err)
		}
		if count > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PostgreSQL lock wait matching %q", queryFragment)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAuthenticatedUpsertDeniesDisabledOwnerAfterSourceLock(t *testing.T) {
	db, sqlDB, sourceID, ownerID, _, ownerName, sourceURL := newAuthenticatedSourceRaceFixture(t)
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin source lock: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`SELECT source_id FROM Sources WHERE source_id=$1 FOR UPDATE`, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock source: %v", err)
	}
	type result struct {
		sourceID uint64
		created  bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		id, created, err := UpsertAuthenticatedSourceContext(context.Background(), db, &Source{
			URL: sourceURL, Name: "changed-by-disabled-owner", Priority: "low", Disabled: true, Restricted: 1,
		}, cfg.SourceConfig{}, ownerID, ownerName, false)
		done <- result{sourceID: id, created: created, err: err}
	}()
	waitForPostgresLockWait(t, sqlDB, "%FROM Sources%")
	if _, err := tx.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=$1`, ownerID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("disable owner in locked transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit owner disable: %v", err)
	}
	outcome := <-done
	if !errors.Is(outcome.err, ErrSourceMutationForbidden) {
		t.Fatalf("stale authenticated upsert error = %v", outcome.err)
	}
	var name string
	var disabled bool
	var owner uint64
	if err := sqlDB.QueryRow(`SELECT name, disabled, usr_id FROM Sources WHERE source_id=$1`, sourceID).Scan(&name, &disabled, &owner); err != nil {
		t.Fatalf("read source after denial: %v", err)
	}
	if name != "race-source" || disabled || owner != ownerID {
		t.Fatalf("disabled owner changed source name=%q disabled=%v owner=%d", name, disabled, owner)
	}
	var provenanceOwner uint64
	if err := sqlDB.QueryRow(`SELECT user_id FROM SourceOwnership WHERE source_id=$1`, sourceID).Scan(&provenanceOwner); err != nil {
		t.Fatalf("read ownership after denial: %v", err)
	}
	if provenanceOwner != ownerID {
		t.Fatalf("disabled owner changed provenance to %d", provenanceOwner)
	}
}

func TestAuthenticatedUpsertDeniesRevokedAdministratorAfterSourceLock(t *testing.T) {
	db, sqlDB, sourceID, _, adminID, _, sourceURL := newAuthenticatedSourceRaceFixture(t)
	adminName := fmt.Sprintf("race-admin-%d", adminID)
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin source lock: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`SELECT source_id FROM Sources WHERE source_id=$1 FOR UPDATE`, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock source: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := UpsertAuthenticatedSourceContext(context.Background(), db, &Source{
			URL: sourceURL, Name: "changed-by-demoted-admin", Priority: "low", Disabled: true, Restricted: 1,
		}, cfg.SourceConfig{}, adminID, adminName, true)
		done <- err
	}()
	waitForPostgresLockWait(t, sqlDB, "%FROM Sources%")
	if _, err := tx.Exec(`DELETE FROM UserRoles WHERE user_id=$1`, adminID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("revoke administrator grant: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit grant revocation: %v", err)
	}
	if err := <-done; !errors.Is(err, ErrSourceMutationForbidden) {
		t.Fatalf("demoted administrator upsert error = %v", err)
	}
	var name string
	var disabled bool
	if err := sqlDB.QueryRow(`SELECT name, disabled FROM Sources WHERE source_id=$1`, sourceID).Scan(&name, &disabled); err != nil {
		t.Fatalf("read source after admin denial: %v", err)
	}
	if name != "race-source" || disabled {
		t.Fatalf("demoted administrator changed source name=%q disabled=%v", name, disabled)
	}
}

func TestAuthenticatedNewSourceInsertSerializesWithDisable(t *testing.T) {
	db, sqlDB, _, ownerID, _, ownerName, _ := newAuthenticatedSourceRaceFixture(t)
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin user lock: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`SELECT user_id FROM Users WHERE user_id=$1 FOR UPDATE`, ownerID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock user: %v", err)
	}
	newURL := fmt.Sprintf("https://race-new-%d.example/", time.Now().UnixNano())
	done := make(chan error, 1)
	go func() {
		_, _, err := UpsertAuthenticatedSourceContext(context.Background(), db, &Source{
			URL: newURL, Name: "new-race-source", Priority: "low", Restricted: 1,
		}, cfg.SourceConfig{}, ownerID, ownerName, false)
		done <- err
	}()
	waitForPostgresLockWait(t, sqlDB, "%FROM Users%")
	if _, err := tx.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=$1`, ownerID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("disable owner: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit owner disable: %v", err)
	}
	if err := <-done; !errors.Is(err, ErrSourceMutationForbidden) {
		t.Fatalf("new-source upsert error = %v", err)
	}
	var sources, provenance int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Sources WHERE url=$1`, newURL).Scan(&sources); err != nil {
		t.Fatalf("count new source: %v", err)
	}
	if sources != 0 {
		t.Fatalf("disabled user committed %d new sources", sources)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM SourceOwnership so JOIN Sources s ON s.source_id=so.source_id WHERE s.url=$1`, newURL).Scan(&provenance); err != nil {
		t.Fatalf("count new provenance: %v", err)
	}
	if provenance != 0 {
		t.Fatalf("disabled user committed %d new provenance rows", provenance)
	}
}
