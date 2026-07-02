package ruleset

import "testing"

func TestGetLifecycleHookStages(t *testing.T) {
	r := &CrawlingRule{Lifecycle: &CrawlingLifecycle{PreCrawl: &CrawlingLifecycleHook{}, PreRequest: &CrawlingLifecycleHook{}, PostResponse: &CrawlingLifecycleHook{}, PreFuzz: &CrawlingLifecycleHook{}, PerFuzzCandidate: &CrawlingLifecycleHook{}, PostFuzz: &CrawlingLifecycleHook{}, PostCrawl: &CrawlingLifecycleHook{}}}
	stages := []string{"pre_crawl", "pre_request", "post_response", "pre_fuzz", "per_fuzz_candidate", "post_fuzz", "post_crawl"}
	for _, s := range stages {
		if h := r.GetLifecycleHook(s); h == nil {
			t.Fatalf("stage %s missing", s)
		}
	}
}
