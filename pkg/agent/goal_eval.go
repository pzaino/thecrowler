// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EvaluateGoal is the deterministic verifier. It has no network, database,
// model or filesystem access: all trusted observations are supplied in the
// input. Unknown/invalid input fails closed.
func EvaluateGoal(input GoalEvalInput) (EvaluationResult, error) {
	if input.Spec == nil {
		return EvaluationResult{}, fmt.Errorf("missing goal spec")
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := EvaluationResult{
		State:       "not_met",
		Criteria:    make([]CriterionResult, 0, len(input.Spec.Acceptance.Criteria)),
		EvaluatedAt: now,
	}
	for _, criterion := range input.Spec.Acceptance.Criteria {
		result.Criteria = append(result.Criteria, evalCriterion(criterion, input, now))
	}
	result.State = combineAcceptance(input.Spec.Acceptance.Mode, result.Criteria)
	return result, nil
}

func evalCriterion(criterion CriterionSpec, input GoalEvalInput, now time.Time) CriterionResult {
	maxAge := input.Spec.Evidence.DefaultMaxAge
	if criterion.Freshness != nil && criterion.Freshness.MaxAge > 0 {
		maxAge = criterion.Freshness.MaxAge
	}
	outcome := evalPredicate(criterion.Predicate, input, now, maxAge)
	return CriterionResult{
		ID:             criterion.ID,
		Result:         string(outcome.state),
		ReasonCode:     outcome.reason,
		EvidenceIDs:    uniqueStrings(outcome.evidenceIDs),
		EvidenceSource: outcome.source,
		ObservedValue:  outcome.observed,
		ObservedAt:     outcome.observedAt,
	}
}

type predicateOutcome struct {
	state       CriterionState
	reason      string
	evidenceIDs []string
	source      string
	observed    any
	observedAt  string
}

func evalPredicate(predicate Predicate, input GoalEvalInput, now time.Time, maxAge time.Duration) predicateOutcome {
	switch predicate.Kind {
	case PredicateAll, PredicateAny:
		return evalComposition(predicate, input, now, maxAge)
	case PredicateEvidenceExists, PredicateEvidenceCount, PredicateFieldEquals,
		PredicateFieldIn, PredicateFieldNumber, PredicateEvidenceScope:
		return evalEvidencePredicate(predicate, input, now, maxAge)
	case PredicateResourceStatus:
		return evalResourceStatus(predicate, input, now, maxAge)
	case PredicateEventObserved:
		return outcome(CriterionError, "event_provenance_unavailable")
	default:
		return outcome(CriterionError, "predicate_invalid")
	}
}

func evalComposition(predicate Predicate, input GoalEvalInput, now time.Time, maxAge time.Duration) predicateOutcome {
	if len(predicate.Predicates) == 0 {
		return outcome(CriterionError, "predicate_invalid")
	}
	children := make([]predicateOutcome, 0, len(predicate.Predicates))
	for _, child := range predicate.Predicates {
		children = append(children, evalPredicate(child, input, now, maxAge))
	}
	// Authorization and verification failures always outrank a pass. This
	// preserves fail-closed behavior for top-level `any` and nested `any`.
	for _, state := range []CriterionState{CriterionError, CriterionDenied} {
		for _, child := range children {
			if child.state == state {
				return mergeOutcome(state, child.reason, children)
			}
		}
	}
	if predicate.Kind == PredicateAny {
		for _, child := range children {
			if child.state == CriterionPass {
				return mergeOutcome(CriterionPass, "criteria_any_met", children)
			}
		}
		return worstNonPass(children)
	}
	for _, child := range children {
		if child.state != CriterionPass {
			return worstNonPass(children)
		}
	}
	return mergeOutcome(CriterionPass, "criteria_all_met", children)
}

func worstNonPass(children []predicateOutcome) predicateOutcome {
	for _, state := range []CriterionState{CriterionError, CriterionDenied, CriterionFail, CriterionStale, CriterionUnavailable} {
		for _, child := range children {
			if child.state == state {
				return mergeOutcome(state, child.reason, children)
			}
		}
	}
	return mergeOutcome(CriterionFail, "criteria_not_met", children)
}

func mergeOutcome(state CriterionState, reason string, children []predicateOutcome) predicateOutcome {
	merged := predicateOutcome{state: state, reason: reason}
	for _, child := range children {
		if child.state == state {
			if merged.source == "" {
				merged.source = child.source
				merged.observed = child.observed
				merged.observedAt = child.observedAt
			}
		}
		merged.evidenceIDs = append(merged.evidenceIDs, child.evidenceIDs...)
	}
	if merged.reason == "" {
		merged.reason = "criteria_not_met"
	}
	return merged
}

func evalEvidencePredicate(predicate Predicate, input GoalEvalInput, now time.Time, maxAge time.Duration) predicateOutcome {
	records, state, reason := matchingEvidence(predicate.Evidence, predicate.Where, input, now, maxAge)
	if state != "" {
		return outcome(state, reason)
	}
	switch predicate.Kind {
	case PredicateEvidenceExists:
		if len(records) == 0 {
			return outcome(CriterionUnavailable, "no_matching_evidence")
		}
		return evidenceOutcome(CriterionPass, "evidence_exists", records)
	case PredicateEvidenceCount:
		count := len(records)
		want := 0
		if value, ok := goalToFloat(predicate.Value); ok {
			want = int(value)
		}
		query, hasQuery, queryOutcome := evidenceQueryResultFor(input, predicate.Evidence, predicate.Where, now, maxAge)
		if queryOutcome.state != "" {
			return queryOutcome
		}
		if count == 0 {
			if predicate.Op == CompareEq && want == 0 {
				if !hasQuery {
					return outcome(CriterionUnavailable, "evidence_query_completion_unavailable")
				}
				if query.State != EvidenceQueryCompleted || query.Count != 0 || query.Limit > 0 && query.Count >= query.Limit {
					return queryFailureOutcome(query)
				}
				return outcome(CriterionPass, "evidence_count_satisfied")
			}
			if hasQuery {
				if query.State == EvidenceQueryFailed || query.State == EvidenceQueryTruncated {
					return queryFailureOutcome(query)
				}
			}
			return outcome(CriterionUnavailable, "no_matching_evidence")
		}
		if hasQuery && query.State == EvidenceQueryTruncated {
			switch predicate.Op {
			case CompareEq, CompareLt, CompareLte:
				return outcome(CriterionError, "evidence_query_truncated")
			case CompareGte, CompareGt:
				if compareInt(count, predicate.Op, want) {
					return evidenceOutcome(CriterionPass, "evidence_count_satisfied", records)
				}
				return outcome(CriterionUnavailable, "evidence_query_truncated")
			}
		}
		if compareInt(count, predicate.Op, want) {
			return evidenceOutcome(CriterionPass, "evidence_count_satisfied", records)
		}
		return evidenceOutcome(CriterionFail, "evidence_count_not_satisfied", records)
	case PredicateFieldEquals, PredicateFieldIn, PredicateFieldNumber:
		return evalFieldPredicate(predicate, records, input, now, maxAge)
	case PredicateEvidenceScope:
		query, hasQuery, queryOutcome := evidenceQueryResultFor(input, predicate.Evidence, predicate.Where, now, maxAge)
		if queryOutcome.state != "" {
			return queryOutcome
		}
		if hasQuery && query.State == EvidenceQueryTruncated {
			return outcome(CriterionError, "evidence_query_truncated")
		}
		if len(records) == 0 {
			return outcome(CriterionUnavailable, "no_matching_evidence")
		}
		for _, record := range records {
			if len(input.Spec.AllowedSources) > 0 && !recordWithinScope(record, input.Spec.AllowedSources) {
				return evidenceOutcome(CriterionDenied, "evidence_out_of_scope", records)
			}
			if predicate.SourceID != nil {
				if len(record.SourceIDs) == 0 || !containsSource(record.SourceIDs, *predicate.SourceID) {
					return evidenceOutcome(CriterionDenied, "evidence_out_of_scope", records)
				}
			}
		}
		return evidenceOutcome(CriterionPass, "evidence_in_scope", records)
	default:
		return outcome(CriterionError, "predicate_invalid")
	}
}

func evalFieldPredicate(predicate Predicate, records []EvidenceRecord, input GoalEvalInput, now time.Time, maxAge time.Duration) predicateOutcome {
	if len(records) == 0 {
		return outcome(CriterionUnavailable, "no_matching_evidence")
	}
	matched := 0
	missing := 0
	mismatched := 0
	selected := make([]EvidenceRecord, 0, len(records))
	for _, record := range records {
		value, found := lookupGoalPath(record.Payload, predicate.Path)
		if !found {
			missing++
			continue
		}
		selected = append(selected, record)
		ok := false
		switch predicate.Kind {
		case PredicateFieldEquals:
			ok = goalValuesEqual(value, predicate.Value)
		case PredicateFieldIn:
			for _, candidate := range predicate.Values {
				if goalValuesEqual(value, candidate) {
					ok = true
					break
				}
			}
		case PredicateFieldNumber:
			number, isNumber := goalToFloat(value)
			want, wantOK := goalToFloat(predicate.Value)
			if !isNumber || !wantOK {
				mismatched++
				continue
			}
			ok = compareFloat(number, predicate.Op, want)
		}
		if ok {
			matched++
		} else {
			mismatched++
		}
	}
	quantifier := predicate.Quantifier
	if quantifier == "" {
		quantifier = "any"
	}
	query, hasQuery, queryOutcome := evidenceQueryResultFor(input, predicate.Evidence, predicate.Where, now, maxAge)
	if queryOutcome.state != "" {
		return queryOutcome
	}
	if hasQuery && query.State == EvidenceQueryTruncated && quantifier == "all" {
		return outcome(CriterionError, "evidence_query_truncated")
	}
	if quantifier == "all" {
		if mismatched > 0 {
			return evidenceOutcome(CriterionFail, "evidence_contradictory", records)
		}
		if missing > 0 {
			return outcome(CriterionUnavailable, "field_missing")
		}
		return evidenceOutcome(CriterionPass, "field_satisfied", selected)
	}
	if matched > 0 {
		return evidenceOutcome(CriterionPass, "field_satisfied", selected)
	}
	if mismatched > 0 {
		return evidenceOutcome(CriterionFail, "field_not_satisfied", selected)
	}
	return outcome(CriterionUnavailable, "field_missing")
}

func evalResourceStatus(predicate Predicate, input GoalEvalInput, now time.Time, maxAge time.Duration) predicateOutcome {
	key := resourceObservationKey(predicate.Resource, predicate.ResourceID)
	obs, ok := input.Resources[key]
	if !ok {
		return outcome(CriterionUnavailable, "resource_unavailable")
	}
	if obs.Err != "" {
		if obs.Err == "out_of_scope" || obs.Err == "denied" {
			return outcome(CriterionDenied, "evidence_out_of_scope")
		}
		return outcome(CriterionUnavailable, obs.Err)
	}
	if input.RunID != "" && (obs.RunID != input.RunID || obs.GoalID != input.GoalID || obs.GoalSpecHash != input.GoalSpecHash) {
		return outcome(CriterionError, "evidence_provenance_invalid")
	}
	cutoff := now.Add(-maxAge)
	if maxAge > 0 && !obs.ObservedAt.IsZero() && obs.ObservedAt.Before(cutoff) {
		return outcome(CriterionStale, "evidence_stale")
	}
	if maxAge > 0 {
		if obs.DataUpdatedAt.IsZero() {
			return outcome(CriterionUnavailable, "freshness_unavailable")
		}
		if obs.DataUpdatedAt.Before(cutoff) {
			return outcome(CriterionStale, "evidence_stale")
		}
	}
	observedAt := ""
	if !obs.ObservedAt.IsZero() {
		observedAt = obs.ObservedAt.UTC().Format(time.RFC3339)
	}
	if obs.Status == predicate.Status {
		return predicateOutcome{
			state: CriterionPass, reason: "resource_status_satisfied",
			source: obs.Source, observed: obs.Status, observedAt: observedAt,
		}
	}
	return predicateOutcome{
		state: CriterionFail, reason: "resource_status_not_satisfied",
		source: obs.Source, observed: obs.Status, observedAt: observedAt,
	}
}

func matchingEvidence(kind EvidenceKind, selector *EvidenceSelector, input GoalEvalInput, now time.Time, maxAge time.Duration) ([]EvidenceRecord, CriterionState, string) {
	filter := EvidenceSelector{}
	if selector != nil {
		filter = *selector
	}
	hash := selectorHash(filter)
	cutoff := now.Add(-maxAge)
	query, hasQuery, queryOutcome := evidenceQueryResultFor(input, kind, selector, now, maxAge)
	if queryOutcome.state != "" {
		return nil, queryOutcome.state, queryOutcome.reason
	}
	if hasQuery && query.State != EvidenceQueryCompleted && query.State != EvidenceQueryTruncated {
		if query.State == EvidenceQueryUnavailable {
			return nil, CriterionUnavailable, query.Reason
		}
		return nil, CriterionError, query.Reason
	}
	var fresh, stale []EvidenceRecord
	for _, record := range input.Records {
		if record.Kind != kind || record.SelectorSHA256 != hash {
			continue
		}
		if err := validateEvidenceRecord(record, input); err != nil {
			return nil, CriterionError, "evidence_provenance_invalid"
		}
		if len(input.Spec.AllowedSources) > 0 && !recordWithinScope(record, input.Spec.AllowedSources) {
			return nil, CriterionDenied, "evidence_out_of_scope"
		}
		if filter.SourceID != nil {
			if len(record.SourceIDs) == 0 || len(record.SourceIDs) != 1 || record.SourceIDs[0] != *filter.SourceID {
				return nil, CriterionDenied, "evidence_source_mismatch"
			}
		}
		if kind == EvidenceToolObservation && (filter.Tool != "" || filter.ArgumentsSHA256 != "") {
			if record.Tool != filter.Tool || record.ArgumentsSHA256 != filter.ArgumentsSHA256 {
				return nil, CriterionDenied, "evidence_tool_mismatch"
			}
		}
		if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
			stale = append(stale, record)
			continue
		}
		if maxAge > 0 && !record.ObservedAt.IsZero() && record.ObservedAt.Before(cutoff) {
			stale = append(stale, record)
			continue
		}
		if maxAge > 0 {
			if kindRequiresDataTimestamp(kind) && record.DataUpdatedAt.IsZero() {
				stale = append(stale, record)
				continue
			}
			if !record.DataUpdatedAt.IsZero() && record.DataUpdatedAt.Before(cutoff) {
				stale = append(stale, record)
				continue
			}
		}
		fresh = append(fresh, record)
	}
	if len(fresh) > 0 {
		return fresh, "", ""
	}
	if len(stale) > 0 {
		return nil, CriterionStale, "evidence_stale"
	}
	if hasQuery {
		if query.State == EvidenceQueryTruncated {
			return nil, CriterionError, query.Reason
		}
		return nil, "", ""
	}
	return nil, "", ""
}

func evidenceQueryResultFor(input GoalEvalInput, kind EvidenceKind, selector *EvidenceSelector, now time.Time, maxAge time.Duration) (EvidenceQueryResult, bool, predicateOutcome) {
	filter := EvidenceSelector{}
	if selector != nil {
		filter = *selector
	}
	query, ok := input.Queries[evidenceQueryKey(kind, filter)]
	if !ok {
		return EvidenceQueryResult{}, false, predicateOutcome{}
	}
	if problem := validateEvidenceQueryResult(query, input, kind, filter, now, maxAge); problem.state != "" {
		return query, true, problem
	}
	return query, true, predicateOutcome{}
}

func validateEvidenceQueryResult(query EvidenceQueryResult, input GoalEvalInput, kind EvidenceKind, selector EvidenceSelector, now time.Time, maxAge time.Duration) predicateOutcome {
	if query.Kind != kind || query.SelectorSHA256 != selectorHash(selector) {
		return outcome(CriterionError, "evidence_query_selector_mismatch")
	}
	if input.LedgerID == "" || query.LedgerID != input.LedgerID ||
		query.RunID != input.RunID || query.GoalID != input.GoalID ||
		query.GoalSpecHash != input.GoalSpecHash ||
		query.AgentID != input.AgentID || query.Owner != input.Owner {
		return outcome(CriterionError, "evidence_query_provenance_invalid")
	}
	if query.CompletedAt.IsZero() || query.QuerySHA256 == "" || query.QuerySHA256 != goalQueryHash(query) {
		return outcome(CriterionError, "evidence_query_completion_invalid")
	}
	if maxAge > 0 && query.CompletedAt.Before(now.Add(-maxAge)) {
		return outcome(CriterionStale, "evidence_query_stale")
	}
	if query.Limit <= 0 || query.Count < 0 {
		return outcome(CriterionError, "evidence_query_invalid")
	}
	switch query.State {
	case EvidenceQueryCompleted:
		if query.Count >= query.Limit {
			return outcome(CriterionError, "evidence_query_invalid")
		}
	case EvidenceQueryTruncated:
		if query.Count != query.Limit {
			return outcome(CriterionError, "evidence_query_invalid")
		}
	case EvidenceQueryUnavailable, EvidenceQueryFailed:
		if query.Count != 0 {
			return outcome(CriterionError, "evidence_query_invalid")
		}
	default:
		return outcome(CriterionError, "evidence_query_invalid")
	}
	return predicateOutcome{}
}

func queryFailureOutcome(query EvidenceQueryResult) predicateOutcome {
	if query.State == EvidenceQueryUnavailable {
		return outcome(CriterionUnavailable, query.Reason)
	}
	return outcome(CriterionError, query.Reason)
}

// validateEvidenceRecord enforces run/ledger/spec provenance, trust class and
// integrity. A hash alone is not authority: the record must be stamped by the
// same ledger and carry a permitted trust class.
func validateEvidenceRecord(record EvidenceRecord, input GoalEvalInput) error {
	if input.LedgerID == "" || input.RunID == "" || input.GoalID == "" ||
		input.GoalSpecHash == "" || input.RunNonceHash == "" || input.AgentID == "" {
		return fmt.Errorf("evaluator provenance context is incomplete")
	}
	if record.EvidenceID == "" || record.LedgerID != input.LedgerID ||
		record.RunID != input.RunID || record.GoalID != input.GoalID ||
		record.GoalSpecHash != input.GoalSpecHash ||
		record.RunNonceHash != input.RunNonceHash ||
		record.AgentID != input.AgentID || record.Owner != input.Owner {
		return fmt.Errorf("evidence does not belong to this run")
	}
	if record.TrustClass != TrustSystemVerified && record.TrustClass != TrustToolObservation {
		return fmt.Errorf("evidence trust class %q is not acceptable", record.TrustClass)
	}
	if record.ObservedAt.IsZero() || record.ExpiresAt.IsZero() {
		return fmt.Errorf("evidence timestamps are incomplete")
	}
	encoded, err := json.Marshal(record.Payload)
	if err != nil {
		return fmt.Errorf("evidence payload is not serializable")
	}
	sum := sha256.Sum256(encoded)
	if hex.EncodeToString(sum[:]) != record.PayloadSHA256 {
		return fmt.Errorf("evidence payload integrity check failed")
	}
	if recordHash(record) != record.RecordSHA256 {
		return fmt.Errorf("evidence record integrity check failed")
	}
	return nil
}

// kindRequiresDataTimestamp distinguishes historical extraction evidence
// from point-in-time metadata/observation checks.
func kindRequiresDataTimestamp(kind EvidenceKind) bool {
	switch kind {
	case EvidenceSourceStatus, EvidenceIndexedPage, EvidenceScrapedField, EvidenceArtifact:
		return true
	default:
		return false
	}
}

func recordWithinScope(record EvidenceRecord, allowed []uint64) bool {
	if len(record.SourceIDs) == 0 {
		return false
	}
	for _, id := range record.SourceIDs {
		if !containsSource(allowed, id) {
			return false
		}
	}
	return true
}

func evidenceOutcome(state CriterionState, reason string, records []EvidenceRecord) predicateOutcome {
	out := predicateOutcome{state: state, reason: reason}
	for _, record := range records {
		out.evidenceIDs = append(out.evidenceIDs, record.EvidenceID)
	}
	return out
}

func outcome(state CriterionState, reason string) predicateOutcome {
	return predicateOutcome{state: state, reason: reason}
}

func combineAcceptance(mode AcceptanceMode, criteria []CriterionResult) string {
	passes := 0
	hasError, hasDenied, hasFail, hasUnavailable := false, false, false, false
	for _, criterion := range criteria {
		switch CriterionState(criterion.Result) {
		case CriterionPass:
			passes++
		case CriterionError:
			hasError = true
		case CriterionDenied:
			hasDenied = true
		case CriterionFail:
			hasFail = true
		case CriterionUnavailable, CriterionStale:
			hasUnavailable = true
		}
	}
	// Authorization/verification failures outrank any pass. A passing
	// criterion cannot hide a denial or integrity error in another criterion.
	if hasError {
		return "error"
	}
	if hasDenied {
		return "not_met"
	}
	if mode == AcceptanceAny && passes > 0 {
		return "met"
	}
	if mode == AcceptanceAll && passes == len(criteria) {
		return "met"
	}
	if hasFail {
		return "not_met"
	}
	if hasUnavailable {
		return "incomplete"
	}
	return "not_met"
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func compareInt(actual int, op CompareOp, want int) bool {
	switch op {
	case CompareEq:
		return actual == want
	case CompareLt:
		return actual < want
	case CompareLte:
		return actual <= want
	case CompareGt:
		return actual > want
	case CompareGte:
		return actual >= want
	default:
		return false
	}
}

func compareFloat(actual float64, op CompareOp, want float64) bool {
	switch op {
	case CompareEq:
		return actual == want
	case CompareLt:
		return actual < want
	case CompareLte:
		return actual <= want
	case CompareGt:
		return actual > want
	case CompareGte:
		return actual >= want
	default:
		return false
	}
}

func goalValuesEqual(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftNumber, leftOK := goalToFloat(left)
	rightNumber, rightOK := goalToFloat(right)
	if leftOK || rightOK {
		return leftOK && rightOK && leftNumber == rightNumber
	}
	switch l := left.(type) {
	case string:
		r, ok := right.(string)
		return ok && l == r
	case bool:
		r, ok := right.(bool)
		return ok && l == r
	default:
		return false
	}
}

func lookupGoalPath(payload map[string]any, path string) (any, bool) {
	if payload == nil || path == "" {
		return nil, false
	}
	segments, err := parseGoalPath(path)
	if err != nil || len(segments) == 0 {
		return nil, false
	}
	var current any = payload
	for _, segment := range segments {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := m[segment.key]
		if !ok {
			return nil, false
		}
		current = value
		for _, index := range segment.indexes {
			array, ok := current.([]any)
			if !ok || index < 0 || index >= len(array) {
				return nil, false
			}
			current = array[index]
		}
	}
	return current, true
}

type goalPathSegment struct {
	key     string
	indexes []int
}

func parseGoalPath(path string) ([]goalPathSegment, error) {
	parts := strings.Split(path, ".")
	out := make([]goalPathSegment, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("empty path segment")
		}
		segment := goalPathSegment{}
		name := part
		if idx := strings.Index(part, "["); idx >= 0 {
			name = part[:idx]
			rest := part[idx:]
			for len(rest) > 0 {
				if rest[0] != '[' {
					return nil, fmt.Errorf("bad path")
				}
				end := strings.Index(rest, "]")
				if end <= 1 {
					return nil, fmt.Errorf("bad path")
				}
				number, err := strconv.Atoi(rest[1:end])
				if err != nil || number < 0 {
					return nil, fmt.Errorf("bad path index")
				}
				segment.indexes = append(segment.indexes, number)
				rest = rest[end+1:]
			}
		}
		if name == "" {
			return nil, fmt.Errorf("empty path key")
		}
		segment.key = name
		out = append(out, segment)
	}
	return out, nil
}
