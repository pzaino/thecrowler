// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const timeSeriesAggregateColumns = `aggregate_id, metric_id, bucket_start, bucket_end,
	information_seed_id, information_seed_candidate_id, source_id, source_information_seed_id,
	index_id, entity_id, subject_type, subject_id, object_type, object_id, correlation_rule_id,
	correlation_object_type_1, correlation_object_id_1, correlation_object_type_2, correlation_object_id_2,
	dimensions, value_count, occurrence_total, distinct_value_count, numeric_count, numeric_sum, numeric_min, numeric_max, numeric_avg,
	percentile_50, percentile_75, percentile_90, percentile_95, percentile_99,
	first_observation_id, first_observed_at, first_value_numeric, first_value_text, first_value_hash,
	last_observation_id, last_observed_at, last_value_numeric, last_value_text, last_value_hash,
	last_value_boolean, last_value_json, first_seen_at, last_seen_at, change_count, aggregate_hash, created_at, deleted_at, last_updated_at`

// UpsertTimeSeriesAggregate inserts or replaces the computed fields of the
// grouping identified by aggregate_hash.
func UpsertTimeSeriesAggregate(db *Handler, aggregate *TimeSeriesAggregate) (*TimeSeriesAggregate, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if aggregate == nil {
		return nil, fmt.Errorf("time-series aggregate is nil")
	}
	if aggregate.MetricID == 0 {
		return nil, fmt.Errorf("time-series aggregate metric ID is required")
	}
	if aggregate.BucketStart.IsZero() || aggregate.BucketEnd.IsZero() {
		return nil, fmt.Errorf("time-series aggregate bucket bounds are required")
	}
	if aggregate.AggregateHash == "" {
		aggregate.AggregateHash, err = TimeSeriesAggregateHash(*aggregate)
		if err != nil {
			return nil, err
		}
	}
	dimensions, err := optionalCanonicalJSON(aggregate.Dimensions)
	if err != nil {
		return nil, err
	}
	a := aggregate
	args := []interface{}{a.MetricID, a.BucketStart.UTC(), a.BucketEnd.UTC(), a.Scope.InformationSeedID, a.Scope.InformationSeedCandidateID, a.Scope.SourceID, a.Scope.SourceInformationSeedID, a.Scope.IndexID, a.Scope.EntityID, nullableString(a.Scope.SubjectType), a.Scope.SubjectID, nullableString(a.Scope.ObjectType), a.Scope.ObjectID, a.Scope.CorrelationRuleID, nullableString(a.Scope.CorrelationObjectType1), a.Scope.CorrelationObjectID1, nullableString(a.Scope.CorrelationObjectType2), a.Scope.CorrelationObjectID2, dimensions, a.ValueCount, a.OccurrenceTotal, a.DistinctValueCount, a.NumericCount, a.NumericSum, a.NumericMin, a.NumericMax, a.NumericAverage, a.Percentile50, a.Percentile75, a.Percentile90, a.Percentile95, a.Percentile99, a.First.ObservationID, a.First.ObservedAt, a.First.ValueNumeric, a.First.ValueText, nullableString(a.First.ValueHash), a.Last.ObservationID, a.Last.ObservedAt, a.Last.ValueNumeric, a.Last.ValueText, nullableString(a.Last.ValueHash), a.LastValueBoolean, optionalRawJSONForAggregate(a.LastValueJSON), a.FirstSeenAt, a.LastSeenAt, a.ChangeCount, a.AggregateHash}
	p := newInformationSeedPlaceholders(dbms)
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	if dbms == DBPostgresStr {
		for _, idx := range []int{18, 43} {
			if args[idx] != nil {
				values[idx] += "::jsonb"
			}
		}
	}
	query := `INSERT INTO TimeSeriesAggregates (metric_id,bucket_start,bucket_end,information_seed_id,
		information_seed_candidate_id,source_id,source_information_seed_id,index_id,entity_id,subject_type,
		subject_id,object_type,object_id,correlation_rule_id,correlation_object_type_1,correlation_object_id_1,
		correlation_object_type_2,correlation_object_id_2,dimensions,value_count,occurrence_total,distinct_value_count,numeric_count,numeric_sum,
		numeric_min,numeric_max,numeric_avg,percentile_50,percentile_75,percentile_90,percentile_95,
		percentile_99,first_observation_id,first_observed_at,first_value_numeric,first_value_text,
		first_value_hash,last_observation_id,last_observed_at,last_value_numeric,last_value_text,
		last_value_hash,last_value_boolean,last_value_json,first_seen_at,last_seen_at,change_count,aggregate_hash) VALUES (` + strings.Join(values, ",") + `)`
	fields := []string{"metric_id", "bucket_start", "bucket_end", "information_seed_id", "information_seed_candidate_id", "source_id", "source_information_seed_id", "index_id", "entity_id", "subject_type", "subject_id", "object_type", "object_id", "correlation_rule_id", "correlation_object_type_1", "correlation_object_id_1", "correlation_object_type_2", "correlation_object_id_2", "dimensions", "value_count", "occurrence_total", "distinct_value_count", "numeric_count", "numeric_sum", "numeric_min", "numeric_max", "numeric_avg", "percentile_50", "percentile_75", "percentile_90", "percentile_95", "percentile_99", "first_observation_id", "first_observed_at", "first_value_numeric", "first_value_text", "first_value_hash", "last_observation_id", "last_observed_at", "last_value_numeric", "last_value_text", "last_value_hash", "last_value_boolean", "last_value_json", "first_seen_at", "last_seen_at", "change_count"}
	updates := make([]string, len(fields))
	for i, field := range fields {
		if dbms == DBMySQLStr {
			updates[i] = field + "=VALUES(" + field + ")"
		} else {
			updates[i] = field + "=excluded." + field
		}
	}
	updates = append(updates, "deleted_at=NULL", "last_updated_at=CURRENT_TIMESTAMP")
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE ` + strings.Join(updates, ",")
	} else {
		query += ` ON CONFLICT (aggregate_hash) DO UPDATE SET ` + strings.Join(updates, ",")
	}
	if _, err = (*db).Exec(query, args...); err != nil {
		return nil, fmt.Errorf("upsert time-series aggregate: %w", err)
	}
	return GetTimeSeriesAggregateByHash(db, a.AggregateHash)
}

// GetTimeSeriesAggregateByHash retrieves one active aggregate grouping.
func GetTimeSeriesAggregateByHash(db *Handler, hash string) (*TimeSeriesAggregate, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		return nil, fmt.Errorf("time-series aggregate hash is required")
	}
	row := (*db).QueryRow(`SELECT `+timeSeriesAggregateColumns+` FROM TimeSeriesAggregates WHERE aggregate_hash = `+informationSeedPlaceholderForDBMS(dbms, 1)+` AND deleted_at IS NULL`, hash)
	a, err := scanTimeSeriesAggregate(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("query time-series aggregate: %w", err)
	}
	return a, nil
}

// QueryTimeSeriesAggregates applies the shared identity, bucket, metric, time
// basis, time range, and portable dimension filters.
func QueryTimeSeriesAggregates(db *Handler, filter TimeSeriesQueryFilter) (TimeSeriesAggregateQueryResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return TimeSeriesAggregateQueryResult{}, err
	}
	p := newInformationSeedPlaceholders(dbms)
	c := []string{"1=1"}
	args := []interface{}{}
	col := func(n string) string { return "a." + n }
	add := func(n string, v interface{}) { c = append(c, col(n)+" = "+p.Next()); args = append(args, v) }
	if !filter.IncludeDeleted {
		c = append(c, "a.deleted_at IS NULL")
	}
	if filter.MetricID != nil {
		add("metric_id", *filter.MetricID)
	}
	if filter.MetricKey != "" {
		c = append(c, "a.metric_id = (SELECT metric_id FROM TimeSeriesMetrics WHERE metric_key = "+p.Next()+" AND deleted_at IS NULL)")
		args = append(args, filter.MetricKey)
	}
	if filter.InformationSeedID != nil {
		add("information_seed_id", *filter.InformationSeedID)
	}
	if filter.InformationSeedCandidateID != nil {
		add("information_seed_candidate_id", *filter.InformationSeedCandidateID)
	}
	if filter.SourceID != nil {
		add("source_id", *filter.SourceID)
	}
	if filter.SourceInformationSeedID != nil {
		add("source_information_seed_id", *filter.SourceInformationSeedID)
	}
	if filter.IndexID != nil {
		add("index_id", *filter.IndexID)
	}
	if filter.EntityID != nil {
		add("entity_id", *filter.EntityID)
	}
	if filter.SubjectType != "" {
		add("subject_type", filter.SubjectType)
	}
	if filter.SubjectID != nil {
		add("subject_id", *filter.SubjectID)
	}
	if filter.ObjectType != "" {
		add("object_type", filter.ObjectType)
	}
	if filter.ObjectID != nil {
		add("object_id", *filter.ObjectID)
	}
	if filter.CorrelationRuleID != nil {
		add("correlation_rule_id", *filter.CorrelationRuleID)
	}
	if filter.CorrelationObjectType1 != "" {
		add("correlation_object_type_1", filter.CorrelationObjectType1)
	}
	if filter.CorrelationObjectID1 != nil {
		add("correlation_object_id_1", *filter.CorrelationObjectID1)
	}
	if filter.CorrelationObjectType2 != "" {
		add("correlation_object_type_2", filter.CorrelationObjectType2)
	}
	if filter.CorrelationObjectID2 != nil {
		add("correlation_object_id_2", *filter.CorrelationObjectID2)
	}
	if filter.Start != nil {
		c = append(c, "a.bucket_start >= "+p.Next())
		args = append(args, filter.Start.UTC())
	}
	if filter.End != nil {
		c = append(c, "a.bucket_start < "+p.Next())
		args = append(args, filter.End.UTC())
	}
	if filter.BucketStart != nil {
		c = append(c, "a.bucket_start >= "+p.Next())
		args = append(args, filter.BucketStart.UTC())
	}
	if filter.BucketEnd != nil {
		c = append(c, "a.bucket_end <= "+p.Next())
		args = append(args, filter.BucketEnd.UTC())
	}
	if filter.Bucket != "" {
		c = append(c, "EXISTS (SELECT 1 FROM TimeSeriesMetrics tm WHERE tm.metric_id=a.metric_id AND tm.bucket="+p.Next()+")")
		args = append(args, string(filter.Bucket))
	}
	if filter.TimeBasis != "" {
		c = append(c, "EXISTS (SELECT 1 FROM TimeSeriesMetrics tm WHERE tm.metric_id=a.metric_id AND tm.time_basis="+p.Next()+")")
		args = append(args, string(filter.TimeBasis))
	}
	limit, offset, err := normalizeTimeSeriesPagination(filter.Pagination)
	if err != nil {
		return TimeSeriesAggregateQueryResult{}, err
	}
	sqlLimit := limit + 1
	direction := "ASC"
	if filter.Descending {
		direction = "DESC"
	}
	query := `SELECT ` + prefixColumns(timeSeriesAggregateColumns, "a") + ` FROM TimeSeriesAggregates a WHERE ` + strings.Join(c, " AND ") + ` ORDER BY a.bucket_start ` + direction + `,a.aggregate_id ` + direction
	if len(filter.Dimensions) == 0 {
		query += ` LIMIT ` + p.Next() + ` OFFSET ` + p.Next()
		args = append(args, sqlLimit, offset)
	}
	rows, err := (*db).ExecuteQuery(query, args...)
	if err != nil {
		return TimeSeriesAggregateQueryResult{}, fmt.Errorf("query time-series aggregates: %w", err)
	}
	defer rows.Close()
	all := []TimeSeriesAggregate{}
	for rows.Next() {
		a, e := scanTimeSeriesAggregate(rows.Scan)
		if e != nil {
			return TimeSeriesAggregateQueryResult{}, e
		}
		if dimensionsContain(a.Dimensions, filter.Dimensions) {
			all = append(all, *a)
		}
	}
	if err = rows.Err(); err != nil {
		return TimeSeriesAggregateQueryResult{}, err
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
		return TimeSeriesAggregateQueryResult{Aggregates: all, Count: len(all), HasMore: hasMore}, nil
	}
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	return TimeSeriesAggregateQueryResult{Aggregates: all, Count: len(all), HasMore: hasMore}, nil
}

func scanTimeSeriesAggregate(scan func(...interface{}) error) (*TimeSeriesAggregate, error) {
	a := &TimeSeriesAggregate{}
	var ids [15]sql.NullInt64
	var subjectType, objectType, cType1, cType2, dimensions, firstText, firstHash, lastText, lastHash sql.NullString
	var nums [12]sql.NullFloat64
	var firstAt, lastAt, firstSeenAt, lastSeenAt, deleted sql.NullTime
	var lastBoolean sql.NullBool
	var lastJSON sql.NullString
	err := scan(&a.ID, &a.MetricID, &a.BucketStart, &a.BucketEnd, &ids[0], &ids[1], &ids[2], &ids[3], &ids[4], &ids[5], &subjectType, &ids[6], &objectType, &ids[7], &ids[8], &cType1, &ids[9], &cType2, &ids[10], &dimensions, &a.ValueCount, &nums[11], &a.DistinctValueCount, &a.NumericCount, &nums[0], &nums[1], &nums[2], &nums[3], &nums[4], &nums[5], &nums[6], &nums[7], &nums[8], &ids[11], &firstAt, &nums[9], &firstText, &firstHash, &ids[12], &lastAt, &nums[10], &lastText, &lastHash, &lastBoolean, &lastJSON, &firstSeenAt, &lastSeenAt, &a.ChangeCount, &a.AggregateHash, &a.CreatedAt, &deleted, &a.LastUpdatedAt)
	if err != nil {
		return nil, err
	}
	a.Scope.InformationSeedID = nullUintPtr(ids[0])
	a.Scope.InformationSeedCandidateID = nullUintPtr(ids[1])
	a.Scope.SourceID = nullUintPtr(ids[2])
	a.Scope.SourceInformationSeedID = nullUintPtr(ids[3])
	a.Scope.IndexID = nullUintPtr(ids[4])
	a.Scope.EntityID = nullUintPtr(ids[5])
	a.Scope.SubjectType = subjectType.String
	a.Scope.SubjectID = nullUintPtr(ids[6])
	a.Scope.ObjectType = objectType.String
	a.Scope.ObjectID = nullUintPtr(ids[7])
	a.Scope.CorrelationRuleID = nullUintPtr(ids[8])
	a.Scope.CorrelationObjectType1 = cType1.String
	a.Scope.CorrelationObjectID1 = nullUintPtr(ids[9])
	a.Scope.CorrelationObjectType2 = cType2.String
	a.Scope.CorrelationObjectID2 = nullUintPtr(ids[10])
	if dimensions.Valid {
		if err = json.Unmarshal([]byte(dimensions.String), &a.Dimensions); err != nil {
			return nil, err
		}
	}
	ptr := func(v sql.NullFloat64) *float64 {
		if !v.Valid {
			return nil
		}
		x := v.Float64
		return &x
	}
	a.OccurrenceTotal = nums[11].Float64
	a.NumericSum = ptr(nums[0])
	a.NumericMin = ptr(nums[1])
	a.NumericMax = ptr(nums[2])
	a.NumericAverage = ptr(nums[3])
	a.Percentile50 = ptr(nums[4])
	a.Percentile75 = ptr(nums[5])
	a.Percentile90 = ptr(nums[6])
	a.Percentile95 = ptr(nums[7])
	a.Percentile99 = ptr(nums[8])
	a.First = TimeSeriesAggregateEdge{ObservationID: nullUintPtr(ids[11]), ObservedAt: nullTimePtr(firstAt), ValueNumeric: ptr(nums[9]), ValueHash: firstHash.String}
	if firstText.Valid {
		a.First.ValueText = &firstText.String
	}
	a.Last = TimeSeriesAggregateEdge{ObservationID: nullUintPtr(ids[12]), ObservedAt: nullTimePtr(lastAt), ValueNumeric: ptr(nums[10]), ValueHash: lastHash.String}
	if lastText.Valid {
		a.Last.ValueText = &lastText.String
	}
	if lastBoolean.Valid {
		a.LastValueBoolean = &lastBoolean.Bool
	}
	if lastJSON.Valid {
		a.LastValueJSON = json.RawMessage(lastJSON.String)
	}
	a.FirstSeenAt = nullTimePtr(firstSeenAt)
	a.LastSeenAt = nullTimePtr(lastSeenAt)
	a.DeletedAt = nullTimePtr(deleted)
	return a, nil
}

func optionalRawJSONForAggregate(raw json.RawMessage) interface{} {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return string(raw)
}
