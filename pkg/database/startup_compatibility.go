package database

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	// RequiredSchemaVersion is the oldest (and only) database schema this writer understands.
	RequiredSchemaVersion = "1.15"
	// WriterGeneration changes whenever two writer implementations may not safely share a database.
	WriterGeneration = "reservation-v1.15"
)

var reservationColumns = map[string][]string{
	"TimeSeriesMetrics":               {"metric_id", "cardinality_policy"},
	"TimeSeriesObservations":          {"observation_id", "metric_id", "series_hash", "dimensions", "deleted_at"},
	"TimeSeriesActiveSeries":          {"metric_id", "series_hash", "series_identity", "reference_count"},
	"TimeSeriesActiveDimensionValues": {"metric_id", "dimension_key", "value_hash", "canonical_value", "reference_count"},
	"TimeSeriesObservationSeries":     {"observation_id", "metric_id", "series_hash"},
	"TimeSeriesObservationDimensions": {"observation_id", "metric_id", "dimension_key", "value_hash"},
}

// CheckStartupCompatibility verifies the schema contract used by reservation writes. It must be
// called after connecting and before starting any goroutine which can write to the database.
func CheckStartupCompatibility(ctx context.Context, db Handler) error {
	detected := "unavailable"
	if err := db.QueryRowContext(ctx, `SELECT version FROM DBSchemaVersion WHERE is_current = `+trueLiteral(db.DBMS())).Scan(&detected); err != nil {
		return compatibilityError(detected, []string{"DBSchemaVersion.current"}, err)
	}
	missing := make([]string, 0)
	if detected != RequiredSchemaVersion {
		missing = append(missing, "schema version")
	}

	required := cloneColumns(reservationColumns)
	switch normalizeDBMS(db.DBMS()) {
	case "postgres":
		required["TimeSeriesCardinalityTokens"] = []string{"token_number"}
		required["TimeSeriesSeriesSlots"] = []string{"metric_id", "slot_number", "series_hash", "identity_value"}
		required["TimeSeriesDimensionSlots"] = []string{"metric_id", "dimension_key", "slot_number", "value_hash", "identity_value"}
	case "mysql", "sqlite":
		required["TimeSeriesCardinalityMaintenanceLock"] = []string{"lock_id"}
	default:
		missing = append(missing, "supported database metadata provider")
	}
	required["DatabaseWriterCompatibility"] = []string{"lock_id", "writer_generation"}

	for table, columns := range required {
		if err := probeColumns(ctx, db, table, columns); err != nil {
			missing = append(missing, table+"("+strings.Join(columns, ",")+")")
		}
	}

	// These named indexes are part of the lookup/locking contract, not optional tuning.
	for _, index := range []string{"idx_ts_observation_series_identity", "idx_ts_observation_dimensions_identity"} {
		if !metadataObjectExists(ctx, db, "index", index) {
			missing = append(missing, "index "+index)
		}
	}
	// Foreign keys prevent normalized membership and active state from diverging; primary/unique
	// constraints are exercised by ON CONFLICT and reservation ownership.
	for _, table := range []string{"TimeSeriesMetrics", "TimeSeriesObservations", "TimeSeriesActiveSeries", "TimeSeriesActiveDimensionValues", "TimeSeriesObservationSeries", "TimeSeriesObservationDimensions"} {
		if !tableConstraintExists(ctx, db, table, "PRIMARY KEY") {
			missing = append(missing, "primary key on "+table)
		}
	}
	for _, table := range []string{"TimeSeriesActiveSeries", "TimeSeriesActiveDimensionValues", "TimeSeriesObservationSeries", "TimeSeriesObservationDimensions"} {
		if !tableConstraintExists(ctx, db, table, "FOREIGN KEY") {
			missing = append(missing, "foreign key on "+table)
		}
	}
	if normalizeDBMS(db.DBMS()) == "postgres" {
		for _, table := range []string{"TimeSeriesCardinalityTokens", "TimeSeriesSeriesSlots", "TimeSeriesDimensionSlots"} {
			if !tableConstraintExists(ctx, db, table, "PRIMARY KEY") {
				missing = append(missing, "primary key on "+table)
			}
		}
		for _, table := range []string{"TimeSeriesSeriesSlots", "TimeSeriesDimensionSlots"} {
			if !tableConstraintExists(ctx, db, table, "UNIQUE") || !tableConstraintExists(ctx, db, table, "FOREIGN KEY") {
				missing = append(missing, "unique/foreign key on "+table)
			}
		}
	}

	var generation string
	if err := db.QueryRowContext(ctx, `SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id = 1`).Scan(&generation); err != nil {
		missing = append(missing, "writer compatibility marker")
	} else if generation != WriterGeneration {
		sort.Strings(missing)
		return compatibilityError(detected, missing, fmt.Errorf("incompatible active writer generation %q (required %q)", generation, WriterGeneration))
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return compatibilityError(detected, missing, nil)
	}
	return nil
}

func compatibilityError(detected string, missing []string, cause error) error {
	detail := ""
	if len(missing) != 0 {
		detail = "; missing or invalid: " + strings.Join(missing, ", ")
	}
	if cause != nil {
		detail += "; cause: " + cause.Error()
	}
	return fmt.Errorf("fatal database compatibility check failed: required schema=%s writer_generation=%s, detected schema=%s%s; stop all old writers, apply database migrations through v%s, then restart only v%s-compatible services", RequiredSchemaVersion, WriterGeneration, detected, detail, RequiredSchemaVersion, RequiredSchemaVersion)
}

func cloneColumns(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}
func normalizeDBMS(s string) string {
	s = strings.ToLower(s)
	if strings.Contains(s, "post") {
		return "postgres"
	}
	if strings.Contains(s, "mysql") {
		return "mysql"
	}
	if strings.Contains(s, "sqlite") {
		return "sqlite"
	}
	return s
}
func trueLiteral(dbms string) string {
	if normalizeDBMS(dbms) == "sqlite" {
		return "1"
	}
	return "TRUE"
}
func probeColumns(ctx context.Context, db Handler, table string, columns []string) error {
	rows, err := db.QueryContext(ctx, `SELECT `+strings.Join(columns, ",")+` FROM `+table+` WHERE 1=0`)
	if err == nil {
		rows.Close()
	}
	return err
}

func metadataObjectExists(ctx context.Context, db Handler, kind, name string) bool {
	var n int
	switch normalizeDBMS(db.DBMS()) {
	case "sqlite":
		return db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type=? AND lower(name)=lower(?)`, kind, name).Scan(&n) == nil && n > 0
	case "postgres":
		return db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND lower(indexname)=lower($1)`, name).Scan(&n) == nil && n > 0
	case "mysql":
		return db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND lower(index_name)=lower(?)`, name).Scan(&n) == nil && n > 0
	}
	return false
}

func tableConstraintExists(ctx context.Context, db Handler, table, kind string) bool {
	var n int
	switch normalizeDBMS(db.DBMS()) {
	case "sqlite":
		// SQLite reports PKs in table_info and FKs separately. Unique reservation keys are PKs here.
		query := `SELECT count(*) FROM pragma_table_info(?) WHERE pk > 0`
		if kind == "FOREIGN KEY" {
			query = `SELECT count(*) FROM pragma_foreign_key_list(?)`
		}
		if kind == "UNIQUE" {
			query = `SELECT count(*) FROM pragma_index_list(?) WHERE "unique"=1`
		}
		return db.QueryRowContext(ctx, query, table).Scan(&n) == nil && n > 0
	case "postgres":
		return db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.table_constraints WHERE table_schema=current_schema() AND lower(table_name)=lower($1) AND constraint_type=$2`, table, kind).Scan(&n) == nil && n > 0
	case "mysql":
		return db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND lower(table_name)=lower(?) AND constraint_type=?`, table, kind).Scan(&n) == nil && n > 0
	}
	return false
}
