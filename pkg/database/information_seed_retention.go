package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// InformationSeedRetentionResult reports terminal seed rows considered and
// removed by one bounded retention pass.
type InformationSeedRetentionResult struct {
	Considered int
	Deleted    int
	DryRun     bool
}

// PurgeInformationSeedRetentionContext removes only old terminal seeds whose
// claim lease is absent or expired. New, pending, error, processing, actively
// leased, and user-disabled pending rows are deliberately retained.
func PurgeInformationSeedRetentionContext(ctx context.Context, db *Handler, policy cfg.InformationSeedRetentionConfig) (InformationSeedRetentionResult, error) {
	result := InformationSeedRetentionResult{DryRun: policy.DryRun}
	if db == nil || *db == nil {
		return result, fmt.Errorf("database handler is nil")
	}
	if !policy.Enabled {
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return result, fmt.Errorf("unsupported database type for information seed retention: %s", (*db).DBMS())
	}
	batchSize := policy.BatchSize
	if batchSize < 1 {
		batchSize = 100
	}
	if batchSize > 1000 {
		batchSize = 1000
	}
	now := time.Now().UTC()
	targets := []struct {
		status string
		cutoff time.Time
	}{
		{status: "completed", cutoff: now.Add(-time.Duration(policy.CompletedDays) * 24 * time.Hour)},
		{status: "disabled", cutoff: now.Add(-time.Duration(policy.DisabledDays) * 24 * time.Hour)},
		{status: "exhausted", cutoff: now.Add(-time.Duration(policy.ExhaustedDays) * 24 * time.Hour)},
	}
	ids := make([]uint64, 0, batchSize)
	for _, target := range targets {
		remaining := batchSize - len(ids)
		if remaining <= 0 {
			break
		}
		candidates, err := retentionCandidateIDsContext(ctx, db, dbms, target.status, target.cutoff, now, remaining)
		if err != nil {
			return result, err
		}
		ids = append(ids, candidates...)
	}
	result.Considered = len(ids)
	if len(ids) == 0 || policy.DryRun {
		return result, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+len(targets)*3)
	for index, id := range ids {
		placeholders[index] = informationSeedPlaceholderForDBMS(dbms, index+1)
		args = append(args, id)
	}
	conditionParts := make([]string, 0, len(targets))
	p := newInformationSeedPlaceholders(dbms)
	p.count = len(ids)
	for _, target := range targets {
		conditionParts = append(conditionParts, `(LOWER(TRIM(status)) = LOWER(TRIM(`+p.Next()+`)) AND last_updated_at < `+p.Next()+` AND (claim_token IS NULL OR lease_expires_at IS NULL OR lease_expires_at < `+p.Next()+`))`)
		args = append(args, target.status, target.cutoff, now)
	}
	query := fmt.Sprintf(`DELETE FROM InformationSeed WHERE information_seed_id IN (%s) AND (%s)`, strings.Join(placeholders, ","), strings.Join(conditionParts, " OR "))
	if dbms != DBMySQLStr {
		query += ` AND deleted_at IS NULL`
	}
	deleteResult, err := (*db).ExecContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("purge terminal information seeds: %w", err)
	}
	if rows, rowsErr := deleteResult.RowsAffected(); rowsErr == nil {
		result.Deleted = int(rows)
	}
	return result, nil
}

func retentionCandidateIDsContext(ctx context.Context, db *Handler, dbms, status string, cutoff, now time.Time, limit int) ([]uint64, error) {
	p := newInformationSeedPlaceholders(dbms)
	query := `SELECT information_seed_id FROM InformationSeed
		WHERE LOWER(TRIM(status)) = LOWER(TRIM(` + p.Next() + `))
		  AND last_updated_at < ` + p.Next() + `
		  AND (claim_token IS NULL OR lease_expires_at IS NULL OR lease_expires_at < ` + p.Next() + `)`
	args := []interface{}{status, cutoff, now}
	if dbms != DBMySQLStr {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY last_updated_at ASC, information_seed_id ASC LIMIT ` + p.Next()
	args = append(args, limit)
	rows, err := (*db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select retention candidates: %w", err)
	}
	return scanInformationSeedIDs(rows)
}
