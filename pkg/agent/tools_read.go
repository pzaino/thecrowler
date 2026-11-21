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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Phase 3 ships vetted read-only adapters backed by narrowed public database
// APIs. No model SQL, URLs, filesystem paths, or query fragments reach SQL:
// identifiers are validated integers and search text travels only as a bound
// stored-function parameter. Returned fields are an explicit safe subset;
// configuration blobs, credential-adjacent metadata, account linkage,
// scraped values and artifact contents are never exposed.

// Read adapter bounds.
const (
	maxSearchQueryLen   = 500
	maxSearchLimit      = 10
	defaultSearchLimit  = 5
	maxSnippetLen       = 1000
	maxTitleLen         = 200
	searchLanguageFixed = "en"

	maxMetadataFieldLen      = 128
	maxMetadataValueLen      = maxSearchQueryLen
	maxMetadataLimit         = maxSearchLimit
	maxMetadataPageURLLen    = maxSearchQueryLen
	maxMetadataStatusLen     = 64
	maxMetadataEngineLen     = 128
	maxMetadataPriorityLen   = 64
	maxMetadataSourceTypeLen = 64
)

// CheckSourceScope enforces per-run source scoping inside adapters. A nil or
// empty scope is unrestricted (documented, operator-chosen); otherwise the
// argument source ID must be a member.
func CheckSourceScope(scope map[uint64]bool, sourceID uint64) error {
	if len(scope) == 0 {
		return nil
	}
	if !scope[sourceID] {
		return denyTool(ToolDenyScope, "source %d is outside the run scope", sourceID)
	}
	return nil
}

// resolveSourceForTool enforces source scope and resolves one authorized
// source with a context-aware lookup. Missing and failed lookups stay
// distinguishable internally without exposing database details.
func resolveSourceForTool(ctx context.Context, db cdb.Handler, scope map[uint64]bool, sourceID uint64) (*cdb.Source, error) {
	if err := CheckSourceScope(scope, sourceID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, denyTool(ToolDenyContext, "run context expired")
	}
	source, err := cdb.GetSourceByIDContext(ctx, &db, sourceID)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, denyTool(ToolDenyContext, "run context expired")
		case errors.Is(err, sql.ErrNoRows):
			return nil, denyTool(ToolDenyScope, "unknown source %d", sourceID)
		default:
			return nil, fmt.Errorf("source lookup failed")
		}
	}
	if source == nil {
		return nil, denyTool(ToolDenyScope, "unknown source %d", sourceID)
	}
	return source, nil
}

// scopedSourceUID resolves a source ID to its UID for result filtering,
// after scope enforcement. Callers must hold a live runtime.
func scopedSourceUID(ctx context.Context, db cdb.Handler, scope map[uint64]bool, sourceID uint64) (string, error) {
	source, err := resolveSourceForTool(ctx, db, scope, sourceID)
	if err != nil {
		return "", err
	}
	return source.UID, nil
}

// toSourceIDArg validates an integer-valued source identifier argument.
func toSourceIDArg(raw any) (uint64, error) {
	switch v := raw.(type) {
	case float64:
		if v < 1 || v != float64(uint64(v)) {
			return 0, fmt.Errorf("source_id must be a positive integer")
		}
		return uint64(v), nil
	case int:
		if v < 1 {
			return 0, fmt.Errorf("source_id must be a positive integer")
		}
		return uint64(v), nil
	case int64:
		if v < 1 {
			return 0, fmt.Errorf("source_id must be a positive integer")
		}
		return uint64(v), nil
	case uint64:
		if v < 1 {
			return 0, fmt.Errorf("source_id must be a positive integer")
		}
		return v, nil
	case json.Number:
		id, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil || id < 1 {
			return 0, fmt.Errorf("source_id must be a positive integer")
		}
		return id, nil
	default:
		return 0, fmt.Errorf("source_id must be a positive integer")
	}
}

// sourceStatusTool implements get_source_status(source_id).
type sourceStatusTool struct{}

func (t *sourceStatusTool) Name() string { return "get_source_status" }

func (t *sourceStatusTool) Description() string {
	return "Read crawler status fields for one source by ID. Read-only."
}

func (t *sourceStatusTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id": map[string]any{"type": "integer", "minimum": 1},
		},
		"required":             []any{"source_id"},
		"additionalProperties": false,
	}
}

func (t *sourceStatusTool) RequiredCapabilities() []string { return []string{"db_read"} }

// EvidenceKind declares the observation kind recorded for authorized runs.
func (t *sourceStatusTool) EvidenceKind() EvidenceKind { return EvidenceToolObservation }

// EvidenceTrustClass declares the trust class for the observation.
func (t *sourceStatusTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }

// IsReadOnly reports that the tool has no side effects.
func (t *sourceStatusTool) IsReadOnly() bool { return true }

func (t *sourceStatusTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || runtime.DB == nil {
		return nil, fmt.Errorf("tool runtime unavailable")
	}
	sourceID, err := toSourceIDArg(args["source_id"])
	if err != nil {
		return nil, err
	}
	if err := CheckSourceScope(runtime.Auth.AllowedSources, sourceID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, denyTool(ToolDenyContext, "run context expired")
	}
	// Narrow public API with a parameterized query; no model SQL involved.
	source, err := cdb.GetSourceByID(&runtime.DB, sourceID)
	if err != nil {
		return nil, fmt.Errorf("unknown source")
	}
	// Explicit safe subset: Config (operator secrets) and UsrID (account
	// linkage) are never exposed to model-facing output.
	return map[string]any{
		"source_id":  source.ID,
		"uid":        source.UID,
		"name":       source.Name,
		"url":        source.URL,
		"priority":   source.Priority,
		"category":   source.CategoryID,
		"restricted": source.Restricted,
		"flags":      source.Flags,
	}, nil
}

// pageSearchTool implements search_indexed_pages(query, source_id?, limit?).
type pageSearchTool struct{}

func (p *pageSearchTool) Name() string { return "search_indexed_pages" }

func (p *pageSearchTool) Description() string {
	return "Full-text search over indexed pages with bounded snippets. Read-only."
}

func (p *pageSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":     map[string]any{"type": "string", "minLength": 1, "maxLength": maxSearchQueryLen},
			"source_id": map[string]any{"type": "integer", "minimum": 1},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchLimit},
		},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
}

func (p *pageSearchTool) RequiredCapabilities() []string { return []string{"db_read"} }

// EvidenceKind declares the observation kind recorded for authorized runs.
func (p *pageSearchTool) EvidenceKind() EvidenceKind { return EvidenceToolObservation }

// EvidenceTrustClass declares the trust class for the observation.
func (p *pageSearchTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }

// IsReadOnly reports that the tool has no side effects.
func (p *pageSearchTool) IsReadOnly() bool { return true }

func (p *pageSearchTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || runtime.DB == nil {
		return nil, fmt.Errorf("tool runtime unavailable")
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" || len(query) > maxSearchQueryLen {
		return nil, fmt.Errorf("query must be 1..%d chars", maxSearchQueryLen)
	}
	limit := defaultSearchLimit
	if raw, present := args["limit"]; present && raw != nil {
		n, err := toSearchLimit(raw)
		if err != nil {
			return nil, err
		}
		limit = n
	}
	// Scoped runs must name their source; the filter below then applies.
	// Unscoped runs search the whole index (documented operator choice).
	filterUID := ""
	if raw, present := args["source_id"]; present && raw != nil {
		sourceID, err := toSourceIDArg(raw)
		if err != nil {
			return nil, err
		}
		uid, err := scopedSourceUID(ctx, runtime.DB, runtime.Auth.AllowedSources, sourceID)
		if err != nil {
			return nil, err
		}
		filterUID = uid
	} else if len(runtime.Auth.AllowedSources) > 0 {
		return nil, denyTool(ToolDenyScope, "scoped runs must specify source_id")
	}
	if err := ctx.Err(); err != nil {
		return nil, denyTool(ToolDenyContext, "run context expired")
	}
	// Narrow public API: stored-function search with bound limit. The query
	// travels only as a bound parameter, never as SQL text. Source scoping
	// is enforced by the database before LIMIT via the SourceUID filter;
	// the Go-side check below remains purely as a defense-in-depth
	// assertion and never establishes correctness on its own.
	rows, err := cdb.SearchPages(ctx, &runtime.DB, query, searchLanguageFixed, cdb.SearchFunctionOptions{Limit: limit, SourceUID: filterUID})
	if err != nil {
		return nil, fmt.Errorf("page search failed")
	}
	pages := make([]any, 0, len(rows))
	for _, row := range rows {
		if filterUID != "" && row.SourceUID != filterUID {
			continue
		}
		pages = append(pages, map[string]any{
			"page_url":   row.PageURL,
			"title":      boundText(nullStringValue(row.Title), maxTitleLen),
			"snippet":    boundText(nullStringValue(row.Snippet), maxSnippetLen),
			"rank":       row.Rank,
			"indexed_at": nullTimeValue(row.LastUpdatedAt),
		})
		if len(pages) >= limit {
			break
		}
	}
	return map[string]any{
		"query": query,
		"pages": pages,
		"count": len(pages),
	}, nil
}

func toSearchLimit(raw any) (int, error) {
	switch v := raw.(type) {
	case float64:
		if v < 1 || v > maxSearchLimit || v != float64(int(v)) {
			return 0, fmt.Errorf("limit must be 1..%d", maxSearchLimit)
		}
		return int(v), nil
	case int:
		if v < 1 || v > maxSearchLimit {
			return 0, fmt.Errorf("limit must be 1..%d", maxSearchLimit)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("limit must be 1..%d", maxSearchLimit)
	}
}

// sourceCrawlStatusTool implements get_source_crawl_status(source_id).
type sourceCrawlStatusTool struct{}

func (t *sourceCrawlStatusTool) Name() string { return "get_source_crawl_status" }

func (t *sourceCrawlStatusTool) Description() string {
	return "Read the current crawl status and safe timestamps for one source by ID. Read-only."
}

func (t *sourceCrawlStatusTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id": map[string]any{"type": "integer", "minimum": 1},
		},
		"required":             []any{"source_id"},
		"additionalProperties": false,
	}
}

func (t *sourceCrawlStatusTool) RequiredCapabilities() []string { return []string{"db_read"} }

func (t *sourceCrawlStatusTool) EvidenceKind() EvidenceKind { return EvidenceToolObservation }

func (t *sourceCrawlStatusTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }

func (t *sourceCrawlStatusTool) IsReadOnly() bool { return true }

func (t *sourceCrawlStatusTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || runtime.DB == nil {
		return nil, fmt.Errorf("tool runtime unavailable")
	}
	sourceID, err := toSourceIDArg(args["source_id"])
	if err != nil {
		return nil, err
	}
	source, err := resolveSourceForTool(ctx, runtime.DB, runtime.Auth.AllowedSources, sourceID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, denyTool(ToolDenyContext, "run context expired")
	}
	rows, err := cdb.GetSourceStatusByUIDContext(ctx, &runtime.DB, source.UID)
	if err != nil {
		return nil, fmt.Errorf("source status lookup failed")
	}
	return sourceCrawlStatusResult(source, rows), nil
}

func authoritativeSourceStatus(rows []cdb.SourceStatusRow) cdb.SourceStatusRow {
	var chosen cdb.SourceStatusRow
	var last time.Time
	for _, row := range rows {
		updated := parseStatusTime(nullStringValue(row.LastUpdatedAt))
		if updated.IsZero() {
			updated = parseStatusTime(nullStringValue(row.LastCrawledAt))
		}
		if chosen.SourceID == 0 || updated.After(last) {
			chosen = row
			last = updated
		}
	}
	return chosen
}

func sourceCrawlStatusResult(source *cdb.Source, rows []cdb.SourceStatusRow) map[string]any {
	chosen := authoritativeSourceStatus(rows)
	return map[string]any{
		"source_id":          source.ID,
		"source_uid":         source.UID,
		"status":             boundText(nullStringValue(chosen.Status), maxMetadataStatusLen),
		"priority":           boundText(nullStringValue(chosen.Priority), maxMetadataPriorityLen),
		"engine":             boundText(nullStringValue(chosen.Engine), maxMetadataEngineLen),
		"created_at":         sourceStatusTimestamp(chosen.CreatedAt),
		"last_updated_at":    sourceStatusTimestamp(chosen.LastUpdatedAt),
		"last_crawled_at":    sourceStatusTimestamp(chosen.LastCrawledAt),
		"last_error_present": strings.TrimSpace(nullStringValue(chosen.LastError)) != "",
		"last_error_at":      sourceStatusTimestamp(chosen.LastErrorAt),
		"restricted":         chosen.Restricted,
		"disabled":           chosen.Disabled,
		"flags":              chosen.Flags,
		"status_records":     len(rows),
	}
}

func sourceStatusTimestamp(value sql.NullString) string {
	parsed := parseStatusTime(nullStringValue(value))
	if parsed.IsZero() {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}

// scrapedFieldMetadataTool implements find_scraped_field_metadata.
type scrapedFieldMetadataTool struct{}

func (t *scrapedFieldMetadataTool) Name() string { return "find_scraped_field_metadata" }

func (t *scrapedFieldMetadataTool) Description() string {
	return "Find metadata-only scraped-field matches for one authorized source. Read-only."
}

func (t *scrapedFieldMetadataTool) InputSchema() map[string]any {
	return metadataSearchInputSchema()
}

func (t *scrapedFieldMetadataTool) RequiredCapabilities() []string { return []string{"db_read"} }

func (t *scrapedFieldMetadataTool) EvidenceKind() EvidenceKind { return EvidenceToolObservation }

func (t *scrapedFieldMetadataTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }

func (t *scrapedFieldMetadataTool) IsReadOnly() bool { return true }

func (t *scrapedFieldMetadataTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || runtime.DB == nil {
		return nil, fmt.Errorf("tool runtime unavailable")
	}
	sourceID, field, value, limit, err := metadataSearchArgs(args)
	if err != nil {
		return nil, err
	}
	source, err := resolveSourceForTool(ctx, runtime.DB, runtime.Auth.AllowedSources, sourceID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, denyTool(ToolDenyContext, "run context expired")
	}
	rows, err := cdb.SearchScrapedDataField(ctx, &runtime.DB, field, value,
		cdb.SearchFunctionOptions{Limit: limit, SourceUID: source.UID})
	if err != nil {
		return nil, fmt.Errorf("scraped field search failed")
	}
	return scrapedFieldMetadataResult(sourceID, source.UID, field, limit, rows), nil
}

func scrapedFieldMetadataResult(sourceID uint64, sourceUID, field string, limit int, rows []cdb.ScrapedDataSearchResult) map[string]any {
	matches := make([]any, 0, minInt(len(rows), limit))
	for _, row := range rows {
		if len(matches) >= limit {
			break
		}
		if row.SourceUID != sourceUID {
			continue
		}
		matchedField := boundText(nullStringValue(row.JSONField), maxMetadataFieldLen)
		if matchedField != "" && matchedField != field {
			continue
		}
		matches = append(matches, map[string]any{
			"index_id":        row.IndexID,
			"page_url":        boundText(nullStringValue(row.PageURL), maxMetadataPageURLLen),
			"matched_field":   matchedField,
			"created_at":      nullTimeValue(row.CreatedAt),
			"last_updated_at": nullTimeValue(row.LastUpdatedAt),
			"rank":            safeRank(row.Rank),
		})
	}
	return map[string]any{
		"source_id":  sourceID,
		"source_uid": sourceUID,
		"field":      field,
		"count":      len(matches),
		"matches":    matches,
	}
}

// artifactMetadataTool implements find_artifact_metadata.
type artifactMetadataTool struct{}

func (t *artifactMetadataTool) Name() string { return "find_artifact_metadata" }

func (t *artifactMetadataTool) Description() string {
	return "Find metadata-only artifact matches for one authorized source. Read-only."
}

func (t *artifactMetadataTool) InputSchema() map[string]any {
	return metadataSearchInputSchema()
}

func (t *artifactMetadataTool) RequiredCapabilities() []string { return []string{"db_read"} }

func (t *artifactMetadataTool) EvidenceKind() EvidenceKind { return EvidenceToolObservation }

func (t *artifactMetadataTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }

func (t *artifactMetadataTool) IsReadOnly() bool { return true }

func (t *artifactMetadataTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	runtime, ok := ToolRuntimeFromContext(ctx)
	if !ok || runtime.DB == nil {
		return nil, fmt.Errorf("tool runtime unavailable")
	}
	sourceID, field, value, limit, err := metadataSearchArgs(args)
	if err != nil {
		return nil, err
	}
	source, err := resolveSourceForTool(ctx, runtime.DB, runtime.Auth.AllowedSources, sourceID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, denyTool(ToolDenyContext, "run context expired")
	}
	rows, err := cdb.SearchArtifactsField(ctx, &runtime.DB, field, value,
		cdb.SearchFunctionOptions{Limit: limit, SourceUID: source.UID})
	if err != nil {
		return nil, fmt.Errorf("artifact search failed")
	}
	return artifactMetadataResult(sourceID, source.UID, field, limit, rows), nil
}

func artifactMetadataResult(sourceID uint64, sourceUID, field string, limit int, rows []cdb.ArtifactSearchResult) map[string]any {
	artifacts := make([]any, 0, minInt(len(rows), limit))
	for _, row := range rows {
		if len(artifacts) >= limit {
			break
		}
		if row.SourceUID != sourceUID {
			continue
		}
		matchedField := boundText(nullStringValue(row.JSONField), maxMetadataFieldLen)
		if matchedField != "" && matchedField != field {
			continue
		}
		artifacts = append(artifacts, map[string]any{
			"artifact_id":     row.ArtifactID,
			"source_type":     boundText(row.SourceType, maxMetadataSourceTypeLen),
			"page_url":        boundText(nullStringValue(row.PageURL), maxMetadataPageURLLen),
			"matched_field":   matchedField,
			"created_at":      nullTimeValue(row.CreatedAt),
			"last_updated_at": nullTimeValue(row.LastUpdatedAt),
			"rank":            safeRank(row.Rank),
		})
	}
	return map[string]any{
		"source_id":  sourceID,
		"source_uid": sourceUID,
		"field":      field,
		"count":      len(artifacts),
		"artifacts":  artifacts,
	}
}

func metadataSearchInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id": map[string]any{"type": "integer", "minimum": 1},
			"field":     map[string]any{"type": "string", "minLength": 1, "maxLength": maxMetadataFieldLen},
			"value":     map[string]any{"type": "string", "minLength": 1, "maxLength": maxMetadataValueLen},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": maxMetadataLimit},
		},
		"required":             []any{"source_id", "field", "value"},
		"additionalProperties": false,
	}
}

func metadataSearchArgs(args map[string]any) (uint64, string, string, int, error) {
	sourceID, err := toSourceIDArg(args["source_id"])
	if err != nil {
		return 0, "", "", 0, err
	}
	field, err := boundedMetadataArg(args, "field", maxMetadataFieldLen)
	if err != nil {
		return 0, "", "", 0, err
	}
	value, err := boundedMetadataArg(args, "value", maxMetadataValueLen)
	if err != nil {
		return 0, "", "", 0, err
	}
	limit := defaultSearchLimit
	if raw, present := args["limit"]; present && raw != nil {
		limit, err = toSearchLimit(raw)
		if err != nil {
			return 0, "", "", 0, err
		}
	}
	return sourceID, field, value, limit, nil
}

func boundedMetadataArg(args map[string]any, name string, max int) (string, error) {
	raw, ok := args[name].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	text := strings.TrimSpace(raw)
	if text == "" || len(text) > max {
		return "", fmt.Errorf("%s must be 1..%d chars", name, max)
	}
	if strings.ContainsRune(text, 0) {
		return "", fmt.Errorf("%s contains an invalid character", name)
	}
	return text, nil
}

func safeRank(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func boundText(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func nullStringValue(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func nullTimeValue(v sql.NullTime) string {
	if !v.Valid {
		return ""
	}
	return v.Time.UTC().Format(time.RFC3339)
}

// RegisterReadTools registers the vetted Phase 3 read-only adapters.
func RegisterReadTools(registry *AgentToolRegistry) error {
	if registry == nil {
		return fmt.Errorf("nil tool registry")
	}
	if err := registry.Register(&sourceStatusTool{}); err != nil {
		return err
	}
	if err := registry.Register(&pageSearchTool{}); err != nil {
		return err
	}
	if err := registry.Register(&sourceCrawlStatusTool{}); err != nil {
		return err
	}
	if err := registry.Register(&scrapedFieldMetadataTool{}); err != nil {
		return err
	}
	if err := registry.Register(&artifactMetadataTool{}); err != nil {
		return err
	}
	return nil
}

// DefaultToolRegistry builds a registry with the vetted read-only set.
func DefaultToolRegistry() (*AgentToolRegistry, error) {
	registry := NewAgentToolRegistry()
	if err := RegisterReadTools(registry); err != nil {
		return nil, err
	}
	return registry, nil
}
