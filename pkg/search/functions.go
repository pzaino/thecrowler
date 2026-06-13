package search

import (
	"context"

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
