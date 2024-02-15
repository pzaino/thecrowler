// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package scrapper implements the scrapper library for the Crowler and
// the Crowler search engine.
package scraper

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

// ApplyRules applies the rules to the provided URL and HTML content.
// It returns a map containing the extracted data or an error if any occurred during the extraction.
func (re *ScraperRuleEngine) ApplyRules(url string, htmlContent string) (map[string]interface{}, error) {
	siteRules, err := re.FindRulesForSite(url)
	if err != nil {
		return nil, err
	}

	for _, group := range siteRules.RuleGroups {
		if re.IsGroupValid(group) {
			extractedData, err := re.extractData(group, url, htmlContent)
			if err != nil {
				return nil, err
			}
			return extractedData, nil
		}
	}

	return nil, fmt.Errorf("no valid rule groups found for URL: %s", url)
}

// extractJSFiles extracts the JavaScript files from the provided document.
// It returns a slice of strings containing the JavaScript files.
func (re *ScraperRuleEngine) extractJSFiles(doc *goquery.Document) []string {
	var jsFiles []string
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			jsFiles = append(jsFiles, src)
		}
	})
	return jsFiles
}

// extractData extracts the data from the provided HTML content using the provided RuleGroup.
// It returns a map containing the extracted data or an error if any occurred during the extraction.
func (re *ScraperRuleEngine) extractData(group rs.RuleGroup, pageURL string, htmlContent string) (map[string]interface{}, error) {
	// Parse the HTML content
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("error parsing HTML: %s", err)
	}
	node, err := htmlquery.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// handle error
		return nil, err
	}

	// Parse the page URL to extract its path
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing page URL: %s", err)
	}
	path := parsedURL.Path

	// Initialize a map to hold the extracted data
	extractedData := make(map[string]interface{})

	// Iterate over the rules in the group
	for _, rule := range group.ScrapingRules {
		// Apply rule only if the path matches
		if strings.HasSuffix(path, rule.Path) || strings.HasSuffix(path, rule.Path+"/") {
			extractedData = re.applyRule(rule, doc, node, htmlContent, extractedData)
		}
	}

	return extractedData, nil
}

func (re *ScraperRuleEngine) applyRule(rule rs.ScrapingRule, doc *goquery.Document, node *html.Node, htmlContent string, extractedData map[string]interface{}) map[string]interface{} {
	// Iterate over the elements to be extracted
	for _, elementSet := range rule.Elements {
		key := elementSet.Key
		selectors := elementSet.Selectors
		for _, element := range selectors {
			selectorType := element.SelectorType
			selector := element.Selector

			var extracted string
			switch selectorType {
			case "css":
				extracted = re.extractByCSS(doc, selector)
			case "xpath":
				extracted = re.extractByXPath(node, selector)
			case "regex":
				extracted = re.extractByRegex(htmlContent, selector)
			default:
				extracted = ""
			}
			if extracted != "" {
				extractedData[key] = extracted
				break
			}
		}
	}

	// Optional: Extract JavaScript files if required
	if rule.JsFiles {
		jsFiles := re.extractJSFiles(doc)
		extractedData["js_files"] = jsFiles
	}

	return extractedData
}

func (re *ScraperRuleEngine) extractByCSS(doc *goquery.Document, selector string) string {
	return doc.Find(selector).Text()
}

func (re *ScraperRuleEngine) extractByXPath(node *html.Node, selector string) string {
	extractedNode := htmlquery.FindOne(node, selector)
	if extractedNode != nil {
		return htmlquery.InnerText(extractedNode)
	}
	return ""
}

func (re *ScraperRuleEngine) extractByRegex(htmlContent string, selector string) string {
	regex, err := regexp.Compile(selector)
	if err != nil {
		// handle regex compilation error
		return ""
	}
	matches := regex.FindStringSubmatch(htmlContent)
	if len(matches) > 1 { // matches[0] is the full match, matches[1] is the first group
		return matches[1]
	}
	return ""
}
