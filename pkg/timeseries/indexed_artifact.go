// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package timeseries

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// IndexedArtifactInput describes a persisted artifact after its index linkage succeeds.
// It is shared by index-owned sources such as keywords and metadata.
type IndexedArtifactInput struct {
	SourceKind  cfg.TimeSeriesSourceKind
	IndexID     uint64
	RowID       uint64
	LinkID      uint64
	SubjectKey  string
	Name        string
	RawValue    string
	Value       interface{}
	Occurrences int64
	Attributes  map[string]interface{}
	ObservedAt  time.Time
}

// IndexedArtifactScopeResolver keeps crawler ownership queries outside the emitter.
type IndexedArtifactScopeResolver interface {
	ResolveIndexedArtifactScopes(input IndexedArtifactInput) ([]cdb.TimeSeriesScope, error)
}

// EmitIndexedArtifact emits matching keyword or metatag metrics through the shared
// policy, change-detection, dedupe, and persistence path.
func (e *Emitter) EmitIndexedArtifact(input IndexedArtifactInput) error {
	if e == nil || e.Repository == nil || e.ArtifactScopes == nil || e.Config == nil || !e.Config.Enabled {
		return nil
	}
	if input.SourceKind != cfg.TimeSeriesSourceKeyword && input.SourceKind != cfg.TimeSeriesSourceMetatag {
		return nil
	}
	enabled := true
	metrics, err := e.Repository.ListMetrics(cdb.TimeSeriesMetricFilter{SourceKind: input.SourceKind, Enabled: &enabled, Pagination: cdb.TimeSeriesPagination{Limit: 10000}})
	if err != nil {
		return e.handleFailure(e.Config.Defaults.FailurePolicy, fmt.Sprintf("lookup %s metrics", input.SourceKind), err)
	}
	for i := range metrics {
		metric := metrics[i]
		if metric.SourceKind != input.SourceKind || !metric.Enabled {
			continue
		}
		if err = e.emitIndexedArtifactMetric(metric, input); err != nil {
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

func (e *Emitter) emitIndexedArtifactMetric(metric cdb.TimeSeriesMetric, input IndexedArtifactInput) error {
	selector, err := decodeMap(metric.Selector)
	if err != nil {
		return fmt.Errorf("decode selector: %w", err)
	}
	selected, transformations, matched, err := selectIndexedArtifactValue(input, selector)
	if err != nil || !matched {
		return err
	}
	value, err := parseIndexedArtifactValue(metric.ValueType, selected)
	if err != nil {
		return err
	}
	scopes, err := e.ArtifactScopes.ResolveIndexedArtifactScopes(input)
	if err != nil {
		return fmt.Errorf("resolve scopes: %w", err)
	}
	if len(scopes) == 0 {
		indexID := input.IndexID
		scopes = []cdb.TimeSeriesScope{{IndexID: &indexID}}
	}
	dimensions, err := e.resolveIndexedArtifactDimensions(metric, input, selected)
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
	effectiveAt, sourceUpdatedAt, timestampSource, err := e.resolveIndexedArtifactTimes(metric, input, selected)
	if err != nil {
		return err
	}
	bucketStart, bucketEnd, err := cdb.TimeSeriesBucketBounds(observedAt, metric.Bucket)
	if err != nil {
		return err
	}
	basePolicy := e.preparationPolicy(metric)
	cardinalityPolicy := e.cardinalityPolicy(metric)
	for _, resolved := range scopes {
		scope := resolved
		scope.SubjectType = string(input.SourceKind)
		subjectID := input.RowID
		scope.SubjectID = &subjectID
		scope.SubjectText = input.SubjectKey
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
			nonce = fmt.Sprintf("%s:%d:%d:%s", input.SourceKind, input.RowID, input.LinkID, observedAt.Format(time.RFC3339Nano))
		}
		observation.DedupeKey, err = cdb.TimeSeriesDedupeKey(metric.DedupeScope, metric.ID, observation, nonce)
		if err != nil {
			return err
		}
		provenance := map[string]interface{}{
			"source_kind": string(input.SourceKind),
			"row_id":      input.RowID,
			"link_id":     input.LinkID,
			"index_id":    input.IndexID,
			"subject_key": input.SubjectKey,
			"parser":      string(metric.ValueType),
		}
		if input.SourceKind == cfg.TimeSeriesSourceKeyword {
			provenance["keyword_id"] = input.RowID
			provenance["keyword_index_id"] = input.LinkID
			provenance["normalized_keyword"] = input.SubjectKey
			provenance["occurrences"] = input.Occurrences
		} else {
			provenance["metatag_id"] = input.RowID
			provenance["metatag_index_id"] = input.LinkID
			provenance["normalized_name"] = input.SubjectKey
		}
		if timestampSource != "" {
			provenance["timestamp_source"] = timestampSource
		}
		if len(transformations) > 0 {
			provenance["transformations"] = transformations
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

func parseIndexedArtifactValue(valueType cfg.TimeSeriesValueType, input interface{}) (cdb.TimeSeriesValue, error) {
	if valueType != cfg.TimeSeriesValueCount {
		return parseValue(valueType, input)
	}
	return parseValue(cfg.TimeSeriesValueInteger, input)
}

func selectIndexedArtifactValue(input IndexedArtifactInput, selector map[string]interface{}) (interface{}, []string, bool, error) {
	caseInsensitive := true
	exact := stringValue(selector["subject_key"])
	if exact == "" {
		if input.SourceKind == cfg.TimeSeriesSourceKeyword {
			exact = stringValue(selector["keyword"])
		} else {
			exact = stringValue(selector["metatag_name"])
			if exact == "" {
				exact = stringValue(selector["name"])
			}
		}
	}
	if exact == "" {
		exact = stringValue(selector["equals"])
	}
	if exact != "" && !artifactTextEqual(input.SubjectKey, exact, caseInsensitive) {
		return nil, nil, false, nil
	}
	matchValue := input.SubjectKey
	if rule, ok := selector["rule"].(map[string]interface{}); ok {
		matched, err := matchArtifactRule(matchValue, rule, caseInsensitive)
		if err != nil || !matched {
			return nil, nil, false, err
		}
	}
	expression := stringValue(selector["subject_regex"])
	if expression == "" {
		expression = stringValue(selector["regex"])
	}
	if expression != "" {
		if caseInsensitive && !strings.HasPrefix(expression, "(?") {
			expression = "(?i)" + expression
		}
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, nil, false, err
		}
		if !re.MatchString(matchValue) {
			return nil, nil, false, nil
		}
	}
	value := input.Value
	if from := stringValue(selector["from"]); from != "" {
		resolved, ok, err := resolveIndexedArtifactSelector(map[string]interface{}{"from": from, "path": selector["path"]}, input, value)
		if err != nil || !ok {
			return nil, nil, false, err
		}
		value = resolved
	}
	transformations := stringSlice(selector["transformations"])
	if one := stringValue(selector["transform"]); one != "" {
		transformations = append(transformations, one)
	}
	value, err := applyTransformations(value, transformations)
	return value, transformations, err == nil, err
}

func matchArtifactRule(value string, rule map[string]interface{}, caseInsensitive bool) (bool, error) {
	normalize := func(text string) string {
		if caseInsensitive {
			return strings.ToLower(text)
		}
		return text
	}
	candidate := normalize(value)
	for key, raw := range rule {
		expected := normalize(stringValue(raw))
		switch strings.ToLower(key) {
		case "equals":
			if candidate != expected {
				return false, nil
			}
		case "contains":
			if !strings.Contains(candidate, expected) {
				return false, nil
			}
		case "prefix", "starts_with":
			if !strings.HasPrefix(candidate, expected) {
				return false, nil
			}
		case "suffix", "ends_with":
			if !strings.HasSuffix(candidate, expected) {
				return false, nil
			}
		case "regex":
			expression := stringValue(raw)
			if caseInsensitive && !strings.HasPrefix(expression, "(?") {
				expression = "(?i)" + expression
			}
			re, err := regexp.Compile(expression)
			if err != nil {
				return false, err
			}
			if !re.MatchString(value) {
				return false, nil
			}
		default:
			return false, fmt.Errorf("unsupported artifact rule %q", key)
		}
	}
	return true, nil
}

func artifactTextEqual(left, right string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
	}
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func (e *Emitter) resolveIndexedArtifactDimensions(metric cdb.TimeSeriesMetric, input IndexedArtifactInput, selected interface{}) (map[string]interface{}, error) {
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
		value, ok, err := resolveIndexedArtifactSelector(definition.Selector, input, selected)
		if err != nil {
			return nil, fmt.Errorf("dimension %q: %w", definition.Key, err)
		}
		if ok {
			result[definition.Key] = value
		}
	}
	return result, nil
}

func resolveIndexedArtifactSelector(selector map[string]interface{}, input IndexedArtifactInput, selected interface{}) (interface{}, bool, error) {
	if constant, ok := selector["constant"]; ok {
		return constant, true, nil
	}
	var root interface{}
	switch stringValue(selector["from"]) {
	case "value":
		root = selected
	case "content", "raw_value":
		root = input.RawValue
	case "subject", "subject_key", "name", "keyword":
		root = input.SubjectKey
	case "occurrences":
		root = input.Occurrences
	case "metric", "artifact":
		root = map[string]interface{}{"source_kind": string(input.SourceKind), "subject_key": input.SubjectKey, "name": input.Name, "row_id": input.RowID, "link_id": input.LinkID, "index_id": input.IndexID, "occurrences": input.Occurrences}
	default:
		root = input.Attributes
	}
	if root == nil {
		return nil, false, nil
	}
	if path := stringValue(selector["path"]); path != "" {
		if text, ok := root.(string); ok {
			var decoded interface{}
			if err := json.Unmarshal([]byte(text), &decoded); err != nil {
				return nil, false, fmt.Errorf("selector path %q requires JSON: %w", path, err)
			}
			root = decoded
		}
		value, ok := lookupPath(root, path)
		return value, ok, nil
	}
	return root, true, nil
}

func (e *Emitter) resolveIndexedArtifactTimes(metric cdb.TimeSeriesMetric, input IndexedArtifactInput, selected interface{}) (*time.Time, *time.Time, string, error) {
	var timestampSelector map[string]interface{}
	if configured := findMetricConfig(e.Config.Metrics, metric.Key); configured != nil {
		timestampSelector = configured.TimestampSelector
	}
	var selectedTime *time.Time
	timestampSource := ""
	if len(timestampSelector) > 0 {
		value, ok, err := resolveIndexedArtifactSelector(timestampSelector, input, selected)
		if err != nil {
			return nil, nil, "", err
		}
		if !ok {
			return nil, nil, "", fmt.Errorf("timestamp selector is not resolvable")
		}
		parsed, err := parseTimestamp(value)
		if err != nil {
			return nil, nil, "", err
		}
		selectedTime = &parsed
		timestampSource = stringValue(timestampSelector["from"])
		if timestampSource == "" {
			timestampSource = "attributes"
		}
	}
	switch metric.TimeBasis {
	case "", cfg.TimeSeriesTimeObservedAt:
		return nil, nil, timestampSource, nil
	case cfg.TimeSeriesTimeSourceTimestamp:
		if selectedTime == nil {
			return nil, nil, "", fmt.Errorf("source timestamp is not resolvable")
		}
		return nil, selectedTime, timestampSource, nil
	case cfg.TimeSeriesTimeEventAt:
		if selectedTime == nil {
			return nil, nil, "", fmt.Errorf("event timestamp is not resolvable")
		}
		return selectedTime, nil, timestampSource, nil
	default:
		return nil, nil, "", fmt.Errorf("unsupported time basis %q", metric.TimeBasis)
	}
}
