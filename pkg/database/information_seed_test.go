package database

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestInformationSeedCRUDAndLinksSQLite(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	createInformationSeedTestSchema(t, db)

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	config := json.RawMessage(`{"topic":"security"}`)
	seedID, err := CreateInformationSeed(&handler, &InformationSeed{
		CategoryID:      7,
		UsrID:           11,
		InformationSeed: " security dork ",
		Status:          "pending",
		Priority:        "high",
		Config:          &config,
	})
	if err != nil {
		t.Fatalf("create information seed: %v", err)
	}
	if seedID == 0 {
		t.Fatal("expected seed ID")
	}

	seed, err := GetInformationSeedByID(&handler, seedID)
	if err != nil {
		t.Fatalf("get information seed: %v", err)
	}
	if seed.InformationSeed != "security dork" || seed.Status != "pending" || seed.Priority != "high" || seed.CategoryID != 7 || seed.UsrID != 11 {
		t.Fatalf("unexpected seed: %#v", seed)
	}
	if seed.Config == nil || string(*seed.Config) != string(config) {
		t.Fatalf("unexpected seed config: %v", seed.Config)
	}

	matches, err := ListInformationSeeds(&handler, InformationSeedFilter{Status: "pending", Priority: "high", CategoryID: uint64Ptr(7), UserID: uint64Ptr(11), Limit: 10})
	if err != nil {
		t.Fatalf("list information seeds: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != seedID {
		t.Fatalf("unexpected list matches: %#v", matches)
	}

	if err = UpdateInformationSeedStatus(&handler, seedID, "error", "boom"); err != nil {
		t.Fatalf("update information seed status: %v", err)
	}
	seed, err = GetInformationSeedByID(&handler, seedID)
	if err != nil {
		t.Fatalf("get updated information seed: %v", err)
	}
	if seed.Status != "error" || !seed.LastError.Valid || seed.LastError.String != "boom" || !seed.LastErrorAt.Valid || !seed.LastProcessedAt.Valid {
		t.Fatalf("unexpected updated seed lifecycle: %#v", seed)
	}

	sourceIDs := []uint64{
		insertInformationSeedTestSource(t, db, "https://one.example", "one"),
		insertInformationSeedTestSource(t, db, "https://two.example", "two"),
	}
	links := []SourceSeedLink{
		{SourceID: sourceIDs[0], InformationSeedID: seedID},
		{SourceID: sourceIDs[1], InformationSeedID: seedID},
		{SourceID: sourceIDs[0], InformationSeedID: seedID},
	}
	if err = LinkSourcesToInformationSeed(&handler, links); err != nil {
		t.Fatalf("link sources to information seed: %v", err)
	}
	if err = LinkSourcesToInformationSeed(&handler, links); err != nil {
		t.Fatalf("link duplicate sources to information seed: %v", err)
	}

	sources, err := GetSourcesForInformationSeed(&handler, seedID)
	if err != nil {
		t.Fatalf("get sources for information seed: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected two linked sources, got %#v", sources)
	}
	if sources[0].ID != sourceIDs[0] || sources[1].ID != sourceIDs[1] {
		t.Fatalf("unexpected linked source order: %#v", sources)
	}
}

func TestClaimInformationSeedsSQLiteFiltersByPriority(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	createInformationSeedTestSchema(t, db)

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	lowID := createClaimInformationSeedTestSeed(t, &handler, db, "low seed", "low", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	highID := createClaimInformationSeedTestSeed(t, &handler, db, "high seed", "high", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	nextHighID := createClaimInformationSeedTestSeed(t, &handler, db, "next high seed", "high", time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))

	claimed, err := ClaimInformationSeeds(&handler, 10, " high ", "test-engine", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("claim high-priority information seeds: %v", err)
	}
	if len(claimed) != 2 || claimed[0].ID != highID || claimed[1].ID != nextHighID {
		t.Fatalf("expected high-priority seeds in creation order, got %#v", claimed)
	}
	for _, seed := range claimed {
		if seed.Priority != "high" || seed.Status != "processing" || seed.Engine != "test-engine" || !seed.LastProcessedAt.Valid || seed.Attempts != 1 {
			t.Fatalf("unexpected claimed seed: %#v", seed)
		}
	}

	lowSeed, err := GetInformationSeedByID(&handler, lowID)
	if err != nil {
		t.Fatalf("get low-priority information seed: %v", err)
	}
	if lowSeed.Status != "new" || lowSeed.Engine != "" || lowSeed.Attempts != 0 {
		t.Fatalf("priority filter claimed non-matching seed: %#v", lowSeed)
	}

	claimed, err = ClaimInformationSeeds(&handler, 10, "", "fallback-engine", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("claim information seeds without priority filter: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != lowID || claimed[0].Priority != "low" {
		t.Fatalf("expected unfiltered claim to pick remaining low-priority seed, got %#v", claimed)
	}
}

func TestClaimInformationSeedsSQLiteLifecycleEligibility(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	createInformationSeedTestSchema(t, db)

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	now := time.Now().UTC()
	newID := createLifecycleInformationSeedTestSeed(t, &handler, db, "new seed", "new", false, sql.NullTime{}, sql.NullTime{}, 0)
	pendingID := createLifecycleInformationSeedTestSeed(t, &handler, db, "pending seed", "pending", false, sql.NullTime{}, sql.NullTime{}, 0)
	disabledID := createLifecycleInformationSeedTestSeed(t, &handler, db, "disabled seed", "new", true, sql.NullTime{}, sql.NullTime{}, 0)
	freshProcessingID := createLifecycleInformationSeedTestSeed(t, &handler, db, "fresh processing seed", "processing", false, sql.NullTime{Time: now.Add(-10 * time.Minute), Valid: true}, sql.NullTime{}, 2)
	staleProcessingID := createLifecycleInformationSeedTestSeed(t, &handler, db, "stale processing seed", "processing", false, sql.NullTime{Time: now.Add(-2 * time.Hour), Valid: true}, sql.NullTime{}, 3)
	freshErrorID := createLifecycleInformationSeedTestSeed(t, &handler, db, "fresh error seed", "error", false, sql.NullTime{}, sql.NullTime{Time: now.Add(-10 * time.Minute), Valid: true}, 4)
	oldErrorID := createLifecycleInformationSeedTestSeed(t, &handler, db, "old error seed", "error", false, sql.NullTime{}, sql.NullTime{Time: now.Add(-2 * time.Hour), Valid: true}, 5)

	claimed, err := ClaimInformationSeeds(&handler, 10, "", "eligibility-engine", time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("claim information seeds by lifecycle eligibility: %v", err)
	}
	assertInformationSeedIDs(t, claimed, []uint64{newID, pendingID, staleProcessingID, oldErrorID})
	for _, seed := range claimed {
		if seed.Status != "processing" || seed.Engine != "eligibility-engine" || !seed.LastProcessedAt.Valid {
			t.Fatalf("unexpected claimed seed state: %#v", seed)
		}
	}
	assertInformationSeedAttempts(t, claimed, map[uint64]int{
		newID:             1,
		pendingID:         1,
		staleProcessingID: 4,
		oldErrorID:        6,
	})

	assertInformationSeedUnclaimed(t, &handler, disabledID, "new", "", 0)
	assertInformationSeedUnclaimed(t, &handler, freshProcessingID, "processing", "previous-engine", 2)
	assertInformationSeedUnclaimed(t, &handler, freshErrorID, "error", "", 4)
}

func TestInformationSeedSQLiteLinkSourceIdempotencyAndDiscoveryMetadata(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	createInformationSeedTestSchema(t, db)

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	seedID, err := CreateInformationSeed(&handler, &InformationSeed{InformationSeed: "metadata seed"})
	if err != nil {
		t.Fatalf("create information seed: %v", err)
	}
	sourceID := insertInformationSeedTestSource(t, db, "https://metadata.example", "metadata")

	if err = LinkSourceToInformationSeed(&handler, sourceID, seedID); err != nil {
		t.Fatalf("link source to information seed: %v", err)
	}
	if err = LinkSourceToInformationSeed(&handler, sourceID, seedID); err != nil {
		t.Fatalf("link duplicate source to information seed: %v", err)
	}
	assertSourceInformationSeedLinkCount(t, db, sourceID, seedID, 1)

	provider := "provider-a"
	query := "dork one"
	rank := 3
	score := 0.75
	reason := "promising"
	metadata := json.RawMessage(`{"first":true,"keep":"yes"}`)
	if err = LinkSourceToInformationSeedWithDiscoveryMetadata(&handler, sourceID, seedID, InformationSeedDiscoveryMetadata{
		DiscoveryProvider: &provider,
		DiscoveryQuery:    &query,
		DiscoveryRank:     &rank,
		CandidateScore:    &score,
		CandidateReason:   &reason,
		DiscoveryMetadata: &metadata,
	}); err != nil {
		t.Fatalf("link source with discovery metadata: %v", err)
	}

	updatedQuery := "dork two"
	updatedMetadata := json.RawMessage(`{"second":2,"keep":"updated"}`)
	if err = LinkSourceToInformationSeedWithDiscoveryMetadata(&handler, sourceID, seedID, InformationSeedDiscoveryMetadata{
		DiscoveryQuery:    &updatedQuery,
		DiscoveryMetadata: &updatedMetadata,
	}); err != nil {
		t.Fatalf("upsert source discovery metadata: %v", err)
	}
	assertSourceInformationSeedLinkCount(t, db, sourceID, seedID, 1)

	row := getSourceInformationSeedMetadataRow(t, db, sourceID, seedID)
	if row.discoveryProvider != provider || row.discoveryQuery != updatedQuery || row.discoveryRank != rank || row.candidateScore != score || row.candidateReason != reason {
		t.Fatalf("unexpected preserved/upserted discovery columns: %#v", row)
	}
	if row.discoveryMetadata["first"] != true || row.discoveryMetadata["second"] != float64(2) || row.discoveryMetadata["keep"] != "updated" {
		t.Fatalf("unexpected merged discovery metadata: %#v", row.discoveryMetadata)
	}
}

func createInformationSeedTestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE InformationSeed (
			information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			information_seed VARCHAR(256) NOT NULL,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			engine VARCHAR(256) DEFAULT '' NOT NULL,
			last_processed_at TIMESTAMP,
			last_error TEXT,
			last_error_at TIMESTAMP,
			disabled BOOLEAN DEFAULT FALSE,
			attempts INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		);
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			name VARCHAR(255),
			usr_id INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			url TEXT NOT NULL UNIQUE,
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			disabled BOOLEAN DEFAULT FALSE,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		);
		CREATE TABLE SourceInformationSeedIndex (
			source_information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			information_seed_id INTEGER NOT NULL,
			discovery_provider VARCHAR(255),
			discovery_query TEXT,
			discovery_rank INTEGER,
			candidate_score REAL,
			candidate_reason TEXT,
			discovery_metadata TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (source_id, information_seed_id),
			FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
			FOREIGN KEY(information_seed_id) REFERENCES InformationSeed(information_seed_id) ON DELETE CASCADE
		);`)
	if err != nil {
		t.Fatalf("create information seed test schema: %v", err)
	}
}

func createClaimInformationSeedTestSeed(t *testing.T, handler *Handler, db *sql.DB, text string, priority string, createdAt time.Time) uint64 {
	t.Helper()
	id, err := CreateInformationSeed(handler, &InformationSeed{
		InformationSeed: text,
		Priority:        priority,
		Status:          "new",
	})
	if err != nil {
		t.Fatalf("create %s information seed: %v", priority, err)
	}
	if _, err = db.Exec(`
		UPDATE InformationSeed
		SET created_at = ?, last_updated_at = ?
		WHERE information_seed_id = ?`, createdAt, createdAt, id); err != nil {
		t.Fatalf("set created_at for information seed %d: %v", id, err)
	}
	return id
}

func insertInformationSeedTestSource(t *testing.T, db *sql.DB, sourceURL, name string) uint64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO Sources (url, name, priority, category_id, usr_id, restricted, flags, config, disabled)
		VALUES (?, ?, 'medium', 7, 11, 2, 3, '{}', FALSE)`, sourceURL, name)
	if err != nil {
		t.Fatalf("insert source %s: %v", sourceURL, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get source id: %v", err)
	}
	return uint64(id)
}

func createLifecycleInformationSeedTestSeed(t *testing.T, handler *Handler, db *sql.DB, text, status string, disabled bool, lastProcessedAt, lastErrorAt sql.NullTime, attempts int) uint64 {
	t.Helper()
	id, err := CreateInformationSeed(handler, &InformationSeed{
		InformationSeed: text,
		Status:          status,
		Disabled:        disabled,
	})
	if err != nil {
		t.Fatalf("create lifecycle information seed %q: %v", text, err)
	}
	engine := ""
	if status == "processing" {
		engine = "previous-engine"
	}
	var lastError interface{}
	if lastErrorAt.Valid {
		lastError = "previous error"
	}
	if _, err = db.Exec(`
		UPDATE InformationSeed
		SET status = ?, disabled = ?, engine = ?, last_processed_at = ?, last_error = ?, last_error_at = ?, attempts = ?
		WHERE information_seed_id = ?`, status, disabled, engine, nullableTimeArg(lastProcessedAt), lastError, nullableTimeArg(lastErrorAt), attempts, id); err != nil {
		t.Fatalf("set lifecycle fields for information seed %d: %v", id, err)
	}
	return id
}

func nullableTimeArg(value sql.NullTime) interface{} {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func assertInformationSeedIDs(t *testing.T, seeds []InformationSeed, expected []uint64) {
	t.Helper()
	if len(seeds) != len(expected) {
		t.Fatalf("expected seed IDs %v, got %#v", expected, seeds)
	}
	for i, id := range expected {
		if seeds[i].ID != id {
			t.Fatalf("expected seed IDs %v, got %#v", expected, seeds)
		}
	}
}

func assertInformationSeedAttempts(t *testing.T, seeds []InformationSeed, expected map[uint64]int) {
	t.Helper()
	for _, seed := range seeds {
		if seed.Attempts != expected[seed.ID] {
			t.Fatalf("expected seed %d attempts %d, got %d", seed.ID, expected[seed.ID], seed.Attempts)
		}
	}
}

func assertInformationSeedUnclaimed(t *testing.T, handler *Handler, seedID uint64, expectedStatus, expectedEngine string, expectedAttempts int) {
	t.Helper()
	seed, err := GetInformationSeedByID(handler, seedID)
	if err != nil {
		t.Fatalf("get unclaimed information seed %d: %v", seedID, err)
	}
	if seed.Status != expectedStatus || seed.Engine != expectedEngine || seed.Attempts != expectedAttempts {
		t.Fatalf("unexpected unclaimed seed %d: status=%q engine=%q attempts=%d", seedID, seed.Status, seed.Engine, seed.Attempts)
	}
}

func assertSourceInformationSeedLinkCount(t *testing.T, db *sql.DB, sourceID, seedID uint64, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM SourceInformationSeedIndex
		WHERE source_id = ? AND information_seed_id = ?`, sourceID, seedID).Scan(&count); err != nil {
		t.Fatalf("count source/information seed links: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d source/information seed links, got %d", expected, count)
	}
}

type sourceInformationSeedMetadataTestRow struct {
	discoveryProvider string
	discoveryQuery    string
	discoveryRank     int
	candidateScore    float64
	candidateReason   string
	discoveryMetadata map[string]interface{}
}

func getSourceInformationSeedMetadataRow(t *testing.T, db *sql.DB, sourceID, seedID uint64) sourceInformationSeedMetadataTestRow {
	t.Helper()
	var (
		provider sql.NullString
		query    sql.NullString
		rank     sql.NullInt64
		score    sql.NullFloat64
		reason   sql.NullString
		metadata sql.NullString
	)
	if err := db.QueryRow(`
		SELECT discovery_provider, discovery_query, discovery_rank, candidate_score, candidate_reason, discovery_metadata
		FROM SourceInformationSeedIndex
		WHERE source_id = ? AND information_seed_id = ?`, sourceID, seedID).Scan(&provider, &query, &rank, &score, &reason, &metadata); err != nil {
		t.Fatalf("get source/information seed metadata row: %v", err)
	}
	if !provider.Valid || !query.Valid || !rank.Valid || !score.Valid || !reason.Valid || !metadata.Valid {
		t.Fatalf("expected all discovery metadata fields to be set: provider=%#v query=%#v rank=%#v score=%#v reason=%#v metadata=%#v", provider, query, rank, score, reason, metadata)
	}
	decoded := map[string]interface{}{}
	if err := json.Unmarshal([]byte(metadata.String), &decoded); err != nil {
		t.Fatalf("decode discovery metadata %q: %v", metadata.String, err)
	}
	return sourceInformationSeedMetadataTestRow{
		discoveryProvider: provider.String,
		discoveryQuery:    query.String,
		discoveryRank:     int(rank.Int64),
		candidateScore:    score.Float64,
		candidateReason:   reason.String,
		discoveryMetadata: decoded,
	}
}

func uint64Ptr(value uint64) *uint64 {
	return &value
}
