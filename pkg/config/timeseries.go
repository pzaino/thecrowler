// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TimeSeriesSourceKind identifies the indexed artifact that supplies a metric.
type TimeSeriesSourceKind string

const (
	TimeSeriesSourceKeyword                  TimeSeriesSourceKind = "keyword"
	TimeSeriesSourceMetatag                  TimeSeriesSourceKind = "metatag"
	TimeSeriesSourceObjectAttribute          TimeSeriesSourceKind = "object_attribute"
	TimeSeriesSourceWebObject                TimeSeriesSourceKind = "webobject"
	TimeSeriesSourceHTTPInfo                 TimeSeriesSourceKind = "httpinfo"
	TimeSeriesSourceNetInfo                  TimeSeriesSourceKind = "netinfo"
	TimeSeriesSourceScreenshot               TimeSeriesSourceKind = "screenshot"
	TimeSeriesSourceFile                     TimeSeriesSourceKind = "file"
	TimeSeriesSourceInformationSeed          TimeSeriesSourceKind = "information_seed"
	TimeSeriesSourceInformationSeedCandidate TimeSeriesSourceKind = "information_seed_candidate"
	TimeSeriesSourceDiscovery                TimeSeriesSourceKind = "source_discovery"
	TimeSeriesSourceEntityMembership         TimeSeriesSourceKind = "entity_membership"
	TimeSeriesSourceObjectCorrelation        TimeSeriesSourceKind = "object_correlation"
	TimeSeriesSourceCorrelationRule          TimeSeriesSourceKind = "correlation_rule"
	TimeSeriesSourceCustom                   TimeSeriesSourceKind = "custom"
)

// TimeSeriesObjectType restricts object_attribute metrics to a stored object family.
type TimeSeriesObjectType string

const (
	TimeSeriesObjectWebObject TimeSeriesObjectType = "webobject"
	TimeSeriesObjectHTTPInfo  TimeSeriesObjectType = "httpinfo"
	TimeSeriesObjectNetInfo   TimeSeriesObjectType = "netinfo"
)

// TimeSeriesValueType describes the logical metric value.
type TimeSeriesValueType string

const (
	TimeSeriesValueInteger   TimeSeriesValueType = "integer"
	TimeSeriesValueDecimal   TimeSeriesValueType = "decimal"
	TimeSeriesValueDuration  TimeSeriesValueType = "duration"
	TimeSeriesValueBoolean   TimeSeriesValueType = "boolean"
	TimeSeriesValueString    TimeSeriesValueType = "string"
	TimeSeriesValueJSON      TimeSeriesValueType = "json"
	TimeSeriesValueCount     TimeSeriesValueType = "count"
	TimeSeriesValueTimestamp TimeSeriesValueType = "timestamp"
)

// TimeSeriesAggregate describes an aggregation computed for a bucket.
type TimeSeriesAggregate string

const (
	TimeSeriesAggregateCount         TimeSeriesAggregate = "count"
	TimeSeriesAggregateSum           TimeSeriesAggregate = "sum"
	TimeSeriesAggregateAverage       TimeSeriesAggregate = "average"
	TimeSeriesAggregateMinimum       TimeSeriesAggregate = "min"
	TimeSeriesAggregateMaximum       TimeSeriesAggregate = "max"
	TimeSeriesAggregateDistinctCount TimeSeriesAggregate = "distinct_count"
	TimeSeriesAggregateFirst         TimeSeriesAggregate = "first"
	TimeSeriesAggregateLast          TimeSeriesAggregate = "last"
	TimeSeriesAggregateP50           TimeSeriesAggregate = "p50"
	TimeSeriesAggregateP75           TimeSeriesAggregate = "p75"
	TimeSeriesAggregateP90           TimeSeriesAggregate = "p90"
	TimeSeriesAggregateP95           TimeSeriesAggregate = "p95"
	TimeSeriesAggregateP99           TimeSeriesAggregate = "p99"
)

// TimeSeriesBucketInterval controls aggregation bucket width.
type TimeSeriesBucketInterval string

const (
	TimeSeriesBucketNone           TimeSeriesBucketInterval = "none"
	TimeSeriesBucketOneMinute      TimeSeriesBucketInterval = "1m"
	TimeSeriesBucketFiveMinutes    TimeSeriesBucketInterval = "5m"
	TimeSeriesBucketFifteenMinutes TimeSeriesBucketInterval = "15m"
	TimeSeriesBucketOneHour        TimeSeriesBucketInterval = "1h"
	TimeSeriesBucketOneDay         TimeSeriesBucketInterval = "1d"
	TimeSeriesBucketOneWeek        TimeSeriesBucketInterval = "1w"
	TimeSeriesBucketOneMonth       TimeSeriesBucketInterval = "1mo"
)

// TimeSeriesTimeBasis selects which timestamp is attached to an observation.
type TimeSeriesTimeBasis string

const (
	TimeSeriesTimeObservedAt      TimeSeriesTimeBasis = "observed_at"
	TimeSeriesTimeEventAt         TimeSeriesTimeBasis = "event_at"
	TimeSeriesTimeSourceTimestamp TimeSeriesTimeBasis = "source_timestamp"
)

// TimeSeriesDedupeScope controls duplicate suppression.
type TimeSeriesDedupeScope string

const (
	TimeSeriesDedupeNone   TimeSeriesDedupeScope = "none"
	TimeSeriesDedupeSource TimeSeriesDedupeScope = "source"
	TimeSeriesDedupeObject TimeSeriesDedupeScope = "object"
	TimeSeriesDedupeGlobal TimeSeriesDedupeScope = "global"
)

// TimeSeriesFailurePolicy controls handling of observation write failures.
type TimeSeriesFailurePolicy string

const (
	TimeSeriesFailureLogSkip      TimeSeriesFailurePolicy = "log_skip"
	TimeSeriesFailureLog          TimeSeriesFailurePolicy = "log"
	TimeSeriesFailureSkip         TimeSeriesFailurePolicy = "skip"
	TimeSeriesFailureRetry        TimeSeriesFailurePolicy = "retry"
	TimeSeriesFailureFailIndexing TimeSeriesFailurePolicy = "fail_indexing"
)

// TimeSeriesCardinalityOverflow controls what happens after a cardinality limit is reached.
type TimeSeriesCardinalityOverflow string

const (
	TimeSeriesCardinalityDrop           TimeSeriesCardinalityOverflow = "drop"
	TimeSeriesCardinalityHash           TimeSeriesCardinalityOverflow = "hash"
	TimeSeriesCardinalityOverflowBucket TimeSeriesCardinalityOverflow = "overflow_bucket"
)

// TimeSeriesConfig is declarative configuration only; it does not enable an emitter by itself.
type TimeSeriesConfig struct {
	Enabled     bool                        `json:"enabled" yaml:"enabled"`
	Defaults    TimeSeriesMetricDefaults    `json:"defaults" yaml:"defaults"`
	Retention   TimeSeriesRetentionConfig   `json:"retention" yaml:"retention"`
	Aggregation TimeSeriesAggregationConfig `json:"aggregation" yaml:"aggregation"`
	Storage     TimeSeriesStorageConfig     `json:"storage" yaml:"storage"`
	Cardinality TimeSeriesCardinalityConfig `json:"cardinality" yaml:"cardinality"`
	Privacy     TimeSeriesPrivacyConfig     `json:"privacy" yaml:"privacy"`
	Metrics     []TimeSeriesMetricConfig    `json:"metrics" yaml:"metrics"`
}

type TimeSeriesMetricDefaults struct {
	ValueType      TimeSeriesValueType      `json:"value_type" yaml:"value_type"`
	Aggregates     []TimeSeriesAggregate    `json:"aggregates" yaml:"aggregates"`
	BucketInterval TimeSeriesBucketInterval `json:"bucket_interval" yaml:"bucket_interval"`
	TimeBasis      TimeSeriesTimeBasis      `json:"time_basis" yaml:"time_basis"`
	DedupeScope    TimeSeriesDedupeScope    `json:"dedupe_scope" yaml:"dedupe_scope"`
	FailurePolicy  TimeSeriesFailurePolicy  `json:"failure_policy" yaml:"failure_policy"`
}

// TimeSeriesRetentionConfig controls the optional scheduled retention pass.
// Automatic deletion is disabled by default so an upgrade from 2.3.0 never
// starts deleting history merely because the feature exists.
type TimeSeriesRetentionConfig struct {
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	Schedule    string `json:"schedule" yaml:"schedule"`
	BatchSize   int    `json:"batch_size" yaml:"batch_size"`
	MaxBatches  int    `json:"max_batches" yaml:"max_batches"`
	MaxDuration string `json:"max_duration" yaml:"max_duration"`
	DryRun      bool   `json:"dry_run" yaml:"dry_run"`
	Raw         string `json:"raw" yaml:"raw"`
	Aggregated  string `json:"aggregated" yaml:"aggregated"`
}

// TimeSeriesRetentionOverride is an optional per-metric replacement for the
// global raw/aggregated retention windows. An omitted or empty field inherits
// the global value; the literal "0" keeps that family forever.
type TimeSeriesRetentionOverride struct {
	Raw        string `json:"raw,omitempty" yaml:"raw,omitempty"`
	Aggregated string `json:"aggregated,omitempty" yaml:"aggregated,omitempty"`
}

// TimeSeriesRetentionWindow is a parsed retention horizon. Retention horizons
// are not all expressible as a time.Duration: calendar months and years change
// length, so they are evaluated against a UTC instant with end-of-month
// clamping. Fixed units (ns, us, µs, ms, s, m, h, d, w) remain exact durations.
type TimeSeriesRetentionWindow struct {
	Specified bool
	Forever   bool
	Months    int
	Fixed     time.Duration
}

const timeSeriesRetentionMaxMonths = 1200

var (
	// Fixed windows keep the historical Go duration grammar (including
	// composites such as 1h30m and fractions such as 1.5h), extended with d and
	// w. Calendar windows are integer y/mo compositions and never mix with fixed
	// units, because a fractional month has no unambiguous calendar meaning.
	timeSeriesFixedRetentionPattern    = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h|d|w))+$`)
	timeSeriesCalendarRetentionPattern = regexp.MustCompile(`^(?:[0-9]+(?:y|mo))+$`)
	timeSeriesFixedRetentionSegment    = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h|d|w)`)
	timeSeriesCalendarRetentionSegment = regexp.MustCompile(`^([0-9]+)(y|mo)`)
)

// ParseTimeSeriesRetentionWindow parses a retention horizon. The empty string
// is rejected so callers must decide explicitly whether it means "inherit" or
// "unset"; "0" (with or without a unit) means keep forever.
func ParseTimeSeriesRetentionWindow(value string) (TimeSeriesRetentionWindow, error) {
	window := TimeSeriesRetentionWindow{}
	value = strings.TrimSpace(value)
	if value == "" {
		return window, fmt.Errorf("retention window must not be empty")
	}
	window.Specified = true
	if value == "0" {
		window.Forever = true
		return window, nil
	}
	if timeSeriesCalendarRetentionPattern.MatchString(value) {
		months, err := parseCalendarRetentionDuration(value)
		if err != nil {
			return window, err
		}
		if months == 0 {
			window.Forever = true
			return window, nil
		}
		if months > timeSeriesRetentionMaxMonths {
			return window, fmt.Errorf("retention window %q exceeds the maximum of %d months", value, timeSeriesRetentionMaxMonths)
		}
		window.Months = months
		return window, nil
	}
	if timeSeriesFixedRetentionPattern.MatchString(value) {
		fixed, err := parseFixedRetentionDuration(value)
		if err != nil {
			return window, err
		}
		if fixed == 0 {
			window.Forever = true
			return window, nil
		}
		window.Fixed = fixed
		return window, nil
	}
	return window, fmt.Errorf("invalid retention window %q", value)
}

func parseCalendarRetentionDuration(value string) (int, error) {
	remaining := value
	months := 0
	for remaining != "" {
		location := timeSeriesCalendarRetentionSegment.FindStringSubmatchIndex(remaining)
		if location == nil || location[0] != 0 {
			return 0, fmt.Errorf("invalid retention window %q", value)
		}
		amountText := remaining[location[2]:location[3]]
		unit := remaining[location[4]:location[5]]
		amount, err := strconv.Atoi(amountText)
		if err != nil || amount < 0 {
			return 0, fmt.Errorf("invalid retention window %q", value)
		}
		switch unit {
		case "y":
			if amount > timeSeriesRetentionMaxMonths/12 {
				return 0, fmt.Errorf("retention window %q exceeds the maximum of %d months", value, timeSeriesRetentionMaxMonths)
			}
			amount *= 12
		}
		if months > timeSeriesRetentionMaxMonths-amount {
			return 0, fmt.Errorf("retention window %q exceeds the maximum of %d months", value, timeSeriesRetentionMaxMonths)
		}
		months += amount
		remaining = remaining[location[1]:]
	}
	return months, nil
}

func parseFixedRetentionDuration(value string) (time.Duration, error) {
	const maxDuration = time.Duration(1<<63 - 1)
	remaining := value
	var total time.Duration
	for remaining != "" {
		location := timeSeriesFixedRetentionSegment.FindStringSubmatchIndex(remaining)
		if location == nil || location[0] != 0 {
			return 0, fmt.Errorf("invalid retention window %q", value)
		}
		amountText := remaining[location[2]:location[3]]
		unit := remaining[location[4]:location[5]]
		var segment time.Duration
		var err error
		switch unit {
		case "d", "w":
			segment, err = time.ParseDuration(amountText + "h")
			if err == nil {
				multiplier := time.Duration(24)
				if unit == "w" {
					multiplier = 7 * 24
				}
				if segment > maxDuration/multiplier {
					return 0, fmt.Errorf("retention window %q overflows", value)
				}
				segment *= multiplier
			}
		default:
			segment, err = time.ParseDuration(amountText + unit)
		}
		if err != nil {
			return 0, fmt.Errorf("invalid retention window %q", value)
		}
		if total > maxDuration-segment {
			return 0, fmt.Errorf("retention window %q overflows", value)
		}
		total += segment
		remaining = remaining[location[1]:]
	}
	return total, nil
}

// Validate checks a programmatically constructed window. Parsed windows always
// satisfy these rules; this protects callers that build options directly.
func (w TimeSeriesRetentionWindow) Validate() error {
	if !w.Specified {
		return nil
	}
	if w.Forever {
		if w.Months != 0 || w.Fixed != 0 {
			return fmt.Errorf("forever retention window must not carry a duration")
		}
		return nil
	}
	if w.Months < 0 || w.Fixed < 0 {
		return fmt.Errorf("retention window must not be negative")
	}
	if w.Months > 0 && w.Fixed > 0 {
		return fmt.Errorf("calendar and fixed retention units cannot be mixed")
	}
	if w.Months > timeSeriesRetentionMaxMonths {
		return fmt.Errorf("retention window exceeds the maximum of %d months", timeSeriesRetentionMaxMonths)
	}
	if w.Months == 0 && w.Fixed == 0 {
		return fmt.Errorf("retention window must be positive or forever")
	}
	return nil
}

// Cutoff returns the oldest instant a finite retention window keeps. The
// second result is false for "keep forever". now is normalized to UTC.
func (w TimeSeriesRetentionWindow) Cutoff(now time.Time) (time.Time, bool) {
	if !w.Specified || w.Forever || (w.Months <= 0 && w.Fixed <= 0) {
		return time.Time{}, false
	}
	now = now.UTC()
	if w.Months > 0 {
		year, month, day := now.Date()
		totalMonths := year*12 + int(month) - 1 - w.Months
		targetYear := totalMonths / 12
		targetMonth := totalMonths % 12
		if targetMonth < 0 {
			targetMonth += 12
			targetYear--
		}
		monthStart := time.Date(targetYear, time.Month(targetMonth+1), 1, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
		lastDay := monthStart.AddDate(0, 1, -1).Day()
		if day > lastDay {
			day = lastDay
		}
		return time.Date(targetYear, time.Month(targetMonth+1), day, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC).Add(-w.Fixed), true
	}
	return now.Add(-w.Fixed), true
}

// ParseTimeSeriesFixedDuration parses the existing fixed duration grammar plus
// the day and week suffixes. Calendar units (mo, y) are deliberately rejected
// because they have no single time.Duration value.
func ParseTimeSeriesFixedDuration(value string) (time.Duration, error) {
	return parseTimeSeriesFixedDuration(value)
}

func parseTimeSeriesFixedDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("duration must not be empty")
	}
	multiplier := time.Duration(1)
	normalized := value
	if strings.HasSuffix(value, "d") {
		multiplier = 24
		normalized = strings.TrimSuffix(value, "d") + "h"
	} else if strings.HasSuffix(value, "w") {
		multiplier = 7 * 24
		normalized = strings.TrimSuffix(value, "w") + "h"
	} else if strings.HasSuffix(value, "mo") || strings.HasSuffix(value, "y") {
		return 0, fmt.Errorf("calendar duration %q is not a fixed duration", value)
	}
	duration, err := time.ParseDuration(normalized)
	if err != nil {
		return 0, err
	}
	if multiplier > 1 {
		const maxDuration = time.Duration(1<<63 - 1)
		if duration > maxDuration/multiplier || duration < -maxDuration/multiplier {
			return 0, fmt.Errorf("duration %q overflows", value)
		}
		duration *= multiplier
	}
	return duration, nil
}

type TimeSeriesAggregationConfig struct {
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	Schedule    string `json:"schedule" yaml:"schedule"`
	BatchSize   int    `json:"batch_size" yaml:"batch_size"`
	MaxBatches  int    `json:"max_batches" yaml:"max_batches"`
	MaxDuration string `json:"max_duration" yaml:"max_duration"`
	Overlap     string `json:"overlap" yaml:"overlap"`
}

type TimeSeriesStorageConfig struct {
	Backend      string                       `json:"backend" yaml:"backend"`
	TablePrefix  string                       `json:"table_prefix" yaml:"table_prefix"`
	Partitioning TimeSeriesPartitioningConfig `json:"partitioning" yaml:"partitioning"`
}

type TimeSeriesPartitioningConfig struct {
	Enabled   bool                     `json:"enabled" yaml:"enabled"`
	Interval  TimeSeriesBucketInterval `json:"interval" yaml:"interval"`
	Precreate int                      `json:"precreate" yaml:"precreate"`
}

type TimeSeriesCardinalityConfig struct {
	MaxSeriesPerMetric    int                           `json:"max_series_per_metric" yaml:"max_series_per_metric"`
	MaxDimensions         int                           `json:"max_dimensions" yaml:"max_dimensions"`
	MaxValuesPerDimension int                           `json:"max_values_per_dimension" yaml:"max_values_per_dimension"`
	Overflow              TimeSeriesCardinalityOverflow `json:"overflow" yaml:"overflow"`
}

type TimeSeriesPrivacyConfig struct {
	HashOnly       bool     `json:"hash_only" yaml:"hash_only"`
	StoreValueText bool     `json:"store_value_text" yaml:"store_value_text"`
	MaxValueLength int      `json:"max_value_length" yaml:"max_value_length"`
	RedactPatterns []string `json:"redact_patterns" yaml:"redact_patterns"`
}

// TimeSeriesMetricConfig defines one metric. Selectors remain generic maps so the
// config package does not acquire crawler/database domain types.
type TimeSeriesMetricConfig struct {
	Key               string                       `json:"key" yaml:"key"`
	Enabled           bool                         `json:"enabled" yaml:"enabled"`
	Description       string                       `json:"description" yaml:"description"`
	SourceKind        TimeSeriesSourceKind         `json:"source_kind" yaml:"source_kind"`
	ObjectType        TimeSeriesObjectType         `json:"object_type,omitempty" yaml:"object_type,omitempty"`
	Selector          map[string]interface{}       `json:"selector" yaml:"selector"`
	ValueType         TimeSeriesValueType          `json:"value_type,omitempty" yaml:"value_type,omitempty"`
	Unit              string                       `json:"unit,omitempty" yaml:"unit,omitempty"`
	Aggregates        []TimeSeriesAggregate        `json:"aggregates,omitempty" yaml:"aggregates,omitempty"`
	BucketInterval    TimeSeriesBucketInterval     `json:"bucket_interval,omitempty" yaml:"bucket_interval,omitempty"`
	TimeBasis         TimeSeriesTimeBasis          `json:"time_basis,omitempty" yaml:"time_basis,omitempty"`
	TimestampSelector map[string]interface{}       `json:"timestamp_selector,omitempty" yaml:"timestamp_selector,omitempty"`
	DedupeScope       TimeSeriesDedupeScope        `json:"dedupe_scope,omitempty" yaml:"dedupe_scope,omitempty"`
	FailurePolicy     TimeSeriesFailurePolicy      `json:"failure_policy,omitempty" yaml:"failure_policy,omitempty"`
	Dimensions        []TimeSeriesDimensionConfig  `json:"dimensions,omitempty" yaml:"dimensions,omitempty"`
	Retention         *TimeSeriesRetentionOverride `json:"retention,omitempty" yaml:"retention,omitempty"`
	Cardinality       *TimeSeriesCardinalityConfig `json:"cardinality,omitempty" yaml:"cardinality,omitempty"`
	Privacy           *TimeSeriesPrivacyConfig     `json:"privacy,omitempty" yaml:"privacy,omitempty"`
}

type TimeSeriesDimensionConfig struct {
	Key      string                 `json:"key" yaml:"key"`
	Selector map[string]interface{} `json:"selector" yaml:"selector"`
}

func defaultTimeSeriesConfig() TimeSeriesConfig {
	return TimeSeriesConfig{
		Defaults:    TimeSeriesMetricDefaults{ValueType: TimeSeriesValueInteger, Aggregates: []TimeSeriesAggregate{TimeSeriesAggregateCount}, BucketInterval: TimeSeriesBucketOneHour, TimeBasis: TimeSeriesTimeObservedAt, DedupeScope: TimeSeriesDedupeNone, FailurePolicy: TimeSeriesFailureLogSkip},
		Retention:   TimeSeriesRetentionConfig{Enabled: false, Schedule: "1h", BatchSize: 1000, MaxBatches: 10, MaxDuration: "1m", DryRun: false, Raw: "30d", Aggregated: "365d"},
		Aggregation: TimeSeriesAggregationConfig{Schedule: "5m", BatchSize: 1000, MaxBatches: 10, MaxDuration: "1m", Overlap: "15m"},
		Storage:     TimeSeriesStorageConfig{Backend: "postgres", TablePrefix: "timeseries", Partitioning: TimeSeriesPartitioningConfig{Interval: TimeSeriesBucketOneDay, Precreate: 7}},
		Cardinality: TimeSeriesCardinalityConfig{MaxSeriesPerMetric: 100000, MaxDimensions: 10, MaxValuesPerDimension: 10000, Overflow: TimeSeriesCardinalityDrop},
		Privacy:     TimeSeriesPrivacyConfig{StoreValueText: false, MaxValueLength: 2048, RedactPatterns: []string{}},
		Metrics:     []TimeSeriesMetricConfig{},
	}
}

// NormalizeTimeSeriesConfig returns a deep copy with documented defaults applied.
// It is exported so persistence code can resolve inherited per-metric retention
// and cardinality policy even when it receives a configuration assembled directly
// rather than through Config.Validate.
func NormalizeTimeSeriesConfig(ts TimeSeriesConfig) TimeSeriesConfig {
	clone := ts
	clone.Metrics = make([]TimeSeriesMetricConfig, len(ts.Metrics))
	for i := range ts.Metrics {
		clone.Metrics[i] = cloneTimeSeriesMetricConfig(ts.Metrics[i])
	}
	applyTimeSeriesDefaults(&clone, defaultTimeSeriesConfig())
	return clone
}

func cloneTimeSeriesMetricConfig(metric TimeSeriesMetricConfig) TimeSeriesMetricConfig {
	clone := metric
	clone.Aggregates = append([]TimeSeriesAggregate(nil), metric.Aggregates...)
	clone.Dimensions = append([]TimeSeriesDimensionConfig(nil), metric.Dimensions...)
	if metric.Retention != nil {
		retention := *metric.Retention
		clone.Retention = &retention
	}
	if metric.Cardinality != nil {
		cardinality := *metric.Cardinality
		clone.Cardinality = &cardinality
	}
	if metric.Privacy != nil {
		privacy := *metric.Privacy
		privacy.RedactPatterns = append([]string(nil), metric.Privacy.RedactPatterns...)
		clone.Privacy = &privacy
	}
	return clone
}

func (c *Config) validateTimeSeries() error {
	d := defaultTimeSeriesConfig()
	applyTimeSeriesDefaults(&c.TimeSeries, d)
	return c.TimeSeries.Validate()
}

func applyTimeSeriesDefaults(c *TimeSeriesConfig, d TimeSeriesConfig) {
	if c.Defaults.ValueType == "" {
		c.Defaults.ValueType = d.Defaults.ValueType
	}
	if len(c.Defaults.Aggregates) == 0 {
		c.Defaults.Aggregates = append([]TimeSeriesAggregate(nil), d.Defaults.Aggregates...)
	}
	if c.Defaults.BucketInterval == "" {
		c.Defaults.BucketInterval = d.Defaults.BucketInterval
	}
	if c.Defaults.TimeBasis == "" {
		c.Defaults.TimeBasis = d.Defaults.TimeBasis
	}
	if c.Defaults.DedupeScope == "" {
		c.Defaults.DedupeScope = d.Defaults.DedupeScope
	}
	if c.Defaults.FailurePolicy == "" {
		c.Defaults.FailurePolicy = d.Defaults.FailurePolicy
	}
	if c.Retention.Raw == "" {
		c.Retention.Raw = d.Retention.Raw
	}
	if c.Retention.Aggregated == "" {
		c.Retention.Aggregated = d.Retention.Aggregated
	}
	if c.Retention.Schedule == "" {
		c.Retention.Schedule = d.Retention.Schedule
	}
	if c.Retention.BatchSize == 0 {
		c.Retention.BatchSize = d.Retention.BatchSize
	}
	if c.Retention.MaxBatches == 0 {
		c.Retention.MaxBatches = d.Retention.MaxBatches
	}
	if c.Retention.MaxDuration == "" {
		c.Retention.MaxDuration = d.Retention.MaxDuration
	}
	if c.Aggregation.Schedule == "" {
		c.Aggregation.Schedule = d.Aggregation.Schedule
	}
	if c.Aggregation.BatchSize == 0 {
		c.Aggregation.BatchSize = d.Aggregation.BatchSize
	}
	if c.Aggregation.MaxBatches == 0 {
		c.Aggregation.MaxBatches = d.Aggregation.MaxBatches
	}
	if c.Aggregation.MaxDuration == "" {
		c.Aggregation.MaxDuration = d.Aggregation.MaxDuration
	}
	if c.Aggregation.Overlap == "" {
		c.Aggregation.Overlap = d.Aggregation.Overlap
	}
	if c.Storage.Backend == "" {
		c.Storage.Backend = d.Storage.Backend
	}
	if c.Storage.TablePrefix == "" {
		c.Storage.TablePrefix = d.Storage.TablePrefix
	}
	if c.Storage.Partitioning.Interval == "" {
		c.Storage.Partitioning.Interval = d.Storage.Partitioning.Interval
	}
	if c.Storage.Partitioning.Precreate == 0 {
		c.Storage.Partitioning.Precreate = d.Storage.Partitioning.Precreate
	}
	if c.Cardinality.MaxSeriesPerMetric == 0 {
		c.Cardinality.MaxSeriesPerMetric = d.Cardinality.MaxSeriesPerMetric
	}
	if c.Cardinality.MaxDimensions == 0 {
		c.Cardinality.MaxDimensions = d.Cardinality.MaxDimensions
	}
	if c.Cardinality.MaxValuesPerDimension == 0 {
		c.Cardinality.MaxValuesPerDimension = d.Cardinality.MaxValuesPerDimension
	}
	if c.Cardinality.Overflow == "" {
		c.Cardinality.Overflow = d.Cardinality.Overflow
	}
	if c.Privacy.MaxValueLength == 0 {
		c.Privacy.MaxValueLength = d.Privacy.MaxValueLength
	}
	if c.Privacy.RedactPatterns == nil {
		c.Privacy.RedactPatterns = []string{}
	}
	for i := range c.Metrics {
		m := &c.Metrics[i]
		if m.ValueType == "" {
			m.ValueType = c.Defaults.ValueType
		}
		if len(m.Aggregates) == 0 {
			m.Aggregates = append([]TimeSeriesAggregate(nil), c.Defaults.Aggregates...)
		}
		if m.BucketInterval == "" {
			m.BucketInterval = c.Defaults.BucketInterval
		}
		if m.TimeBasis == "" {
			m.TimeBasis = c.Defaults.TimeBasis
		}
		if m.DedupeScope == "" {
			m.DedupeScope = c.Defaults.DedupeScope
		}
		if m.FailurePolicy == "" {
			m.FailurePolicy = c.Defaults.FailurePolicy
		}
		if m.Retention != nil {
			if strings.TrimSpace(m.Retention.Raw) == "" {
				m.Retention.Raw = c.Retention.Raw
			}
			if strings.TrimSpace(m.Retention.Aggregated) == "" {
				m.Retention.Aggregated = c.Retention.Aggregated
			}
		}
		if m.Cardinality != nil {
			if m.Cardinality.MaxSeriesPerMetric == 0 {
				m.Cardinality.MaxSeriesPerMetric = c.Cardinality.MaxSeriesPerMetric
			}
			if m.Cardinality.MaxDimensions == 0 {
				m.Cardinality.MaxDimensions = c.Cardinality.MaxDimensions
			}
			if m.Cardinality.MaxValuesPerDimension == 0 {
				m.Cardinality.MaxValuesPerDimension = c.Cardinality.MaxValuesPerDimension
			}
			if m.Cardinality.Overflow == "" {
				m.Cardinality.Overflow = c.Cardinality.Overflow
			}
		}
		if m.Privacy != nil {
			if m.Privacy.MaxValueLength == 0 {
				m.Privacy.MaxValueLength = c.Privacy.MaxValueLength
			}
			if m.Privacy.RedactPatterns == nil {
				m.Privacy.RedactPatterns = append([]string(nil), c.Privacy.RedactPatterns...)
			}
		}
	}
}

func (c TimeSeriesConfig) Validate() error {
	var errs []string
	validateValue := func(field, value string, allowed map[string]bool) {
		if !allowed[value] {
			errs = append(errs, fmt.Sprintf("timeseries.%s: invalid value %q", field, value))
		}
	}
	validateValue("defaults.value_type", string(c.Defaults.ValueType), valueTypes)
	validateAggregates("defaults", c.Defaults.ValueType, c.Defaults.Aggregates, &errs)
	validateValue("defaults.bucket_interval", string(c.Defaults.BucketInterval), bucketIntervals)
	validateValue("defaults.time_basis", string(c.Defaults.TimeBasis), timeBases)
	validateValue("defaults.dedupe_scope", string(c.Defaults.DedupeScope), dedupeScopes)
	validateFailure("defaults.failure_policy", c.Defaults.FailurePolicy, &errs)
	validateRetentionWindow("retention.raw", c.Retention.Raw, &errs)
	validateRetentionWindow("retention.aggregated", c.Retention.Aggregated, &errs)
	validatePositiveDuration("retention.schedule", c.Retention.Schedule, &errs)
	validatePositiveDuration("aggregation.schedule", c.Aggregation.Schedule, &errs)
	validatePositiveDuration("aggregation.max_duration", c.Aggregation.MaxDuration, &errs)
	validatePositiveDuration("aggregation.overlap", c.Aggregation.Overlap, &errs)
	if c.Retention.BatchSize < 1 || c.Retention.BatchSize > 100000 {
		errs = append(errs, "timeseries.retention.batch_size: must be between 1 and 100000")
	}
	if c.Retention.MaxBatches < 1 || c.Retention.MaxBatches > 1000 {
		errs = append(errs, "timeseries.retention.max_batches: must be between 1 and 1000")
	}
	validatePositiveDuration("retention.max_duration", c.Retention.MaxDuration, &errs)
	if c.Aggregation.BatchSize < 1 || c.Aggregation.BatchSize > 100000 {
		errs = append(errs, "timeseries.aggregation.batch_size: must be between 1 and 100000")
	}
	if c.Aggregation.MaxBatches < 1 || c.Aggregation.MaxBatches > 1000 {
		errs = append(errs, "timeseries.aggregation.max_batches: must be between 1 and 1000")
	}
	if c.Storage.Backend != "postgres" {
		errs = append(errs, fmt.Sprintf("timeseries.storage.backend: invalid value %q", c.Storage.Backend))
	}
	if strings.TrimSpace(c.Storage.TablePrefix) == "" {
		errs = append(errs, "timeseries.storage.table_prefix: must not be empty")
	}
	validateValue("storage.partitioning.interval", string(c.Storage.Partitioning.Interval), bucketIntervalsNoNone)
	if c.Storage.Partitioning.Precreate < 0 || c.Storage.Partitioning.Precreate > 365 {
		errs = append(errs, "timeseries.storage.partitioning.precreate: must be between 0 and 365")
	}
	validateCardinality("cardinality", c.Cardinality, &errs)
	validatePrivacy("privacy", c.Privacy, &errs)

	seen := map[string]int{}
	for i := range c.Metrics {
		m := c.Metrics[i]
		p := fmt.Sprintf("metrics[%d]", i)
		m.Key = strings.TrimSpace(m.Key)
		if m.Key == "" {
			errs = append(errs, fmt.Sprintf("timeseries.%s.key: must not be empty", p))
		} else if first, ok := seen[m.Key]; ok {
			errs = append(errs, fmt.Sprintf("timeseries.%s.key: duplicate key %q (first used at metrics[%d].key)", p, m.Key, first))
		} else {
			seen[m.Key] = i
		}
		validateValue(p+".source_kind", string(m.SourceKind), sourceKinds)
		if m.SourceKind == TimeSeriesSourceObjectAttribute {
			validateValue(p+".object_type", string(m.ObjectType), objectTypes)
		} else if m.ObjectType != "" {
			errs = append(errs, fmt.Sprintf("timeseries.%s.object_type: only applies to source_kind %q", p, TimeSeriesSourceObjectAttribute))
		}
		if required := requiredSelectorKey(m.SourceKind); required != "" && selectorString(m.Selector, required) == "" {
			errs = append(errs, fmt.Sprintf("timeseries.%s.selector.%s: is required for source_kind %q", p, required, m.SourceKind))
		}
		validateValue(p+".value_type", string(m.ValueType), valueTypes)
		validateAggregates(p, m.ValueType, m.Aggregates, &errs)
		validateValue(p+".bucket_interval", string(m.BucketInterval), bucketIntervals)
		validateValue(p+".time_basis", string(m.TimeBasis), timeBases)
		validateValue(p+".dedupe_scope", string(m.DedupeScope), dedupeScopes)
		validateFailure(p+".failure_policy", m.FailurePolicy, &errs)
		if (m.TimeBasis == TimeSeriesTimeEventAt || m.TimeBasis == TimeSeriesTimeSourceTimestamp) && len(m.TimestampSelector) == 0 {
			errs = append(errs, fmt.Sprintf("timeseries.%s.timestamp_selector: is required when time_basis is %q", p, m.TimeBasis))
		}
		if len(m.Dimensions) > c.Cardinality.MaxDimensions {
			errs = append(errs, fmt.Sprintf("timeseries.%s.dimensions: has %d entries; maximum is %d", p, len(m.Dimensions), c.Cardinality.MaxDimensions))
		}
		dims := map[string]bool{}
		for j, dim := range m.Dimensions {
			dp := fmt.Sprintf("%s.dimensions[%d]", p, j)
			if strings.TrimSpace(dim.Key) == "" {
				errs = append(errs, "timeseries."+dp+".key: must not be empty")
			} else if dims[dim.Key] {
				errs = append(errs, fmt.Sprintf("timeseries.%s.key: duplicate dimension key %q", dp, dim.Key))
			}
			dims[dim.Key] = true
			if len(dim.Selector) == 0 {
				errs = append(errs, "timeseries."+dp+".selector: must not be empty")
			}
		}
		if m.Cardinality != nil {
			validateCardinality(p+".cardinality", *m.Cardinality, &errs)
		}
		if m.Retention != nil {
			if strings.TrimSpace(m.Retention.Raw) != "" {
				validateRetentionWindow(p+".retention.raw", m.Retention.Raw, &errs)
			}
			if strings.TrimSpace(m.Retention.Aggregated) != "" {
				validateRetentionWindow(p+".retention.aggregated", m.Retention.Aggregated, &errs)
			}
		}
		if m.Privacy != nil {
			validatePrivacy(p+".privacy", *m.Privacy, &errs)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid time-series configuration: %s", strings.Join(errs, "; "))
	}
	return nil
}

var (
	sourceKinds           = stringSet(TimeSeriesSourceKeyword, TimeSeriesSourceMetatag, TimeSeriesSourceObjectAttribute, TimeSeriesSourceWebObject, TimeSeriesSourceHTTPInfo, TimeSeriesSourceNetInfo, TimeSeriesSourceScreenshot, TimeSeriesSourceFile, TimeSeriesSourceInformationSeed, TimeSeriesSourceInformationSeedCandidate, TimeSeriesSourceDiscovery, TimeSeriesSourceEntityMembership, TimeSeriesSourceObjectCorrelation, TimeSeriesSourceCorrelationRule, TimeSeriesSourceCustom)
	objectTypes           = stringSet(TimeSeriesObjectWebObject, TimeSeriesObjectHTTPInfo, TimeSeriesObjectNetInfo)
	valueTypes            = stringSet(TimeSeriesValueInteger, TimeSeriesValueDecimal, TimeSeriesValueDuration, TimeSeriesValueBoolean, TimeSeriesValueString, TimeSeriesValueJSON, TimeSeriesValueCount, TimeSeriesValueTimestamp)
	aggregates            = stringSet(TimeSeriesAggregateCount, TimeSeriesAggregateSum, TimeSeriesAggregateAverage, TimeSeriesAggregateMinimum, TimeSeriesAggregateMaximum, TimeSeriesAggregateDistinctCount, TimeSeriesAggregateFirst, TimeSeriesAggregateLast, TimeSeriesAggregateP50, TimeSeriesAggregateP75, TimeSeriesAggregateP90, TimeSeriesAggregateP95, TimeSeriesAggregateP99)
	bucketIntervals       = stringSet(TimeSeriesBucketNone, TimeSeriesBucketOneMinute, TimeSeriesBucketFiveMinutes, TimeSeriesBucketFifteenMinutes, TimeSeriesBucketOneHour, TimeSeriesBucketOneDay, TimeSeriesBucketOneWeek, TimeSeriesBucketOneMonth)
	bucketIntervalsNoNone = stringSet(TimeSeriesBucketOneMinute, TimeSeriesBucketFiveMinutes, TimeSeriesBucketFifteenMinutes, TimeSeriesBucketOneHour, TimeSeriesBucketOneDay, TimeSeriesBucketOneWeek, TimeSeriesBucketOneMonth)
	timeBases             = stringSet(TimeSeriesTimeObservedAt, TimeSeriesTimeEventAt, TimeSeriesTimeSourceTimestamp)
	dedupeScopes          = stringSet(TimeSeriesDedupeNone, TimeSeriesDedupeSource, TimeSeriesDedupeObject, TimeSeriesDedupeGlobal)
	overflowBehaviors     = stringSet(TimeSeriesCardinalityDrop, TimeSeriesCardinalityHash, TimeSeriesCardinalityOverflowBucket)
)

func stringSet[T ~string](values ...T) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[string(v)] = true
	}
	return out
}
func requiredSelectorKey(k TimeSeriesSourceKind) string {
	switch k {
	case TimeSeriesSourceMetatag:
		return "metatag_name"
	case TimeSeriesSourceObjectAttribute:
		return "attribute_key"
	default:
		return ""
	}
}
func selectorString(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
func validatePositiveDuration(field, value string, errs *[]string) {
	d, err := parseTimeSeriesFixedDuration(value)
	if err != nil || d <= 0 {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s: must be a positive Go duration", field))
	}
}
func validateRetentionWindow(field, value string, errs *[]string) {
	if _, err := ParseTimeSeriesRetentionWindow(value); err != nil {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s: %v", field, err))
	}
}

func validateFailure(field string, value TimeSeriesFailurePolicy, errs *[]string) {
	if value != TimeSeriesFailureLogSkip && value != TimeSeriesFailureRetry && value != TimeSeriesFailureFailIndexing && value != TimeSeriesFailureLog && value != TimeSeriesFailureSkip {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s: invalid value %q", field, value))
	}
}
func validateCardinality(field string, c TimeSeriesCardinalityConfig, errs *[]string) {
	if c.MaxSeriesPerMetric < 1 || c.MaxSeriesPerMetric > 10000000 {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s.max_series_per_metric: must be between 1 and 10000000", field))
	}
	if c.MaxDimensions < 0 || c.MaxDimensions > 64 {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s.max_dimensions: must be between 0 and 64", field))
	}
	if c.MaxValuesPerDimension < 1 || c.MaxValuesPerDimension > 1000000 {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s.max_values_per_dimension: must be between 1 and 1000000", field))
	}
	if !overflowBehaviors[string(c.Overflow)] {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s.overflow: invalid value %q", field, c.Overflow))
	}
}
func validatePrivacy(field string, p TimeSeriesPrivacyConfig, errs *[]string) {
	if p.MaxValueLength < 1 || p.MaxValueLength > 1048576 {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s.max_value_length: must be between 1 and 1048576", field))
	}
	if p.HashOnly && p.StoreValueText {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s: hash_only and store_value_text cannot both be true", field))
	}
	for i, pattern := range p.RedactPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			*errs = append(*errs, fmt.Sprintf("timeseries.%s.redact_patterns[%d]: invalid regular expression: %v", field, i, err))
		}
	}
}
func validateAggregates(field string, valueType TimeSeriesValueType, values []TimeSeriesAggregate, errs *[]string) {
	numeric := valueType == TimeSeriesValueInteger || valueType == TimeSeriesValueDecimal || valueType == TimeSeriesValueDuration || valueType == TimeSeriesValueCount
	seen := map[TimeSeriesAggregate]bool{}
	for i, a := range values {
		if !aggregates[string(a)] {
			*errs = append(*errs, fmt.Sprintf("timeseries.%s.aggregates[%d]: invalid value %q", field, i, a))
			continue
		}
		if seen[a] {
			*errs = append(*errs, fmt.Sprintf("timeseries.%s.aggregates[%d]: duplicate aggregate %q", field, i, a))
		}
		seen[a] = true
		needsNumeric := a == TimeSeriesAggregateSum || a == TimeSeriesAggregateAverage || a == TimeSeriesAggregateMinimum || a == TimeSeriesAggregateMaximum || strings.HasPrefix(string(a), "p")
		if needsNumeric && !numeric {
			*errs = append(*errs, fmt.Sprintf("timeseries.%s.aggregates[%d]: %q requires integer, decimal, or duration values (got %q)", field, i, a, valueType))
		}
		if (valueType == TimeSeriesValueBoolean || valueType == TimeSeriesValueJSON) && (a == TimeSeriesAggregateFirst || a == TimeSeriesAggregateLast) {
			*errs = append(*errs, fmt.Sprintf("timeseries.%s.aggregates[%d]: %q is incompatible with %q values", field, i, a, valueType))
		}
	}
	if len(values) == 0 {
		*errs = append(*errs, fmt.Sprintf("timeseries.%s.aggregates: must contain at least one aggregate", field))
	}
}
