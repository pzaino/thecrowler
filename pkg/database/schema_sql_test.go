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
		"mysql-setup-v1.4.mysql",
		"sqlite-setup-v1.4.sqlite3",
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
