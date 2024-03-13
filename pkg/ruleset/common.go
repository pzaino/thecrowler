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

// Package ruleset implements the ruleset library for the Crowler and
// the scrapper.
package ruleset

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

const (
	errRulesetNotFound   = "ruleset not found"
	errRuleGroupNotFound = "rule group not found"
	errEmptyPath         = "empty path provided"
	errEmptyURL          = "empty URL provided"
	errParsingURL        = "error parsing URL: %s"
	errEmptyName         = "empty name provided"
	errActionNotFound    = "action rule not found"
	errScrapingNotFound  = "scraping rule not found"
)

// UnmarshalYAML parses date strings from the YAML file.
func (ct *CustomTime) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var dateStr string
	if err := unmarshal(&dateStr); err != nil {
		return err
	}

	// Parse date string in RFC3339 format, if it fails, try with the "2006-01-02" format
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		t, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return err
		}
	}

	ct.Time = t
	return nil
}

// IsEmpty checks if the CustomTime is empty.
func (ct *CustomTime) IsEmpty() bool {
	return ct.Time.IsZero()
}

// ParseRules parses a YAML file containing site rules and returns a slice of SiteRules.
// It takes a file path as input and returns the parsed site rules or an error if the file cannot be read or parsed.
func ParseRules(schema *jsonschema.Schema, path string) ([]Ruleset, error) {
	files, err := filepath.Glob(path)
	if err != nil {
		fmt.Println("Error finding JSON files:", err)
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	var rulesets []Ruleset
	for _, file := range files {
		ruleset, err := parseRuleset(schema, file)
		if err != nil {
			return nil, err
		}

		rulesets = append(rulesets, ruleset...)
	}

	return rulesets, nil
}

func parseRuleset(schema *jsonschema.Schema, file string) ([]Ruleset, error) {
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// Parse the YAML file using the schema
	if schema != nil {
		err := validateRuleset(schema, yamlFile)
		if err != nil {
			return nil, err
		}
	}

	var ruleset []Ruleset
	err = yaml.Unmarshal(yamlFile, &ruleset)
	if err != nil {
		return nil, err
	}

	return ruleset, nil
}

func validateRuleset(schema *jsonschema.Schema, yamlFile []byte) error {
	// create a validation context
	ctx := context.Background()

	// Validate the ruleset against the schema
	errors, err := (*schema).ValidateBytes(ctx, yamlFile)
	if err != nil {
		if len(errors) > 0 {
			return fmt.Errorf("validation failed: %v", errors)
		}
		return err
	}

	return nil
}

// ParseRules is an interface for parsing rules from a file.
func (p *DefaultRuleParser) ParseRules(file string) ([]Ruleset, error) {
	return ParseRules(nil, file)
}

// InitializeLibrary initializes the library by parsing the rules from the specified file
// and creating a new rule engine with the parsed sites.
// It returns a pointer to the created RuleEngine and an error if any occurred during parsing.
func InitializeLibrary(rulesFile string) (*RuleEngine, error) {
	rules, err := ParseRules(nil, rulesFile)
	if err != nil {
		return nil, err
	}

	engine := NewRuleEngine("./schemas/ruleset-schema.json", rules)
	return engine, nil
}

// LoadRulesFromFile loads the rules from the specified file and returns a pointer to the created RuleEngine.
func LoadRulesFromFile(files []string) (*RuleEngine, error) {
	var rules []Ruleset
	for _, file := range files {
		r, err := ParseRules(nil, file)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r...)
	}
	return NewRuleEngine("", rules), nil
}

// loadRulesFromConfig loads the rules from the configuration file and returns a pointer to the created RuleEngine.
func loadRulesFromConfig(config cfg.Ruleset) (*[]Ruleset, error) {
	if config.Path == nil {
		return nil, fmt.Errorf(errEmptyPath)
	}
	if config.Host == "" {
		// Rules are stored locally
		var ruleset []Ruleset
		for _, path := range config.Path {
			schema, err := LoadSchema(config.SchemaPath)
			if err != nil {
				return &ruleset, fmt.Errorf("failed to load schema: %v", err)
			}
			rules, err := ParseRules(schema, path)
			if err != nil {
				return nil, err
			}
			ruleset = append(ruleset, rules...)
		}
		return &ruleset, nil
	}
	// Rules are stored remotely
	ruleset, err := loadRulesFromRemote(config)
	if err != nil {
		return nil, err
	}
	return ruleset, nil
}

func loadRulesFromRemote(config cfg.Ruleset) (*[]Ruleset, error) {
	var ruleset []Ruleset

	// Construct the URL to download the rules from
	for _, path := range config.Path {
		url := fmt.Sprintf("http://%s/%s", config.Host, path)
		httpClient := &http.Client{
			Transport: cmn.SafeTransport(config.Timeout, config.SSLMode),
		}
		resp, err := httpClient.Get(url)
		if err != nil {
			return &ruleset, fmt.Errorf("failed to fetch rules from %s: %v", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return &ruleset, fmt.Errorf("received non-200 response from %s: %d", url, resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return &ruleset, fmt.Errorf("failed to read response body: %v", err)
		}

		schema, err := LoadSchema(config.SchemaPath)
		if err != nil {
			return &ruleset, fmt.Errorf("failed to load schema: %v", err)
		}

		// Assuming your ParseRules function can parse the rules from the response body
		rules, err := ParseRules(schema, string(body))
		if err != nil {
			return &ruleset, fmt.Errorf("failed to parse new rules chunk: %v", err)
		}
		resp.Body.Close()
		ruleset = append(ruleset, rules...)
	}

	return &ruleset, nil
}

func findScrapingRuleByPath(parsedPath string, rules []ScrapingRule) (*ScrapingRule, error) {
	for _, r := range rules {
		for _, p := range r.PreConditions {
			if strings.ToLower(strings.TrimSpace(p.Path)) == parsedPath {
				return &r, nil
			}
		}
	}
	return nil, fmt.Errorf(errScrapingNotFound)
}

// LoadSchema loads the JSON Schema from the specified file and returns a pointer to the created Schema.
func LoadSchema(schemaPath string) (*jsonschema.Schema, error) {
	if strings.TrimSpace(schemaPath) == "" {
		return nil, fmt.Errorf("empty schema path")
	}

	// Load JSON Schema
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to read ruleset's schema file: %v", err)
		return nil, err
	}

	schema := &jsonschema.Schema{}
	if err := schema.UnmarshalJSON(schemaData); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to unmarshal ruleset's schema: %v", err)
		return nil, err
	}

	return schema, nil
}
