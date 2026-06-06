// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package timeseries provides source-agnostic orchestration for emitting metric observations.
package timeseries

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Repository is the minimal Task 3 persistence contract needed by an emitter.
type Repository interface {
	ListMetrics(filter cdb.TimeSeriesMetricFilter) ([]cdb.TimeSeriesMetric, error)
	PreviousObservation(lookup cdb.TimeSeriesChangeLookup) (*cdb.TimeSeriesObservation, error)
	InsertObservation(observation *cdb.TimeSeriesObservation) (cdb.TimeSeriesInsertResult, error)
}

// ScopeResolver keeps source-specific ownership discovery outside the emitter.
type ScopeResolver interface {
	ResolveScopes(input ObjectAttributeInput) ([]cdb.TimeSeriesScope, error)
}

// CardinalityGuard owns backend-specific series and dimension accounting.
type CardinalityGuard interface {
	Exceeded(metric cdb.TimeSeriesMetric, scope cdb.TimeSeriesScope, dimensions map[string]interface{}, policy cfg.TimeSeriesCardinalityConfig) (bool, error)
}

// Logger is intentionally small so crawler logging and tests can supply adapters.
type Logger interface {
	Printf(format string, args ...interface{})
}

// ObjectAttributeInput is the agnostic event produced after an ObjectAttributes write.
type ObjectAttributeInput struct {
	ObjectType        string
	ObjectID          uint64
	AttributeKey      string
	RawValue          string
	NormalizedValue   string
	AttributeType     string
	SelectorPath      string
	Transformations   []string
	ObjectDetails     map[string]interface{}
	SiblingAttributes map[string]interface{}
	ObservedAt        time.Time
	SourceUpdatedAt   *time.Time
}

// Emitter evaluates configured object_attribute metrics.
type Emitter struct {
	Repository     Repository
	Scopes         ScopeResolver
	ArtifactScopes IndexedArtifactScopeResolver
	Cardinality    CardinalityGuard
	Config         *cfg.TimeSeriesConfig
	Logger         Logger
	Now            func() time.Time
}

// EmitObjectAttribute emits all matching enabled metrics. Per-metric safe failures are logged and skipped.
func (e *Emitter) EmitObjectAttribute(input ObjectAttributeInput) error {
	if e == nil || e.Repository == nil || e.Scopes == nil || e.Config == nil || !e.Config.Enabled {
		return nil
	}
	enabled := true
	metrics, err := e.Repository.ListMetrics(cdb.TimeSeriesMetricFilter{SourceKind: cfg.TimeSeriesSourceObjectAttribute, Enabled: &enabled, Pagination: cdb.TimeSeriesPagination{Limit: 10000}})
	if err != nil {
		return e.handleFailure(e.Config.Defaults.FailurePolicy, "lookup object-attribute metrics", err)
	}
	for i := range metrics {
		metric := metrics[i]
		if metric.SourceKind != cfg.TimeSeriesSourceObjectAttribute || !metric.Enabled {
			continue
		}
		if err = e.emitMetric(metric, input); err != nil {
			policy := metric.FailurePolicy
			if policy == "" {
				policy = e.Config.Defaults.FailurePolicy
			}
			if failure := e.handleFailure(policy, fmt.Sprintf("emit metric %q", metric.Key), err); failure != nil {
				return failure
			}
		}
	}
	return nil
}

func (e *Emitter) emitMetric(metric cdb.TimeSeriesMetric, input ObjectAttributeInput) error {
	selector, err := decodeMap(metric.Selector)
	if err != nil {
		return fmt.Errorf("decode selector: %w", err)
	}
	if string(metric.ObjectType) != input.ObjectType || stringValue(selector["attribute_key"]) != input.AttributeKey {
		return nil
	}
	selected, path, transformations, matched, err := selectMetricValue(input, selector)
	if err != nil || !matched {
		return err
	}
	value, err := parseValue(metric.ValueType, selected)
	if err != nil {
		return err
	}

	scopes, err := e.Scopes.ResolveScopes(input)
	if err != nil {
		return fmt.Errorf("resolve scopes: %w", err)
	}
	if len(scopes) == 0 {
		id := input.ObjectID
		scopes = []cdb.TimeSeriesScope{{ObjectType: input.ObjectType, ObjectID: &id}}
	}
	dimensions, err := e.resolveDimensions(metric, input, selected)
	if err == nil {
		dimensions, err = redactDimensions(dimensions, e.preparationPolicy(metric).RedactPatterns)
	}
	if err != nil {
		return err
	}
	observedAt := input.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = e.now()
	}
	effectiveAt, sourceUpdatedAt, err := e.resolveTimes(metric, input, selected, observedAt)
	if err != nil {
		return err
	}
	bucketStart, bucketEnd, err := cdb.TimeSeriesBucketBounds(observedAt, metric.Bucket)
	if err != nil {
		return err
	}
	basePolicy := e.preparationPolicy(metric)
	cardinalityPolicy := e.cardinalityPolicy(metric)
	for _, scope := range scopes {
		policy := basePolicy
		if e.Cardinality != nil {
			policy.CardinalityExceeded, err = e.Cardinality.Exceeded(metric, scope, dimensions, cardinalityPolicy)
			if err != nil {
				return fmt.Errorf("check cardinality: %w", err)
			}
		}
		observation := cdb.TimeSeriesObservation{MetricID: metric.ID, ObservedAt: observedAt, EffectiveAt: effectiveAt, CollectedAt: e.now(), SourceUpdatedAt: sourceUpdatedAt, BucketStart: bucketStart, BucketEnd: bucketEnd, Scope: scope, Value: value, Dimensions: cloneMap(dimensions)}
		prepared, prepareErr := cdb.PrepareTimeSeriesObservation(observation, metric.ValueType, policy)
		if prepareErr != nil {
			return prepareErr
		}
		observation = prepared.Observation
		previous, previousErr := e.Repository.PreviousObservation(cdb.TimeSeriesChangeLookup{MetricID: metric.ID, Scope: scope, Dimensions: observation.Dimensions, Before: observedAt, TimeBasis: metric.TimeBasis})
		if previousErr != nil && !errors.Is(previousErr, cdb.ErrTimeSeriesObservationNotFound) {
			return fmt.Errorf("lookup previous observation: %w", previousErr)
		}
		applyChange(&observation, previous, previousErr, observedAt)
		nonce := ""
		if metric.DedupeScope == cfg.TimeSeriesDedupeNone {
			nonce = fmt.Sprintf("%s:%d:%s:%s", input.ObjectType, input.ObjectID, input.AttributeKey, observedAt.Format(time.RFC3339Nano))
		}
		observation.DedupeKey, err = cdb.TimeSeriesDedupeKey(metric.DedupeScope, metric.ID, observation, nonce)
		if err != nil {
			return err
		}
		provenance := map[string]interface{}{"source_kind": string(cfg.TimeSeriesSourceObjectAttribute), "attribute_key": input.AttributeKey, "object_type": input.ObjectType, "object_id": input.ObjectID}
		if scope.SourceID != nil {
			provenance["source_id"] = *scope.SourceID
		}
		selectorPath := path
		if selectorPath == "" {
			selectorPath = input.SelectorPath
		}
		if selectorPath != "" {
			provenance["selector_path"] = selectorPath
		}
		allTransformations := append(append([]string(nil), input.Transformations...), transformations...)
		if len(allTransformations) > 0 {
			provenance["transformations"] = allTransformations
		}
		if prepared.Redacted {
			provenance["redacted"] = true
		}
		if prepared.HashedOnly {
			provenance["hash_only"] = true
		}
		if prepared.Truncated {
			provenance["truncated"] = true
		}
		observation.Provenance, err = json.Marshal(provenance)
		if err != nil {
			return err
		}
		observation.ProvenanceHash, err = cdb.TimeSeriesProvenanceHash(observation.Provenance)
		if err != nil {
			return err
		}
		if _, err = e.Repository.InsertObservation(&observation); err != nil {
			return err
		}
	}
	return nil
}

func (e *Emitter) cardinalityPolicy(metric cdb.TimeSeriesMetric) cfg.TimeSeriesCardinalityConfig {
	policy := e.Config.Cardinality
	if configured := findMetricConfig(e.Config.Metrics, metric.Key); configured != nil && configured.Cardinality != nil {
		policy = *configured.Cardinality
	}
	return policy
}

func (e *Emitter) preparationPolicy(metric cdb.TimeSeriesMetric) cdb.TimeSeriesPreparationPolicy {
	privacy := e.Config.Privacy
	cardinality := e.Config.Cardinality
	if configured := findMetricConfig(e.Config.Metrics, metric.Key); configured != nil {
		if configured.Privacy != nil {
			privacy = *configured.Privacy
		}
		if configured.Cardinality != nil {
			cardinality = *configured.Cardinality
		}
	}
	return cdb.TimeSeriesPreparationPolicy{MaxDimensions: cardinality.MaxDimensions, MaxValueLength: privacy.MaxValueLength, RedactPatterns: privacy.RedactPatterns, HashOnly: metric.HashOnly || privacy.HashOnly, StoreValueText: metric.StoreValueText || privacy.StoreValueText, Overflow: cardinality.Overflow}
}

func (e *Emitter) resolveDimensions(metric cdb.TimeSeriesMetric, input ObjectAttributeInput, selected interface{}) (map[string]interface{}, error) {
	var definitions []cfg.TimeSeriesDimensionConfig
	if configured := findMetricConfig(e.Config.Metrics, metric.Key); configured != nil {
		definitions = configured.Dimensions
	}
	if len(definitions) == 0 && len(metric.Dimensions) > 0 {
		if err := json.Unmarshal(metric.Dimensions, &definitions); err != nil {
			return nil, fmt.Errorf("decode dimensions: %w", err)
		}
	}
	result := make(map[string]interface{}, len(definitions))
	for _, definition := range definitions {
		value, ok, err := resolveSelector(definition.Selector, input, selected)
		if err != nil {
			return nil, fmt.Errorf("dimension %q: %w", definition.Key, err)
		}
		if ok {
			result[definition.Key] = value
		}
	}
	return result, nil
}

func redactDimensions(dimensions map[string]interface{}, patterns []string) (map[string]interface{}, error) {
	if len(dimensions) == 0 || len(patterns) == 0 {
		return dimensions, nil
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, re)
	}
	var redact func(interface{}) interface{}
	redact = func(value interface{}) interface{} {
		switch typed := value.(type) {
		case string:
			for _, re := range compiled {
				typed = re.ReplaceAllString(typed, "[REDACTED]")
			}
			return typed
		case []interface{}:
			out := make([]interface{}, len(typed))
			for i := range typed {
				out[i] = redact(typed[i])
			}
			return out
		case map[string]interface{}:
			out := make(map[string]interface{}, len(typed))
			for key, item := range typed {
				out[key] = redact(item)
			}
			return out
		default:
			return value
		}
	}
	out := make(map[string]interface{}, len(dimensions))
	for key, value := range dimensions {
		out[key] = redact(value)
	}
	return out, nil
}

func selectMetricValue(input ObjectAttributeInput, selector map[string]interface{}) (interface{}, string, []string, bool, error) {
	var value interface{} = input.NormalizedValue
	path := stringValue(selector["path"])
	if path != "" {
		decoded := interface{}(nil)
		if err := json.Unmarshal([]byte(input.RawValue), &decoded); err != nil {
			return nil, path, nil, false, fmt.Errorf("selector path %q requires JSON: %w", path, err)
		}
		var ok bool
		value, ok = lookupPath(decoded, path)
		if !ok {
			return nil, path, nil, false, nil
		}
	}
	if expected, ok := selector["equals"]; ok && fmt.Sprint(value) != fmt.Sprint(expected) {
		return nil, path, nil, false, nil
	}
	if expression := stringValue(selector["regex"]); expression != "" {
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, path, nil, false, err
		}
		matches := re.FindStringSubmatch(fmt.Sprint(value))
		if matches == nil {
			return nil, path, nil, false, nil
		}
		if len(matches) > 1 {
			value = matches[1]
		}
	}
	transformations := stringSlice(selector["transformations"])
	if one := stringValue(selector["transform"]); one != "" {
		transformations = append(transformations, one)
	}
	var err error
	value, err = applyTransformations(value, transformations)
	return value, path, transformations, err == nil, err
}

func resolveSelector(selector map[string]interface{}, input ObjectAttributeInput, selected interface{}) (interface{}, bool, error) {
	if constant, ok := selector["constant"]; ok {
		return constant, true, nil
	}
	from := stringValue(selector["from"])
	path := stringValue(selector["path"])
	var root interface{}
	switch from {
	case "value":
		root = selected
	case "metric":
		root = map[string]interface{}{"attribute_key": input.AttributeKey, "object_type": input.ObjectType, "attribute_type": input.AttributeType}
	case "sibling", "sibling_attribute":
		key := stringValue(selector["attribute_key"])
		root = input.SiblingAttributes[key]
	default:
		if key := stringValue(selector["attribute_key"]); key != "" {
			root = input.SiblingAttributes[key]
		} else {
			root = input.ObjectDetails
		}
	}
	if root == nil {
		return nil, false, nil
	}
	if path != "" {
		value, ok := lookupPath(root, path)
		return value, ok, nil
	}
	return root, true, nil
}

func parseValue(valueType cfg.TimeSeriesValueType, input interface{}) (cdb.TimeSeriesValue, error) {
	text := strings.TrimSpace(fmt.Sprint(input))
	switch valueType {
	case cfg.TimeSeriesValueInteger:
		v, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return cdb.TimeSeriesValue{}, fmt.Errorf("parse integer %q: %w", text, err)
		}
		return cdb.TimeSeriesValue{Integer: &v}, nil
	case cfg.TimeSeriesValueDecimal, cfg.TimeSeriesValueDuration:
		var v float64
		var err error
		if valueType == cfg.TimeSeriesValueDuration {
			var duration time.Duration
			duration, err = time.ParseDuration(text)
			if err == nil {
				v = duration.Seconds()
			} else {
				v, err = strconv.ParseFloat(text, 64)
			}
		} else {
			v, err = strconv.ParseFloat(text, 64)
		}
		if err != nil {
			return cdb.TimeSeriesValue{}, fmt.Errorf("parse numeric %q: %w", text, err)
		}
		return cdb.TimeSeriesValue{Numeric: &v}, nil
	case cfg.TimeSeriesValueBoolean:
		v, err := strconv.ParseBool(strings.ToLower(text))
		if err != nil {
			return cdb.TimeSeriesValue{}, fmt.Errorf("parse boolean %q: %w", text, err)
		}
		return cdb.TimeSeriesValue{Boolean: &v}, nil
	case cfg.TimeSeriesValueString:
		v := fmt.Sprint(input)
		return cdb.TimeSeriesValue{Text: &v}, nil
	case cfg.TimeSeriesValueJSON:
		raw, err := json.Marshal(input)
		if stringInput, ok := input.(string); ok && json.Valid([]byte(stringInput)) {
			raw = []byte(stringInput)
		} else if err != nil {
			return cdb.TimeSeriesValue{}, err
		}
		if !json.Valid(raw) {
			return cdb.TimeSeriesValue{}, fmt.Errorf("invalid JSON value")
		}
		return cdb.TimeSeriesValue{JSON: raw}, nil
	case cfg.TimeSeriesValueTimestamp:
		{
			v, err := parseTimestamp(input)
			if err != nil {
				return cdb.TimeSeriesValue{}, err
			}
			return cdb.TimeSeriesValue{Timestamp: &v}, nil
		}
	case cfg.TimeSeriesValueCount:
		v := int64(1)
		return cdb.TimeSeriesValue{Integer: &v}, nil
	default:
		return cdb.TimeSeriesValue{}, fmt.Errorf("unsupported value type %q", valueType)
	}
}

func (e *Emitter) resolveTimes(metric cdb.TimeSeriesMetric, input ObjectAttributeInput, selected interface{}, observed time.Time) (*time.Time, *time.Time, error) {
	var timestampSelector map[string]interface{}
	if configured := findMetricConfig(e.Config.Metrics, metric.Key); configured != nil {
		timestampSelector = configured.TimestampSelector
	}
	var selectedTime *time.Time
	if len(timestampSelector) > 0 {
		value, ok, err := resolveSelector(timestampSelector, input, selected)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("timestamp selector is not resolvable")
		}
		parsed, err := parseTimestamp(value)
		if err != nil {
			return nil, nil, err
		}
		selectedTime = &parsed
	}
	sourceUpdated := input.SourceUpdatedAt
	if sourceUpdated != nil {
		normalized := sourceUpdated.UTC()
		sourceUpdated = &normalized
	}
	switch metric.TimeBasis {
	case "", cfg.TimeSeriesTimeObservedAt:
		return nil, sourceUpdated, nil
	case cfg.TimeSeriesTimeSourceTimestamp:
		if selectedTime != nil {
			sourceUpdated = selectedTime
		}
		if sourceUpdated == nil {
			return nil, nil, fmt.Errorf("source timestamp is not resolvable")
		}
		return nil, sourceUpdated, nil
	case cfg.TimeSeriesTimeEventAt:
		if selectedTime == nil {
			return nil, sourceUpdated, fmt.Errorf("event timestamp is not resolvable")
		}
		return selectedTime, sourceUpdated, nil
	default:
		return nil, nil, fmt.Errorf("unsupported time basis %q", metric.TimeBasis)
	}
}

func applyChange(observation *cdb.TimeSeriesObservation, previous *cdb.TimeSeriesObservation, err error, at time.Time) {
	if errors.Is(err, cdb.ErrTimeSeriesObservationNotFound) || previous == nil {
		observation.IsChanged = true
		observation.ChangeType = "new"
		observation.ChangeDetectedAt = &at
		return
	}
	if err != nil {
		return
	}
	observation.PreviousObservationID = &previous.ID
	observation.PreviousValueHash = previous.ValueHash
	observation.IsChanged = previous.ValueHash != observation.ValueHash
	if previous.DeletedAt != nil {
		observation.ChangeType = "reappeared"
		observation.IsChanged = true
	} else if observation.IsChanged {
		observation.ChangeType = "changed"
	} else {
		observation.ChangeType = "unchanged"
	}
	if observation.IsChanged {
		observation.ChangeDetectedAt = &at
	}
	if previous.Value.Numeric != nil && observation.Value.Numeric != nil {
		delta := *observation.Value.Numeric - *previous.Value.Numeric
		observation.ChangeDeltaNumeric = &delta
	}
}

func (e *Emitter) handleFailure(policy cfg.TimeSeriesFailurePolicy, context string, err error) error {
	if err == nil {
		return nil
	}
	if policy == cfg.TimeSeriesFailureFailIndexing {
		return fmt.Errorf("%s: %w", context, err)
	}
	if policy == cfg.TimeSeriesFailureSkip {
		return nil
	}
	if e.Logger != nil {
		e.Logger.Printf("time-series %s: %v", context, err)
	}
	return nil
}
func (e *Emitter) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}
func findMetricConfig(metrics []cfg.TimeSeriesMetricConfig, key string) *cfg.TimeSeriesMetricConfig {
	for i := range metrics {
		if metrics[i].Key == key {
			return &metrics[i]
		}
	}
	return nil
}
func decodeMap(raw json.RawMessage) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if len(raw) == 0 {
		return result, nil
	}
	return result, json.Unmarshal(raw, &result)
}
func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func stringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			result = append(result, stringValue(item))
		}
		return result
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}
func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	output := make(map[string]interface{}, len(input))
	for k, v := range input {
		output[k] = v
	}
	return output
}
func parseTimestamp(value interface{}) (time.Time, error) {
	text := strings.TrimSpace(fmt.Sprint(value))
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), nil
		}
	}
	if unix, err := strconv.ParseInt(text, 10, 64); err == nil {
		if unix > 9999999999 {
			unix /= 1000
		}
		return time.Unix(unix, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("parse timestamp %q", text)
}
func lookupPath(root interface{}, path string) (interface{}, bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(path, "$"), "."), ".")
	return lookupPathParts(root, parts)
}

func lookupPathParts(current interface{}, parts []string) (interface{}, bool) {
	if len(parts) == 0 {
		return current, true
	}
	part := parts[0]
	if part == "" {
		return lookupPathParts(current, parts[1:])
	}
	key, wildcard, index, hasIndex := parseSelectorPathPart(part)
	if key != "" {
		typed, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = mapValueFold(typed, key)
		if !ok {
			return nil, false
		}
	}
	if wildcard {
		values, ok := current.([]interface{})
		if !ok {
			return nil, false
		}
		selected := make([]interface{}, 0, len(values))
		for _, value := range values {
			resolved, matched := lookupPathParts(value, parts[1:])
			if !matched {
				continue
			}
			if nested, isSlice := resolved.([]interface{}); isSlice {
				selected = append(selected, nested...)
			} else {
				selected = append(selected, resolved)
			}
		}
		return selected, true
	}
	if hasIndex {
		values, ok := current.([]interface{})
		if !ok || index < 0 || index >= len(values) {
			return nil, false
		}
		current = values[index]
	} else if key == "" {
		switch typed := current.(type) {
		case map[string]interface{}:
			value, ok := mapValueFold(typed, part)
			if !ok {
				return nil, false
			}
			current = value
		case []interface{}:
			parsed, err := strconv.Atoi(part)
			if err != nil || parsed < 0 || parsed >= len(typed) {
				return nil, false
			}
			current = typed[parsed]
		default:
			return nil, false
		}
	}
	return lookupPathParts(current, parts[1:])
}

func parseSelectorPathPart(part string) (key string, wildcard bool, index int, hasIndex bool) {
	if part == "[*]" || part == "*" {
		return "", true, 0, false
	}
	open := strings.Index(part, "[")
	if open < 0 || !strings.HasSuffix(part, "]") {
		return "", false, 0, false
	}
	key = part[:open]
	inside := part[open+1 : len(part)-1]
	if inside == "*" {
		return key, true, 0, false
	}
	parsed, err := strconv.Atoi(inside)
	if err != nil {
		return key, false, 0, false
	}
	return key, false, parsed, true
}

func mapValueFold(values map[string]interface{}, key string) (interface{}, bool) {
	if value, ok := values[key]; ok {
		return value, true
	}
	for candidate, value := range values {
		if strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return nil, false
}

func applyTransformations(value interface{}, transformations []string) (interface{}, error) {
	result := value
	for _, transformation := range transformations {
		switch strings.ToLower(strings.TrimSpace(transformation)) {
		case "", "identity":
		case "trim":
			result = strings.TrimSpace(fmt.Sprint(result))
		case "lowercase":
			result = strings.ToLower(fmt.Sprint(result))
		case "uppercase":
			result = strings.ToUpper(fmt.Sprint(result))
		case "length", "count":
			switch typed := result.(type) {
			case []interface{}:
				result = len(typed)
			case map[string]interface{}:
				result = len(typed)
			default:
				result = len([]rune(fmt.Sprint(result)))
			}
		case "first":
			if values, ok := result.([]interface{}); ok {
				if len(values) == 0 {
					return nil, nil
				}
				result = values[0]
			}
		case "sha256", "hash":
			canonical, err := cdb.CanonicalTimeSeriesJSON(result)
			if err != nil {
				return nil, err
			}
			result = cdb.TimeSeriesSubjectHash(string(canonical))
		case "milliseconds_to_seconds":
			number, err := strconv.ParseFloat(fmt.Sprint(result), 64)
			if err != nil {
				return nil, err
			}
			result = number / 1000
		default:
			return nil, fmt.Errorf("unsupported transformation %q", transformation)
		}
	}
	return result, nil
}
