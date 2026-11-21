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
	"database/sql"
	"errors"
	"fmt"
)

// SourceEnabledForResume reports whether a live source is enabled for an
// automatic resume-eligibility decision. It is deliberately narrower than the
// owner lookup: it does not grant authority and does not change any source.
// A missing or soft-deleted source reports false with no error. Unsupported
// backends and database failures return an error so callers can classify them
// as unavailable rather than as a denial.
func SourceEnabledForResume(ctx context.Context, db *Handler, sourceID uint64) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, fmt.Errorf("database handler is nil")
	}
	if sourceID == 0 {
		return false, fmt.Errorf("source ID is required")
	}
	if !supportsCrawlOperationStore(*db) {
		return false, ErrAsyncGoalPersistenceUnsupported
	}
	query := `SELECT disabled FROM Sources WHERE source_id = ` +
		sourceOwnershipPlaceholder(*db, 1) + ` AND deleted_at IS NULL`
	var disabled bool
	err := (*db).QueryRowContext(ctx, query, sourceID).Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read source resume state: %w", err)
	}
	return !disabled, nil
}
