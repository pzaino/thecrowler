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

package scrapper

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"gopkg.in/yaml.v2"
)

func ParseRules(file string) ([]SiteRules, error) {
	var sites []SiteRules

	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(yamlFile, &sites)
	if err != nil {
		return nil, err
	}

	return sites, nil
}

func InitializeLibrary(rulesFile string) (*RuleEngine, error) {
	sites, err := ParseRules(rulesFile)
	if err != nil {
		return nil, err
	}

	engine := NewRuleEngine(sites)
	return engine, nil
}

func NewRuleEngine(sites []SiteRules) *RuleEngine {
	// Implementation of the RuleEngine initialization
	return &RuleEngine{
		SiteRules: sites,
	}
}

func (s *SiteRules) GetEnabledRuleGroups() []RuleGroup {
	var enabledRuleGroups []RuleGroup

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			enabledRuleGroups = append(enabledRuleGroups, rg)
		}
	}

	return enabledRuleGroups
}

func (s *SiteRules) GetEnabledRules() []Rule {
	var enabledRules []Rule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			enabledRules = append(enabledRules, rg.Rules...)
		}
	}

	return enabledRules
}

func (s *SiteRules) GetEnabledRulesByGroup(groupName string) []Rule {
	var enabledRules []Rule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled && rg.GroupName == groupName {
			enabledRules = append(enabledRules, rg.Rules...)
		}
	}

	return enabledRules
}

func (s *SiteRules) GetEnabledRulesByPath(path string) []Rule {
	var enabledRules []Rule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled {
			for _, r := range rg.Rules {
				if r.Path == path {
					enabledRules = append(enabledRules, r)
				}
			}
		}
	}

	return enabledRules
}

func (s *SiteRules) GetEnabledRulesByPathAndGroup(path, groupName string) []Rule {
	var enabledRules []Rule

	for _, rg := range s.RuleGroups {
		if rg.IsEnabled && rg.GroupName == groupName {
			for _, r := range rg.Rules {
				if r.Path == path {
					enabledRules = append(enabledRules, r)
				}
			}
		}
	}

	return enabledRules
}

func (re *RuleEngine) ApplyRules(url string, htmlContent string) (map[string]interface{}, error) {
	siteRules, err := re.findRulesForSite(url)
	if err != nil {
		return nil, err
	}

	for _, group := range siteRules.RuleGroups {
		if re.isGroupValid(group) {
			extractedData, err := re.extractData(group, url, htmlContent)
			if err != nil {
				return nil, err
			}
			return extractedData, nil
		}
	}

	return nil, fmt.Errorf("no valid rule groups found for URL: %s", url)
}

func (re *RuleEngine) isGroupValid(group RuleGroup) bool {
	// Check if the group is enabled
	if !group.IsEnabled {
		return false
	}

	// Check if the rules group has a valid_from and valid_to date
	if group.ValidFrom == "" && group.ValidTo == "" {
		log.Printf("No valid_from and valid_to dates found for group: %s", group.GroupName)
		return true
	}

	var validFrom, validTo time.Time
	var err error

	// Parse the 'valid_from' date if present
	if group.ValidFrom != "" {
		validFrom, err = time.Parse("2006-01-02", group.ValidFrom)
		if err != nil {
			return false
		}
	}

	// Parse the 'valid_to' date if present
	if group.ValidTo != "" {
		validTo, err = time.Parse("2006-01-02", group.ValidTo)
		if err != nil {
			return false
		}
	}

	// Get the current time
	now := time.Now()

	log.Printf("Validating group: %s", group.GroupName)
	log.Printf("Valid from: %s", validFrom)
	log.Printf("Valid to: %s", validTo)
	log.Printf("Current time: %s", now)

	// Check the range only if both dates are provided
	if group.ValidFrom != "" && group.ValidTo != "" {
		return now.After(validFrom) && now.Before(validTo)
	}

	// If only valid_from is provided
	if group.ValidFrom != "" {
		return now.After(validFrom)
	}

	// If only valid_to is provided
	if group.ValidTo != "" {
		return now.Before(validTo)
	}

	return false
}

func (re *RuleEngine) findRulesForSite(inputURL string) (*SiteRules, error) {
	if inputURL == "" {
		return nil, fmt.Errorf("empty URL provided")
	}

	// Parse the input URL to extract the domain
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL: %s", err)
	}
	inputDomain := parsedURL.Hostname()

	// Iterate over the SiteRules to find a matching domain
	for _, siteRule := range re.SiteRules {
		if strings.Contains(siteRule.Site, inputDomain) {
			return &siteRule, nil
		}
	}

	return nil, fmt.Errorf("no rules found for URL: %s", inputURL)
}

func (re *RuleEngine) extractJSFiles(doc *goquery.Document) []string {
	var jsFiles []string
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			jsFiles = append(jsFiles, src)
		}
	})
	return jsFiles
}

func (re *RuleEngine) extractData(group RuleGroup, pageURL string, htmlContent string) (map[string]interface{}, error) {
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
	for _, rule := range group.Rules {
		// Apply rule only if the path matches
		if strings.HasSuffix(path, rule.Path) || strings.HasSuffix(path, rule.Path+"/") {
			// Iterate over the elements to be extracted
			for _, element := range rule.Elements {
				key := element.Key
				selectorType := element.SelectorType
				selector := element.Selector

				var extracted string
				switch selectorType {
				case "css":
					extracted = doc.Find(selector).Text()
				case "xpath":
					//extracted = htmlquery.FindOne(node, selector).Data
					extractedNode := htmlquery.FindOne(node, selector)
					if extractedNode != nil {
						extracted = htmlquery.InnerText(extractedNode)
					} else {
						extracted = ""
					}
				case "regex":
					re, err := regexp.Compile(selector)
					if err != nil {
						// handle regex compilation error
						return nil, fmt.Errorf("error compiling regex: %s", err)
					}
					matches := re.FindStringSubmatch(htmlContent)
					if len(matches) > 1 { // matches[0] is the full match, matches[1] is the first group
						extracted = matches[1]
					}
				default:
					// handle unknown selector type
					extracted = ""
				}
				extractedData[key] = extracted
			}

			// Optional: Extract JavaScript files if required
			// JS file extraction logic
			if rule.JsFiles {
				jsFiles := re.extractJSFiles(doc)
				extractedData["js_files"] = jsFiles
			}
		}
	}

	return extractedData, nil
}
