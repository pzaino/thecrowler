package crawler

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
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

func extractContent(ctx *ProcessContext, wd *vdi.WebDriver, selector rs.Selector, all bool) []interface{} {
	result, err := crawlerExtractor(ctx, wd).Extract(scraper.ExtractRequest{Selector: selector, All: all})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-ExtractContent] Failed to extract '%s': %v", selector.Selector, err)
		return []interface{}{}
	}
	return result.Values
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

func extractDataFromElement(_ *ProcessContext, item interface{}, selector rs.Selector) []string {
	result, err := scraper.ExtractElement(scraper.ElementRequest{Element: item, Selector: selector})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error extracting data from element: %v", err)
		return []string{}
	}
	return result.Values
}

func fallbackExtractByCSS(ctx *ProcessContext, doc *goquery.Document, selector rs.Selector, all bool) []string {
	// Retained for package-local compatibility; extraction now lives in scraper.Extractor.
	var results []string
	var elements []*goquery.Selection
	if all {
		doc.Find(selector.Selector).Each(func(_ int, s *goquery.Selection) { elements = append(elements, s) })
	} else if s := doc.Find(selector.Selector).First(); s.Length() > 0 {
		elements = append(elements, s)
	}
	for _, element := range elements {
		if matchValue(ctx, element, selector) {
			results = append(results, extractDataFromElement(ctx, element, selector)...)
			if !all {
				break
			}
		}
	}
	return results
}

func fallbackExtractByRegex(content, pattern string, all bool) []string {
	result, err := scraper.ExtractRegex(scraper.RegexRequest{Content: content, Pattern: pattern, All: all})
	if err != nil {
		return []string{}
	}
	return result.Values
}

func extractJSFiles(wd *vdi.WebDriver) []CollectedScript {
	result, err := scraper.ExtractJavaScriptFiles(scraper.JavaScriptRequest{Driver: wd})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error extracting scripts: %v", err)
		return nil
	}
	return result.Scripts
}

func detectObfuscation(content string) bool   { return scraper.DetectObfuscation(content) }
func deobfuscateScript(content string) string { return scraper.DeobfuscateScript(content) }
func lintScript(content string) []string      { return scraper.LintScript(content) }
func containsObfuscationPatterns(content string) bool {
	return scraper.ContainsObfuscationPatterns(content)
}

func stripHTML(data string) string {
	result, _ := scraper.Clean(scraper.TransformRequest{Data: []byte(data), Details: map[string]interface{}{"remove_html": true}})
	return string(result.Data)
}
func stripSpecialChars(data string) string {
	result, _ := scraper.Clean(scraper.TransformRequest{Data: []byte(data), Details: map[string]interface{}{"remove_special_chars": true}})
	return string(result.Data)
}
func stripNumbers(data string) string {
	result, _ := scraper.Clean(scraper.TransformRequest{Data: []byte(data), Details: map[string]interface{}{"remove_numbers": true}})
	return string(result.Data)
}

// FindElementByType is a temporary compatibility wrapper around scraper.Extractor.
func FindElementByType(ctx *ProcessContext, wd *vdi.WebDriver, selector rs.Selector) (vdi.WebElement, error) {
	result, err := crawlerExtractor(ctx, wd).FindElement(scraper.LookupRequest{Selector: selector})
	if err != nil {
		return nil, err
	}
	return result.Elements[0], nil
}

// FindElementsByType is a temporary compatibility wrapper around scraper.Extractor.
func FindElementsByType(ctx *ProcessContext, wd *vdi.WebDriver, selector rs.Selector) ([]vdi.WebElement, error) {
	if strings.EqualFold(strings.TrimSpace(selector.SelectorType), strPluginCall) {
		pluginName := strings.TrimSpace(selector.Selector)
		plugin, exists := ctx.re.JSPlugins.GetPlugin(pluginName)
		if !exists {
			return nil, fmt.Errorf("plugin not found: %s", pluginName)
		}
		value, err := (*wd).ExecuteScript(plugin.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("error executing plugin: %w", err)
		}
		elements, ok := value.([]vdi.WebElement)
		if !ok {
			return nil, fmt.Errorf("plugin did not return a list of elements")
		}
		return elements, nil
	}
	result, err := crawlerExtractor(ctx, wd).FindElements(scraper.LookupRequest{Selector: selector})
	return result.Elements, err
}

// TransformTextToHTML is a temporary compatibility wrapper around scraper.ParseHTML.
func TransformTextToHTML(text string) (*html.Node, error) { return scraper.ParseHTML(text) }

// ExtractHTMLData is a temporary compatibility wrapper around scraper.ExtractHTMLData.
func ExtractHTMLData(node *html.Node) HTMLNode { return scraper.ExtractHTMLData(node) }
