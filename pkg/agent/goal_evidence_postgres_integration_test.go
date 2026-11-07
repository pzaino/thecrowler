//go:build integration

package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func integrationDBConfig(t *testing.T) cfg.Config {
	t.Helper()
	value := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	port, err := strconv.Atoi(value("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	return cfg.Config{Database: cfg.Database{
		Type:         cdb.DBPostgresStr,
		Host:         value("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"),
		Port:         port,
		User:         value("DOCKER_POSTGRES_DB_USER", "postgres"),
		Password:     value("DOCKER_POSTGRES_PASSWORD", "postgres"),
		DBName:       value("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode:      value("DOCKER_POSTGRES_SSL_MODE", "disable"),
		MaxConns:     10,
		MaxIdleConns: 5,
	}}
}

func integrationGoalSpec(sourceID uint64, criterion CriterionSpec) *AgentGoalSpec {
	spec := goalEvalSpec(AcceptanceAll, criterion)
	spec.GoalID = "wp02-source-lookup-integration"
	spec.SpecHash = "wp02-source-lookup-integration-spec"
	spec.AllowedSources = []uint64{sourceID}
	spec.Evidence.Allow = []EvidenceKind{EvidenceSourceStatus}
	spec.Budgets = GoalBudgets{
		MaxModelRounds: 2, MaxToolCalls: 2,
		MaxEvidenceRecords: 8, MaxEvidenceBytes: 32768,
		MaxTotalResultBytes: 65536, MaxElapsed: time.Minute,
		MaxConsecutiveDenials: 2,
	}
	return spec
}

func statusCountCriterion(sourceID uint64, status string, op CompareOp, value float64) CriterionSpec {
	return CriterionSpec{
		ID: "status_count", Description: "status evidence count",
		Predicate: Predicate{
			Kind: PredicateEvidenceCount, Evidence: EvidenceSourceStatus,
			Where: &EvidenceSelector{SourceID: &sourceID, Status: status},
			Op:    op, Value: value,
		},
	}
}

func evaluateIntegrationGoal(t *testing.T, db cdb.Handler, spec *AgentGoalSpec, sourceID uint64) EvaluationResult {
	t.Helper()
	now := time.Now().UTC()
	ledger := NewGoalEvidenceLedger(
		"integration-run", "integration-nonce", "integration-trace",
		spec.GoalID, spec.SpecHash, "integration-agent", "integration-owner",
		[]uint64{sourceID}, spec.Evidence, spec.Budgets, now.Add(time.Hour),
	)
	queries := map[string]EvidenceQueryResult{}
	registry := NewDefaultGoalEvidenceRegistry(db)
	if err := refreshGoalEvidence(context.Background(), spec, registry, ledger, queries, now); err != nil {
		t.Fatalf("refresh evidence: %v", err)
	}
	evaluation, err := EvaluateGoal(GoalEvalInput{
		Spec: spec, Records: ledger.Snapshot(), Queries: queries, Now: now,
		LedgerID: ledger.ID(), RunID: "integration-run", GoalID: spec.GoalID,
		GoalSpecHash: spec.SpecHash, RunNonceHash: ledger.RunNonceHash(),
		AgentID: "integration-agent", Owner: "integration-owner",
	})
	if err != nil {
		t.Fatalf("evaluate goal: %v", err)
	}
	return evaluation
}

// TestPostgresDefaultGoalEvidenceProviderSourceLookup exercises the shipped
// database evidence provider against a disposable PostgreSQL database. It
// proves that source lookup failures cannot satisfy a zero-result criterion,
// while a completed query for an existing source can still prove zero.
func TestPostgresDefaultGoalEvidenceProviderSourceLookup(t *testing.T) {
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	config := integrationDBConfig(t)
	db, err := cdb.NewHandler(config)
	if err != nil {
		t.Fatalf("create database handler: %v", err)
	}
	if err := db.Connect(config); err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	suffix := time.Now().UnixNano()
	source := &cdb.Source{
		URL:        fmt.Sprintf("https://wp02-evidence-%d.example/", suffix),
		Name:       fmt.Sprintf("wp02-evidence-%d", suffix),
		Priority:   "high",
		Restricted: 1,
	}
	sourceID, err := cdb.CreateSourceContext(ctx, &db, source, cfg.SourceConfig{})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID)
	})

	zeroSpec := integrationGoalSpec(sourceID, statusCountCriterion(sourceID, "completed", CompareEq, 0))

	// The source exists and is status "new". A completed query filtered for
	// status "completed" validly proves zero matching status records.
	if evaluation := evaluateIntegrationGoal(t, db, zeroSpec, sourceID); evaluation.State != "met" {
		t.Fatalf("valid completed zero result was not accepted: %+v", evaluation)
	}

	// A matching status makes the zero criterion false and a gte threshold true.
	if _, err := db.Exec(`UPDATE Sources SET status='completed', last_updated_at=NOW(), last_crawled_at=NOW() WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("set source status: %v", err)
	}
	if evaluation := evaluateIntegrationGoal(t, db, zeroSpec, sourceID); evaluation.State == "met" {
		t.Fatalf("zero criterion passed with matching evidence: %+v", evaluation)
	}
	atLeastOneSpec := integrationGoalSpec(sourceID, statusCountCriterion(sourceID, "completed", CompareGte, 1))
	if evaluation := evaluateIntegrationGoal(t, db, atLeastOneSpec, sourceID); evaluation.State != "met" {
		t.Fatalf("matching status did not satisfy the positive criterion: %+v", evaluation)
	}

	// An unknown source must not be treated as a completed zero-result query.
	missingID := sourceID + 1000000
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM Sources WHERE source_id=$1)`, missingID).Scan(&exists); err != nil {
		t.Fatalf("check missing source: %v", err)
	}
	if exists {
		t.Fatalf("fixture source %d unexpectedly exists", missingID)
	}
	missingSpec := integrationGoalSpec(missingID, statusCountCriterion(missingID, "completed", CompareEq, 0))
	missingSpec.AllowedSources = []uint64{missingID}
	if evaluation := evaluateIntegrationGoal(t, db, missingSpec, missingID); evaluation.State == "met" {
		t.Fatalf("unknown source proved zero status records: %+v", evaluation)
	}

	if _, err := db.Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("delete source: %v", err)
	}

	// A closed database must be unavailable, never a successful empty query.
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if evaluation := evaluateIntegrationGoal(t, db, zeroSpec, sourceID); evaluation.State == "met" {
		t.Fatalf("closed database proved zero status records: %+v", evaluation)
	}
}
