// Copyright 2023 Paolo Fabio Zaino
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

// Package main (API) implements the API server for the Crowler search engine.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	infoseeddiag "github.com/pzaino/thecrowler/pkg/infoseed"
)

var errInvalidSourceConfig = errors.New("invalid source configuration")

func isSourceConfigValidationError(err error) bool {
	return errors.Is(err, errInvalidSourceConfig)
}

const (
	errFailedToInitializeDBHandler = "Failed to initialize database handler"
	errFailedToConnectToDB         = "Error connecting to the database"
	errFailedToStartTransaction    = "Failed to start transaction"
	errFailedToCommitTransaction   = "Failed to commit transaction"

	infoAllSourcesStatus    = "All Sources status"
	infoAllInformationSeeds = "All Information Seeds"
	//infoSourceStatus     = "Source status"
	infoSourceRemoved = "Source and related data removed successfully"
)

func performAddInformationSeed(query string, qType int, db *cdb.Handler) (InformationSeedResponse, error) {
	var params informationSeedAddRequest
	if qType == getQuery {
		params.InformationSeed = strings.TrimSpace(query)
	} else {
		if err := rejectInformationSeedRequestCredentials([]byte(query)); err != nil {
			return InformationSeedResponse{Message: "Provider credentials are not accepted in information seed requests"}, err
		}
		if err := json.Unmarshal([]byte(query), &params); err != nil {
			return InformationSeedResponse{Message: "Invalid information seed request"}, fmt.Errorf("invalid JSON: %w", err)
		}
		if err := validateInformationSeedConfig(params.Config); err != nil {
			return InformationSeedResponse{Message: "Invalid information seed config"}, err
		}
	}

	params.InformationSeed = strings.TrimSpace(params.InformationSeed)
	if params.InformationSeed == "" {
		return InformationSeedResponse{Message: "Information seed text is required"}, fmt.Errorf("information_seed is required")
	}
	if params.UsrID == 0 && params.UserID != 0 {
		params.UsrID = params.UserID
	}
	if params.Status == "" {
		params.Status = "new"
	}

	id, err := cdb.CreateInformationSeedAndNotify(nil, db, &cdb.InformationSeed{
		CategoryID:      params.CategoryID,
		UsrID:           params.UsrID,
		InformationSeed: params.InformationSeed,
		Status:          params.Status,
		Priority:        params.Priority,
		Engine:          params.Engine,
		Disabled:        params.Disabled,
		Config:          params.Config,
	})
	if err != nil {
		return InformationSeedResponse{Message: "Failed to add information seed"}, err
	}

	row, err := informationSeedRowByID(db, id)
	if err != nil {
		return InformationSeedResponse{Message: "Failed to load added information seed"}, err
	}
	return InformationSeedResponse{Message: "Information seed added successfully", Item: row}, nil
}

func validateInformationSeedConfig(config *json.RawMessage) error {
	if config == nil {
		return nil
	}
	if len(*config) == 0 || !json.Valid(*config) {
		return fmt.Errorf("information seed config must be valid JSON")
	}
	var object map[string]interface{}
	if err := json.Unmarshal(*config, &object); err != nil {
		return fmt.Errorf("information seed config must be a JSON object: %w", err)
	}
	if object == nil {
		return fmt.Errorf("information seed config must be a JSON object")
	}
	return nil
}

func rejectInformationSeedRequestCredentials(raw []byte) error {
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	if key, ok := containsCredentialKey(decoded); ok {
		return fmt.Errorf("provider credential field %q must be configured globally, not in request bodies", key)
	}
	return nil
}

func containsCredentialKey(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, nested := range typed {
			if isInformationSeedCredentialKey(key) {
				return key, true
			}
			if nestedKey, ok := containsCredentialKey(nested); ok {
				return nestedKey, true
			}
		}
	case []interface{}:
		for _, nested := range typed {
			if nestedKey, ok := containsCredentialKey(nested); ok {
				return nestedKey, true
			}
		}
	}
	return "", false
}

func isInformationSeedCredentialKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "api_key", "api_id", "api_secret", "api_token", "token", "secret", "username", "password", "bearer_token", "access_token", "refresh_token", "client_secret":
		return true
	default:
		return false
	}
}

func performGetInformationSeedStatus(id uint64, db *cdb.Handler) (InformationSeedResponse, error) {
	row, err := informationSeedRowByID(db, id)
	if err != nil {
		return InformationSeedResponse{Message: "Failed to get information seed status"}, err
	}
	return InformationSeedResponse{Message: "Information seed status", Item: row}, nil
}

func performListInformationSeeds(values url.Values, db *cdb.Handler) (InformationSeedListResponse, error) {
	filter, err := informationSeedFilterFromValues(values)
	if err != nil {
		return InformationSeedListResponse{Message: "Invalid information seed filters"}, err
	}
	seeds, err := cdb.ListInformationSeedsWithStats(db, filter)
	if err != nil {
		return InformationSeedListResponse{Message: "Failed to list information seeds"}, err
	}

	items := make([]InformationSeedRow, 0, len(seeds))
	for _, seed := range seeds {
		items = append(items, informationSeedRowFromDB(seed.InformationSeed, seed.DiscoveredSourceCount))
	}

	return InformationSeedListResponse{Message: infoAllInformationSeeds, Items: items, Limit: filter.Limit, Offset: filter.Offset}, nil
}

func performListInformationSeedSources(seedID uint64, values url.Values, db *cdb.Handler) (InformationSeedLinkedSourceListResponse, error) {
	if _, err := cdb.GetInformationSeedByID(db, seedID); err != nil {
		return InformationSeedLinkedSourceListResponse{Message: "Information seed not found", InformationSeedID: seedID}, err
	}
	pagination, err := informationSeedPaginationFromValues(values)
	if err != nil {
		return InformationSeedLinkedSourceListResponse{Message: "Invalid linked source filters", InformationSeedID: seedID}, err
	}
	linked, err := cdb.ListSourcesForInformationSeed(db, seedID, cdb.InformationSeedLinkedSourceFilter{Limit: pagination.Limit, Offset: pagination.Offset})
	if err != nil {
		return InformationSeedLinkedSourceListResponse{Message: "Failed to list linked information seed sources", InformationSeedID: seedID}, err
	}
	items := make([]InformationSeedLinkedSourceRow, 0, len(linked))
	for _, source := range linked {
		items = append(items, informationSeedLinkedSourceRowFromDB(source))
	}
	return InformationSeedLinkedSourceListResponse{Message: "Information seed linked sources", InformationSeedID: seedID, Items: items, Limit: pagination.Limit, Offset: pagination.Offset}, nil
}

func performListInformationSeedCandidateDecisions(seedID uint64, values url.Values, db *cdb.Handler) (InformationSeedCandidateListResponse, error) {
	if _, err := cdb.GetInformationSeedByID(db, seedID); err != nil {
		return InformationSeedCandidateListResponse{Message: "Information seed not found", InformationSeedID: seedID}, err
	}
	pagination, err := informationSeedPaginationFromValues(values)
	if err != nil {
		return InformationSeedCandidateListResponse{Message: "Invalid candidate decision filters", InformationSeedID: seedID}, err
	}
	candidates, err := cdb.ListInformationSeedCandidateDecisions(db, seedID, cdb.InformationSeedCandidateFilter{Limit: pagination.Limit, Offset: pagination.Offset})
	if err != nil {
		return InformationSeedCandidateListResponse{Message: "Failed to list information seed candidate decisions", InformationSeedID: seedID}, err
	}
	items := make([]InformationSeedCandidateRow, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, informationSeedCandidateRowFromDB(candidate))
	}
	return InformationSeedCandidateListResponse{Message: "Information seed candidate decisions", InformationSeedID: seedID, Items: items, Limit: pagination.Limit, Offset: pagination.Offset}, nil
}

func performGetInformationSeedDiagnostics(seedID uint64, db *cdb.Handler) (InformationSeedDiagnosticsResponse, error) {
	if _, err := cdb.GetInformationSeedByID(db, seedID); err != nil {
		return InformationSeedDiagnosticsResponse{Message: "Information seed not found", InformationSeedID: seedID, ProviderRequests: map[string]int{}, RejectionStages: map[string]map[string]int{}}, err
	}
	events, err := cdb.ListInformationSeedEvents(db, seedID, cdb.InformationSeedEventFilter{Limit: 25})
	if err != nil {
		return InformationSeedDiagnosticsResponse{Message: "Failed to load information seed diagnostics", InformationSeedID: seedID, ProviderRequests: map[string]int{}, RejectionStages: map[string]map[string]int{}}, err
	}
	resp := InformationSeedDiagnosticsResponse{
		Message:           "Information seed diagnostics",
		InformationSeedID: seedID,
		ProviderRequests:  map[string]int{},
		RejectionStages:   map[string]map[string]int{},
		ProviderFailures:  []map[string]interface{}{},
		PluginFailures:    []map[string]interface{}{},
		ErrorSummaries:    []string{},
	}
	for _, event := range events {
		details := redactedInformationSeedDetails(event.Details)
		if resp.LatestEventTimestamp == "" {
			resp.LatestEventTimestamp = event.Timestamp
		}
		if resp.RunID == "" {
			resp.RunID, _ = details["run_id"].(string)
		}
		if resp.RunAttempt == 0 {
			resp.RunAttempt = intFromDiagnostic(details["run_attempt"])
		}
		mergeProviderRequests(resp.ProviderRequests, details["provider_metrics"])
		if len(resp.RejectionStages) == 0 {
			resp.RejectionStages = diagnosticNestedIntMap(details["candidate_rejection_stages"])
		}
		resp.ProviderFailures = append(resp.ProviderFailures, diagnosticMapSlice(details["provider_failures"])...)
		resp.PluginFailures = append(resp.PluginFailures, diagnosticMapSlice(details["plugin_failures"])...)
		resp.ErrorSummaries = append(resp.ErrorSummaries, diagnosticStringSlice(details["error_summaries"])...)
	}
	return resp, nil
}

func redactedInformationSeedDetails(details map[string]interface{}) map[string]interface{} {
	redacted, ok := infoseeddiag.RedactDiagnosticValue(details).(map[string]interface{})
	if !ok || redacted == nil {
		return map[string]interface{}{}
	}
	return redacted
}

func mergeProviderRequests(dst map[string]int, raw interface{}) {
	metrics := diagnosticNestedIntMap(raw)
	for provider, providerMetrics := range metrics {
		if requests := providerMetrics["requests"]; requests > dst[provider] {
			dst[provider] = requests
		}
	}
}

func diagnosticNestedIntMap(raw interface{}) map[string]map[string]int {
	result := map[string]map[string]int{}
	outer, ok := raw.(map[string]interface{})
	if !ok {
		return result
	}
	for key, value := range outer {
		inner := map[string]int{}
		switch typed := value.(type) {
		case map[string]interface{}:
			for innerKey, innerValue := range typed {
				inner[innerKey] = intFromDiagnostic(innerValue)
			}
		case map[string]int:
			for innerKey, innerValue := range typed {
				inner[innerKey] = innerValue
			}
		}
		if len(inner) > 0 {
			result[key] = inner
		}
	}
	return result
}

func diagnosticMapSlice(raw interface{}) []map[string]interface{} {
	items := []map[string]interface{}{}
	slice, ok := raw.([]interface{})
	if !ok {
		return items
	}
	for _, item := range slice {
		if mapped, ok := item.(map[string]interface{}); ok {
			items = append(items, redactedInformationSeedDetails(mapped))
		}
	}
	return items
}

func diagnosticStringSlice(raw interface{}) []string {
	items := []string{}
	slice, ok := raw.([]interface{})
	if !ok {
		return items
	}
	for _, item := range slice {
		if value, ok := item.(string); ok {
			items = append(items, infoseeddiag.RedactDiagnosticString(value))
		}
	}
	return items
}

func intFromDiagnostic(raw interface{}) int {
	switch typed := raw.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		value, _ := typed.Int64()
		return int(value)
	default:
		return 0
	}
}

func performRetryInformationSeed(query string, db *cdb.Handler) (InformationSeedResponse, error) {
	id, err := parseInformationSeedIDFromJSON(query)
	if err != nil {
		return InformationSeedResponse{Message: "Invalid information seed retry request"}, err
	}
	return performRerunInformationSeedByID(id, db)
}

func performRerunInformationSeedByID(id uint64, db *cdb.Handler) (InformationSeedResponse, error) {
	if err := cdb.RerunInformationSeed(db, id); err != nil {
		return InformationSeedResponse{Message: "Failed to rerun information seed"}, err
	}
	row, err := informationSeedRowByID(db, id)
	if err != nil {
		return InformationSeedResponse{Message: "Failed to load rerun information seed"}, err
	}
	return InformationSeedResponse{Message: "Information seed queued for rerun", Item: row}, nil
}

func performDisableInformationSeed(query string, db *cdb.Handler) (InformationSeedResponse, error) {
	id, err := parseInformationSeedIDFromJSON(query)
	if err != nil {
		return InformationSeedResponse{Message: "Invalid information seed disable request"}, err
	}
	return performDisableInformationSeedByID(id, db)
}

func performDisableInformationSeedByID(id uint64, db *cdb.Handler) (InformationSeedResponse, error) {
	if err := cdb.DisableInformationSeed(db, id); err != nil {
		return InformationSeedResponse{Message: "Failed to disable information seed"}, err
	}
	row, err := informationSeedRowByID(db, id)
	if err != nil {
		return InformationSeedResponse{Message: "Failed to load disabled information seed"}, err
	}
	return InformationSeedResponse{Message: "Information seed disabled", Item: row}, nil
}

func performEnableInformationSeedByID(id uint64, query string, db *cdb.Handler) (InformationSeedResponse, error) {
	queuePending, err := parseInformationSeedEnableQueuePending(query)
	if err != nil {
		return InformationSeedResponse{Message: "Invalid information seed enable request"}, err
	}
	if err := cdb.EnableInformationSeed(db, id, queuePending); err != nil {
		return InformationSeedResponse{Message: "Failed to enable information seed"}, err
	}
	row, err := informationSeedRowByID(db, id)
	if err != nil {
		return InformationSeedResponse{Message: "Failed to load enabled information seed"}, err
	}
	return InformationSeedResponse{Message: "Information seed enabled", Item: row}, nil
}

func performListInformationSeedEvents(seedID uint64, values url.Values, db *cdb.Handler) (InformationSeedEventListResponse, error) {
	if _, err := cdb.GetInformationSeedByID(db, seedID); err != nil {
		return InformationSeedEventListResponse{Message: "Information seed not found", InformationSeedID: seedID}, err
	}
	pagination, err := informationSeedPaginationFromValues(values)
	if err != nil {
		return InformationSeedEventListResponse{Message: "Invalid information seed event filters", InformationSeedID: seedID}, err
	}
	events, err := cdb.ListInformationSeedEvents(db, seedID, cdb.InformationSeedEventFilter{Limit: pagination.Limit, Offset: pagination.Offset})
	if err != nil {
		return InformationSeedEventListResponse{Message: "Failed to list information seed events", InformationSeedID: seedID}, err
	}
	items := make([]InformationSeedEventRow, 0, len(events))
	for _, event := range events {
		items = append(items, informationSeedEventRowFromDB(event))
	}
	return InformationSeedEventListResponse{Message: "Information seed discovery events", InformationSeedID: seedID, Items: items, Limit: pagination.Limit, Offset: pagination.Offset}, nil
}

type informationSeedPagination struct {
	Limit  int
	Offset int
}

func informationSeedFilterFromValues(values url.Values) (cdb.InformationSeedFilter, error) {
	pagination, err := informationSeedPaginationFromValues(values)
	if err != nil {
		return cdb.InformationSeedFilter{}, err
	}
	filter := cdb.InformationSeedFilter{Limit: pagination.Limit, Offset: pagination.Offset}
	filter.Status = strings.TrimSpace(values.Get("status"))
	filter.Priority = strings.TrimSpace(values.Get("priority"))
	if values.Has("disabled") {
		disabled, err := strconv.ParseBool(values.Get("disabled"))
		if err != nil {
			return cdb.InformationSeedFilter{}, fmt.Errorf("disabled must be a boolean")
		}
		filter.Disabled = &disabled
	}
	if values.Has("category") || values.Has("category_id") {
		value := values.Get("category_id")
		if value == "" {
			value = values.Get("category")
		}
		id, err := parseNonNegativeUint(value, "category")
		if err != nil {
			return cdb.InformationSeedFilter{}, err
		}
		filter.CategoryID = &id
	}
	if values.Has("user") || values.Has("user_id") || values.Has("usr_id") {
		value := values.Get("usr_id")
		if value == "" {
			value = values.Get("user_id")
		}
		if value == "" {
			value = values.Get("user")
		}
		id, err := parseNonNegativeUint(value, "user")
		if err != nil {
			return cdb.InformationSeedFilter{}, err
		}
		filter.UsrID = &id
	}
	return filter, nil
}

func informationSeedPaginationFromValues(values url.Values) (informationSeedPagination, error) {
	const defaultLimit = 100
	const maxLimit = 500
	limit := defaultLimit
	if values.Has("limit") {
		parsed, err := strconv.Atoi(values.Get("limit"))
		if err != nil || parsed < 0 {
			return informationSeedPagination{}, fmt.Errorf("limit must be a non-negative integer")
		}
		if parsed > maxLimit {
			return informationSeedPagination{}, fmt.Errorf("limit must be less than or equal to %d", maxLimit)
		}
		limit = parsed
	}
	offset := 0
	if values.Has("offset") {
		parsed, err := strconv.Atoi(values.Get("offset"))
		if err != nil || parsed < 0 {
			return informationSeedPagination{}, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = parsed
	}
	return informationSeedPagination{Limit: limit, Offset: offset}, nil
}

func parseNonNegativeUint(value, name string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return parsed, nil
}

func parseInformationSeedIDFromJSON(query string) (uint64, error) {
	var req informationSeedIDRequest
	if err := json.Unmarshal([]byte(query), &req); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}
	if req.InformationSeedID == 0 {
		return 0, fmt.Errorf("information_seed_id is required")
	}
	return req.InformationSeedID, nil
}

func parseInformationSeedEnableQueuePending(query string) (bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return false, nil
	}
	var req informationSeedEnableRequest
	if err := json.Unmarshal([]byte(query), &req); err != nil {
		return false, fmt.Errorf("invalid JSON: %w", err)
	}
	return req.Pending || req.QueuePending, nil
}

func informationSeedRowByID(db *cdb.Handler, id uint64) (InformationSeedRow, error) {
	seeds, err := cdb.ListInformationSeedsWithStats(db, cdb.InformationSeedFilter{ID: id, Limit: 1})
	if err != nil {
		return InformationSeedRow{}, err
	}
	if len(seeds) == 0 {
		return InformationSeedRow{}, fmt.Errorf("no information seed found with ID %d", id)
	}
	return informationSeedRowFromDB(seeds[0].InformationSeed, seeds[0].DiscoveredSourceCount), nil
}

func informationSeedRowFromDB(seed cdb.InformationSeed, discoveredSourceCount uint64) InformationSeedRow {
	lastError := infoseeddiag.RedactDiagnosticString(nullStringString(seed.LastError))
	return InformationSeedRow{
		ID:                    seed.ID,
		CreatedAt:             nullTimeString(seed.CreatedAt),
		LastUpdatedAt:         nullTimeString(seed.LastUpdatedAt),
		CategoryID:            seed.CategoryID,
		UsrID:                 seed.UsrID,
		InformationSeed:       seed.InformationSeed,
		Status:                seed.Status,
		Priority:              seed.Priority,
		Engine:                seed.Engine,
		LastProcessedAt:       nullTimeString(seed.LastProcessedAt),
		HasError:              lastError != "" || seed.LastErrorAt.Valid,
		LastError:             lastError,
		LastErrorAt:           nullTimeString(seed.LastErrorAt),
		Disabled:              seed.Disabled,
		Attempts:              seed.Attempts,
		Config:                redactRawMessage(seed.Config),
		DiscoveredSourceCount: discoveredSourceCount,
	}
}

func informationSeedLinkedSourceRowFromDB(linked cdb.InformationSeedLinkedSource) InformationSeedLinkedSourceRow {
	return InformationSeedLinkedSourceRow{
		SourceID:                   linked.Source.ID,
		Priority:                   linked.Source.Priority,
		CategoryID:                 linked.Source.CategoryID,
		Name:                       linked.Source.Name,
		UsrID:                      linked.Source.UsrID,
		URL:                        linked.Source.URL,
		Restricted:                 linked.Source.Restricted,
		Flags:                      linked.Source.Flags,
		Config:                     redactRawMessage(linked.Source.Config),
		Disabled:                   linked.Source.Disabled,
		SourceInformationSeedIndex: sourceInformationSeedIndexRowFromDB(linked.Index),
	}
}

func sourceInformationSeedIndexRowFromDB(index cdb.SourceInformationSeedIndex) SourceInformationSeedIndexRow {
	var rank *int
	if index.DiscoveryRank.Valid {
		value := int(index.DiscoveryRank.Int64)
		rank = &value
	}
	var score *float64
	if index.CandidateScore.Valid {
		value := index.CandidateScore.Float64
		score = &value
	}
	var metadata *json.RawMessage
	if index.DiscoveryMetadata.Valid {
		raw := json.RawMessage(index.DiscoveryMetadata.String)
		metadata = &raw
	}
	return SourceInformationSeedIndexRow{
		ID:                index.ID,
		SourceID:          index.SourceID,
		InformationSeedID: index.InformationSeedID,
		DiscoveryProvider: nullStringString(index.DiscoveryProvider),
		DiscoveryQuery:    nullStringString(index.DiscoveryQuery),
		DiscoveryRank:     rank,
		CandidateScore:    score,
		CandidateReason:   nullStringString(index.CandidateReason),
		DiscoveryMetadata: redactRawMessage(metadata),
		CreatedAt:         nullTimeString(index.CreatedAt),
		LastUpdatedAt:     nullTimeString(index.LastUpdatedAt),
	}
}

func informationSeedEventRowFromDB(event cdb.Event) InformationSeedEventRow {
	return InformationSeedEventRow{
		ID:        event.ID,
		SourceID:  event.SourceID,
		Type:      event.Type,
		Severity:  event.Severity,
		Timestamp: event.Timestamp,
		Details:   redactedInformationSeedDetails(event.Details),
	}
}

func informationSeedCandidateRowFromDB(candidate cdb.InformationSeedCandidate) InformationSeedCandidateRow {
	return InformationSeedCandidateRow{
		ID:                candidate.ID,
		InformationSeedID: candidate.InformationSeedID,
		NormalizedURL:     candidate.NormalizedURL,
		Host:              candidate.Host,
		Provider:          candidate.Provider,
		Query:             candidate.Query,
		Rank:              candidate.Rank,
		Score:             candidate.Score,
		DecisionStatus:    candidate.DecisionStatus,
		RejectionReason:   candidate.RejectionReason,
		Metadata:          redactRawMessage(candidate.Metadata),
		RunAttempt:        candidate.RunAttempt,
		CreatedAt:         nullTimeString(candidate.CreatedAt),
		LastUpdatedAt:     nullTimeString(candidate.LastUpdatedAt),
	}
}

func redactRawMessage(raw *json.RawMessage) *json.RawMessage {
	if raw == nil || len(*raw) == 0 {
		return raw
	}
	var decoded interface{}
	if err := json.Unmarshal(*raw, &decoded); err != nil {
		return raw
	}
	redacted := infoseeddiag.RedactDiagnosticValue(decoded)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return raw
	}
	msg := json.RawMessage(encoded)
	return &msg
}

func nullTimeString(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

func nullStringString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func performAddSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var params addSourceRequest

	if qType == getQuery {
		// Simple GET-style request, only URL provided
		params.URL = strings.TrimSpace(cmn.NormalizeURL(query))

		// Apply defaults
		params.Status = "pending"
		params.Restricted = 2
		params.Disabled = false
		params.Flags = 0
		params.CategoryID = 0
		params.UsrID = 0
		params.Config = cfg.SourceConfig{} // empty/default
	} else {
		// extract AddSource parameters from JSON
		extractAddSourceParams(query, &params)

		params.URL = strings.TrimSpace(cmn.NormalizeURL(params.URL))
		if params.URL == "" {
			return ConsoleResponse{Message: "Invalid URL"}, fmt.Errorf("invalid URL")
		}
	}

	// Validate & reformat config (same as your logic)
	if !params.Config.IsEmpty() {
		if err := validateAndReformatConfig(&params.Config); err != nil {
			return ConsoleResponse{Message: "Invalid config"}, err
		}
	}

	// Convert request into cdb.Source struct
	dbSource := cdb.Source{
		URL:        params.URL,              // from addSourceRequest.URL
		Name:       "",                      // console does not specify Name
		Priority:   "",                      // console does not specify Priority
		CategoryID: params.CategoryID,       // Source category ID (uint64)
		UsrID:      params.UsrID,            // Source user ID (uint64)
		Restricted: uint(params.Restricted), // nolint:gosec // This is a controlled value // Restriction level (int --> uint)
		Disabled:   params.Disabled,         // bool (0 by default)
		Flags:      uint(params.Flags),      // nolint:gosec // This is a controlled value // Source flags (int --> uint)
		// Status is intentionally NOT set from console
	}

	// Use your new SAFE CreateSource() logic
	sourceID, err := cdb.CreateSource(db, &dbSource, params.Config)
	if err != nil {
		return ConsoleResponse{
			Message: "Failed to add the source",
		}, err
	}

	msg := fmt.Sprintf("Website inserted successfully with ID: %d", sourceID)
	return ConsoleResponse{Message: msg}, nil
}

func extractAddSourceParams(query string, params *addSourceRequest) {
	params.Restricted = -1

	// Unmarshal query into params
	err := json.Unmarshal([]byte(query), &params)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling the query: %v", err)
	}

	// Check for missing parameters
	if params.Status == "" {
		params.Status = "pending"
	}
	if params.Restricted < 0 || params.Restricted > 4 {
		params.Restricted = 0
	}

	if !params.Config.IsEmpty() {
		// Validate and potentially reformat the existing Config JSON
		// First, marshal the params.Config struct to JSON
		configJSON, err := json.Marshal(params.Config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "marshalling the Config field: %v", err)
		}

		// Unmarshal the JSON into a map to check for invalid JSON
		var jsonRaw map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &jsonRaw); err != nil {
			// Handle invalid JSON
			cmn.DebugMsg(cmn.DbgLvlError, "Config field contains invalid JSON: %v", err)
		}

		// Re-marshal to ensure the JSON is in a standardized format (optional)
		configJSONChecked, err := json.Marshal(jsonRaw)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "re-marshalling the Config field: %v", err)
		}
		if err := json.Unmarshal(configJSONChecked, &params.Config); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling the Config field: %v", err)
		}
	}
}

/*
func getDefaultConfig() cfg.SourceConfig {
	defaultConfig := map[string]string{}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)
	var config cfg.SourceConfig
	_ = json.Unmarshal(defaultConfigJSON, &config)
	return config
}
*/

func validateAndReformatConfig(config *cfg.SourceConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("%w: failed to marshal config to JSON: %v", errInvalidSourceConfig, err)
	}

	var jsonRaw map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &jsonRaw); err != nil {
		return fmt.Errorf("%w: config field contains invalid JSON: %v", errInvalidSourceConfig, err)
	}

	configJSONChecked, err := json.Marshal(jsonRaw)
	if err != nil {
		return fmt.Errorf("%w: failed to marshal Config field: %v", errInvalidSourceConfig, err)
	}

	if err := json.Unmarshal(configJSONChecked, config); err != nil {
		return fmt.Errorf("%w: failed to unmarshal validated JSON back to Config struct: %v", errInvalidSourceConfig, err)
	}

	if err := config.ValidateEmailSource(); err != nil {
		return fmt.Errorf("%w: %v", errInvalidSourceConfig, err)
	}

	return nil
}

func performRemoveSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var results ConsoleResponse
	var sourceURL string // Assuming the source URL is passed. Adjust as necessary based on input.

	if qType == getQuery {
		// Direct extraction from query if it's a simple GET request
		sourceURL = query
	} else {
		// Handle extraction from a JSON document or other POST data
		// Assuming you have a method or logic to extract the URL from the POST body
		return ConsoleResponse{Message: "Invalid request"}, nil
	}

	// Start a transaction
	tx, err := (*db).Begin()
	if err != nil {
		return ConsoleResponse{Message: errFailedToStartTransaction}, err
	}

	// Proceed with deleting the source using the obtained source_id
	results, err = removeSource(tx, sourceURL)
	if err != nil {
		return ConsoleResponse{Message: "Failed to remove source and related data"}, err
	}

	// If everything went well, commit the transaction
	err = tx.Commit()
	if err != nil {
		return ConsoleResponse{Message: errFailedToCommitTransaction}, err
	}

	results.Message = infoSourceRemoved
	return results, nil
}

func removeSource(tx *sql.Tx, sourceURL string) (ConsoleResponse, error) {
	var results ConsoleResponse
	results.Message = "Failed to remove the source"

	// First, get the source_id for the given URL to ensure it exists and to use in cascading deletes if necessary
	var sourceID uint64
	err := tx.QueryRow("SELECT source_id FROM Sources WHERE url = $1", sourceURL).Scan(&sourceID)
	if err != nil {
		return results, err
	}

	// Proceed with deleting the source using the obtained source_id
	_, err = tx.Exec("DELETE FROM Sources WHERE source_id = $1", sourceID)
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to delete source"}, err2
		}
		return ConsoleResponse{Message: "Failed to delete source and related data"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_httpinfo();")
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_netinfo();")
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err
	}

	results.Message = infoSourceRemoved
	return results, nil
}

// SourcesStatusResponse represents the full JSON response:
//
//	{
//	  "message": "All Sources status",
//	  "items": [...]
//	}
type SourcesStatusResponse struct {
	Message string         `json:"message"`
	Items   []SourceStatus `json:"items"`
}

// SourceStatus represents one item in the "items" array.
type SourceStatus struct {
	SourceID      int                  `json:"source_id"`
	SourceUID     string               `json:"source_uid"`
	URL           DBString             `json:"url"`
	Status        DBString             `json:"status"`
	Priority      DBString             `json:"priority"`
	Engine        DBString             `json:"engine"`
	CreatedAt     DBString             `json:"created_at"`
	LastUpdatedAt DBString             `json:"last_updated_at"`
	LastCrawledAt DBString             `json:"last_crawled_at"`
	LastError     DBString             `json:"last_error"`
	LastErrorAt   DBString             `json:"last_error_at"`
	Restricted    int                  `json:"restricted"`
	Disabled      bool                 `json:"disabled"`
	Flags         int                  `json:"flags"`
	Config        SourceConfigResponse `json:"config"`
	EmailStatus   *SourceEmailStatus   `json:"email_status,omitempty"`
}

// DBString matches the JSON shape produced by sql.NullString-like values:
//
//	{
//	  "String": "...",
//	  "Valid": true
//	}
type DBString struct {
	String string `json:"String"`
	Valid  bool   `json:"Valid"`
}

func performGetURLStatus(query string, qType int, db *cdb.Handler) (StatusResponse, error) {
	var sourceURL string // Assuming the source URL is passed. Adjust as necessary based on input.

	if qType == getQuery {
		// Direct extraction from query if it's a simple GET request
		sourceURL = query
	} else {
		// Handle extraction from a JSON document or other POST data
		// Assuming you have a method or logic to extract the URL from the POST body
		return StatusResponse{Message: "Invalid request"}, nil
	}

	results, err := getURLStatus(db, sourceURL)
	if err != nil {
		return StatusResponse{Message: "Failed to get the status"}, err
	}

	return results, nil
}

func getURLStatus(db *cdb.Handler, sourceURL string) (StatusResponse, error) {
	var results StatusResponse
	results.Message = "Failed to get the status"

	statuses, err := cdb.GetSourceStatusByURL(db, sourceURL)
	if err != nil {
		return results, err
	}

	results.Message = infoAllSourcesStatus
	results.Items = sourceStatusRowsFromDB(statuses)
	return results, nil
}

func performGetAllURLStatus(_ int, db *cdb.Handler) (StatusResponse, error) {
	// using _ instead of qType because for now we don't need it
	results, err := getAllURLStatus(db)
	if err != nil {
		return StatusResponse{Message: "Failed to get all statuses"}, err
	}

	return results, nil
}

func getAllURLStatus(db *cdb.Handler) (StatusResponse, error) {
	var results StatusResponse
	results.Message = "Failed to get all statuses"

	statuses, err := cdb.ListSourceStatuses(db)
	if err != nil {
		return results, err
	}

	results.Message = infoAllSourcesStatus
	results.Items = sourceStatusRowsFromDB(statuses)
	return results, nil
}

func sourceStatusRowsFromDB(rows []cdb.SourceStatusRow) []StatusResponseRow {
	statuses := make([]StatusResponseRow, 0, len(rows))
	for _, row := range rows {
		statuses = append(statuses, StatusResponseRow{
			SourceID:      row.SourceID,
			SourceUID:     row.SourceUID,
			URL:           row.URL,
			Status:        row.Status,
			Priority:      row.Priority,
			Engine:        row.Engine,
			CreatedAt:     row.CreatedAt,
			LastUpdatedAt: row.LastUpdatedAt,
			LastCrawledAt: row.LastCrawledAt,
			LastError:     row.LastError,
			LastErrorAt:   row.LastErrorAt,
			Restricted:    row.Restricted,
			Disabled:      row.Disabled,
			Flags:         row.Flags,
			Config:        SourceConfigResponse(row.Config),
			EmailStatus:   sourceEmailStatusFromDB(row.EmailStatus),
		})
	}
	return statuses
}

func sourceEmailStatusFromDB(status *cdb.SourceEmailStatusRow) *SourceEmailStatus {
	if status == nil {
		return nil
	}

	response := &SourceEmailStatus{
		ListenerStatus: status.ListenerStatus,
		CursorSummary: SourceEmailCursorSummary{
			MailboxCount:          status.MailboxCount,
			CheckpointedMailboxes: status.CheckpointedMailboxes,
			HasTokenCursor:        status.HasTokenCursor,
			HasHistoryCursor:      status.HasHistoryCursor,
			HasUIDCursor:          status.HasUIDCursor,
		},
		ProcessedCount:    status.ProcessedCount,
		FailedCount:       status.FailedCount,
		LastErrorCategory: status.LastErrorCategory,
	}
	if status.LastSynchronizedAt.Valid {
		response.LastSynchronizedAt = status.LastSynchronizedAt.String
	}
	return response
}

func performUpdateSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var sqlParams updateSourceRequest
	var sourceConfig *string
	var sourceDetails *string

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		sqlParams.URL = cmn.NormalizeURL(query)
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &sqlParams)
		if err != nil {
			return ConsoleResponse{Message: "Invalid update request"}, fmt.Errorf("invalid JSON: %w", err)
		}
	}

	var requestedConfig *cfg.SourceConfig
	if sqlParams.Config != nil {
		validated := cfg.SourceConfig(*sqlParams.Config)
		if err := validateAndReformatConfig(&validated); err != nil {
			return ConsoleResponse{Message: "Invalid config"}, err
		}
		requestedConfig = &validated
	}

	// Resolve sourceID if only URL is provided
	if sqlParams.SourceID == 0 && sqlParams.URL != "" {
		sourceID, err := cdb.GetSourceID(cdb.SourceFilter{URL: sqlParams.URL}, db)
		if err != nil {
			return ConsoleResponse{Message: "Failed to resolve Source ID"}, err
		}
		sqlParams.SourceID = int64(sourceID) //nolint:gosec // This is a controlled value
	} else if sqlParams.SourceID == 0 {
		return ConsoleResponse{Message: "Source ID or URL must be provided"}, fmt.Errorf("missing Source ID or URL")
	}

	// Retrieve existing data for the source
	var existingData cdb.UpdateSourceRequest
	selectQuery := `
        SELECT url, status, restricted, disabled, flags, config, details
        FROM Sources
        WHERE source_id = $1
    `
	err := (*db).QueryRow(selectQuery, sqlParams.SourceID).Scan(
		&existingData.URL,
		&existingData.Status,
		&existingData.Restricted,
		&existingData.Disabled,
		&existingData.Flags,
		&sourceConfig,
		&sourceDetails,
	)
	if err != nil {
		return ConsoleResponse{Message: "Failed to retrieve source data"}, fmt.Errorf("error querying existing source data: %w", err)
	}
	// extract and validate Config JSON:
	var srcConfig cfg.SourceConfig
	if sourceConfig != nil {
		//existingData.Config = json.RawMessage(*sourceConfig)
		// Transform sourceConfig into the API source configuration struct.
		if err := json.Unmarshal([]byte(*sourceConfig), &srcConfig); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling the Config field from DB: %v", err)
		}
	}

	// extract free JSON form Details:
	if sourceDetails != nil {
		existingData.Details = json.RawMessage(*sourceDetails)
	} else {
		existingData.Details = json.RawMessage("{}")
	}

	mergedConfig := srcConfig
	if requestedConfig != nil {
		mergedConfig = *requestedConfig
	}

	// Merge existing data with provided updates
	mergedData := cdb.UpdateSourceRequest{
		SourceID:   sqlParams.SourceID,
		URL:        coalesce(sqlParams.URL, existingData.URL),
		Status:     coalesce(sqlParams.Status, existingData.Status),
		Restricted: coalesceInt(sqlParams.Restricted, existingData.Restricted),
		Disabled:   coalesceBool(sqlParams.Disabled, existingData.Disabled),
		Flags:      coalesceInt(sqlParams.Flags, existingData.Flags),
		Details:    coalesceJSON(sqlParams.Details, existingData.Details),
	}

	mergedConfigJSON, err := json.Marshal(mergedConfig)
	if err != nil {
		return ConsoleResponse{Message: "Invalid config"}, fmt.Errorf("%w: failed to marshal config for update: %v", errInvalidSourceConfig, err)
	}

	// Perform the update
	updateQuery := `
        UPDATE Sources
        SET url = $1,
            status = $2,
            restricted = $3,
            disabled = $4,
            flags = $5,
            config = $6::jsonb,
            details = $7::jsonb
        WHERE source_id = $8
    `
	_, err = (*db).Exec(updateQuery,
		cmn.NormalizeURL(mergedData.URL),
		mergedData.Status,
		mergedData.Restricted,
		mergedData.Disabled,
		mergedData.Flags,
		mergedConfigJSON,
		mergedData.Details,
		mergedData.SourceID,
	)
	if err != nil {
		return ConsoleResponse{Message: "Failed to update source"}, err
	}

	return ConsoleResponse{Message: "Source updated successfully"}, nil
}

func coalesce(newValue, existingValue string) string {
	if newValue != "" {
		return newValue
	}
	return existingValue
}

func coalesceInt(newValue, existingValue int) int {
	if newValue != 0 {
		return newValue
	}
	return existingValue
}

func coalesceBool(newValue, existingValue bool) bool {
	// In case of boolean, use a specific value (e.g., a pointer or extra logic)
	// Here, assuming `false` is not a valid new value
	if newValue {
		return newValue
	}
	return existingValue
}

func coalesceJSON(newValue, existingValue json.RawMessage) json.RawMessage {
	if len(newValue) > 0 {
		return newValue
	}
	return existingValue
}

func performVacuumSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var filter cdb.SourceFilter

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		filter.URL = cmn.NormalizeURL(query)
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &filter)
		if err != nil {
			return ConsoleResponse{Message: "Invalid vacuum request"}, fmt.Errorf("invalid JSON: %w", err)
		}
	}

	// Resolve sourceID if only URL is provided
	if filter.SourceID == 0 && filter.URL != "" {
		sourceID, err := cdb.GetSourceID(filter, db)
		if err != nil {
			return ConsoleResponse{Message: "Failed to resolve Source ID"}, err
		}
		filter.SourceID = int64(sourceID) //nolint:gosec // This is a controlled value
	} else if filter.SourceID == 0 {
		return ConsoleResponse{Message: "Source ID or URL must be provided"}, fmt.Errorf("missing Source ID or URL")
	}

	tx, err := (*db).Begin()
	if err != nil {
		return ConsoleResponse{Message: "Failed to start transaction"}, err
	}

	// Deleting indexed data
	queries := []string{
		"DELETE FROM KeywordIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM MetaTagsIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM WebObjectsIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM NetInfoIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM HTTPInfoIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM SourceSearchIndex WHERE source_id = $1",
	}

	for _, query := range queries {
		_, err := tx.Exec(query, filter.SourceID)
		if err != nil {
			err2 := tx.Rollback() // Rollback if any query fails
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to rollback transaction: %v", err2)
			}
			return ConsoleResponse{Message: "Failed to vacuum source data"}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return ConsoleResponse{Message: "Failed to commit transaction"}, err
	}

	return ConsoleResponse{Message: "Source vacuumed successfully"}, nil
}

func performAddOwner(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var owner cdb.OwnerRequest // Define a struct for owner if not already present

	if qType == getQuery {
		// Create a JSON document with the owner name
		jDoc := fmt.Sprintf(`{"name": "%s"}`, strings.ReplaceAll(strings.TrimSpace(query), "\"", ""))
		err := json.Unmarshal([]byte(jDoc), &owner.Details)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner name"}, fmt.Errorf("failed to parse owner name: %w", err)
		}
	} else {
		// Parse POST request JSON
		err := json.Unmarshal([]byte(query), &owner)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner data"}, fmt.Errorf("failed to parse owner data: %w", err)
		}
	}

	// Insert owner into the database
	queryStr := `
		INSERT INTO Owners (parent_id, details)
		VALUES ($1, $2)
		RETURNING owner_id
	`
	var ownerID int64
	err := (*db).QueryRow(queryStr, owner.Details).Scan(&ownerID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to add owner"}, fmt.Errorf("error adding owner: %w", err)
	}

	return ConsoleResponse{Message: fmt.Sprintf("Owner added successfully with ID %d", ownerID)}, nil
}

func performAddCategory(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var category cdb.CategoryRequest // Define a struct for category if not already present

	if qType == getQuery {
		// For GET requests, assume `query` is a simple name
		category.Name = strings.TrimSpace(query)
		if category.Name == "" {
			return ConsoleResponse{Message: "Invalid category name"}, fmt.Errorf("category name is required")
		}
	} else {
		// Parse POST request JSON
		err := json.Unmarshal([]byte(query), &category)
		if err != nil {
			return ConsoleResponse{Message: "Invalid category data"}, fmt.Errorf("failed to parse category data: %w", err)
		}
	}

	// Insert category into the database
	queryStr := `
		INSERT INTO Categories (name, parent_id, description)
		VALUES ($1, $2, $3)
		RETURNING category_id
	`
	var categoryID int64
	err := (*db).QueryRow(queryStr, category.Name, category.ParentID, category.Description).Scan(&categoryID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to add category"}, fmt.Errorf("error adding category: %w", err)
	}

	return ConsoleResponse{Message: fmt.Sprintf("Category added successfully with ID %d", categoryID)}, nil
}

func performUpdateOwner(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var owner cdb.OwnerRequest

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		jDoc := fmt.Sprintf(`{"details": %s}`, strings.ReplaceAll(strings.TrimSpace(query), "\"", ""))
		err := json.Unmarshal([]byte(jDoc), &owner)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner data"}, fmt.Errorf("failed to parse owner data: %w", err)
		}
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &owner)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner data"}, fmt.Errorf("failed to parse owner data: %w", err)
		}
	}

	if owner.OwnerID == 0 {
		return ConsoleResponse{Message: "Owner ID must be provided"}, fmt.Errorf("missing Owner ID")
	}

	// Update owner in the database
	queryStr := `
		UPDATE Owners
		SET parent_id = COALESCE($1, parent_id),
		    details = COALESCE($2, details::jsonb)
		WHERE owner_id = $3
	`
	_, err := (*db).Exec(queryStr, owner.DetailsHash, owner.Details, owner.OwnerID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to update owner"}, fmt.Errorf("error updating owner: %w", err)
	}

	return ConsoleResponse{Message: "Owner updated successfully"}, nil
}

func performRemoveOwner(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var ownerID int64

	if qType == getQuery {
		// Parse the query as a GET request
		id, err := strconv.ParseInt(query, 10, 64)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner ID"}, fmt.Errorf("failed to parse owner ID: %w", err)
		}
		ownerID = id
	} else {
		// Parse the query as a POST request (JSON payload)
		var req map[string]int64
		err := json.Unmarshal([]byte(query), &req)
		if err != nil || req["owner_id"] == 0 {
			return ConsoleResponse{Message: "Invalid owner ID"}, fmt.Errorf("missing or invalid owner ID")
		}
		ownerID = req["owner_id"]
	}

	// Remove owner from the database
	queryStr := `DELETE FROM Owners WHERE owner_id = $1`
	_, err := (*db).Exec(queryStr, ownerID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to remove owner"}, fmt.Errorf("error removing owner: %w", err)
	}

	return ConsoleResponse{Message: "Owner removed successfully"}, nil
}

func performUpdateCategory(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var category cdb.CategoryRequest

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		category.Name = strings.TrimSpace(query)
		if category.Name == "" {
			return ConsoleResponse{Message: "Invalid category name"}, fmt.Errorf("category name is required")
		}
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &category)
		if err != nil {
			return ConsoleResponse{Message: "Invalid category data"}, fmt.Errorf("failed to parse category data: %w", err)
		}
	}

	if category.CategoryID == 0 {
		return ConsoleResponse{Message: "Category ID must be provided"}, fmt.Errorf("missing Category ID")
	}

	// Update category in the database
	queryStr := `
		UPDATE Categories
		SET name = COALESCE($1, name),
		    parent_id = COALESCE($2, parent_id),
		    description = COALESCE($3, description)
		WHERE category_id = $4
	`
	_, err := (*db).Exec(queryStr, category.Name, category.ParentID, category.Description, category.CategoryID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to update category"}, fmt.Errorf("error updating category: %w", err)
	}

	return ConsoleResponse{Message: "Category updated successfully"}, nil
}

func performRemoveCategory(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var categoryID int64

	if qType == getQuery {
		// Parse the query as a GET request
		id, err := strconv.ParseInt(query, 10, 64)
		if err != nil {
			return ConsoleResponse{Message: "Invalid category ID"}, fmt.Errorf("failed to parse category ID: %w", err)
		}
		categoryID = id
	} else {
		// Parse the query as a POST request (JSON payload)
		var req map[string]int64
		err := json.Unmarshal([]byte(query), &req)
		if err != nil || req["category_id"] == 0 {
			return ConsoleResponse{Message: "Invalid category ID"}, fmt.Errorf("missing or invalid category ID")
		}
		categoryID = req["category_id"]
	}

	// Remove category from the database
	queryStr := `DELETE FROM Categories WHERE category_id = $1`
	_, err := (*db).Exec(queryStr, categoryID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to remove category"}, fmt.Errorf("error removing category: %w", err)
	}

	return ConsoleResponse{Message: "Category removed successfully"}, nil
}
