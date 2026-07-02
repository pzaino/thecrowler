package search

import (
	"context"
	"fmt"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// FunctionOptions controls pagination for PostgreSQL search functions.
type FunctionOptions = cdb.SearchFunctionOptions

type CorrelatedSourceResult = cdb.CorrelatedSourceSearchResult
type PageResult = cdb.PageSearchResult
type ScrapedDataResult = cdb.ScrapedDataSearchResult
type ArtifactResult = cdb.ArtifactSearchResult
type ArtifactFieldsResult = cdb.ArtifactFieldsSearchResult
type ArtifactAttributeResult = cdb.ArtifactAttributeSearchResult
type ObjectAttributeResult = cdb.ObjectAttributeSearchResult
type ObjectAttributesResult = cdb.ObjectAttributesSearchResult
type SourceStatusResult = cdb.SourceStatusRow

// SourceUIDResult identifies a source found by its human-readable name or URL.
type SourceUIDResult struct {
	SourceUID string
	Name      string
	URL       string
}

func FindCorrelatedSourcesByDomain(ctx context.Context, db *cdb.Handler, domain string, opts FunctionOptions) ([]CorrelatedSourceResult, error) {
	return cdb.FindCorrelatedSourcesByDomain(ctx, db, domain, opts)
}
func SearchPages(ctx context.Context, db *cdb.Handler, query, language string, opts FunctionOptions) ([]PageResult, error) {
	return cdb.SearchPages(ctx, db, query, language, opts)
}
func SearchScrapedDataFunction(ctx context.Context, db *cdb.Handler, query string, opts FunctionOptions) ([]ScrapedDataResult, error) {
	return cdb.SearchScrapedData(ctx, db, query, opts)
}
func SearchScrapedDataField(ctx context.Context, db *cdb.Handler, fieldName, fieldValue string, opts FunctionOptions) ([]ScrapedDataResult, error) {
	return cdb.SearchScrapedDataField(ctx, db, fieldName, fieldValue, opts)
}
func SearchArtifacts(ctx context.Context, db *cdb.Handler, query string, opts FunctionOptions) ([]ArtifactResult, error) {
	return cdb.SearchArtifacts(ctx, db, query, opts)
}
func SearchArtifactsField(ctx context.Context, db *cdb.Handler, fieldName, fieldValue string, opts FunctionOptions) ([]ArtifactResult, error) {
	return cdb.SearchArtifactsField(ctx, db, fieldName, fieldValue, opts)
}
func SearchArtifactsFields(ctx context.Context, db *cdb.Handler, filters map[string]string, opts FunctionOptions) ([]ArtifactFieldsResult, error) {
	return cdb.SearchArtifactsFields(ctx, db, filters, opts)
}
func SearchArtifactsByAttribute(ctx context.Context, db *cdb.Handler, fieldName, fieldValue string, opts FunctionOptions) ([]ArtifactAttributeResult, error) {
	return cdb.SearchArtifactsByAttribute(ctx, db, fieldName, fieldValue, opts)
}
func SearchObjectsByAttribute(ctx context.Context, db *cdb.Handler, fieldName, fieldValue string, opts FunctionOptions) ([]ObjectAttributeResult, error) {
	return cdb.SearchObjectsByAttribute(ctx, db, fieldName, fieldValue, opts)
}
func SearchObjectsByAttributes(ctx context.Context, db *cdb.Handler, filters map[string]string, opts FunctionOptions) ([]ObjectAttributesResult, error) {
	return cdb.SearchObjectsByAttributes(ctx, db, filters, opts)
}

// GetSourceStatusByUID returns status information for one stable source UID.
func GetSourceStatusByUID(_ context.Context, db *cdb.Handler, sourceUID string) ([]SourceStatusResult, error) {
	return cdb.GetSourceStatusByUID(db, sourceUID)
}

// FindSourceUIDsByName returns sources whose names exactly match name,
// case-insensitively.
func FindSourceUIDsByName(_ context.Context, db *cdb.Handler, name string) ([]SourceUIDResult, error) {
	return findSourceUIDs(db, sqlSourceUIDByName, name)
}

// FindSourceUIDsByURL returns sources whose URLs exactly match sourceURL,
// case-insensitively.
func FindSourceUIDsByURL(_ context.Context, db *cdb.Handler, sourceURL string) ([]SourceUIDResult, error) {
	return findSourceUIDs(db, sqlSourceUIDByURL, sourceURL)
}

func findSourceUIDs(db *cdb.Handler, query, value string) ([]SourceUIDResult, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	rows, err := (*db).ExecuteQuery(query, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	results := []SourceUIDResult{}
	for rows.Next() {
		var result SourceUIDResult
		if err := rows.Scan(&result.SourceUID, &result.Name, &result.URL); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
