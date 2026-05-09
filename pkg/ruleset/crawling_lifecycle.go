package ruleset

import "strings"

type CrawlingLifecycle struct {
	PreCrawl         *CrawlingLifecycleHook `json:"pre_crawl,omitempty" yaml:"pre_crawl,omitempty"`
	PreRequest       *CrawlingLifecycleHook `json:"pre_request,omitempty" yaml:"pre_request,omitempty"`
	PostResponse     *CrawlingLifecycleHook `json:"post_response,omitempty" yaml:"post_response,omitempty"`
	PreFuzz          *CrawlingLifecycleHook `json:"pre_fuzz,omitempty" yaml:"pre_fuzz,omitempty"`
	PerFuzzCandidate *CrawlingLifecycleHook `json:"per_fuzz_candidate,omitempty" yaml:"per_fuzz_candidate,omitempty"`
	PostFuzz         *CrawlingLifecycleHook `json:"post_fuzz,omitempty" yaml:"post_fuzz,omitempty"`
	PostCrawl        *CrawlingLifecycleHook `json:"post_crawl,omitempty" yaml:"post_crawl,omitempty"`
}

type CrawlingLifecycleHook struct {
	AgentCall *AgentCall `json:"agent_call,omitempty" yaml:"agent_call,omitempty"`
}

func (c *CrawlingRule) GetLifecycleHook(stage string) *CrawlingLifecycleHook {
	if c == nil || c.Lifecycle == nil {
		return nil
	}
	switch strings.TrimSpace(strings.ToLower(stage)) {
	case "pre_crawl":
		return c.Lifecycle.PreCrawl
	case "pre_request":
		return c.Lifecycle.PreRequest
	case "post_response":
		return c.Lifecycle.PostResponse
	case "pre_fuzz":
		return c.Lifecycle.PreFuzz
	case "per_fuzz_candidate":
		return c.Lifecycle.PerFuzzCandidate
	case "post_fuzz":
		return c.Lifecycle.PostFuzz
	case "post_crawl":
		return c.Lifecycle.PostCrawl
	default:
		return nil
	}
}
