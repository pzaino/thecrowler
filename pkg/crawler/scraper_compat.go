package crawler

import (
	"fmt"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	scraper "github.com/pzaino/thecrowler/pkg/scraper"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
	"golang.org/x/net/html"
)

func crawlerExtractor(ctx *ProcessContext, wd *vdi.WebDriver) scraper.Extractor {
	return scraper.Extractor{
		Driver: wd,
		MatchValue: func(item interface{}, selector rs.Selector) (bool, error) {
			return matchValue(ctx, item, selector), nil
		},
		ExtractExternal: func(selector rs.Selector) ([]interface{}, error) {
			switch strings.ToLower(strings.TrimSpace(selector.SelectorType)) {
			case strPluginCall:
				res := executeRuleCall(ctx, wd, RuleCallRequest{Kind: RuleCallKindPlugin, PluginName: selector.Selector, TimeoutSec: 30, OnError: "fail", Caller: "scraping.selector"})
				if !res.Success {
					return nil, fmt.Errorf("plugin selector %q failed", selector.Selector)
				}
				return normalizeRuleCallOutput(res.Value), nil
			case string(RuleCallKindAgent):
				if selector.AgentCall == nil {
					return nil, fmt.Errorf("agent selector is missing agent_call details")
				}
				res := executeRuleCall(ctx, wd, normalizeFromAgentCall(selector.AgentCall, "scraping.selector"))
				if !res.Success {
					return nil, fmt.Errorf("agent selector failed")
				}
				return normalizeRuleCallOutput(res.Value), nil
			default:
				return nil, fmt.Errorf("unsupported external selector type %q", selector.SelectorType)
			}
		},
	}
}

func normalizeRuleCallOutput(value interface{}) []interface{} {
	if value == nil {
		return []interface{}{}
	}
	if values, ok := value.([]interface{}); ok {
		return values
	}
	return []interface{}{value}
}

func extractJSFiles(wd *vdi.WebDriver) []CollectedScript {
	result, err := scraper.ExtractJavaScriptFiles(scraper.JavaScriptRequest{Driver: wd})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error extracting scripts: %v", err)
		return nil
	}
	return result.Scripts
}

// TransformTextToHTML is retained for the crawler page-processing boundary.
// Deprecated: migrate the remaining page-processing caller to scraper.ParseHTML.
func TransformTextToHTML(text string) (*html.Node, error) { return scraper.ParseHTML(text) }

// ExtractHTMLData is retained for the crawler page-processing boundary.
// Deprecated: migrate the remaining page-processing caller to scraper.ExtractHTMLData.
func ExtractHTMLData(node *html.Node) HTMLNode { return scraper.ExtractHTMLData(node) }
