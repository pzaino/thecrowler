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
	"fmt"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Phase 3 ships exactly two vetted read-only adapters, both backed by real
// narrowed public database APIs. No model SQL, URLs, filesystem paths, or
// query fragments reach SQL: identifiers are validated integers and search
// text travels only as a bound stored-function parameter. Returned fields
// are an explicit safe subset; configuration blobs, credential-adjacent
// metadata, and account linkage are never exposed.

// Read adapter bounds.
const (
	maxSearchQueryLen   = 500
	maxSearchLimit      = 10
	defaultSearchLimit  = 5
	maxSnippetLen       = 1000
	maxTitleLen         = 200
	searchLanguageFixed = "en"
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

// scopedSourceUID resolves a source ID to its UID for result filtering,
// after scope enforcement. Callers must hold a live runtime.
func scopedSourceUID(ctx context.Context, db cdb.Handler, scope map[uint64]bool, sourceID uint64) (string, error) {
	if err := CheckSourceScope(scope, sourceID); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", denyTool(ToolDenyContext, "run context expired")
	}
	source, err := cdb.GetSourceByID(&db, sourceID)
	if err != nil {
		return "", denyTool(ToolDenyScope, "unknown source %d", sourceID)
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
