//go:build go1.22

package ruleset

import (
	"os"
	"testing"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

// FuzzParseRuleset is a fuzz test for the parseRuleset function
func FuzzParseRuleset(f *testing.F) {
	// Add initial seed inputs
	f.Add([]byte(`format_version: "1.0"
author: "test"
created_at: "2023-01-01T00:00:00Z"
description: "Test ruleset"
ruleset_name: "TestRuleset"
rule_groups: []
`))

	// Adding interesting cases found by the fuzzer
	f.Add([]byte(`0`)) // Example of an interesting case that caused an error in the past

	f.Fuzz(func(t *testing.T, data []byte) {
		// Create a temporary file to hold the fuzzed ruleset
		tmpFile, err := os.CreateTemp("", "ruleset-*.yaml")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.Write(data); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		if err := tmpFile.Close(); err != nil {
			t.Fatalf("failed to close temp file: %v", err)
		}

		// Load the schema file
		schemaFile := "../../schemas/ruleset-schema.json"
		schemaData, err := os.ReadFile(schemaFile)
		if err != nil {
			t.Fatalf("failed to read schema file: %v", err)
		}

		// Unmarshal the schema file
		rs := &jsonschema.Schema{}
		if err := rs.UnmarshalJSON(schemaData); err != nil {
			t.Fatalf("failed to unmarshal schema: %v", err)
		}

		// Read the ruleset file
		rulesFile, err := os.ReadFile(tmpFile.Name())
		if err != nil {
			t.Fatalf("failed to read temp file: %v", err)
		}

		// Unmarshal the YAML content to check its validity
		var yamlDoc map[string]interface{}
		if err := yaml.Unmarshal(rulesFile, &yamlDoc); err != nil {
			t.Skipf("failed to unmarshal rules file: %v", err)
		}

		// Call parseRuleset with the fuzzed data
		_, err = parseRuleset(rs, &rulesFile, "yaml")
		if err != nil {
			t.Skip()
		}
	})
}
