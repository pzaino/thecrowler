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

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// SearchFunctionOptions controls pagination applied by the Go wrappers around
// PostgreSQL search functions. The database functions themselves do not accept
// pagination arguments, so non-zero Limit and Offset values are appended outside
// the function call. A zero Limit means "no LIMIT".
type SearchFunctionOptions struct {
	Limit  int
	Offset int
}

// CorrelatedSourceSearchResult represents one row returned by
// find_correlated_sources_by_domain(domain TEXT).
type CorrelatedSourceSearchResult struct {
	SourceID uint64
	URL      string
}

// PageSearchResult represents one row returned by search_pages(q TEXT, lang TEXT).
type PageSearchResult struct {
	IndexID       uint64
	PageURL       string
	Title         sql.NullString
	Snippet       sql.NullString
	CreatedAt     sql.NullTime
	LastUpdatedAt sql.NullTime
	Rank          float64
}

// ScrapedDataSearchResult represents one row returned by search_scraped_data and
// search_scraped_data_field.
type ScrapedDataSearchResult struct {
	IndexID       uint64
	PageURL       sql.NullString
	JSONField     sql.NullString
	JSONValue     sql.NullString
	CreatedAt     sql.NullTime
	LastUpdatedAt sql.NullTime
	Rank          float64
}

// ArtifactSearchResult represents one row returned by search_artifacts and
// search_artifacts_field.
type ArtifactSearchResult struct {
	SourceType    string
	ArtifactID    uint64
	PageURL       sql.NullString
	JSONField     sql.NullString
	JSONValue     sql.NullString
	CreatedAt     sql.NullTime
	LastUpdatedAt sql.NullTime
	Rank          float64
}

// ArtifactFieldsSearchResult represents one row returned by
// search_artifacts_fields(filters JSONB).
type ArtifactFieldsSearchResult struct {
	SourceType    string
	ArtifactID    uint64
	PageURL       sql.NullString
	CreatedAt     sql.NullTime
	LastUpdatedAt sql.NullTime
	MatchedFields json.RawMessage
	Rank          float64
}

// ArtifactAttributeSearchResult represents one row returned by
// search_artifacts_by_attribute(field_name TEXT, field_value TEXT).
type ArtifactAttributeSearchResult struct {
	SourceType     string
	ArtifactID     uint64
	PageURL        sql.NullString
	AttributeKey   sql.NullString
	AttributeValue sql.NullString
	AttributeType  sql.NullString
	CreatedAt      sql.NullTime
	LastUpdatedAt  sql.NullTime
	Rank           float64
}

// ObjectAttributeSearchResult represents one row returned by
// search_objects_by_attribute(field_name TEXT, field_value TEXT).
type ObjectAttributeSearchResult struct {
	SourceType    string
	ObjectID      uint64
	PageURL       sql.NullString
	Details       json.RawMessage
	Attributes    json.RawMessage
	CreatedAt     sql.NullTime
	LastUpdatedAt sql.NullTime
	Rank          float64
}

// ObjectAttributesSearchResult represents one row returned by
// search_objects_by_attributes(filters JSONB).
type ObjectAttributesSearchResult struct {
	SourceType    string
	ObjectID      uint64
	PageURL       sql.NullString
	Details       json.RawMessage
	Attributes    json.RawMessage
	CreatedAt     sql.NullTime
	LastUpdatedAt sql.NullTime
	MatchedFields json.RawMessage
	Rank          float64
}

// FindCorrelatedSourcesByDomain calls the PostgreSQL stored function
// find_correlated_sources_by_domain(domain TEXT). SQLite and MySQL do not ship
// equivalent stored functions in the current schema, so this wrapper returns an
// explicit unsupported-DBMS error for those paths.
func FindCorrelatedSourcesByDomain(ctx context.Context, db *Handler, domain string, opts SearchFunctionOptions) ([]CorrelatedSourceSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "find_correlated_sources_by_domain", []string{"$1"}, []interface{}{domain}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort close after scanning

	var results []CorrelatedSourceSearchResult
	for rows.Next() {
		var row CorrelatedSourceSearchResult
		if err := rows.Scan(&row.SourceID, &row.URL); err != nil {
			return nil, fmt.Errorf("scan correlated source search result: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate correlated source search results: %w", err)
	}
	return results, nil
}

// SearchPages calls search_pages(q TEXT, lang TEXT).
func SearchPages(ctx context.Context, db *Handler, q, lang string, opts SearchFunctionOptions) ([]PageSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_pages", []string{"$1", "$2"}, []interface{}{q, lang}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var results []PageSearchResult
	for rows.Next() {
		var row PageSearchResult
		if err := rows.Scan(&row.IndexID, &row.PageURL, &row.Title, &row.Snippet, &row.CreatedAt, &row.LastUpdatedAt, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan page search result: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate page search results: %w", err)
	}
	return results, nil
}

// SearchScrapedData calls search_scraped_data(q TEXT).
func SearchScrapedData(ctx context.Context, db *Handler, q string, opts SearchFunctionOptions) ([]ScrapedDataSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_scraped_data", []string{"$1"}, []interface{}{q}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanScrapedDataSearchResults(rows)
}

// SearchScrapedDataField calls search_scraped_data_field(field_name TEXT, field_value TEXT).
func SearchScrapedDataField(ctx context.Context, db *Handler, fieldName, fieldValue string, opts SearchFunctionOptions) ([]ScrapedDataSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_scraped_data_field", []string{"$1", "$2"}, []interface{}{fieldName, fieldValue}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanScrapedDataSearchResults(rows)
}

// SearchArtifacts calls search_artifacts(q TEXT).
func SearchArtifacts(ctx context.Context, db *Handler, q string, opts SearchFunctionOptions) ([]ArtifactSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_artifacts", []string{"$1"}, []interface{}{q}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanArtifactSearchResults(rows)
}

// SearchArtifactsField calls search_artifacts_field(field_name TEXT, field_value TEXT).
func SearchArtifactsField(ctx context.Context, db *Handler, fieldName, fieldValue string, opts SearchFunctionOptions) ([]ArtifactSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_artifacts_field", []string{"$1", "$2"}, []interface{}{fieldName, fieldValue}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanArtifactSearchResults(rows)
}

// SearchArtifactsFields calls search_artifacts_fields(filters JSONB).
func SearchArtifactsFields(ctx context.Context, db *Handler, filters map[string]string, opts SearchFunctionOptions) ([]ArtifactFieldsSearchResult, error) {
	filterJSON, err := marshalSearchFilters(filters)
	if err != nil {
		return nil, err
	}
	rows, err := queryPostgresSearchFunction(ctx, db, "search_artifacts_fields", []string{"$1::jsonb"}, []interface{}{filterJSON}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var results []ArtifactFieldsSearchResult
	for rows.Next() {
		var row ArtifactFieldsSearchResult
		var matchedFields []byte
		if err := rows.Scan(&row.SourceType, &row.ArtifactID, &row.PageURL, &row.CreatedAt, &row.LastUpdatedAt, &matchedFields, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan artifact fields search result: %w", err)
		}
		row.MatchedFields = json.RawMessage(matchedFields)
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact fields search results: %w", err)
	}
	return results, nil
}

// SearchArtifactsByAttribute calls search_artifacts_by_attribute(field_name TEXT, field_value TEXT).
func SearchArtifactsByAttribute(ctx context.Context, db *Handler, fieldName, fieldValue string, opts SearchFunctionOptions) ([]ArtifactAttributeSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_artifacts_by_attribute", []string{"$1", "$2"}, []interface{}{fieldName, fieldValue}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var results []ArtifactAttributeSearchResult
	for rows.Next() {
		var row ArtifactAttributeSearchResult
		if err := rows.Scan(&row.SourceType, &row.ArtifactID, &row.PageURL, &row.AttributeKey, &row.AttributeValue, &row.AttributeType, &row.CreatedAt, &row.LastUpdatedAt, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan artifact attribute search result: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact attribute search results: %w", err)
	}
	return results, nil
}

// SearchObjectsByAttribute calls search_objects_by_attribute(field_name TEXT, field_value TEXT).
func SearchObjectsByAttribute(ctx context.Context, db *Handler, fieldName, fieldValue string, opts SearchFunctionOptions) ([]ObjectAttributeSearchResult, error) {
	rows, err := queryPostgresSearchFunction(ctx, db, "search_objects_by_attribute", []string{"$1", "$2"}, []interface{}{fieldName, fieldValue}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var results []ObjectAttributeSearchResult
	for rows.Next() {
		var row ObjectAttributeSearchResult
		var details, attributes sql.NullString
		if err := rows.Scan(&row.SourceType, &row.ObjectID, &row.PageURL, &details, &attributes, &row.CreatedAt, &row.LastUpdatedAt, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan object attribute search result: %w", err)
		}
		row.Details = rawMessageFromNullString(details)
		row.Attributes = rawMessageFromNullString(attributes)
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate object attribute search results: %w", err)
	}
	return results, nil
}

// SearchObjectsByAttributes calls search_objects_by_attributes(filters JSONB).
func SearchObjectsByAttributes(ctx context.Context, db *Handler, filters map[string]string, opts SearchFunctionOptions) ([]ObjectAttributesSearchResult, error) {
	filterJSON, err := marshalSearchFilters(filters)
	if err != nil {
		return nil, err
	}
	rows, err := queryPostgresSearchFunction(ctx, db, "search_objects_by_attributes", []string{"$1::jsonb"}, []interface{}{filterJSON}, opts)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var results []ObjectAttributesSearchResult
	for rows.Next() {
		var row ObjectAttributesSearchResult
		var details, attributes, matchedFields sql.NullString
		if err := rows.Scan(&row.SourceType, &row.ObjectID, &row.PageURL, &details, &attributes, &row.CreatedAt, &row.LastUpdatedAt, &matchedFields, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan object attributes search result: %w", err)
		}
		row.Details = rawMessageFromNullString(details)
		row.Attributes = rawMessageFromNullString(attributes)
		row.MatchedFields = rawMessageFromNullString(matchedFields)
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate object attributes search results: %w", err)
	}
	return results, nil
}

func queryPostgresSearchFunction(ctx context.Context, db *Handler, functionName string, functionArgs []string, args []interface{}, opts SearchFunctionOptions) (*sql.Rows, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if normalizeInformationSeedDBMS((*db).DBMS()) != DBPostgresStr {
		return nil, fmt.Errorf("unsupported database type for PostgreSQL search function %s: %s", functionName, (*db).DBMS())
	}
	if opts.Limit < 0 || opts.Offset < 0 {
		return nil, fmt.Errorf("limit and offset must be non-negative")
	}

	query := fmt.Sprintf("SELECT * FROM %s(%s)", functionName, joinFunctionArgs(functionArgs))
	if opts.Limit > 0 {
		args = append(args, opts.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if opts.Offset > 0 {
		args = append(args, opts.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := (*db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("execute PostgreSQL search function %s: %w", functionName, err)
	}
	return rows, nil
}

func joinFunctionArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	joined := args[0]
	for _, arg := range args[1:] {
		joined += ", " + arg
	}
	return joined
}

func marshalSearchFilters(filters map[string]string) (string, error) {
	if len(filters) == 0 {
		return "", fmt.Errorf("at least one search filter must be provided")
	}
	filterJSON, err := json.Marshal(filters)
	if err != nil {
		return "", fmt.Errorf("marshal search filters: %w", err)
	}
	return string(filterJSON), nil
}

func scanScrapedDataSearchResults(rows *sql.Rows) ([]ScrapedDataSearchResult, error) {
	var results []ScrapedDataSearchResult
	for rows.Next() {
		var row ScrapedDataSearchResult
		if err := rows.Scan(&row.IndexID, &row.PageURL, &row.JSONField, &row.JSONValue, &row.CreatedAt, &row.LastUpdatedAt, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan scraped data search result: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scraped data search results: %w", err)
	}
	return results, nil
}

func scanArtifactSearchResults(rows *sql.Rows) ([]ArtifactSearchResult, error) {
	var results []ArtifactSearchResult
	for rows.Next() {
		var row ArtifactSearchResult
		if err := rows.Scan(&row.SourceType, &row.ArtifactID, &row.PageURL, &row.JSONField, &row.JSONValue, &row.CreatedAt, &row.LastUpdatedAt, &row.Rank); err != nil {
			return nil, fmt.Errorf("scan artifact search result: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact search results: %w", err)
	}
	return results, nil
}

func rawMessageFromNullString(value sql.NullString) json.RawMessage {
	if !value.Valid {
		return nil
	}
	return json.RawMessage(value.String)
}
