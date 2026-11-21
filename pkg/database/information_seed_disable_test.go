package database

import (
	"context"
	"testing"
	"time"
)

// TestDisableInformationSeedPreservesActiveClaim is a lifecycle regression:
// disabling is a claim-exclusion flag, not a claim cancellation. Clearing the
// token while a worker owns the row strands it in "processing" because the
// worker's fenced finalize can no longer match the claim.
func TestDisableInformationSeedPreservesActiveClaim(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	createInformationSeedTestSchema(t, db)
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})

	id, err := CreateInformationSeed(&handler, &InformationSeed{
		InformationSeed: "disable active claim",
		Status:          "processing",
		Priority:        "low",
	})
	if err != nil {
		t.Fatalf("create information seed: %v", err)
	}
	if _, err = db.Exec(`UPDATE InformationSeed
		SET claim_token = 'claim-active', fencing_token = 7,
			lease_expires_at = CURRENT_TIMESTAMP, heartbeat_at = CURRENT_TIMESTAMP,
			attempts = 1
		WHERE information_seed_id = ?`, id); err != nil {
		t.Fatalf("seed active claim: %v", err)
	}

	if err = DisableInformationSeed(&handler, id); err != nil {
		t.Fatalf("disable information seed: %v", err)
	}
	var disabled bool
	var status, token string
	var fencing int64
	var hasLease, hasHeartbeat bool
	if err = db.QueryRow(`SELECT disabled, status, COALESCE(claim_token, ''), fencing_token,
		(lease_expires_at IS NOT NULL), (heartbeat_at IS NOT NULL)
		FROM InformationSeed WHERE information_seed_id = ?`, id).Scan(&disabled, &status, &token, &fencing, &hasLease, &hasHeartbeat); err != nil {
		t.Fatalf("read disabled seed: %v", err)
	}
	if !disabled || status != "processing" || token != "claim-active" || fencing != 7 || !hasLease || !hasHeartbeat {
		t.Fatalf("disable mutated the active claim: disabled=%t status=%q token=%q fencing=%d lease=%t heartbeat=%t", disabled, status, token, fencing, hasLease, hasHeartbeat)
	}

	if err = HeartbeatInformationSeedClaimContext(context.Background(), &handler, id, token, fencing, time.Minute); err != nil {
		t.Fatalf("heartbeat claimed seed after disable: %v", err)
	}
	if err = UpdateInformationSeedStatusFencedContext(context.Background(), &handler, id, token, fencing, "completed", "", nil); err != nil {
		t.Fatalf("finalize claimed seed after disable: %v", err)
	}
	var finalStatus, finalToken string
	var finalDisabled bool
	if err = db.QueryRow(`SELECT status, disabled, COALESCE(claim_token, '') FROM InformationSeed WHERE information_seed_id = ?`, id).Scan(&finalStatus, &finalDisabled, &finalToken); err != nil {
		t.Fatalf("read finalized seed: %v", err)
	}
	if finalStatus != "completed" || !finalDisabled || finalToken != "" {
		t.Fatalf("unexpected finalized state: status=%q disabled=%t token=%q", finalStatus, finalDisabled, finalToken)
	}
}
