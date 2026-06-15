package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseSearchFunctionQuerySourceIDBIGINT(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/search/webobjects_by_source?source_id=9223372036854775807", nil)
	query, err := parseSearchFunctionQuery(request)
	if err != nil {
		t.Fatalf("parseSearchFunctionQuery() error = %v", err)
	}
	if query.SourceID != int64(9223372036854775807) {
		t.Fatalf("SourceID = %d, want max BIGINT", query.SourceID)
	}
}

func TestParseSearchFunctionQueryRejectsSourceIDOutsideBIGINT(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/search/webobjects_by_source?source_id=9223372036854775808", nil)
	if _, err := parseSearchFunctionQuery(request); err == nil {
		t.Fatal("parseSearchFunctionQuery() accepted source_id outside BIGINT range")
	}
}

func TestParseSearchFunctionQuerySourceUIDLookups(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/search/source_uid_by_name?source_uid=stable-uid&source_name=Example+Source&source_url=https%3A%2F%2Fexample.test", nil)
	query, err := parseSearchFunctionQuery(request)
	if err != nil {
		t.Fatalf("parseSearchFunctionQuery() error = %v", err)
	}
	if query.SourceUID != "stable-uid" || query.SourceName != "Example Source" || query.SourceURL != "https://example.test" {
		t.Fatalf("unexpected source lookup query: %#v", query)
	}
}
