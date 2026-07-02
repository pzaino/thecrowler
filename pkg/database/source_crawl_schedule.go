package database

import (
	"context"
	"fmt"
)

// ScheduleSourceCrawl makes an idle source eligible for the existing atomic
// engine claim path. It deliberately leaves processing sources unchanged: a
// listener hint is advisory, and changing an active source back to pending
// could let another engine claim it before the current crawl finishes. Missing,
// disabled, and processing sources are intentionally no-ops.
func ScheduleSourceCrawl(ctx context.Context, db *Handler, sourceID uint64) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if sourceID == 0 {
		return fmt.Errorf("source ID is required")
	}

	placeholder := "$1"
	if normalizeInformationSeedDBMS((*db).DBMS()) != DBPostgresStr {
		placeholder = "?"
	}
	_, err := (*db).ExecContext(ctx, `UPDATE Sources
		SET status = 'pending'
		WHERE source_id = `+placeholder+`
		  AND disabled = FALSE
		  AND COALESCE(LOWER(TRIM(status)), '') <> 'processing'`, sourceID)
	if err != nil {
		return fmt.Errorf("schedule source %d crawl: %w", sourceID, err)
	}
	return nil
}
