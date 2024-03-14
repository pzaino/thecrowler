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
	"encoding/json"
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
// It takes a file path as input and returns the parsed site rules or an error if the
// file cannot be read or parsed.
// This function is meant to process both fully qualified path names and path names
// with wild-chars like "*" (hence it's a "bulk" loader)
func BulkLoadRules(schema *jsonschema.Schema, path string) ([]Ruleset, error) {
	files, err := filepath.Glob(path)
	if err != nil {
		fmt.Println("Error finding rule files:", err)
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	var rulesets []Ruleset
	for _, file := range files {
		// Extract file's extension from file string.
		// For example json for example.json or yaml for example.yaml
		fileType := cmn.GetFileExt(file)
		if fileType != "yaml" && fileType != "json" && fileType != "" {
			// Ignore unsupported file types
			continue
		}

		// Load the specified file
		rulesFile, err := os.ReadFile(file)
		if err != nil {
			return rulesets, err
		}

		// Parse it and get the correspondent Ruleset
		ruleset, err := parseRuleset(schema, &rulesFile, fileType)
		if err != nil {
			return rulesets, err
		}

		// It's valid, let's add it to the rulesets list
		rulesets = append(rulesets, ruleset...)
	}

	return rulesets, nil
}

// parseRuleset is responsible for parsing a given ruleset.
// if a parsing schema is provided then it uses that, otherwise it
// parses only the correct YAML/JSON syntax for the given ruleset.
func parseRuleset(schema *jsonschema.Schema, file *[]byte, fileType string) ([]Ruleset, error) {
	var err error

	// Parse the YAML/JSON Ruleset using the provided schema
	if schema != nil {
		err = validateRuleset(schema, file, fileType)
		if err != nil {
			return nil, err
		}
	}

	var ruleset []Ruleset
	if fileType == "yaml" || fileType == "" {
		err = yaml.Unmarshal((*file), &ruleset)
		if err != nil {
			return nil, err
		}
	} else {
		err = json.Unmarshal((*file), &ruleset)
		if err != nil {
			return nil, err
		}
	}

	return ruleset, nil
}

func validateRuleset(schema *jsonschema.Schema, ruleFile *[]byte, fileType string) error {
	var jsonData interface{}
	ruleData := *ruleFile

	// Unmarshal based on fileType.
	if fileType == "yaml" || fileType == "" {
		if err := yaml.Unmarshal(ruleData, &jsonData); err != nil {
			return fmt.Errorf("error unmarshalling YAML: %v", err)
		}
		// Convert map[interface{}]interface{} to map[string]interface{}.
		jsonData = cmn.ConvertInterfaceMapToStringMap(jsonData)
	} else if fileType == "json" {
		if err := json.Unmarshal(ruleData, &jsonData); err != nil {
			return fmt.Errorf("error unmarshalling JSON: %v", err)
		}
	}

	// Convert the unmarshalled data back to JSON to prepare it for validation.
	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		return fmt.Errorf("error marshalling to JSON: %v", err)
	}

	// Create a validation context.
	ctx := context.Background()

	// Validate the ruleset against the schema.
	errors, err := schema.ValidateBytes(ctx, jsonBytes)
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
	return BulkLoadRules(nil, file)
}

// InitializeLibrary initializes the library by parsing the rules from the specified file
// and creating a new rule engine with the parsed sites.
// It returns a pointer to the created RuleEngine and an error if any occurred during parsing.
func InitializeLibrary(rulesFile string) (*RuleEngine, error) {
	rules, err := BulkLoadRules(nil, rulesFile)
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
		r, err := BulkLoadRules(nil, file)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r...)
	}
	return NewRuleEngine("", rules), nil
}

// loadRulesFromConfig loads the rules from the configuration file and returns a pointer to the created RuleEngine.
func loadRulesFromConfig(schema *jsonschema.Schema, config cfg.Ruleset) (*[]Ruleset, error) {
	if config.Path == nil {
		return nil, fmt.Errorf(errEmptyPath)
	}
	if config.Host == "" {
		// Rules are stored locally
		ruleset := loadRulesFromLocal(schema, config)
		return ruleset, nil
	}
	// Rules are stored remotely
	ruleset, err := loadRulesFromRemote(schema, config)
	if err != nil {
		return nil, err
	}
	return ruleset, nil
}

func loadRulesFromLocal(schema *jsonschema.Schema, config cfg.Ruleset) *[]Ruleset {
	var ruleset []Ruleset

	// Construct the URL to download the rules from
	for _, path := range config.Path {
		rules, err := BulkLoadRules(schema, path)
		if err == nil {
			ruleset = append(ruleset, rules...)
		}
	}

	return &ruleset
}

// loadRulesFromRemote loads rules from a distribution server either on the local net or the
// internet.
// The request format is a get request (it supports both http and https protocols)
// and the path is the ruleset file name, for example:
// http://example.com/accept-cookies-ruleset.yaml
func loadRulesFromRemote(schema *jsonschema.Schema, config cfg.Ruleset) (*[]Ruleset, error) {
	var ruleset []Ruleset

	// Construct the URL to download the rules from
	for _, path := range config.Path {

		fileType := strings.ToLower(strings.TrimSpace(filepath.Ext(path)))
		if fileType != "yaml" && fileType != "json" {
			// Ignore unsupported file types
			continue
		}

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

		// Process ENV variables
		interpolatedData := cmn.InterpolateEnvVars(string(body))

		// I am assuming that the response body is actually a ruleset
		// this may need reviewing later on.
		data := []byte(interpolatedData)
		rules, err := parseRuleset(schema, &data, fileType)
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
