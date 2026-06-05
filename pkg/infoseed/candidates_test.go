package infoseed

import (
	"context"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func testSeed() cdb.InformationSeed {
	return cdb.InformationSeed{ID: 1, InformationSeed: "seed", CategoryID: 2, UsrID: 3}
}

func TestNormalizeURLCanonicalizesAndRemovesTracking(t *testing.T) {
	got, host, ok := NormalizeURL("HTTPS://Example.COM:443/path?utm_source=x&b=2&a=1#frag", []string{"utm_source"})
	if !ok {
		t.Fatal("expected URL to normalize")
	}
	if host != "example.com" {
		t.Fatalf("expected host example.com, got %q", host)
	}
	if got != "https://example.com/path?a=1&b=2" {
		t.Fatalf("unexpected normalized URL: %s", got)
	}
}

func TestNormalizeCandidatesDropsUnsupportedAndDeduplicatesHost(t *testing.T) {
	candidates := []Candidate{
		{URL: "https://example.com:443/a?utm_campaign=x"},
		{URL: "https://example.com/b"},
		{URL: "mailto:test@example.com"},
		{URL: "http://other.example:80/"},
	}
	got := NormalizeCandidates(candidates, CandidateOptions{TrackingParams: []string{"utm_campaign"}, DeduplicateHost: true})
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %#v", len(got), got)
	}
	if got[0].URL != "https://example.com/a" || got[1].URL != "http://other.example/" {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestValidateCandidatePluginOutputRequiresContract(t *testing.T) {
	output, err := validateCandidatePluginOutput([]byte(`{"accepted":true,"score":0.87,"reason":"trusted source","tags":["news"]}`))
	if err != nil {
		t.Fatalf("expected valid plugin output, got %v", err)
	}
	if output.Accepted == nil || !*output.Accepted || output.Score == nil || *output.Score != 0.87 {
		t.Fatalf("unexpected output: %#v", output)
	}

	if _, err := validateCandidatePluginOutput([]byte(`{"accept":true,"score":1,"reason":"legacy field"}`)); err == nil {
		t.Fatal("expected legacy/unknown fields to fail schema validation")
	}
	if _, err := validateCandidatePluginOutput([]byte(`{"accepted":true,"score":1}`)); err == nil {
		t.Fatal("expected missing reason to fail schema validation")
	}
}

func TestValidateSourceOverridesSafeAllowList(t *testing.T) {
	overrides, err := validateSourceOverrides(map[string]interface{}{
		"name":          "Example Source",
		"priority":      "high",
		"restricted":    float64(1),
		"flags":         float64(2),
		"source_config": map[string]interface{}{"version": "1.0.0"},
	})
	if err != nil {
		t.Fatalf("expected safe source overrides, got %v", err)
	}
	if overrides.Name == nil || *overrides.Name != "Example Source" {
		t.Fatalf("unexpected name override: %#v", overrides.Name)
	}
	if len(overrides.SourceConfig) == 0 {
		t.Fatal("expected source_config override to be preserved")
	}

	if _, err := validateSourceOverrides(map[string]interface{}{"url": "https://evil.example"}); err == nil {
		t.Fatal("expected unsafe url override to fail")
	}
	if _, err := validateSourceOverrides(map[string]interface{}{"restricted": 1.5}); err == nil {
		t.Fatal("expected non-integer restricted override to fail")
	}
}

func TestCandidateStageOrderingNormalizationFiltersThenPlugins(t *testing.T) {
	minScore := 0.5
	raw := []Candidate{
		{URL: "https://allowed.example/a", Score: 0.9},
		{URL: "https://allowed.example/a#frag", Score: 0.9},
		{URL: "https://allowed.example/low", Score: 0.1},
		{URL: "https://denied.example/b", Score: 0.9},
		{URL: "ftp://allowed.example/c", Score: 0.9},
	}
	normalized := NormalizeCandidatesWithRejections(raw, CandidateOptions{})
	if got := normalized.Rejected[CandidateRejectionDuplicateURL]; got != 1 {
		t.Fatalf("expected normalization to reject duplicate URL before filters, got %d", got)
	}
	if got := normalized.Rejected[CandidateRejectionInvalidURL]; got != 1 {
		t.Fatalf("expected normalization to reject unsupported URL before filters, got %d", got)
	}

	filtered := ApplyBuiltInCandidateFilters(normalized.Candidates, CandidateFilters{AllowedDomains: []string{"allowed.example"}, DeniedDomains: []string{"denied.example"}, RequiredSchemes: []string{"https"}, MinScore: &minScore})
	if got := filtered.Rejected[CandidateRejectionMinimumScore]; got != 1 {
		t.Fatalf("expected built-in minimum score rejection, got %d", got)
	}
	if got := filtered.Rejected[CandidateRejectionAllowedDomain]; got != 1 {
		t.Fatalf("expected built-in allowed domain rejection, got %d", got)
	}

	r := &Runner{Processors: []CandidateProcessor{recordingProcessor{name: "policy", calls: nil}}}
	processed, err := r.applyProcessors(testContext(t), testSeed(), SeedRunConfig{}, filtered.Candidates, []string{"policy"})
	if err != nil {
		t.Fatalf("unexpected processor error: %v", err)
	}
	if len(processed) != 1 || processed[0].URL != "https://allowed.example/a" {
		t.Fatalf("expected only normalized and built-in-filtered candidate to reach plugins, got %#v", processed)
	}
}

func TestSelectProcessorsCandidatePluginsAreOrderedAllowList(t *testing.T) {
	processors := []CandidateProcessor{
		namedNoopProcessor{name: "registered-first"},
		namedNoopProcessor{name: "registered-second"},
		namedNoopProcessor{name: "registered-third"},
	}
	selected := selectProcessors(processors, []string{"registered-third", "missing", "registered-first", "registered-third"})
	if len(selected) != 2 {
		t.Fatalf("expected two selected processors, got %d", len(selected))
	}
	if selected[0].(NamedCandidateProcessor).ProcessorName() != "registered-third" || selected[1].(NamedCandidateProcessor).ProcessorName() != "registered-first" {
		t.Fatalf("candidate_plugins should order execution and allow only named processors, got %#v", selected)
	}

	selected = selectProcessors(processors, nil)
	if len(selected) != len(processors) {
		t.Fatalf("omitted candidate_plugins should keep registration order, got %d", len(selected))
	}
}

func TestBuiltInCandidateFiltersEveryFilter(t *testing.T) {
	minScore := 0.5
	candidates := []Candidate{
		{URL: "http://allowed.example/scheme", Host: "allowed.example", Score: 1},
		{URL: "https://outside.example/allowed", Host: "outside.example", Score: 1},
		{URL: "https://blocked.allowed.example/denied", Host: "blocked.allowed.example", Score: 1},
		{URL: "https://allowed.example/score", Host: "allowed.example", Score: 0.1},
		{URL: "https://allowed.example/one", Host: "allowed.example", Score: 1},
		{URL: "https://allowed.example/two", Host: "allowed.example", Score: 1},
		{URL: "https://sub.allowed.example/domain", Host: "sub.allowed.example", Score: 1},
	}
	got := ApplyBuiltInCandidateFilters(
		candidates,
		CandidateFilters{
			AllowedDomains:         []string{"allowed.example"},
			DeniedDomains:          []string{"blocked.allowed.example"},
			RequiredSchemes:        []string{"https"},
			MinScore:               &minScore,
			MaxCandidatesPerHost:   1,
			MaxCandidatesPerDomain: 2,
		})
	want := map[string]int{
		CandidateRejectionRequiredScheme:       1,
		CandidateRejectionAllowedDomain:        1,
		CandidateRejectionDeniedDomain:         1,
		CandidateRejectionMinimumScore:         1,
		CandidateRejectionMaxCandidatesPerHost: 1,
	}
	for reason, count := range want {
		if got.Rejected[reason] != count {
			t.Fatalf("expected rejection %s=%d, got %d (all %#v)", reason, count, got.Rejected[reason], got.Rejected)
		}
	}
	if len(got.Candidates) != 2 || got.Candidates[0].URL != "https://allowed.example/one" || got.Candidates[1].URL != "https://sub.allowed.example/domain" {
		t.Fatalf("unexpected accepted candidates: %#v", got.Candidates)
	}
}

func TestBuiltInCandidateFilterCandidateLimit(t *testing.T) {
	candidates := []Candidate{
		{URL: "https://example.com/one", Host: "example.com", Score: 1},
		{URL: "https://example.com/two", Host: "example.com", Score: 1},
	}
	got := ApplyBuiltInCandidateFilters(candidates, CandidateFilters{MaxCandidates: 1})
	if got.Rejected[CandidateRejectionCandidateLimit] != 1 {
		t.Fatalf("expected candidate limit rejection, got %#v", got.Rejected)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].URL != "https://example.com/one" {
		t.Fatalf("unexpected accepted candidates: %#v", got.Candidates)
	}
}

func TestInformationSeedEventPayloadGroupsRejectionsByStage(t *testing.T) {
	stats := newSeedDiscoveryStats()
	stats.addRejectedAtStage(CandidateRejectionStageNormalization, CandidateRejectionDuplicateURL, 2)
	stats.addRejectedAtStage(CandidateRejectionStageBuiltInFilters, CandidateRejectionMinimumScore, 1)
	payload := informationSeedEventPayload(testSeed(), 0, stats)
	stages, ok := payload["candidate_rejection_stages"].(map[string]map[string]int)
	if !ok {
		t.Fatalf("expected stage rejection summary, got %#v", payload["candidate_rejection_stages"])
	}
	if stages[CandidateRejectionStageNormalization][CandidateRejectionDuplicateURL] != 2 || stages[CandidateRejectionStageBuiltInFilters][CandidateRejectionMinimumScore] != 1 {
		t.Fatalf("unexpected stage summaries: %#v", stages)
	}
}

func TestApplyProcessorsGroupsSourceOverrideValidationRejections(t *testing.T) {
	r := &Runner{Processors: []CandidateProcessor{errorProcessor{name: "bad-override", err: errString("candidate plugin \"bad-override\" malformed output: unsafe source_overrides: field \"url\" is not allowed")}}}
	processed, rejections, err := r.applyProcessorsWithRejections(testContext(t), testSeed(), SeedRunConfig{}, []Candidate{{URL: "https://example.com/", Host: "example.com"}}, []string{"bad-override"})
	if err == nil {
		t.Fatal("expected processor error")
	}
	if len(processed) != 0 {
		t.Fatalf("expected candidate to be rejected, got %#v", processed)
	}
	if rejections[CandidateRejectionStageSourceOverrideValidation][CandidateRejectionSourceOverrideInvalid] != 1 {
		t.Fatalf("expected source override validation rejection, got %#v", rejections)
	}
}

func TestValidateCandidatePluginDecisionValidatesOverridesBeforeDecision(t *testing.T) {
	if _, err := validateCandidatePluginDecision([]byte(`{"accepted":false,"score":0,"reason":"reject","source_overrides":{"url":"https://evil.example"}}`)); err == nil {
		t.Fatal("expected invalid source_overrides to fail even for rejected plugin decisions")
	}
}

type namedNoopProcessor struct{ name string }

func (p namedNoopProcessor) ProcessorName() string { return p.name }

func (p namedNoopProcessor) ProcessCandidate(_ context.Context, input CandidatePluginInput) (Candidate, bool, error) {
	return input.Candidate, true, nil
}

type recordingProcessor struct {
	name  string
	calls *[]string
}

func (p recordingProcessor) ProcessorName() string { return p.name }

func (p recordingProcessor) ProcessCandidate(_ context.Context, input CandidatePluginInput) (Candidate, bool, error) {
	if p.calls != nil {
		*p.calls = append(*p.calls, input.Candidate.URL)
	}
	return input.Candidate, true, nil
}

type errorProcessor struct {
	name string
	err  error
}

func (p errorProcessor) ProcessorName() string { return p.name }

func (p errorProcessor) ProcessCandidate(_ context.Context, input CandidatePluginInput) (Candidate, bool, error) {
	return input.Candidate, false, p.err
}

type errString string

func (e errString) Error() string { return string(e) }
