package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// ExecuteRule runs the complete scraping-rule lifecycle and returns the legacy
// JSON object fragment consumed by crawler document assembly.
func ExecuteRule(ctx context.Context, runtime *Runtime, rule *rs.ScrapingRule, webPage *vdi.WebDriver) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if rule == nil {
		return "", fmt.Errorf("scraping rule is required")
	}
	rt := runtimeOrZero(runtime)
	if rt.BeforeRule != nil {
		if err := rt.BeforeRule(ctx, rule); err != nil {
			return "", err
		}
	}
	for _, condition := range rule.WaitConditions {
		if rt.WaitCondition == nil {
			return "", fmt.Errorf("executing wait conditions: wait condition runtime capability is unavailable")
		}
		if err := rt.WaitCondition(ctx, condition); err != nil {
			return "", fmt.Errorf("executing wait conditions: executing wait condition: %v", err)
		}
	}

	shouldExecute := true
	if len(rule.Conditions) != 0 {
		if rt.MatchConditions == nil {
			shouldExecute = false
		} else {
			matches, err := rt.MatchConditions(ctx, rule.Conditions)
			shouldExecute = err == nil && matches
		}
	}
	if !shouldExecute {
		return "", nil
	}
	if rt.BeforeApply != nil {
		if err := rt.BeforeApply(ctx, rule); err != nil {
			return "", err
		}
	}

	var errs []error
	extracted, err := ApplyRule(ctx, &rt, rule, webPage)
	if err != nil {
		errs = append(errs, err)
	}
	if err == nil && rt.AugmentResult != nil {
		extracted = rt.AugmentResult(ctx, rule, extracted)
	}
	processed := processExtractedData(extracted)
	cleaned := cleanJSONDocument(processed)
	jsonData, err := json.Marshal(cleaned)
	if err != nil {
		errs = append(errs, fmt.Errorf("marshalling JSON: '%v', for JSON: %v", err, cleaned))
	}
	for i := range rule.PostProcessing {
		updated, stepErr := ApplyPostProcessingStep(ctx, &rt, "", 0, &rule.PostProcessing[i], jsonData)
		if stepErr == nil {
			jsonData = updated
		}
	}

	fragment := strings.TrimSpace(string(jsonData))
	if strings.HasPrefix(fragment, "{") && strings.HasSuffix(fragment, "}") {
		fragment = fragment[1 : len(fragment)-1]
	}
	if len(errs) == 0 {
		return fragment, nil
	}
	var message strings.Builder
	for _, execErr := range errs {
		message.WriteString(execErr.Error())
		message.WriteByte('\n')
	}
	return fragment, fmt.Errorf("executing scraping rule: %v", message.String())
}

func cleanJSONDocument(doc map[string]interface{}) map[string]interface{} {
	cleaned := make(map[string]interface{})
	for key, value := range doc {
		switch value := value.(type) {
		case map[string]interface{}:
			cleaned[key] = cleanJSONDocument(value)
		case []interface{}:
			var valid []interface{}
			for _, item := range value {
				switch item := item.(type) {
				case map[string]interface{}:
					valid = append(valid, cleanJSONDocument(item))
				case string, float64, bool, nil:
					valid = append(valid, item)
				}
			}
			cleaned[key] = valid
		case string, float64, bool, nil:
			cleaned[key] = value
		}
	}
	return cleaned
}

func processExtractedData(extracted map[string]interface{}) map[string]interface{} {
	processed := make(map[string]interface{})
	for key, data := range extracted {
		if key == "false" || key == "true" {
			continue
		}
		if key == "" {
			return extracted
		}
		switch value := data.(type) {
		case string:
			if json.Valid([]byte(value)) {
				var decoded interface{}
				if json.Unmarshal([]byte(value), &decoded) == nil {
					processed[key] = decoded
					continue
				}
			}
			entity := value
			if stringIsHTML(value) {
				if converted, err := processHTMLToJSON(value); err == nil {
					entity = converted
				}
			}
			processed[key] = entity
		case map[string]interface{}:
			processed[key] = processExtractedData(value)
		case []interface{}:
			items := make([]interface{}, 0, len(value))
			for _, item := range value {
				if nested, ok := item.(map[string]interface{}); ok {
					items = append(items, processExtractedData(nested))
				} else {
					items = append(items, item)
				}
			}
			processed[key] = items
		default:
			processed[key] = value
		}
	}
	return processed
}

func stringIsHTML(value string) bool {
	if !strings.Contains(value, "<") && !strings.Contains(value, ">") {
		return false
	}
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return false
	}
	var found bool
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data != "html" && node.Data != "head" && node.Data != "body" {
			found = true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return found
}

func processHTMLToJSON(value string) (string, error) {
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return "", err
	}
	var items []map[string]interface{}
	parseLegacyNode(doc, nil, &items)
	data, err := json.MarshalIndent(items, "", "    ")
	return string(data), err
}

func parseLegacyNode(node *html.Node, current map[string]interface{}, items *[]map[string]interface{}) {
	if node.Type == html.ElementNode {
		next := make(map[string]interface{})
		for _, attr := range node.Attr {
			next[attr.Key] = attr.Val
		}
		if node.FirstChild != nil && node.FirstChild.Type == html.TextNode {
			next["text"] = strings.TrimSpace(node.FirstChild.Data)
		}
		if len(next) > 0 {
			if current == nil {
				*items = append(*items, next)
			} else {
				if _, exists := current["children"]; !exists {
					current["children"] = []map[string]interface{}{}
				}
				current["children"] = append(current["children"].([]map[string]interface{}), next)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			parseLegacyNode(child, next, items)
		}
	} else if node.Type == html.TextNode && strings.TrimSpace(node.Data) != "" && current != nil && len(current) == 0 {
		current["text"] = strings.TrimSpace(node.Data)
	}
}
