// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"encoding/json"
	"errors"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	// ErrTimeSeriesMetricNotFound is returned when a metric lookup has no match.
	ErrTimeSeriesMetricNotFound = errors.New("time-series metric not found")
	// ErrTimeSeriesMetricTypeConflict prevents an upsert from silently changing
	// the storage representation of an existing metric.
	ErrTimeSeriesMetricTypeConflict = errors.New("time-series metric value type conflict")
	// ErrTimeSeriesObservationNotFound is returned when an observation has no match.
	ErrTimeSeriesObservationNotFound = errors.New("time-series observation not found")
	// ErrTimeSeriesValueRejected indicates that a resolved storage/cardinality
	// policy intentionally rejected an observation before it reached SQL.
	ErrTimeSeriesValueRejected = errors.New("time-series value rejected by policy")
)

// TimeSeriesMetric is the database representation of a configured metric.
type TimeSeriesMetric struct {
	ID                uint64
	Key               string
	DisplayName       string
	Description       string
	SourceKind        cfg.TimeSeriesSourceKind
	ValueType         cfg.TimeSeriesValueType
	Aggregate         cfg.TimeSeriesAggregate
	Bucket            cfg.TimeSeriesBucketInterval
	TimeBasis         cfg.TimeSeriesTimeBasis
	DedupeScope       cfg.TimeSeriesDedupeScope
	ObjectType        cfg.TimeSeriesObjectType
	FailurePolicy     cfg.TimeSeriesFailurePolicy
	Selector          json.RawMessage
	Dimensions        json.RawMessage
	RetentionPolicy   json.RawMessage
	CardinalityPolicy json.RawMessage
	Unit              string
	Enabled           bool
	StoreValueText    bool
	HashOnly          bool
	CreatedAt         time.Time
	DeletedAt         *time.Time
	LastUpdatedAt     time.Time
}

// TimeSeriesValue contains exactly one logical value. Timestamp is available
// for extracted timestamp metrics even though Task 2 currently exposes no
// timestamp value type.
type TimeSeriesValue struct {
	Numeric   *float64
	Integer   *int64
	Boolean   *bool
	Text      *string
	JSON      json.RawMessage
	Timestamp *time.Time
}

// TimeSeriesScope identifies the series owner. Pointer IDs preserve the
// distinction between an absent value and a real zero in canonical hashes.
type TimeSeriesScope struct {
	InformationSeedID          *uint64
	InformationSeedCandidateID *uint64
	SourceID                   *uint64
	SourceInformationSeedID    *uint64
	IndexID                    *uint64
	EntityID                   *uint64
	SubjectType                string
	SubjectID                  *uint64
	SubjectText                string
	ObjectType                 string
	ObjectID                   *uint64
	CorrelationRuleID          *uint64
	CorrelationObjectType1     string
	CorrelationObjectID1       *uint64
	CorrelationObjectType2     string
	CorrelationObjectID2       *uint64
}

// TimeSeriesObservation is an append-only metric fact.
type TimeSeriesObservation struct {
	ID                    uint64
	MetricID              uint64
	ObservedAt            time.Time
	EffectiveAt           *time.Time
	CollectedAt           time.Time
	SourceUpdatedAt       *time.Time
	BucketStart           time.Time
	BucketEnd             time.Time
	Scope                 TimeSeriesScope
	Value                 TimeSeriesValue
	ValueHash             string
	PreviousObservationID *uint64
	PreviousValueHash     string
	IsChanged             bool
	ChangeType            string
	ChangeDeltaNumeric    *float64
	ChangeDetectedAt      *time.Time
	DedupeKey             string
	Dimensions            map[string]interface{}
	Provenance            json.RawMessage
	ProvenanceHash        string
	CreatedAt             time.Time
	DeletedAt             *time.Time
	LastUpdatedAt         time.Time
}

// TimeSeriesAggregate stores one materialized grouping bucket.
type TimeSeriesAggregate struct {
	ID             uint64
	MetricID       uint64
	BucketStart    time.Time
	BucketEnd      time.Time
	Scope          TimeSeriesScope
	Dimensions     map[string]interface{}
	ValueCount     int64
	NumericCount   int64
	NumericSum     *float64
	NumericMin     *float64
	NumericMax     *float64
	NumericAverage *float64
	Percentile50   *float64
	Percentile75   *float64
	Percentile90   *float64
	Percentile95   *float64
	Percentile99   *float64
	First          TimeSeriesAggregateEdge
	Last           TimeSeriesAggregateEdge
	ChangeCount    int64
	AggregateHash  string
	CreatedAt      time.Time
	DeletedAt      *time.Time
	LastUpdatedAt  time.Time
}

// TimeSeriesAggregateEdge records the first or last value represented by an aggregate.
type TimeSeriesAggregateEdge struct {
	ObservationID *uint64
	ObservedAt    *time.Time
	ValueNumeric  *float64
	ValueText     *string
	ValueHash     string
}

// TimeSeriesPagination is shared by all list operations. Limit zero selects a
// conservative default; negative values are rejected.
type TimeSeriesPagination struct {
	Limit  int
	Offset int
}

// TimeSeriesMetricFilter controls metric listing.
type TimeSeriesMetricFilter struct {
	Key            string
	SourceKind     cfg.TimeSeriesSourceKind
	ValueType      cfg.TimeSeriesValueType
	Enabled        *bool
	IncludeDeleted bool
	Pagination     TimeSeriesPagination
}

// TimeSeriesQueryFilter is shared by raw and aggregate queries. Dimensions are
// matched as a portable subset in Go, avoiding backend-specific JSON operators.
type TimeSeriesQueryFilter struct {
	MetricID                   *uint64
	MetricKey                  string
	InformationSeedID          *uint64
	InformationSeedCandidateID *uint64
	SourceID                   *uint64
	SourceInformationSeedID    *uint64
	IndexID                    *uint64
	EntityID                   *uint64
	SubjectType                string
	SubjectID                  *uint64
	ObjectType                 string
	ObjectID                   *uint64
	CorrelationRuleID          *uint64
	CorrelationObjectType1     string
	CorrelationObjectID1       *uint64
	CorrelationObjectType2     string
	CorrelationObjectID2       *uint64
	Dimensions                 map[string]interface{}
	Start                      *time.Time
	End                        *time.Time
	BucketStart                *time.Time
	BucketEnd                  *time.Time
	Bucket                     cfg.TimeSeriesBucketInterval
	TimeBasis                  cfg.TimeSeriesTimeBasis
	IncludeDeleted             bool
	Descending                 bool
	Pagination                 TimeSeriesPagination
}

// TimeSeriesAggregateQueryResult gives callers both records and stable paging metadata.
type TimeSeriesAggregateQueryResult struct {
	Aggregates []TimeSeriesAggregate
	Count      int
	HasMore    bool
}

// TimeSeriesObservationQueryResult gives callers both records and stable paging metadata.
type TimeSeriesObservationQueryResult struct {
	Observations []TimeSeriesObservation
	Count        int
	HasMore      bool
}

// TimeSeriesInsertResult distinguishes an idempotent duplicate from a new write.
type TimeSeriesInsertResult struct {
	ObservationID uint64
	Inserted      bool
	Duplicate     bool
}

// TimeSeriesChangeLookup identifies the previous observation in the same logical series.
type TimeSeriesChangeLookup struct {
	MetricID   uint64
	Scope      TimeSeriesScope
	Dimensions map[string]interface{}
	Before     time.Time
	TimeBasis  cfg.TimeSeriesTimeBasis
}

// TimeSeriesPreparationPolicy is the resolved metric/global policy applied by
// PrepareTimeSeriesObservation. CardinalityExceeded is supplied by the caller
// that owns cardinality accounting; database code owns only the transformation.
type TimeSeriesPreparationPolicy struct {
	MaxDimensions       int
	MaxValueLength      int
	RedactPatterns      []string
	HashOnly            bool
	StoreValueText      bool
	CardinalityExceeded bool
	Overflow            cfg.TimeSeriesCardinalityOverflow
	OverflowBucket      string
}

// TimeSeriesPreparationResult documents policy transformations.
type TimeSeriesPreparationResult struct {
	Observation TimeSeriesObservation
	Truncated   bool
	Redacted    bool
	HashedOnly  bool
	Overflowed  bool
}

// TimeSeriesRepository is the reusable persistence contract consumed by later
// emitters, aggregators, and APIs. It deliberately contains no orchestration or
// scheduling behavior.
type TimeSeriesRepository interface {
	UpsertMetric(metric *TimeSeriesMetric) (*TimeSeriesMetric, error)
	MetricByID(id uint64) (*TimeSeriesMetric, error)
	MetricByKey(key string) (*TimeSeriesMetric, error)
	ListMetrics(filter TimeSeriesMetricFilter) ([]TimeSeriesMetric, error)
	InsertObservation(observation *TimeSeriesObservation) (TimeSeriesInsertResult, error)
	InsertObservationBatch(observations []TimeSeriesObservation) ([]TimeSeriesInsertResult, error)
	QueryObservations(filter TimeSeriesQueryFilter) (TimeSeriesObservationQueryResult, error)
	PreviousObservation(lookup TimeSeriesChangeLookup) (*TimeSeriesObservation, error)
	UpsertAggregate(aggregate *TimeSeriesAggregate) (*TimeSeriesAggregate, error)
	QueryAggregates(filter TimeSeriesQueryFilter) (TimeSeriesAggregateQueryResult, error)
}

// HandlerTimeSeriesRepository adapts the existing Handler abstraction to the
// TimeSeriesRepository contract without exposing a backend-specific connection.
type HandlerTimeSeriesRepository struct{ DB *Handler }

func (r HandlerTimeSeriesRepository) UpsertMetric(m *TimeSeriesMetric) (*TimeSeriesMetric, error) {
	return UpsertTimeSeriesMetric(r.DB, m)
}
func (r HandlerTimeSeriesRepository) MetricByID(id uint64) (*TimeSeriesMetric, error) {
	return GetTimeSeriesMetricByID(r.DB, id)
}
func (r HandlerTimeSeriesRepository) MetricByKey(key string) (*TimeSeriesMetric, error) {
	return GetTimeSeriesMetricByKey(r.DB, key)
}
func (r HandlerTimeSeriesRepository) ListMetrics(f TimeSeriesMetricFilter) ([]TimeSeriesMetric, error) {
	return ListTimeSeriesMetrics(r.DB, f)
}
func (r HandlerTimeSeriesRepository) InsertObservation(o *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	return InsertTimeSeriesObservation(r.DB, o)
}
func (r HandlerTimeSeriesRepository) InsertObservationBatch(o []TimeSeriesObservation) ([]TimeSeriesInsertResult, error) {
	return InsertTimeSeriesObservations(r.DB, o)
}
func (r HandlerTimeSeriesRepository) QueryObservations(f TimeSeriesQueryFilter) (TimeSeriesObservationQueryResult, error) {
	return QueryTimeSeriesObservations(r.DB, f)
}
func (r HandlerTimeSeriesRepository) PreviousObservation(l TimeSeriesChangeLookup) (*TimeSeriesObservation, error) {
	return FindPreviousTimeSeriesObservation(r.DB, l)
}
func (r HandlerTimeSeriesRepository) UpsertAggregate(a *TimeSeriesAggregate) (*TimeSeriesAggregate, error) {
	return UpsertTimeSeriesAggregate(r.DB, a)
}
func (r HandlerTimeSeriesRepository) QueryAggregates(f TimeSeriesQueryFilter) (TimeSeriesAggregateQueryResult, error) {
	return QueryTimeSeriesAggregates(r.DB, f)
}
