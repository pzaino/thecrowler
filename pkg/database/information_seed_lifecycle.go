// Copyright 2023 Paolo Fabio Zaino
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

package database

import (
	"context"
	"fmt"
	"strings"
)

// RerunInformationSeed queues a completed or errored information seed for a new
// discovery attempt. Seeds that are already in a scheduler-visible state are not
// mutated, but schedulers are still woken so pending work can be claimed without
// waiting for the next poll.
func RerunInformationSeed(db *Handler, id uint64) error {
	return RerunInformationSeedContext(context.Background(), db, id)
}
func RerunInformationSeedContext(ctx context.Context, db *Handler, id uint64) error {
	seed, err := GetInformationSeedByIDContext(ctx, db, id)
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(seed.Status)) {
	case "completed", "error", "exhausted", "new", "pending":
		if err := requeueInformationSeedContext(ctx, db, id); err != nil {
			return err
		}
	case "processing":
		// Leave the active lifecycle state intact; processing rows are governed by
		// claim-lease recovery and cannot be stolen by a manual rerun request.
	default:
		return fmt.Errorf("information seed %d cannot be rerun from status %q", id, seed.Status)
	}

	WakeInformationSeedSchedulers()
	return nil
}

func requeueInformationSeedContext(ctx context.Context, db *Handler, id uint64) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed requeue: %s", (*db).DBMS())
	}
	p := newInformationSeedPlaceholders(dbms)
	nowExpr := "CURRENT_TIMESTAMP"
	if dbms == DBPostgresStr {
		nowExpr = "NOW()"
	}
	query := `UPDATE InformationSeed
		SET status = 'pending', attempts = 0, last_error = NULL, last_error_at = NULL,
			next_retry_at = NULL, claim_token = NULL, fencing_token = 0,
			lease_expires_at = NULL, heartbeat_at = NULL, last_processed_at = ` + nowExpr + `
		WHERE information_seed_id = ` + p.Next() + ` AND deleted_at IS NULL`
	if dbms == DBMySQLStr {
		query = `UPDATE InformationSeed
			SET status = 'pending', attempts = 0, last_error = NULL, last_error_at = NULL,
				next_retry_at = NULL, claim_token = NULL, fencing_token = 0,
				lease_expires_at = NULL, heartbeat_at = NULL, last_processed_at = CURRENT_TIMESTAMP
			WHERE information_seed_id = ?`
	}
	result, err := (*db).ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to requeue information seed %d: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return fmt.Errorf("no information seed found with ID %d", id)
	}
	return nil
}

// DisableInformationSeed prevents a seed from being claimed by schedulers.
func DisableInformationSeed(db *Handler, id uint64) error {
	return DisableInformationSeedContext(context.Background(), db, id)
}
func DisableInformationSeedContext(ctx context.Context, db *Handler, id uint64) error {
	return SetInformationSeedDisabledContext(ctx, db, id, true)
}

// EnableInformationSeed clears the disabled flag. When queuePending is true, the
// seed is also moved to pending so the scheduler can claim it immediately.
func EnableInformationSeed(db *Handler, id uint64, queuePending bool) error {
	return EnableInformationSeedContext(context.Background(), db, id, queuePending)
}
func EnableInformationSeedContext(ctx context.Context, db *Handler, id uint64, queuePending bool) error {
	if err := SetInformationSeedDisabledContext(ctx, db, id, false); err != nil {
		return err
	}
	if queuePending {
		if err := requeueInformationSeedContext(ctx, db, id); err != nil {
			return err
		}
	}
	WakeInformationSeedSchedulers()
	return nil
}
