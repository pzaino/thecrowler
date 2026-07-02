// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// NormalizeTimeSeriesSubject normalizes subject text before hashing: leading
// and trailing whitespace is removed, internal Unicode whitespace is collapsed
// to one ASCII space, and letters are lower-cased.
func NormalizeTimeSeriesSubject(subject string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(subject, unicode.IsSpace), " "))
}

// CanonicalTimeSeriesJSON returns deterministic JSON. Object keys are sorted by
// encoding/json, numbers retain their JSON spelling through UseNumber, and nil
// is encoded explicitly as null.
func CanonicalTimeSeriesJSON(value interface{}) ([]byte, error) {
	if raw, ok := value.(json.RawMessage); ok {
		if len(raw) == 0 {
			return []byte("null"), nil
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var decoded interface{}
		if err := decoder.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		value = decoded
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON: %w", err)
	}
	return encoded, nil
}

func timeSeriesSHA256(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		// Length framing prevents ambiguous concatenations.
		_, _ = fmt.Fprintf(h, "%d:%s|", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalOptionalUint64(value *uint64) string {
	if value == nil {
		return "absent"
	}
	return fmt.Sprintf("present:%d", *value)
}

func canonicalOptionalString(value string) string {
	if value == "" {
		return "absent"
	}
	return "present:" + value
}

func canonicalOptionalTime(value *time.Time) string {
	if value == nil {
		return "absent"
	}
	return "present:" + value.UTC().Format(time.RFC3339Nano)
}

// TimeSeriesSubjectHash hashes normalized subject text and explicitly represents absence.
func TimeSeriesSubjectHash(subject string) string {
	return timeSeriesSHA256("subject", canonicalOptionalString(NormalizeTimeSeriesSubject(subject)))
}

// TimeSeriesDimensionHash hashes canonical JSON with explicit absence.
func TimeSeriesDimensionHash(dimensions map[string]interface{}) (string, error) {
	if dimensions == nil {
		return timeSeriesSHA256("dimensions", "absent"), nil
	}
	canonical, err := CanonicalTimeSeriesJSON(dimensions)
	if err != nil {
		return "", err
	}
	return timeSeriesSHA256("dimensions", "present:"+string(canonical)), nil
}

// TimeSeriesValueHash hashes the logical value, including its selected type.
func TimeSeriesValueHash(valueType cfg.TimeSeriesValueType, value TimeSeriesValue) (string, error) {
	var representation string
	switch valueType {
	case cfg.TimeSeriesValueInteger, cfg.TimeSeriesValueCount:
		if value.Integer == nil {
			representation = "absent"
		} else {
			representation = fmt.Sprintf("present:%d", *value.Integer)
		}
	case cfg.TimeSeriesValueDecimal, cfg.TimeSeriesValueDuration:
		if value.Numeric == nil {
			representation = "absent"
		} else {
			representation = fmt.Sprintf("present:%g", *value.Numeric)
		}
	case cfg.TimeSeriesValueBoolean:
		if value.Boolean == nil {
			representation = "absent"
		} else {
			representation = fmt.Sprintf("present:%t", *value.Boolean)
		}
	case cfg.TimeSeriesValueString:
		if value.Text == nil {
			representation = "absent"
		} else {
			representation = "present:" + *value.Text
		}
	case cfg.TimeSeriesValueJSON:
		if len(value.JSON) == 0 {
			representation = "absent"
		} else {
			canonical, err := CanonicalTimeSeriesJSON(value.JSON)
			if err != nil {
				return "", err
			}
			representation = "present:" + string(canonical)
		}
	case cfg.TimeSeriesValueTimestamp:
		if value.Timestamp == nil {
			return "", fmt.Errorf("unsupported time-series value type %q", valueType)
		}
		representation = "present:" + value.Timestamp.UTC().Format(time.RFC3339Nano)
	default:
		return "", fmt.Errorf("unsupported time-series value type %q", valueType)
	}
	return timeSeriesSHA256("value", string(valueType), representation), nil
}

func canonicalScopeParts(scope TimeSeriesScope) []string {
	return []string{
		"seed=" + canonicalOptionalUint64(scope.InformationSeedID),
		"candidate=" + canonicalOptionalUint64(scope.InformationSeedCandidateID),
		"source=" + canonicalOptionalUint64(scope.SourceID),
		"source_seed=" + canonicalOptionalUint64(scope.SourceInformationSeedID),
		"index=" + canonicalOptionalUint64(scope.IndexID),
		"entity=" + canonicalOptionalUint64(scope.EntityID),
		"subject_type=" + canonicalOptionalString(scope.SubjectType),
		"subject_id=" + canonicalOptionalUint64(scope.SubjectID),
		"object_type=" + canonicalOptionalString(scope.ObjectType),
		"object_id=" + canonicalOptionalUint64(scope.ObjectID),
		"rule=" + canonicalOptionalUint64(scope.CorrelationRuleID),
		"correlation_type_1=" + canonicalOptionalString(scope.CorrelationObjectType1),
		"correlation_id_1=" + canonicalOptionalUint64(scope.CorrelationObjectID1),
		"correlation_type_2=" + canonicalOptionalString(scope.CorrelationObjectType2),
		"correlation_id_2=" + canonicalOptionalUint64(scope.CorrelationObjectID2),
	}
}

// TimeSeriesDedupeKey returns a key framed by the selected scope:
//   - none: includes a caller nonce, allowing every event;
//   - source: includes source/seed/index ownership;
//   - object: includes the complete source and object/subject/entity scope;
//   - global: ignores ownership and deduplicates metric/value/time globally.
//
// Every scope also includes metric, effective time, value, and dimensions.
func TimeSeriesDedupeKey(scope cfg.TimeSeriesDedupeScope, metricID uint64, observation TimeSeriesObservation, nonce string) (string, error) {
	dimensionHash, err := TimeSeriesDimensionHash(observation.Dimensions)
	if err != nil {
		return "", err
	}
	parts := []string{"dedupe_scope=" + string(scope), fmt.Sprintf("metric=%d", metricID), "observed_at=" + observation.ObservedAt.UTC().Format(time.RFC3339Nano), "effective_at=" + canonicalOptionalTime(observation.EffectiveAt), "value_hash=" + observation.ValueHash, "dimension_hash=" + dimensionHash}
	scopeParts := canonicalScopeParts(observation.Scope)
	switch scope {
	case cfg.TimeSeriesDedupeNone:
		if nonce == "" {
			return "", fmt.Errorf("dedupe scope %q requires a nonce", scope)
		}
		parts = append(parts, "nonce="+nonce)
	case cfg.TimeSeriesDedupeSource:
		parts = append(parts, scopeParts[0:5]...)
	case cfg.TimeSeriesDedupeObject:
		parts = append(parts, scopeParts...)
	case cfg.TimeSeriesDedupeGlobal:
		// Intentionally no owner fields.
	default:
		return "", fmt.Errorf("unsupported time-series dedupe scope %q", scope)
	}
	return timeSeriesSHA256(parts...), nil
}

// TimeSeriesAggregateHash includes every aggregate grouping field: metric,
// bucket boundaries, complete scope, and canonical dimensions.
func TimeSeriesAggregateHash(aggregate TimeSeriesAggregate) (string, error) {
	dimensionHash, err := TimeSeriesDimensionHash(aggregate.Dimensions)
	if err != nil {
		return "", err
	}
	parts := []string{"aggregate", fmt.Sprintf("metric=%d", aggregate.MetricID), "bucket_start=" + aggregate.BucketStart.UTC().Format(time.RFC3339Nano), "bucket_end=" + aggregate.BucketEnd.UTC().Format(time.RFC3339Nano)}
	parts = append(parts, canonicalScopeParts(aggregate.Scope)...)
	parts = append(parts, "dimension_hash="+dimensionHash)
	return timeSeriesSHA256(parts...), nil
}

// TimeSeriesBucketBounds returns [start,end) boundaries in UTC. Weeks start on
// Monday 00:00 UTC. Calendar months use "1mo" or "month" and end at the first
// day of the next UTC month.
func TimeSeriesBucketBounds(at time.Time, bucket cfg.TimeSeriesBucketInterval) (time.Time, time.Time, error) {
	at = at.UTC()
	var start time.Time
	switch string(bucket) {
	case "1m":
		start = at.Truncate(time.Minute)
		return start, start.Add(time.Minute), nil
	case "5m":
		start = at.Truncate(5 * time.Minute)
		return start, start.Add(5 * time.Minute), nil
	case "15m":
		start = at.Truncate(15 * time.Minute)
		return start, start.Add(15 * time.Minute), nil
	case "1h":
		start = at.Truncate(time.Hour)
		return start, start.Add(time.Hour), nil
	case "1d":
		start = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 1), nil
	case "1w":
		start = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		days := (int(start.Weekday()) + 6) % 7
		start = start.AddDate(0, 0, -days)
		return start, start.AddDate(0, 0, 7), nil
	case "1mo", "month":
		start = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0), nil
	case "none":
		return at, at, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported time-series bucket %q", bucket)
	}
}

// PrepareTimeSeriesObservation applies resolved privacy, value length, and
// cardinality transformations without performing SQL.
func PrepareTimeSeriesObservation(observation TimeSeriesObservation, valueType cfg.TimeSeriesValueType, policy TimeSeriesPreparationPolicy) (TimeSeriesPreparationResult, error) {
	result := TimeSeriesPreparationResult{Observation: observation}
	if policy.MaxDimensions > 0 && len(observation.Dimensions) > policy.MaxDimensions {
		return result, fmt.Errorf("%w: dimensions %d exceed limit %d", ErrTimeSeriesValueRejected, len(observation.Dimensions), policy.MaxDimensions)
	}
	if policy.CardinalityExceeded {
		switch policy.Overflow {
		case cfg.TimeSeriesCardinalityDrop:
			return result, fmt.Errorf("%w: cardinality limit exceeded", ErrTimeSeriesValueRejected)
		case cfg.TimeSeriesCardinalityHash:
			result.HashedOnly = true
		case cfg.TimeSeriesCardinalityOverflowBucket:
			bucket := policy.OverflowBucket
			if bucket == "" {
				bucket = "__overflow__"
			}
			result.Observation.Dimensions = map[string]interface{}{"overflow": bucket}
			result.Overflowed = true
		default:
			return result, fmt.Errorf("%w: unsupported cardinality overflow %q", ErrTimeSeriesValueRejected, policy.Overflow)
		}
	}
	if result.Observation.Value.Text != nil {
		value := *result.Observation.Value.Text
		for _, pattern := range policy.RedactPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return result, fmt.Errorf("invalid redaction pattern %q: %w", pattern, err)
			}
			replaced := re.ReplaceAllString(value, "[REDACTED]")
			if replaced != value {
				result.Redacted = true
				value = replaced
			}
		}
		if policy.MaxValueLength > 0 && len([]rune(value)) > policy.MaxValueLength {
			value = string([]rune(value)[:policy.MaxValueLength])
			result.Truncated = true
		}
		result.Observation.Value.Text = &value
	}
	hash, err := TimeSeriesValueHash(valueType, result.Observation.Value)
	if err != nil {
		return result, err
	}
	result.Observation.ValueHash = hash
	if policy.HashOnly || result.HashedOnly {
		result.Observation.Value = TimeSeriesValue{}
		result.HashedOnly = true
	} else if !policy.StoreValueText {
		result.Observation.Value.Text = nil
	}
	return result, nil
}

// TimeSeriesProvenanceHash hashes canonical provenance without exposing its values.
func TimeSeriesProvenanceHash(provenance json.RawMessage) (string, error) {
	if len(provenance) == 0 {
		return timeSeriesSHA256("provenance", "absent"), nil
	}
	canonical, err := CanonicalTimeSeriesJSON(provenance)
	if err != nil {
		return "", err
	}
	return timeSeriesSHA256("provenance", "present:"+string(canonical)), nil
}
