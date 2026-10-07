//go:build integration

package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// TestPostgresStatementLevelArtifactCleanup verifies the v1.16 cleanup
// triggers against a live PostgreSQL: bulk and single-row deletes must remove
// ObjectAttributes/EntityMemberships/ObjectCorrelations exactly for the
// deleted artifacts, unrelated rows must survive, an aborted transaction must
// restore the cleaned state, and the trigger must actually be statement-level
// with the legacy per-row function gone.
func TestPostgresStatementLevelArtifactCleanup(t *testing.T) {
	_, sqlDB := openPostgresIntegrationTestDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	assertTriggerShape(ctx, t, sqlDB)

	suffix := fmt.Sprintf("cleanup%d", time.Now().UnixNano())
	f := insertArtifactCleanupFixture(ctx, t, sqlDB, suffix)
	t.Cleanup(func() { cleanupArtifactCleanupFixture(context.Background(), sqlDB, f) })

	countWhere := func(query string, args ...interface{}) int {
		t.Helper()
		var n int
		if err := sqlDB.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", query, err)
		}
		return n
	}
	attrsOf := func(objectType string, objectID uint64) int {
		return countWhere(`SELECT count(*) FROM ObjectAttributes WHERE object_type=$1 AND object_id=$2`, objectType, objectID)
	}
	memOf := func(objectType string, objectID uint64) int {
		return countWhere(`SELECT count(*) FROM EntityMemberships WHERE object_type=$1 AND object_id=$2`, objectType, objectID)
	}
	corOfSide := func(side int, objectType string, objectID uint64) int {
		return countWhere(fmt.Sprintf(`SELECT count(*) FROM ObjectCorrelations WHERE object_type_%d=$1 AND object_id_%d=$2`, side, side), objectType, objectID)
	}

	t.Run("rollback restores cleaned rows", func(t *testing.T) {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM WebObjects WHERE object_id=$1`, f.wo1); err != nil {
			t.Fatal(err)
		}
		var inTx int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM ObjectAttributes WHERE object_type='webobject' AND object_id=$1`, f.wo1).Scan(&inTx); err != nil {
			t.Fatal(err)
		}
		if inTx != 0 {
			t.Fatalf("attributes inside deleting transaction = %d, want 0", inTx)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if n := attrsOf("webobject", f.wo1); n != 1 {
			t.Fatalf("attributes after rollback = %d, want 1", n)
		}
		if n := corOfSide(1, "webobject", f.wo1); n != 2 {
			t.Fatalf("correlations after rollback = %d, want 2", n)
		}
	})

	t.Run("bulk delete cleans deleted set only", func(t *testing.T) {
		res, err := sqlDB.ExecContext(ctx, `DELETE FROM WebObjects WHERE object_id IN ($1,$2)`, f.wo1, f.wo2)
		if err != nil {
			t.Fatal(err)
		}
		if rows, _ := res.RowsAffected(); rows != 2 {
			t.Fatalf("bulk deleted rows = %d, want 2", rows)
		}
		for _, id := range []uint64{f.wo1, f.wo2} {
			if n := attrsOf("webobject", id); n != 0 {
				t.Errorf("attributes for deleted webobject %d = %d, want 0", id, n)
			}
			if n := memOf("webobject", id); n != 0 {
				t.Errorf("memberships for deleted webobject %d = %d, want 0", id, n)
			}
			if n := corOfSide(1, "webobject", id) + corOfSide(2, "webobject", id); n != 0 {
				t.Errorf("correlations for deleted webobject %d = %d, want 0", id, n)
			}
		}
		if n := attrsOf("webobject", f.wo3); n != 1 {
			t.Errorf("attributes for surviving webobject = %d, want 1", n)
		}
		if n := memOf("webobject", f.wo3); n != 1 {
			t.Errorf("memberships for surviving webobject = %d, want 1", n)
		}
		if n := countWhere(`SELECT count(*) FROM ObjectCorrelations WHERE object_type_1='webobject' AND object_id_1=$1 AND object_type_2='webobject' AND object_id_2=$2`, f.wo3, f.wo4); n != 1 {
			t.Errorf("unrelated correlation rows = %d, want 1", n)
		}
	})

	t.Run("single row delete per artifact type", func(t *testing.T) {
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM NetInfo WHERE netinfo_id=$1`, f.ni1); err != nil {
			t.Fatal(err)
		}
		if n := attrsOf("netinfo", f.ni1); n != 0 {
			t.Errorf("attributes for deleted netinfo = %d, want 0", n)
		}
		if n := attrsOf("netinfo", f.ni2); n != 1 {
			t.Errorf("attributes for surviving netinfo = %d, want 1", n)
		}
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM HTTPInfo WHERE httpinfo_id=$1`, f.hi1); err != nil {
			t.Fatal(err)
		}
		if n := attrsOf("httpinfo", f.hi1); n != 0 {
			t.Errorf("attributes for deleted httpinfo = %d, want 0", n)
		}
		if n := attrsOf("httpinfo", f.hi2); n != 1 {
			t.Errorf("attributes for surviving httpinfo = %d, want 1", n)
		}
		if n := countWhere(`SELECT count(*) FROM ObjectCorrelations WHERE object_type_1='httpinfo' AND object_id_1=$1`, f.hi2); n != 1 {
			t.Errorf("unrelated httpinfo correlation = %d, want 1", n)
		}
	})
}

func assertTriggerShape(ctx context.Context, t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, trigger := range []string{"trg_cleanup_webobject", "trg_cleanup_netinfo", "trg_cleanup_httpinfo"} {
		var tgtype int
		if err := sqlDB.QueryRowContext(ctx, `SELECT tgtype FROM pg_trigger WHERE NOT tgisinternal AND tgname=$1`, trigger).Scan(&tgtype); err != nil {
			t.Fatalf("locate %s: %v", trigger, err)
		}
		if tgtype&1 != 0 {
			t.Errorf("%s tgtype=%d, want statement-level (row bit clear)", trigger, tgtype)
		}
		if tgtype&8 == 0 {
			t.Errorf("%s tgtype=%d, want DELETE trigger", trigger, tgtype)
		}
	}
	var legacy string
	if err := sqlDB.QueryRowContext(ctx, `SELECT coalesce(to_regprocedure('cleanup_artifact_data()')::text, '')`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != "" {
		t.Error("legacy per-row cleanup_artifact_data() still exists")
	}
	var current string
	if err := sqlDB.QueryRowContext(ctx, `SELECT version FROM DBSchemaVersion WHERE is_current`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != RequiredSchemaVersion {
		t.Errorf("current schema version = %s, want %s", current, RequiredSchemaVersion)
	}
}

type artifactCleanupFixture struct {
	wo1, wo2, wo3, wo4 uint64
	ni1, ni2           uint64
	hi1, hi2           uint64
	ruleID, entityID   uint64
}

func insertArtifactCleanupFixture(ctx context.Context, t *testing.T, sqlDB *sql.DB, suffix string) artifactCleanupFixture {
	t.Helper()
	var f artifactCleanupFixture
	insertID := func(query string, args ...interface{}) uint64 {
		t.Helper()
		var id uint64
		if err := sqlDB.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.wo1 = insertID(`INSERT INTO WebObjects(object_hash, details) VALUES($1,'{}') RETURNING object_id`, "wo1"+suffix)
	f.wo2 = insertID(`INSERT INTO WebObjects(object_hash, details) VALUES($1,'{}') RETURNING object_id`, "wo2"+suffix)
	f.wo3 = insertID(`INSERT INTO WebObjects(object_hash, details) VALUES($1,'{}') RETURNING object_id`, "wo3"+suffix)
	f.wo4 = insertID(`INSERT INTO WebObjects(object_hash, details) VALUES($1,'{}') RETURNING object_id`, "wo4"+suffix)
	f.ni1 = insertID(`INSERT INTO NetInfo(details_hash, details) VALUES($1,'{}') RETURNING netinfo_id`, "ni1"+suffix)
	f.ni2 = insertID(`INSERT INTO NetInfo(details_hash, details) VALUES($1,'{}') RETURNING netinfo_id`, "ni2"+suffix)
	f.hi1 = insertID(`INSERT INTO HTTPInfo(details_hash, details) VALUES($1,'{}') RETURNING httpinfo_id`, "hi1"+suffix)
	f.hi2 = insertID(`INSERT INTO HTTPInfo(details_hash, details) VALUES($1,'{}') RETURNING httpinfo_id`, "hi2"+suffix)
	f.ruleID = insertID(`INSERT INTO CorrelationRules(rule_name) VALUES($1) RETURNING rule_id`, suffix)
	f.entityID = insertID(`INSERT INTO Entities(entity_type) VALUES('test') RETURNING entity_id`)

	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO ObjectAttributes(object_id, object_type, attribute_key, attribute_value, normalized_value, value_hash)
		SELECT v.id, v.t, 'k', 'v', 'v', repeat('a',64)
		FROM (VALUES ($1::bigint,'webobject'),($2::bigint,'webobject'),($3::bigint,'webobject'),($4::bigint,'webobject'),
		      ($5::bigint,'netinfo'),($6::bigint,'netinfo'),($7::bigint,'httpinfo'),($8::bigint,'httpinfo'))
		      AS v(id,t)`,
		f.wo1, f.wo2, f.wo3, f.wo4, f.ni1, f.ni2, f.hi1, f.hi2); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO EntityMemberships(entity_id, object_id, object_type)
		SELECT $1, v.id, v.t
		FROM (VALUES ($2::bigint,'webobject'),($3::bigint,'webobject'),($4::bigint,'webobject'),($5::bigint,'webobject'),
		      ($6::bigint,'netinfo'),($7::bigint,'netinfo'),($8::bigint,'httpinfo'),($9::bigint,'httpinfo'))
		      AS v(id,t)`,
		f.entityID, f.wo1, f.wo2, f.wo3, f.wo4, f.ni1, f.ni2, f.hi1, f.hi2); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO ObjectCorrelations(object_type_1,object_id_1,object_type_2,object_id_2,rule_id,score)
		VALUES
		  ('webobject',$1,'webobject',$2,$3,0.5),
		  ('webobject',$1,'webobject',$4,$3,0.5),
		  ('webobject',$4,'webobject',$5,$3,0.5),
		  ('httpinfo',$6,'netinfo',$7,$3,0.5)`,
		f.wo1, f.wo2, f.ruleID, f.wo3, f.wo4, f.hi2, f.ni2); err != nil {
		t.Fatal(err)
	}
	return f
}

func cleanupArtifactCleanupFixture(ctx context.Context, sqlDB *sql.DB, f artifactCleanupFixture) {
	woIDs := []int64{int64(f.wo1), int64(f.wo2), int64(f.wo3), int64(f.wo4)}
	niIDs := []int64{int64(f.ni1), int64(f.ni2)}
	hiIDs := []int64{int64(f.hi1), int64(f.hi2)}
	statements := []struct {
		query string
		arg   interface{}
	}{
		{`DELETE FROM ObjectCorrelations WHERE rule_id=$1`, f.ruleID},
		{`DELETE FROM ObjectAttributes WHERE object_type='webobject' AND object_id = ANY($1)`, woIDs},
		{`DELETE FROM EntityMemberships WHERE object_type='webobject' AND object_id = ANY($1)`, woIDs},
		{`DELETE FROM WebObjects WHERE object_id = ANY($1)`, woIDs},
		{`DELETE FROM Entities WHERE entity_id=$1`, f.entityID},
		{`DELETE FROM CorrelationRules WHERE rule_id=$1`, f.ruleID},
		{`DELETE FROM ObjectAttributes WHERE object_type='netinfo' AND object_id = ANY($1)`, niIDs},
		{`DELETE FROM EntityMemberships WHERE object_type='netinfo' AND object_id = ANY($1)`, niIDs},
		{`DELETE FROM NetInfo WHERE netinfo_id = ANY($1)`, niIDs},
		{`DELETE FROM ObjectAttributes WHERE object_type='httpinfo' AND object_id = ANY($1)`, hiIDs},
		{`DELETE FROM EntityMemberships WHERE object_type='httpinfo' AND object_id = ANY($1)`, hiIDs},
		{`DELETE FROM HTTPInfo WHERE httpinfo_id = ANY($1)`, hiIDs},
	}
	for _, statement := range statements {
		// Best-effort cleanup: ignore errors so a failed assertion upstream
		// does not cascade into cleanup noise that hides the real failure.
		_, _ = sqlDB.ExecContext(ctx, statement.query, statement.arg)
	}
}
