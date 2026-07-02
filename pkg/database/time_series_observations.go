// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const timeSeriesObservationColumns = `observation_id, metric_id, observed_at, effective_at, collected_at,
	source_updated_at, bucket_start, bucket_end, information_seed_id, information_seed_candidate_id,
	source_id, source_information_seed_id, index_id, entity_id, subject_type, subject_id,
	object_type, object_id, correlation_rule_id, correlation_object_type_1, correlation_object_id_1,
	correlation_object_type_2, correlation_object_id_2, value_numeric, value_integer, value_boolean,
	value_text, value_json, value_timestamp, value_hash, previous_observation_id, previous_value_hash,
	is_changed, change_type, change_delta_numeric, change_detected_at, dedupe_key, dimensions,
	provenance, provenance_hash, created_at, deleted_at, last_updated_at`

// InsertTimeSeriesObservation inserts one fact. A duplicate dedupe_key returns a
// successful result with Duplicate=true and the existing observation ID.
func InsertTimeSeriesObservation(db *Handler, observation *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return TimeSeriesInsertResult{}, err
	}
	if observation == nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation is nil")
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("begin time-series observation transaction: %w", err)
	}
	result, err := insertTimeSeriesObservationTx(tx, dbms, observation)
	if err != nil {
		_ = (*db).Rollback(tx)
		return TimeSeriesInsertResult{}, err
	}
	if err = (*db).Commit(tx); err != nil {
		_ = (*db).Rollback(tx)
		return TimeSeriesInsertResult{}, fmt.Errorf("commit time-series observation: %w", err)
	}
	return result, nil
}

// InsertTimeSeriesObservations inserts a batch atomically. Duplicates are
// policy successes; any other error rolls the whole batch back.
func InsertTimeSeriesObservations(db *Handler, observations []TimeSeriesObservation) ([]TimeSeriesInsertResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if len(observations) == 0 {
		return []TimeSeriesInsertResult{}, nil
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("begin time-series observation batch: %w", err)
	}
	results := make([]TimeSeriesInsertResult, 0, len(observations))
	for i := range observations {
		result, insertErr := insertTimeSeriesObservationTx(tx, dbms, &observations[i])
		if insertErr != nil {
			_ = (*db).Rollback(tx)
			return nil, fmt.Errorf("insert time-series observation batch item %d: %w", i, insertErr)
		}
		results = append(results, result)
	}
	if err = (*db).Commit(tx); err != nil {
		_ = (*db).Rollback(tx)
		return nil, fmt.Errorf("commit time-series observation batch: %w", err)
	}
	return results, nil
}

func insertTimeSeriesObservationTx(tx *sql.Tx, dbms string, o *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	if o.MetricID == 0 {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation metric ID is required")
	}
	if o.ObservedAt.IsZero() {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation observed_at is required")
	}
	if o.DedupeKey == "" {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation dedupe key is required")
	}
	if o.ValueHash == "" {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation value hash is required")
	}
	if o.CollectedAt.IsZero() {
		o.CollectedAt = time.Now().UTC()
	}
	if o.BucketStart.IsZero() || o.BucketEnd.IsZero() {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation bucket bounds are required")
	}
	dimensions, err := optionalCanonicalJSON(o.Dimensions)
	if err != nil {
		return TimeSeriesInsertResult{}, err
	}
	provenance, err := optionalCanonicalRawJSON(o.Provenance)
	if err != nil {
		return TimeSeriesInsertResult{}, err
	}
	valueJSON, err := optionalCanonicalRawJSON(o.Value.JSON)
	if err != nil {
		return TimeSeriesInsertResult{}, err
	}
	args := []interface{}{o.MetricID, o.ObservedAt.UTC(), o.EffectiveAt, o.CollectedAt.UTC(), o.SourceUpdatedAt, o.BucketStart.UTC(), o.BucketEnd.UTC(), o.Scope.InformationSeedID, o.Scope.InformationSeedCandidateID, o.Scope.SourceID, o.Scope.SourceInformationSeedID, o.Scope.IndexID, o.Scope.EntityID, nullableString(o.Scope.SubjectType), o.Scope.SubjectID, nullableString(o.Scope.ObjectType), o.Scope.ObjectID, o.Scope.CorrelationRuleID, nullableString(o.Scope.CorrelationObjectType1), o.Scope.CorrelationObjectID1, nullableString(o.Scope.CorrelationObjectType2), o.Scope.CorrelationObjectID2, o.Value.Numeric, o.Value.Integer, o.Value.Boolean, o.Value.Text, valueJSON, o.Value.Timestamp, o.ValueHash, o.PreviousObservationID, nullableString(o.PreviousValueHash), o.IsChanged, nullableString(o.ChangeType), o.ChangeDeltaNumeric, o.ChangeDetectedAt, o.DedupeKey, dimensions, provenance, nullableString(o.ProvenanceHash)}
	p := newInformationSeedPlaceholders(dbms)
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	if dbms == DBPostgresStr {
		for _, idx := range []int{26, 36, 37} {
			if args[idx] != nil {
				values[idx] += "::jsonb"
			}
		}
	}
	query := `INSERT INTO TimeSeriesObservations (metric_id, observed_at, effective_at, collected_at, source_updated_at,
		bucket_start, bucket_end, information_seed_id, information_seed_candidate_id, source_id,
		source_information_seed_id, index_id, entity_id, subject_type, subject_id, object_type, object_id,
		correlation_rule_id, correlation_object_type_1, correlation_object_id_1, correlation_object_type_2,
		correlation_object_id_2, value_numeric, value_integer, value_boolean, value_text, value_json,
		value_timestamp, value_hash, previous_observation_id, previous_value_hash, is_changed, change_type,
		change_delta_numeric, change_detected_at, dedupe_key, dimensions, provenance, provenance_hash) VALUES (` + strings.Join(values, ",") + `)`
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE dedupe_key=VALUES(dedupe_key)`
	} else {
		query += ` ON CONFLICT (dedupe_key) DO NOTHING`
	}
	result, err := tx.Exec(query, args...)
	if err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("insert time-series observation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("inspect time-series observation insert: %w", err)
	}
	var id uint64
	lookup := `SELECT observation_id FROM TimeSeriesObservations WHERE dedupe_key = ` + informationSeedPlaceholderForDBMS(dbms, 1)
	if err = tx.QueryRow(lookup, o.DedupeKey).Scan(&id); err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("lookup inserted time-series observation: %w", err)
	}
	o.ID = id
	return TimeSeriesInsertResult{ObservationID: id, Inserted: affected == 1, Duplicate: affected != 1}, nil
}

func optionalCanonicalJSON(value map[string]interface{}) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := CanonicalTimeSeriesJSON(value)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}
func optionalCanonicalRawJSON(value json.RawMessage) (interface{}, error) {
	if len(value) == 0 {
		return nil, nil
	}
	raw, err := CanonicalTimeSeriesJSON(value)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// QueryTimeSeriesObservations queries both seed-associated and direct-source
// observations. Dimension subset matching is performed portably after SQL.
func QueryTimeSeriesObservations(db *Handler, filter TimeSeriesQueryFilter) (TimeSeriesObservationQueryResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	conditions, args, p, err := buildTimeSeriesQueryConditions(dbms, filter, "o")
	if err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	limit, offset, err := normalizeTimeSeriesPagination(filter.Pagination)
	if err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	sqlLimit := limit + 1
	direction := "ASC"
	if filter.Descending {
		direction = "DESC"
	}
	query := `SELECT ` + prefixColumns(timeSeriesObservationColumns, "o") + ` FROM TimeSeriesObservations o WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY o.observed_at ` + direction + `, o.observation_id ` + direction
	if len(filter.Dimensions) == 0 {
		query += ` LIMIT ` + p.Next() + ` OFFSET ` + p.Next()
		args = append(args, sqlLimit, offset)
	}
	rows, err := (*db).ExecuteQuery(query, args...)
	if err != nil {
		return TimeSeriesObservationQueryResult{}, fmt.Errorf("query time-series observations: %w", err)
	}
	defer rows.Close()
	all := []TimeSeriesObservation{}
	for rows.Next() {
		o, e := scanTimeSeriesObservation(rows.Scan)
		if e != nil {
			return TimeSeriesObservationQueryResult{}, e
		}
		if dimensionsContain(o.Dimensions, filter.Dimensions) {
			all = append(all, *o)
		}
	}
	if err = rows.Err(); err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	if len(filter.Dimensions) > 0 {
		start := filter.Pagination.Offset
		if start > len(all) {
			start = len(all)
		}
		end := start + limit
		if end > len(all) {
			end = len(all)
		}
		hasMore := end < len(all)
		all = all[start:end]
		return TimeSeriesObservationQueryResult{Observations: all, Count: len(all), HasMore: hasMore}, nil
	}
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	return TimeSeriesObservationQueryResult{Observations: all, Count: len(all), HasMore: hasMore}, nil
}

func buildTimeSeriesQueryConditions(dbms string, f TimeSeriesQueryFilter, alias string) ([]string, []interface{}, *informationSeedPlaceholders, error) {
	p := newInformationSeedPlaceholders(dbms)
	c := []string{"1=1"}
	a := []interface{}{}
	col := func(name string) string { return alias + "." + name }
	add := func(name string, value interface{}) { c = append(c, col(name)+" = "+p.Next()); a = append(a, value) }
	if !f.IncludeDeleted {
		c = append(c, col("deleted_at")+" IS NULL")
	}
	if f.MetricID != nil {
		add("metric_id", *f.MetricID)
	}
	if f.MetricKey != "" {
		c = append(c, col("metric_id")+" = (SELECT metric_id FROM TimeSeriesMetrics WHERE metric_key = "+p.Next()+" AND deleted_at IS NULL)")
		a = append(a, f.MetricKey)
	}
	if f.InformationSeedID != nil {
		add("information_seed_id", *f.InformationSeedID)
	}
	if f.InformationSeedCandidateID != nil {
		add("information_seed_candidate_id", *f.InformationSeedCandidateID)
	}
	if f.SourceID != nil {
		add("source_id", *f.SourceID)
	}
	if f.SourceInformationSeedID != nil {
		add("source_information_seed_id", *f.SourceInformationSeedID)
	}
	if f.IndexID != nil {
		add("index_id", *f.IndexID)
	}
	if f.EntityID != nil {
		add("entity_id", *f.EntityID)
	}
	if f.SubjectType != "" {
		add("subject_type", f.SubjectType)
	}
	if f.SubjectID != nil {
		add("subject_id", *f.SubjectID)
	}
	if f.SubjectText != "" {
		add("subject_text", f.SubjectText)
	}
	if f.ObjectType != "" {
		add("object_type", f.ObjectType)
	}
	if f.ObjectID != nil {
		add("object_id", *f.ObjectID)
	}
	if f.CorrelationRuleID != nil {
		add("correlation_rule_id", *f.CorrelationRuleID)
	}
	if f.CorrelationObjectType1 != "" {
		add("correlation_object_type_1", f.CorrelationObjectType1)
	}
	if f.CorrelationObjectID1 != nil {
		add("correlation_object_id_1", *f.CorrelationObjectID1)
	}
	if f.CorrelationObjectType2 != "" {
		add("correlation_object_type_2", f.CorrelationObjectType2)
	}
	if f.CorrelationObjectID2 != nil {
		add("correlation_object_id_2", *f.CorrelationObjectID2)
	}
	timeColumn := "observed_at"
	switch f.TimeBasis {
	case "", cfg.TimeSeriesTimeObservedAt:
	case cfg.TimeSeriesTimeEventAt:
		timeColumn = "effective_at"
	case cfg.TimeSeriesTimeSourceTimestamp:
		timeColumn = "source_updated_at"
	default:
		return nil, nil, nil, fmt.Errorf("unsupported time-series time basis %q", f.TimeBasis)
	}
	if f.Start != nil {
		c = append(c, col(timeColumn)+" >= "+p.Next())
		a = append(a, f.Start.UTC())
	}
	if f.End != nil {
		c = append(c, col(timeColumn)+" < "+p.Next())
		a = append(a, f.End.UTC())
	}
	if f.BucketStart != nil {
		c = append(c, col("bucket_start")+" >= "+p.Next())
		a = append(a, f.BucketStart.UTC())
	}
	if f.BucketEnd != nil {
		c = append(c, col("bucket_end")+" <= "+p.Next())
		a = append(a, f.BucketEnd.UTC())
	}
	if f.Bucket != "" {
		c = append(c, "EXISTS (SELECT 1 FROM TimeSeriesMetrics tm WHERE tm.metric_id = "+col("metric_id")+" AND tm.bucket = "+p.Next()+")")
		a = append(a, string(f.Bucket))
	}
	return c, a, p, nil
}

func prefixColumns(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i := range parts {
		parts[i] = alias + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}

func scanTimeSeriesObservation(scan func(...interface{}) error) (*TimeSeriesObservation, error) {
	o := &TimeSeriesObservation{}
	var effective, sourceUpdated, valueTimestamp, changeDetected, deleted sql.NullTime
	var ids [15]sql.NullInt64
	var subjectType, objectType, cType1, cType2, valueText, valueJSON, previousHash, changeType, dimensions, provenance, provenanceHash sql.NullString
	var numeric, delta sql.NullFloat64
	var integer sql.NullInt64
	var boolean sql.NullBool
	err := scan(&o.ID, &o.MetricID, &o.ObservedAt, &effective, &o.CollectedAt, &sourceUpdated, &o.BucketStart, &o.BucketEnd, &ids[0], &ids[1], &ids[2], &ids[3], &ids[4], &ids[5], &subjectType, &ids[6], &objectType, &ids[7], &ids[8], &cType1, &ids[9], &cType2, &ids[10], &numeric, &integer, &boolean, &valueText, &valueJSON, &valueTimestamp, &o.ValueHash, &ids[11], &previousHash, &o.IsChanged, &changeType, &delta, &changeDetected, &o.DedupeKey, &dimensions, &provenance, &provenanceHash, &o.CreatedAt, &deleted, &o.LastUpdatedAt)
	if err != nil {
		return nil, err
	}
	o.EffectiveAt = nullTimePtr(effective)
	o.SourceUpdatedAt = nullTimePtr(sourceUpdated)
	o.Scope.InformationSeedID = nullUintPtr(ids[0])
	o.Scope.InformationSeedCandidateID = nullUintPtr(ids[1])
	o.Scope.SourceID = nullUintPtr(ids[2])
	o.Scope.SourceInformationSeedID = nullUintPtr(ids[3])
	o.Scope.IndexID = nullUintPtr(ids[4])
	o.Scope.EntityID = nullUintPtr(ids[5])
	o.Scope.SubjectType = subjectType.String
	o.Scope.SubjectID = nullUintPtr(ids[6])
	o.Scope.ObjectType = objectType.String
	o.Scope.ObjectID = nullUintPtr(ids[7])
	o.Scope.CorrelationRuleID = nullUintPtr(ids[8])
	o.Scope.CorrelationObjectType1 = cType1.String
	o.Scope.CorrelationObjectID1 = nullUintPtr(ids[9])
	o.Scope.CorrelationObjectType2 = cType2.String
	o.Scope.CorrelationObjectID2 = nullUintPtr(ids[10])
	if numeric.Valid {
		o.Value.Numeric = &numeric.Float64
	}
	if integer.Valid {
		o.Value.Integer = &integer.Int64
	}
	if boolean.Valid {
		o.Value.Boolean = &boolean.Bool
	}
	if valueText.Valid {
		o.Value.Text = &valueText.String
	}
	if valueJSON.Valid {
		o.Value.JSON = json.RawMessage(valueJSON.String)
	}
	o.Value.Timestamp = nullTimePtr(valueTimestamp)
	o.PreviousObservationID = nullUintPtr(ids[11])
	o.PreviousValueHash = previousHash.String
	o.ChangeType = changeType.String
	if delta.Valid {
		o.ChangeDeltaNumeric = &delta.Float64
	}
	o.ChangeDetectedAt = nullTimePtr(changeDetected)
	if dimensions.Valid {
		if err = json.Unmarshal([]byte(dimensions.String), &o.Dimensions); err != nil {
			return nil, err
		}
	}
	if provenance.Valid {
		o.Provenance = json.RawMessage(provenance.String)
	}
	o.ProvenanceHash = provenanceHash.String
	o.DeletedAt = nullTimePtr(deleted)
	return o, nil
}
func nullUintPtr(v sql.NullInt64) *uint64 {
	if !v.Valid {
		return nil
	}
	x := uint64(v.Int64)
	return &x
}
func nullTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	x := v.Time
	return &x
}
func dimensionsContain(actual, expected map[string]interface{}) bool {
	if len(expected) == 0 {
		return true
	}
	for key, want := range expected {
		got, ok := actual[key]
		if !ok {
			return false
		}
		a, _ := CanonicalTimeSeriesJSON(got)
		b, _ := CanonicalTimeSeriesJSON(want)
		if string(a) != string(b) {
			return false
		}
	}
	return true
}

// FindPreviousTimeSeriesObservation hides change-state SQL from emitters.
func FindPreviousTimeSeriesObservation(db *Handler, lookup TimeSeriesChangeLookup) (*TimeSeriesObservation, error) {
	if lookup.MetricID == 0 {
		return nil, fmt.Errorf("time-series change lookup metric ID is required")
	}
	filter := TimeSeriesQueryFilter{
		MetricID: &lookup.MetricID, InformationSeedID: lookup.Scope.InformationSeedID,
		InformationSeedCandidateID: lookup.Scope.InformationSeedCandidateID,
		SourceID:                   lookup.Scope.SourceID, SourceInformationSeedID: lookup.Scope.SourceInformationSeedID,
		IndexID: lookup.Scope.IndexID, EntityID: lookup.Scope.EntityID,
		SubjectType: lookup.Scope.SubjectType, SubjectID: lookup.Scope.SubjectID,
		ObjectType: lookup.Scope.ObjectType, ObjectID: lookup.Scope.ObjectID,
		CorrelationRuleID:      lookup.Scope.CorrelationRuleID,
		CorrelationObjectType1: lookup.Scope.CorrelationObjectType1,
		CorrelationObjectID1:   lookup.Scope.CorrelationObjectID1,
		CorrelationObjectType2: lookup.Scope.CorrelationObjectType2,
		CorrelationObjectID2:   lookup.Scope.CorrelationObjectID2,
		Dimensions:             lookup.Dimensions, End: &lookup.Before, TimeBasis: lookup.TimeBasis, IncludeDeleted: true,
		Pagination: TimeSeriesPagination{Limit: 10000},
	}
	result, err := QueryTimeSeriesObservations(db, filter)
	if err != nil {
		return nil, err
	}
	if len(result.Observations) == 0 {
		return nil, ErrTimeSeriesObservationNotFound
	}
	return &result.Observations[0], nil
}
