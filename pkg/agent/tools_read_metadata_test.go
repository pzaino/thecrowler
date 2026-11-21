package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func metadataToolAuth(allowlist ...string) ToolAuthContext {
	allow := map[string]bool{}
	for _, name := range allowlist {
		allow[name] = true
	}
	return ToolAuthContext{
		Identity:    AgentIdentity{AgentID: "op", TrustLevel: "trusted", Capabilities: []string{"db_read"}},
		Enforcement: true,
		Allowlist:   allow,
		RunID:       "run-1",
		TraceID:     "trace-1",
	}
}

func statusRowsWithError(status, lastError string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"source_id", "source_uid", "url", "status", "priority", "sub_priority",
		"engine", "created_at", "last_updated_at", "last_crawled_at",
		"last_error", "last_error_at", "restricted", "disabled", "flags", "config",
	}).AddRow(
		uint64(7), "uid-7", "https://example.com", status, "high", 0,
		"engine-1", "2026-11-06 12:00:00", "2026-11-06 12:01:00", "2026-11-06 12:01:00",
		lastError, "", 0, false, 0, []byte(`{"secret":"config-secret"}`),
	)
}

func TestGetSourceCrawlStatusAdapter(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{"get_source_crawl_status": true}
	tool := &sourceCrawlStatusTool{}
	ctx := toolTestContext(handler, auth)

	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	mock.ExpectQuery("WHERE source_uid").WillReturnRows(statusRowsWithError("completed", "SECRET_LAST_ERROR"))
	expectEmptyMailboxStatus(mock)

	result, err := tool.Execute(ctx, map[string]any{"source_id": float64(7)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result["source_id"] != uint64(7) || result["source_uid"] != "uid-7" || result["status"] != "completed" {
		t.Fatalf("unexpected status result: %v", result)
	}
	if result["status_records"] != 1 || result["last_crawled_at"] == "" {
		t.Fatalf("missing status metadata: %v", result)
	}
	if result["last_error_present"] != true {
		t.Fatalf("last-error presence indicator missing: %v", result)
	}
	rendered := fmt.Sprint(result)
	for _, forbidden := range []string{"SECRET_LAST_ERROR", "config-secret", `"config"`, "usr_id"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("sensitive field %q exposed: %v", forbidden, result)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetSourceCrawlStatusEmptyAndFailures(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{"get_source_crawl_status": true}
	tool := &sourceCrawlStatusTool{}
	ctx := toolTestContext(handler, auth)

	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	mock.ExpectQuery("WHERE source_uid").WillReturnRows(sqlmock.NewRows([]string{
		"source_id", "source_uid", "url", "status", "priority", "sub_priority",
		"engine", "created_at", "last_updated_at", "last_crawled_at",
		"last_error", "last_error_at", "restricted", "disabled", "flags", "config",
	}))
	result, err := tool.Execute(ctx, map[string]any{"source_id": float64(7)})
	if err != nil {
		t.Fatalf("empty status execute: %v", err)
	}
	if result["status_records"] != 0 || result["status"] != "" {
		t.Fatalf("empty status result wrong: %v", result)
	}

	mock.ExpectQuery("SELECT source_id").WillReturnError(sql.ErrNoRows)
	if _, err := tool.Execute(ctx, map[string]any{"source_id": float64(404)}); err == nil {
		t.Fatal("unknown source must fail")
	}

	mock.ExpectQuery("SELECT source_id").WillReturnError(errors.New("connection refused"))
	if _, err := tool.Execute(ctx, map[string]any{"source_id": float64(7)}); err == nil {
		t.Fatal("database failure must fail")
	} else if strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("database detail leaked: %v", err)
	}

	auth.AllowedSources = map[uint64]bool{7: true}
	scopedCtx := toolTestContext(handler, auth)
	if _, err := tool.Execute(scopedCtx, map[string]any{"source_id": float64(8)}); err == nil {
		t.Fatal("out-of-scope source must fail")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"source_id": float64(7)}); err == nil {
		t.Fatal("missing runtime must fail")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("scope/runtime checks must not touch DB: %v", err)
	}
}

func metadataSourceExpectation(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
}

func metadataSourceExpectationFor(mock sqlmock.Sqlmock, sourceID int64, uid string) {
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(sourceID, uid, "https://example.com", "Example"))
}

func metadataScrapedRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"index_id", "source_uid", "page_url", "json_field", "json_val",
		"created_at", "last_updated_at", "rank",
	}).AddRow(
		uint64(11), "uid-7", "https://example.com/page", "price", "SECRET_SCRAPED_VALUE",
		nil, nil, 0.9,
	).AddRow(
		uint64(12), "uid-other", "https://other.example/page", "price", "OTHER_SECRET",
		nil, nil, 0.8,
	)
}

func metadataArtifactRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"source_type", "artifact_id", "source_uid", "page_url", "json_field", "json_val",
		"created_at", "last_updated_at", "rank",
	}).AddRow(
		"webobject", uint64(22), "uid-7", "https://example.com/page", "kind", "SECRET_ARTIFACT_VALUE",
		nil, nil, 0.7,
	).AddRow(
		"webobject", uint64(23), "uid-other", "https://other.example/page", "kind", "OTHER_SECRET",
		nil, nil, 0.6,
	)
}

func TestFindScrapedFieldMetadataAdapter(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{"find_scraped_field_metadata": true}
	tool := &scrapedFieldMetadataTool{}
	ctx := toolTestContext(handler, auth)

	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_scraped_data_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WithArgs("price", "42", "uid-7", 5).WillReturnRows(metadataScrapedRows())
	result, err := tool.Execute(ctx, map[string]any{
		"source_id": float64(7), "field": "price", "value": "42",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result["count"] != 1 || result["source_uid"] != "uid-7" || result["field"] != "price" {
		t.Fatalf("unexpected metadata result: %v", result)
	}
	matches, ok := result["matches"].([]any)
	if !ok || len(matches) != 1 {
		t.Fatalf("unexpected matches: %v", result["matches"])
	}
	match := matches[0].(map[string]any)
	if match["index_id"] != uint64(11) || match["page_url"] != "https://example.com/page" || match["matched_field"] != "price" {
		t.Fatalf("metadata subset wrong: %v", match)
	}
	rendered := fmt.Sprint(result)
	for _, forbidden := range []string{"SECRET_SCRAPED_VALUE", "OTHER_SECRET", `"json_val"`} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("scraped value %q leaked: %v", forbidden, result)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestFindArtifactMetadataAdapter(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{"find_artifact_metadata": true}
	tool := &artifactMetadataTool{}
	ctx := toolTestContext(handler, auth)

	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_artifacts_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WithArgs("kind", "report", "uid-7", 5).WillReturnRows(metadataArtifactRows())
	result, err := tool.Execute(ctx, map[string]any{
		"source_id": float64(7), "field": "kind", "value": "report",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result["count"] != 1 || result["source_uid"] != "uid-7" || result["field"] != "kind" {
		t.Fatalf("unexpected metadata result: %v", result)
	}
	artifacts, ok := result["artifacts"].([]any)
	if !ok || len(artifacts) != 1 {
		t.Fatalf("unexpected artifacts: %v", result["artifacts"])
	}
	artifact := artifacts[0].(map[string]any)
	if artifact["artifact_id"] != uint64(22) || artifact["source_type"] != "webobject" || artifact["matched_field"] != "kind" {
		t.Fatalf("artifact subset wrong: %v", artifact)
	}
	rendered := fmt.Sprint(result)
	for _, forbidden := range []string{"SECRET_ARTIFACT_VALUE", "OTHER_SECRET", `"json_val"`} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("artifact value %q leaked: %v", forbidden, result)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMetadataToolsEmptyBoundsAndFailures(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{
		"find_scraped_field_metadata": true,
		"find_artifact_metadata":      true,
	}
	scraped := &scrapedFieldMetadataTool{}
	artifact := &artifactMetadataTool{}
	ctx := toolTestContext(handler, auth)

	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_scraped_data_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WillReturnRows(sqlmock.NewRows([]string{
			"index_id", "source_uid", "page_url", "json_field", "json_val",
			"created_at", "last_updated_at", "rank",
		}))
	result, err := scraped.Execute(ctx, map[string]any{
		"source_id": float64(7), "field": "price", "value": "42",
	})
	if err != nil || result["count"] != 0 {
		t.Fatalf("empty metadata result: %v, %v", result, err)
	}

	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_artifacts_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WillReturnError(errors.New("db down"))
	if _, err := artifact.Execute(ctx, map[string]any{
		"source_id": float64(7), "field": "kind", "value": "report",
	}); err == nil {
		t.Fatal("artifact search failure must fail")
	} else if strings.Contains(err.Error(), "db down") {
		t.Fatalf("database detail leaked: %v", err)
	}

	for _, tool := range []AgentTool{scraped, artifact} {
		for _, args := range []map[string]any{
			{"source_id": float64(7), "field": "", "value": "x"},
			{"source_id": float64(7), "field": "x", "value": ""},
			{"source_id": float64(7), "field": strings.Repeat("f", maxMetadataFieldLen+1), "value": "x"},
			{"source_id": float64(7), "field": "x", "value": strings.Repeat("v", maxMetadataValueLen+1)},
			{"source_id": float64(7), "field": "x", "value": "y", "limit": float64(11)},
		} {
			if _, err := tool.Execute(ctx, args); err == nil {
				t.Fatalf("%s args %v: expected rejection", tool.Name(), args)
			}
		}
	}

	auth.AllowedSources = map[uint64]bool{7: true}
	scopedCtx := toolTestContext(handler, auth)
	if _, err := scraped.Execute(scopedCtx, map[string]any{
		"source_id": float64(8), "field": "price", "value": "42",
	}); err == nil {
		t.Fatal("out-of-scope metadata source must fail")
	}
	if _, err := artifact.Execute(context.Background(), map[string]any{
		"source_id": float64(7), "field": "kind", "value": "report",
	}); err == nil {
		t.Fatal("missing runtime must fail")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("scope/runtime/argument checks must not touch DB: %v", err)
	}
}

func TestMetadataSourceFilterAndLimitAreBounded(t *testing.T) {
	_, mock, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{"find_scraped_field_metadata": true}
	tool := &scrapedFieldMetadataTool{}
	ctx := toolTestContext(handler, auth)

	rows := sqlmock.NewRows([]string{
		"index_id", "source_uid", "page_url", "json_field", "json_val",
		"created_at", "last_updated_at", "rank",
	}).AddRow(uint64(1), "uid-other", "https://other/1", "price", "SECRET", nil, nil, 0.9).
		AddRow(uint64(2), "uid-7", "https://example/2", "price", "SECRET", nil, nil, 0.8).
		AddRow(uint64(3), "uid-7", "https://example/3", "price", "SECRET", nil, nil, 0.7)
	metadataSourceExpectation(mock)
	mock.ExpectQuery(`^SELECT \* FROM search_scraped_data_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
		WithArgs("price", "42", "uid-7", 2).WillReturnRows(rows)
	result, err := tool.Execute(ctx, map[string]any{
		"source_id": float64(7), "field": "price", "value": "42", "limit": float64(2),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result["count"] != 2 {
		t.Fatalf("result was not limited to 2: %v", result)
	}
	if strings.Contains(fmt.Sprint(result), "uid-other") {
		t.Fatalf("cross-source row leaked: %v", result)
	}
}

func TestMetadataToolsAuthorization(t *testing.T) {
	tools := []AgentTool{
		&sourceCrawlStatusTool{},
		&scrapedFieldMetadataTool{},
		&artifactMetadataTool{},
	}
	argsFor := func(name string) map[string]any {
		if name == "get_source_crawl_status" {
			return map[string]any{"source_id": float64(7)}
		}
		return map[string]any{"source_id": float64(7), "field": "price", "value": "42"}
	}
	for _, tool := range tools {
		t.Run(tool.Name(), func(t *testing.T) {
			args := argsFor(tool.Name())
			base := metadataToolAuth(tool.Name())
			base.AllowedSources = map[uint64]bool{7: true}
			if err := AuthorizeToolCall(base, tool, args); err != nil {
				t.Fatalf("valid call rejected: %v", err)
			}

			missingRequired := map[string]any{}
			if tool.Name() != "get_source_crawl_status" {
				missingRequired = map[string]any{"source_id": float64(7)}
			}
			cases := []struct {
				name   string
				mutate func(*ToolAuthContext)
				args   map[string]any
				code   string
			}{
				{"missing db_read", func(a *ToolAuthContext) { a.Identity.Capabilities = nil }, args, ToolDenyCapability},
				{"missing allowlist", func(a *ToolAuthContext) { a.Allowlist = map[string]bool{} }, args, ToolDenyNotAllowlisted},
				{"all does not allowlist", func(a *ToolAuthContext) {
					a.Allowlist = map[string]bool{}
					a.Identity.Capabilities = []string{"all"}
				}, args, ToolDenyNotAllowlisted},
				{"insufficient trust", func(a *ToolAuthContext) { a.Identity.TrustLevel = "restricted" }, args, ToolDenyTrust},
				{"forbidden contract", func(a *ToolAuthContext) {
					a.Identity.Contract = &AgentContract{ForbiddenActions: []string{"tool:" + tool.Name()}}
				}, args, ToolDenyContract},
				{"missing required field", func(*ToolAuthContext) {}, missingRequired, ToolDenyArguments},
				{"oversized field", func(*ToolAuthContext) {}, map[string]any{
					"source_id": float64(7), "field": strings.Repeat("f", maxMetadataFieldLen+1), "value": "x",
				}, ToolDenyArguments},
				{"unexpected property", func(*ToolAuthContext) {}, map[string]any{
					"source_id": float64(7), "field": "price", "value": "42", "sql": "SELECT 1",
				}, ToolDenyArguments},
				{"string source id", func(*ToolAuthContext) {}, map[string]any{"source_id": "7"}, ToolDenyArguments},
			}
			for _, tc := range cases {
				auth := base
				tc.mutate(&auth)
				err := AuthorizeToolCall(auth, tool, tc.args)
				var authErr *ToolAuthError
				if !errors.As(err, &authErr) || authErr.Code != tc.code {
					t.Fatalf("%s: got %v, want code %s", tc.name, err, tc.code)
				}
			}
		})
	}
}

func TestMetadataToolsConcurrentScopes(t *testing.T) {
	type outcome struct {
		sourceID uint64
		result   map[string]any
		err      error
		mock     sqlmock.Sqlmock
	}
	results := make(chan outcome, 2)
	for _, sourceID := range []uint64{7, 8} {
		sourceID := sourceID
		_, mock, handler, auth := newSQLMockRuntime(t)
		auth.Allowlist = map[string]bool{"find_scraped_field_metadata": true}
		auth.AllowedSources = map[uint64]bool{sourceID: true}
		uid := fmt.Sprintf("uid-%d", sourceID)
		metadataSourceExpectationFor(mock, int64(sourceID), uid)
		mock.ExpectQuery(`^SELECT \* FROM search_scraped_data_field\(\$1, \$2\) WHERE source_uid = \$3 LIMIT \$4$`).
			WithArgs("price", "42", uid, 5).WillReturnRows(sqlmock.NewRows([]string{
			"index_id", "source_uid", "page_url", "json_field", "json_val",
			"created_at", "last_updated_at", "rank",
		}).AddRow(uint64(sourceID), uid, "https://example/", "price", "SECRET", nil, nil, 0.9))
		ctx := toolTestContext(handler, auth)
		go func() {
			result, err := (&scrapedFieldMetadataTool{}).Execute(ctx, map[string]any{
				"source_id": float64(sourceID), "field": "price", "value": "42",
			})
			results <- outcome{sourceID: sourceID, result: result, err: err, mock: mock}
		}()
	}
	for i := 0; i < 2; i++ {
		out := <-results
		if out.err != nil {
			t.Fatalf("source %d: execute: %v", out.sourceID, out.err)
		}
		uid := fmt.Sprintf("uid-%d", out.sourceID)
		if out.result["source_uid"] != uid || out.result["count"] != 1 {
			t.Fatalf("source %d scope leak: %v", out.sourceID, out.result)
		}
		if err := out.mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("source %d unmet expectations: %v", out.sourceID, err)
		}
	}
}

func TestMetadataToolContextDeadline(t *testing.T) {
	_, _, handler, auth := newSQLMockRuntime(t)
	auth.Allowlist = map[string]bool{"get_source_crawl_status": true}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ctx = ContextWithToolRuntime(ctx, ToolRuntime{
		DB: handler, Auth: auth, Deadline: time.Now().Add(-time.Second),
	})
	if _, err := (&sourceCrawlStatusTool{}).Execute(ctx, map[string]any{"source_id": float64(7)}); err == nil {
		t.Fatal("expired deadline must fail")
	}
}
