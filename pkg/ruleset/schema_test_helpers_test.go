package ruleset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/qri-io/jsonschema"
	"gopkg.in/yaml.v2"
)

// rulesetRepositoryRoot locates the repository from this source file rather
// than relying on the test process working directory.
func rulesetRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate ruleset test source")
	}
	for dir := filepath.Dir(source); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "schemas", "crowler-ruleset-schema.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			t.Fatalf("cannot locate schemas/crowler-ruleset-schema.json from %s", source)
		}
	}
}

var (
	rulesetSchemaOnce    sync.Once
	cachedRulesetSchema  *jsonschema.Schema
	cachedSchemaDocument map[string]interface{}
	cachedSchemaError    error
)

func loadRulesetSchemaDocument(t *testing.T) (*jsonschema.Schema, map[string]interface{}) {
	t.Helper()
	rulesetSchemaOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(rulesetRepositoryRoot(t), "schemas", "crowler-ruleset-schema.json"))
		if err != nil {
			cachedSchemaError = err
			return
		}
		cachedRulesetSchema = &jsonschema.Schema{}
		if err := json.Unmarshal(data, cachedRulesetSchema); err != nil {
			cachedSchemaError = fmt.Errorf("decode validator schema: %w", err)
			return
		}
		if err := json.Unmarshal(data, &cachedSchemaDocument); err != nil {
			cachedSchemaError = fmt.Errorf("decode navigable schema: %w", err)
		}
	})
	if cachedSchemaError != nil {
		t.Fatal(cachedSchemaError)
	}
	return cachedRulesetSchema, cachedSchemaDocument
}

func schemaPath(t *testing.T, root interface{}, path ...string) map[string]interface{} {
	t.Helper()
	current := root
	for _, part := range path {
		object, ok := current.(map[string]interface{})
		if !ok {
			t.Fatalf("schema path %s does not traverse an object", strings.Join(path, "."))
		}
		current, ok = object[part]
		if !ok {
			t.Fatalf("schema path %s has no component %q", strings.Join(path, "."), part)
		}
	}
	object, ok := current.(map[string]interface{})
	if !ok {
		t.Fatalf("schema path %s is not an object", strings.Join(path, "."))
	}
	return object
}

func schemaEnum(t *testing.T, root interface{}, path ...string) []string {
	t.Helper()
	node := schemaPath(t, root, path...)
	values, ok := node["enum"].([]interface{})
	if !ok {
		t.Fatalf("schema path %s does not contain an enum", strings.Join(path, "."))
	}
	result := make([]string, len(values))
	for i, value := range values {
		var ok bool
		result[i], ok = value.(string)
		if !ok {
			t.Fatalf("non-string enum at %s[%d]", strings.Join(path, "."), i)
		}
	}
	return result
}

func normalizeYAMLToJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var value interface{}
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode YAML: %v", err)
	}
	value, violations := normalizeYAMLValue(value, "$", nil)
	if len(violations) != 0 {
		t.Fatalf("normalize YAML: %s", strings.Join(violations, "; "))
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode normalized YAML: %v", err)
	}
	return result
}

func minimalRulesetFixture(t *testing.T, ruleKind string, rule map[string]interface{}) []byte {
	t.Helper()
	group := map[string]interface{}{"group_name": "canonical", "is_enabled": true, "url": "https://example.test/*", ruleKind: []interface{}{rule}}
	document := map[string]interface{}{"format_version": "1.0.5", "author": "ruleset tests", "created_at": "2026-01-01T00:00:00Z", "description": "minimal complete ruleset", "ruleset_name": "canonical", "rule_groups": []interface{}{group}}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validateFixture(t *testing.T, data []byte, format string) {
	t.Helper()
	schema, _ := loadRulesetSchemaDocument(t)
	if err := ValidateRulesetConfig(schema, data, format); err != nil {
		t.Fatalf("validate %s fixture: %v", format, err)
	}
}

func mustFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rulesetRepositoryRoot(t), "pkg", "ruleset", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func requireInvalidFixture(t *testing.T, name string) {
	t.Helper()
	data := mustFixture(t, name)
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	schema, _ := loadRulesetSchemaDocument(t)
	if err := ValidateRulesetConfig(schema, data, ext); err == nil {
		t.Fatalf("invalid fixture %s was accepted", name)
	} else {
		t.Logf("%s rejected: %v", name, err)
	}
}

func fixtureFormat(name string) (string, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json":
		return "json", nil
	case ".yaml", ".yml":
		return "yaml", nil
	default:
		return "", fmt.Errorf("unsupported fixture extension")
	}
}
