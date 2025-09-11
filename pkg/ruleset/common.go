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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

const (
	errNotFound          = "not found"
	errRulesetNotFound   = "ruleset " + errNotFound
	errRuleGroupNotFound = "rule group " + errNotFound
	errEmptyPath         = "empty path provided"
	errEmptyURL          = "empty URL provided"
	errParsingURL        = "error parsing URL: %s"
	errEmptyName         = "empty name provided"
	errActionNotFound    = "action rule " + errNotFound
	errScrapingNotFound  = "scraping rule " + errNotFound
	errCrawlingNotFound  = "crawling rule " + errNotFound
	errDetectionNotFound = "detection rule " + errNotFound
	errInvalidURL        = "invalid URL"
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

// BulkLoadRules parses a YAML file containing site rules and returns a slice of SiteRules.
// It takes a file path as input and returns the parsed site rules or an error if the
// file cannot be read or parsed.
// This function is meant to process both fully qualified path names and path names
// with wild-chars like "*" (hence it's a "bulk" loader)
func BulkLoadRules(schema *jsonschema.Schema, path string) ([]Ruleset, error) {
	files, err := filepath.Glob(path)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "finding ruleset files: %v", err)
		return nil, nil
	}
	if len(files) == 0 {
		cmn.DebugMsg(cmn.DbgLvlError, "finding ruleset files: %v", err)
		return nil, nil
	}

	var rulesets []Ruleset
	for _, file := range files {
		// Extract file's extension from file string.
		// For example json for example.json or yaml for example.yaml
		fileType := cmn.GetFileExt(file)
		if (fileType != "yaml") && (fileType != "json") && (fileType != "") && (fileType != "yml") {
			// Ignore unsupported file types
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "Loading rules from file: %s", file)

		// Load the specified file
		rulesFile, err := os.ReadFile(file) //nolint:gosec // This is a read-only operation and we are not using end-user input
		if err != nil {
			return rulesets, err
		}

		// Parse it and get the correspondent Ruleset
		loadedOK := true
		ruleset, err := parseRuleset(schema, &rulesFile, fileType)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "parsing ruleset: %v", err)
			loadedOK = false
		}

		// It's valid, let's add it to the rulesets list
		if loadedOK {
			rulesets = append(rulesets, ruleset)
		}
	}

	return rulesets, nil
}

// parseRuleset is responsible for parsing a given ruleset.
// if a parsing schema is provided then it uses that, otherwise it
// parses only the correct YAML/JSON syntax for the given ruleset.
func parseRuleset(schema *jsonschema.Schema, file *[]byte, fileType string) (Ruleset, error) {
	var err error

	// Parse the YAML/JSON Ruleset using the provided schema
	validated := false
	if schema != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Validating ruleset against schema")
		err = validateRuleset(schema, file, fileType)
		if err != nil {
			return Ruleset{}, err
		}
		fileType = "yaml"
		validated = true
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Ruleset validated")
	}

	var ruleset Ruleset
	fileData := *file
	if (fileType == "yaml") || (fileType == "yml") || fileType == "" {
		err = yaml.Unmarshal(fileData, &ruleset)
		if err != nil && !validated {
			return ruleset, err
		}
	} else {
		err = json.Unmarshal(fileData, &ruleset)
		if err != nil && !validated {
			return ruleset, err
		}
	}

	// Print the ruleset for debugging purposes
	//fmt.Printf("%+v\n", ruleset)

	return ruleset, nil
}

func validateRuleset(schema *jsonschema.Schema, ruleFile *[]byte, fileType string) error {
	var jsonData interface{}
	ruleData := *ruleFile

	// Unmarshal based on fileType.
	if (fileType == "yaml") || (fileType == "yml") || (fileType == "") {
		if err := yaml.Unmarshal(ruleData, &jsonData); err != nil {
			return fmt.Errorf("problems unmarshalling YAML: %v", err)
		}
		// Convert map[interface{}]interface{} to map[string]interface{}.
		jsonData = cmn.ConvertInterfaceMapToStringMap(jsonData)
	} else if fileType == "json" {
		if err := json.Unmarshal(ruleData, &jsonData); err != nil {
			return fmt.Errorf("problems unmarshalling JSON: %v", err)
		}
	}

	// Convert the unmarshalled data back to JSON to prepare it for validation.
	jsonBytes, err := json.MarshalIndent(jsonData, "", "  ")
	if err != nil {
		return fmt.Errorf("problems marshalling to JSON: %v", err)
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

	// Convert jsonBytes back to YAML
	jsonData = map[string]interface{}{}
	if err := json.Unmarshal(jsonBytes, &jsonData); err != nil {
		return fmt.Errorf("error unmarshalling JSON: %v", err)
	}
	jsonBytes, err = yaml.Marshal(jsonData)
	if err != nil {
		return fmt.Errorf("error marshalling to YAML: %v", err)
	}

	// Pretty print the JSON data for debugging purposes.
	//fmt.Println(string(jsonBytes))

	// Update the ruleFile with the marshalled JSON data.
	*ruleFile = jsonBytes

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
	rules, _ := BulkLoadRules(nil, rulesFile)

	engine := NewRuleEngine("./schemas/crowler-ruleset-schema.json", rules)
	return engine, nil
}

// LoadRulesFromFile loads the rules from the specified file and returns a pointer to the created RuleEngine.
func LoadRulesFromFile(files []string) (*RuleEngine, error) {
	var rules []Ruleset
	for _, file := range files {
		r, err := BulkLoadRules(nil, file)
		if err == nil {
			rules = append(rules, r...)
		}
	}
	return NewRuleEngine("", rules), nil
}

// loadRulesFromConfig loads the rules from the configuration file and returns a pointer to the created RuleEngine.
func loadRulesFromConfig(schema *jsonschema.Schema, config cfg.RulesetConfig) (*[]Ruleset, error) {
	if config.Path == nil {
		return nil, fmt.Errorf("%s", errEmptyPath)
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

func loadRulesFromLocal(schema *jsonschema.Schema, config cfg.RulesetConfig) *[]Ruleset {
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
func loadRulesFromRemote(schema *jsonschema.Schema, config cfg.RulesetConfig) (*[]Ruleset, error) {
	var ruleset []Ruleset

	// Construct the URL to download the rules from
	for _, path := range config.Path {

		fileType := strings.ToLower(strings.TrimSpace(filepath.Ext(path)))
		if fileType != "yaml" && fileType != "json" {
			// Ignore unsupported file types
			continue
		}

		// Set the protocol:
		proto := ""
		switch strings.ToLower(strings.TrimSpace(config.Type)) {
		case "http":
			proto = "http"
		case "ftp":
			proto = "ftp"
		default:
			proto = "s3"
		}

		if config.SSLMode == cmn.EnableStr && proto == "http" {
			proto = "https"
		}
		if config.SSLMode == cmn.EnableStr && proto == "ftp" {
			proto = "ftps"
		}

		// Construct the URL
		url := ""
		if config.Port != "" && config.Port != "80" && config.Port != "443" {
			url = fmt.Sprintf("%s://%s:%s/%s", proto, config.Host, config.Port, path)
		} else {
			url = fmt.Sprintf("%s://%s/%s", proto, config.Host, path)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "Downloading ruleset from %s", url)

		// Download the ruleset file
		rulesetBody, err := cmn.FetchRemoteFile(url, config.Timeout, config.SSLMode)
		if err != nil {
			return &ruleset, fmt.Errorf("failed to fetch rules from %s: %v", url, err)
		}

		// Process ENV variables
		interpolatedData := cmn.InterpolateEnvVars(rulesetBody)

		// I am assuming that the response body is actually a ruleset
		// this may need reviewing later on.
		data := []byte(interpolatedData)
		rules, err := parseRuleset(schema, &data, fileType)
		if err != nil {
			return &ruleset, fmt.Errorf("failed to parse new rules chunk: %v", err)
		}
		ruleset = append(ruleset, rules)
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
	return nil, fmt.Errorf("%s", errScrapingNotFound)
}

// LoadSchema loads the JSON Schema from the specified file and returns a pointer to the created Schema.
func LoadSchema(schemaPath string) (*jsonschema.Schema, error) {
	if strings.TrimSpace(schemaPath) == "" {
		return nil, fmt.Errorf("empty schema path")
	}

	// Load JSON Schema
	schemaData, err := os.ReadFile(schemaPath) //nolint:gosec // This is a read-only operation and we are not using end-user input
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

/// --- Prepare Items for Search --- ///

// PrepareURLForSearch prepares the URL for search by trimming spaces and converting it to lowercase.
func PrepareURLForSearch(urlStr string) (string, error) {
	if strings.TrimSpace(urlStr) == "" {
		return "", fmt.Errorf("%s", errEmptyURL)
	}

	_, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("%s", errInvalidURL)
	}

	return strings.TrimSpace(urlStr), nil
}

// PrepareNameForSearch prepares the name for search by trimming spaces and converting it to lowercase.
func PrepareNameForSearch(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%s", errEmptyName)
	}
	return strings.ToLower(strings.TrimSpace(name)), nil
}

// PreparePathForSearch prepares the path for search by trimming spaces and converting it to lowercase.
func PreparePathForSearch(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s", errEmptyPath)
	}
	return strings.ToLower(strings.TrimSpace(path)), nil
}

// IsURL checks if the provided string is a URL or a a pattern to match URLs.
// returns false if it's not a URL or a pattern to match URLs otherwise returns true.
func IsURL(urlStr string) bool {
	urlStr = strings.TrimSpace(urlStr)
	if urlStr == "" {
		return false
	} else if urlStr == "*" {
		return true
	}

	var re *regexp.Regexp
	var once sync.Once

	// Define a function to compile the regex only once
	once.Do(func() {
		// The pattern below is capable of matching regEx patterns used to match URLs
		// I understand this might be mind-blowing for somebody, so trust me, it works.
		urlHeaders := "(?i)[\\^]?[\\s]*(\\{0,2}http[s]?[\\[s\\]]?.*:|\\{0,2}ftp[s]?:|\\{0,2}www\\.|\\.[a-z]{2,})"

		re = regexp.MustCompile(urlHeaders)
	})

	// Check if it urlStr matches the pattern and so it's a URL or a pattern to match URLs
	return re.MatchString(urlStr)
}

// CheckURL checks if the provided URL match.
func CheckURL(urlStr, urlPattern string) bool {
	if !IsURL(urlPattern) {
		return false
	}

	if urlPattern == "*" {
		return true
	}

	// Check if the URL matches the pattern
	matched, err := regexp.MatchString(urlPattern, urlStr)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "error matching URL: %v", err)
		return false
	}

	cmn.DebugMsg(cmn.DbgLvlDebug3, "URL '%s' matched: %t", urlStr, matched)
	return matched
}
