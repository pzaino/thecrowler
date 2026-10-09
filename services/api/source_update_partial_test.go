package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// updateSourceFixture describes the Sources row inserted before exercising
// POST /v1/source/update against the SQLite-backed test handler.
type updateSourceFixture struct {
	sourceID    int64
	url         string
	subPriority int
	status      string
	restricted  int
	flags       int
	disabled    bool
	config      string
	details     string
}

func newUpdateSourceFixture(sourceID int64, url string) updateSourceFixture {
	return updateSourceFixture{
		sourceID: sourceID,
		url:      url,
		status:   "new",
		config:   "{}",
		details:  "{}",
	}
}

// setupUpdateSourceTest wires the SQLite test database into the package-level
// handler globals and restores them when the test finishes.
func setupUpdateSourceTest(t *testing.T) cdb.Handler {
	t.Helper()
	oldHandler := dbHandler
	oldGate := dbAdmission
	t.Cleanup(func() {
		dbHandler = oldHandler
		dbAdmission = oldGate
	})
	handler, cleanup := setupSourceAPITestDB(t)
	t.Cleanup(cleanup)
	dbHandler = handler
	dbAdmission = newDBAdmissionGate(1)
	return handler
}

func insertUpdateSourceFixture(t *testing.T, handler cdb.Handler, f updateSourceFixture) {
	t.Helper()
	_, err := handler.(*sourceAPITestHandler).db.Exec(`
		INSERT INTO Sources (source_id, source_uid, url, sub_priority, status, restricted, flags, disabled, config, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.sourceID, "source-uid-"+strconv.FormatInt(f.sourceID, 10), f.url, f.subPriority, f.status,
		f.restricted, f.flags, f.disabled, f.config, f.details)
	if err != nil {
		t.Fatalf("insert source fixture: %v", err)
	}
}

// readUpdateSourceRow returns the persisted state of the mutable source
// columns so tests can assert on stored values rather than SQL arguments.
func readUpdateSourceRow(t *testing.T, handler cdb.Handler, sourceID int64) (url string, subPriority int, status string, restricted int, disabled bool, flags int, config string, details string) {
	t.Helper()
	err := handler.(*sourceAPITestHandler).db.QueryRow(`
		SELECT url, sub_priority, status, restricted, disabled, flags, config, details
		FROM Sources WHERE source_id = ?`, sourceID).Scan(
		&url, &subPriority, &status, &restricted, &disabled, &flags, &config, &details)
	if err != nil {
		t.Fatalf("read persisted source row: %v", err)
	}
	return url, subPriority, status, restricted, disabled, flags, config, details
}

func postUpdateSource(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/source/update", strings.NewReader(body))
	updateSourceHandler(recorder, request)
	return recorder
}

func requireUpdateSourceOK(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

// TestUpdateSourceByIDKeepsCaseSensitiveURL reproduces the reported
// production issue: disabling a source by source_id only must not rewrite the
// stored Instagram-style URL (or any other stored column).
func TestUpdateSourceByIDKeepsCaseSensitiveURL(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	const storedURL = "https://www.instagram.com/p/DeEzfjLoCSs"
	fixture := newUpdateSourceFixture(236, storedURL)
	fixture.subPriority = 7
	fixture.status = "completed"
	fixture.restricted = 2
	fixture.flags = 3
	fixture.config = `{"version":"1.0","format_version":"1.0"}`
	fixture.details = `{"crawl":{"ok":true}}`
	insertUpdateSourceFixture(t, handler, fixture)

	recorder := postUpdateSource(t, `{"source_id":236,"disabled":true}`)
	requireUpdateSourceOK(t, recorder)

	url, subPriority, status, restricted, disabled, flags, config, details := readUpdateSourceRow(t, handler, 236)
	if url != storedURL {
		t.Fatalf("stored url = %q, want %q", url, storedURL)
	}
	if !disabled {
		t.Fatalf("stored disabled = false, want true")
	}
	if subPriority != 7 || status != "completed" || restricted != 2 || flags != 3 {
		t.Fatalf("unrelated columns changed: sub_priority=%d status=%q restricted=%d flags=%d",
			subPriority, status, restricted, flags)
	}
	if config != fixture.config || details != fixture.details {
		t.Fatalf("unrelated JSON columns changed: config=%q details=%q", config, details)
	}
}

// TestUpdateSourceKeepsURLWhenUpdatingStatus verifies that updating any
// non-URL field leaves the stored URL byte-for-byte unchanged.
func TestUpdateSourceKeepsURLWhenUpdatingStatus(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	const storedURL = "https://Example.test/CaseSensitive/Path"
	insertUpdateSourceFixture(t, handler, newUpdateSourceFixture(41, storedURL))

	recorder := postUpdateSource(t, `{"source_id":41,"status":"pending"}`)
	requireUpdateSourceOK(t, recorder)

	url, _, status, _, _, _, _, _ := readUpdateSourceRow(t, handler, 41)
	if url != storedURL {
		t.Fatalf("stored url = %q, want %q", url, storedURL)
	}
	if status != "pending" {
		t.Fatalf("stored status = %q, want %q", status, "pending")
	}
}

// TestUpdateSourceExplicitURLChange verifies that an explicitly supplied url is
// canonicalized with the same function used when storing sources, preserving
// host and path case so the stored URL remains safe to crawl.
func TestUpdateSourceExplicitURLChange(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	insertUpdateSourceFixture(t, handler, newUpdateSourceFixture(41, "https://old.example.test/path"))

	recorder := postUpdateSource(t, `{"source_id":41,"url":"https://New.Example.Test/Path"}`)
	requireUpdateSourceOK(t, recorder)

	url, _, _, _, _, _, _, _ := readUpdateSourceRow(t, handler, 41)
	const want = "https://New.Example.Test/Path"
	if url != want {
		t.Fatalf("stored url = %q, want case-preserving %q", url, want)
	}
}

// TestUpdateSourceByURLIsCaseSensitive reproduces the reported production
// issue: URL-based lookup must match the stored URL exactly so that
// case-sensitive paths (for example Instagram shortcodes) resolve to the right
// source, while differently-cased URLs stay independent.
func TestUpdateSourceByURLIsCaseSensitive(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	const storedURL = "https://www.instagram.com/p/DeEzfjLoCSs"
	insertUpdateSourceFixture(t, handler, newUpdateSourceFixture(43, storedURL))

	ok := postUpdateSource(t, `{"url":"https://www.instagram.com/p/DeEzfjLoCSs","disabled":true}`)
	requireUpdateSourceOK(t, ok)

	url, _, _, _, disabled, _, _, _ := readUpdateSourceRow(t, handler, 43)
	if !disabled {
		t.Fatal("stored disabled = false, want true")
	}
	if url != storedURL {
		t.Fatalf("stored url = %q, want %q", url, storedURL)
	}

	miss := postUpdateSource(t, `{"url":"https://www.instagram.com/p/deezfjlocss","disabled":false}`)
	if miss.Code == http.StatusOK {
		t.Fatalf("differently-cased URL unexpectedly matched a source: %s", miss.Body.String())
	}

	_, _, _, _, disabledAfter, _, _, _ := readUpdateSourceRow(t, handler, 43)
	if !disabledAfter {
		t.Fatal("differently-cased lookup mutated the stored source")
	}
}

// TestUpdateSourceByURLPreservesCaseSensitivePath verifies that resolving a
// source by its exact (mixed-case) URL leaves the stored URL byte-for-byte
// unchanged rather than rewriting it.
func TestUpdateSourceByURLPreservesCaseSensitivePath(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	const storedURL = "https://Example.test/CaseSensitive/Path"
	insertUpdateSourceFixture(t, handler, newUpdateSourceFixture(44, storedURL))

	recorder := postUpdateSource(t, `{"url":"https://Example.test/CaseSensitive/Path","status":"pending"}`)
	requireUpdateSourceOK(t, recorder)

	url, _, status, _, _, _, _, _ := readUpdateSourceRow(t, handler, 44)
	if url != storedURL {
		t.Fatalf("stored url = %q, want unchanged %q", url, storedURL)
	}
	if status != "pending" {
		t.Fatalf("stored status = %q, want %q", status, "pending")
	}
}

// TestUpdateSourceRejectsExplicitEmptyURL verifies that an explicitly supplied
// empty url is rejected as a client error and never replaces the stored URL.
func TestUpdateSourceRejectsExplicitEmptyURL(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	const storedURL = "https://source.example.test"
	insertUpdateSourceFixture(t, handler, newUpdateSourceFixture(41, storedURL))

	recorder := postUpdateSource(t, `{"source_id":41,"url":""}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "url must not be empty") {
		t.Fatalf("unexpected error response: %s", recorder.Body.String())
	}

	url, _, _, _, _, _, _, _ := readUpdateSourceRow(t, handler, 41)
	if url != storedURL {
		t.Fatalf("stored url = %q, want unchanged %q", url, storedURL)
	}
}

// TestUpdateSourceDisabledFalseReenables verifies that an explicit
// disabled:false is applied even though it is the zero value for bool.
func TestUpdateSourceDisabledFalseReenables(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	fixture := newUpdateSourceFixture(41, "https://source.example.test")
	fixture.disabled = true
	insertUpdateSourceFixture(t, handler, fixture)

	recorder := postUpdateSource(t, `{"source_id":41,"disabled":false}`)
	requireUpdateSourceOK(t, recorder)

	_, _, _, _, disabled, _, _, _ := readUpdateSourceRow(t, handler, 41)
	if disabled {
		t.Fatalf("stored disabled = true, want false")
	}
}

// TestUpdateSourceFlagsZeroClears verifies that an explicit flags:0 clears
// previously stored nonzero flags.
func TestUpdateSourceFlagsZeroClears(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	fixture := newUpdateSourceFixture(41, "https://source.example.test")
	fixture.flags = 5
	insertUpdateSourceFixture(t, handler, fixture)

	recorder := postUpdateSource(t, `{"source_id":41,"flags":0}`)
	requireUpdateSourceOK(t, recorder)

	_, _, _, _, _, flags, _, _ := readUpdateSourceRow(t, handler, 41)
	if flags != 0 {
		t.Fatalf("stored flags = %d, want 0", flags)
	}
}

// TestUpdateSourceRestrictedZeroResets verifies that an explicit
// restricted:0 sets the restriction level to zero.
func TestUpdateSourceRestrictedZeroResets(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	fixture := newUpdateSourceFixture(41, "https://source.example.test")
	fixture.restricted = 3
	insertUpdateSourceFixture(t, handler, fixture)

	recorder := postUpdateSource(t, `{"source_id":41,"restricted":0}`)
	requireUpdateSourceOK(t, recorder)

	_, _, _, restricted, _, _, _, _ := readUpdateSourceRow(t, handler, 41)
	if restricted != 0 {
		t.Fatalf("stored restricted = %d, want 0", restricted)
	}
}

// TestUpdateSourceOmittedConfigPreserved verifies that updating an unrelated
// field passes the stored configuration through byte-for-byte instead of
// reserializing it through the config struct.
func TestUpdateSourceOmittedConfigPreserved(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	fixture := newUpdateSourceFixture(41, "https://source.example.test")
	fixture.config = `{"version": "1.0",  "keep_me": true}`
	fixture.details = `{"cursor": "ABC-123"}`
	insertUpdateSourceFixture(t, handler, fixture)

	recorder := postUpdateSource(t, `{"source_id":41,"status":"pending"}`)
	requireUpdateSourceOK(t, recorder)

	_, _, status, _, _, _, config, details := readUpdateSourceRow(t, handler, 41)
	if status != "pending" {
		t.Fatalf("stored status = %q, want %q", status, "pending")
	}
	if config != fixture.config {
		t.Fatalf("stored config = %q, want unchanged %q", config, fixture.config)
	}
	if details != fixture.details {
		t.Fatalf("stored details = %q, want unchanged %q", details, fixture.details)
	}
}

// TestUpdateSourceResolvesSourceByURL verifies that URL-based source
// identification keeps working when no source_id is supplied.
func TestUpdateSourceResolvesSourceByURL(t *testing.T) {
	handler := setupUpdateSourceTest(t)
	const storedURL = "https://byurl.example.test"
	insertUpdateSourceFixture(t, handler, newUpdateSourceFixture(42, storedURL))

	recorder := postUpdateSource(t, `{"url":"https://byurl.example.test","disabled":true}`)
	requireUpdateSourceOK(t, recorder)

	url, _, _, _, disabled, _, _, _ := readUpdateSourceRow(t, handler, 42)
	if !disabled {
		t.Fatalf("stored disabled = false, want true")
	}
	if url != storedURL {
		t.Fatalf("stored url = %q, want %q", url, storedURL)
	}
}
