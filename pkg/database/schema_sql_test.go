package database

import (
	"os"
	"strings"
	"testing"
)

func TestInformationSeedSchemaSQLContainsFreshInstallAndUpgradeCoverage(t *testing.T) {
	t.Parallel()

	commonFragments := []string{
		"status VARCHAR(50)",
		"priority VARCHAR(64)",
		"engine VARCHAR(256)",
		"last_processed_at",
		"last_error_at",
		"attempts",
		"CREATE TABLE IF NOT EXISTS InformationSeedCandidate",
		"normalized_url",
		"decision_status",
		"run_attempt",
		"discovery_provider",
		"discovery_query",
		"discovery_rank",
		"candidate_score",
		"candidate_reason",
		"discovery_metadata",
		"idx_informationseed_claim_queue",
		"idx_informationseed_engine_status",
		"idx_informationseedcandidate_seed_decision",
		"idx_informationseedcandidate_normalized_url",
		"idx_sourceinformationseedindex_seed_source",
		"idx_sourceinformationseedindex_provider_rank",
	}

	files := []string{
		"postgresql-setup.pgsql",
		"mysql-setup.mysql",
		"sqlite-setup.sqlite3",
		"postgresql-migration-v1.8.pgsql",
		"mysql-migration-v1.8.mysql",
		"sqlite-migration-v1.8.sqlite3",
	}

	for _, file := range files {
		file := file
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			upperContent := strings.ToUpper(string(content))
			for _, fragment := range commonFragments {
				if !strings.Contains(upperContent, strings.ToUpper(fragment)) {
					t.Fatalf("%s missing schema fragment %q", file, fragment)
				}
			}
		})
	}
}

func TestPostgresInformationSeedClaimFunctionIsAtomicAndUpgradeSafe(t *testing.T) {
	t.Parallel()

	files := []string{"postgresql-setup.pgsql", "postgresql-migration-v1.10.pgsql"}
	fragments := []string{
		"CREATE OR REPLACE FUNCTION update_informationseed(",
		"FOR UPDATE SKIP LOCKED",
		"UPDATE InformationSeed AS claimed_seed",
		"SET status = 'processing'",
		"engine = p_engineID",
		"last_processed_at = NOW()",
		"attempts = COALESCE(claimed_seed.attempts, 0) + 1",
		"RETURNING claimed_seed.information_seed_id",
	}

	for _, file := range files {
		file := file
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			upperContent := strings.ToUpper(string(content))
			for _, fragment := range fragments {
				if !strings.Contains(upperContent, strings.ToUpper(fragment)) {
					t.Fatalf("%s missing atomic Information Seed claim fragment %q", file, fragment)
				}
			}
		})
	}
}

func TestTimeSeriesSchemaSQLContainsFreshInstallAndUpgradeCoverage(t *testing.T) {
	t.Parallel()

	commonFragments := []string{
		"CREATE TABLE IF NOT EXISTS TimeSeriesMetrics",
		"metric_key",
		"display_name",
		"source_kind",
		"value_type",
		"aggregate",
		"bucket",
		"time_basis",
		"dedupe_scope",
		"failure_policy",
		"retention_policy",
		"cardinality_policy",
		"store_value_text",
		"hash_only",
		"CREATE TABLE IF NOT EXISTS TimeSeriesObservations",
		"observed_at",
		"effective_at",
		"collected_at",
		"source_updated_at",
		"information_seed_candidate_id",
		"source_information_seed_id",
		"subject_type",
		"correlation_rule_id",
		"value_numeric",
		"value_integer",
		"value_boolean",
		"value_text",
		"value_json",
		"value_timestamp",
		"previous_value_hash",
		"is_changed",
		"change_delta_numeric",
		"dedupe_key",
		"provenance",
		"CREATE TABLE IF NOT EXISTS TimeSeriesAggregates",
		"numeric_sum",
		"numeric_avg",
		"percentile_50",
		"percentile_90",
		"percentile_95",
		"percentile_99",
		"first_observation_id",
		"last_observation_id",
		"change_count",
		"aggregate_hash",
		"idx_timeseriesobservations_metric_bucket",
		"idx_timeseriesobservations_seed_candidate",
		"idx_timeseriesobservations_subject",
		"idx_timeseriesobservations_correlation_rule",
		"idx_timeseriesaggregates_metric_bucket",
		"idx_timeseriesaggregates_aggregate_hash",
		"ON DELETE SET NULL",
	}

	files := []string{
		"postgresql-setup.pgsql",
		"mysql-setup.mysql",
		"sqlite-setup.sqlite3",
		"postgresql-migration-v1.9.pgsql",
		"mysql-migration-v1.9.mysql",
		"sqlite-migration-v1.9.sqlite3",
	}

	for _, file := range files {
		file := file
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			upperContent := strings.ToUpper(string(content))
			for _, fragment := range commonFragments {
				if !strings.Contains(upperContent, strings.ToUpper(fragment)) {
					t.Fatalf("%s missing time-series schema fragment %q", file, fragment)
				}
			}
		})
	}
}

func TestTimeSeriesSchemaSQLUsesDialectAppropriateJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file      string
		fragments []string
	}{
		{
			file: "postgresql-migration-v1.9.pgsql",
			fragments: []string{
				"selector JSONB NOT NULL",
				"dimensions JSONB",
				"USING GIN(dimensions)",
			},
		},
		{
			file: "mysql-migration-v1.9.mysql",
			fragments: []string{
				"selector JSON NOT NULL",
				"dimensions JSON",
				"KEY idx_timeseriesobservations_metric_bucket",
			},
		},
		{
			file: "sqlite-migration-v1.9.sqlite3",
			fragments: []string{
				"canonical JSON encoded in TEXT",
				"selector TEXT NOT NULL",
				"dimensions TEXT",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.file, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(test.file)
			if err != nil {
				t.Fatalf("read %s: %v", test.file, err)
			}
			upperContent := strings.ToUpper(string(content))
			for _, fragment := range test.fragments {
				if !strings.Contains(upperContent, strings.ToUpper(fragment)) {
					t.Fatalf("%s missing dialect fragment %q", test.file, fragment)
				}
			}
		})
	}
}
