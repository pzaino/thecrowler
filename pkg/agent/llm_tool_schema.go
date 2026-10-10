// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/qri-io/jsonschema"
)

// jsonSchemaCompileMu serializes JSON Schema compilation: the underlying
// implementation keeps an unsynchronized global keyword registry, so
// concurrent compiles race. Validation of already-compiled schemas stays
// lock-free; compiles are tiny and infrequent.
var jsonSchemaCompileMu sync.Mutex

// compileJSONSchema unmarshals one schema document for dialect checks.
func compileJSONSchema(doc []byte) (jsonschema.Schema, error) {
	var compiled jsonschema.Schema
	jsonSchemaCompileMu.Lock()
	defer jsonSchemaCompileMu.Unlock()
	if err := json.Unmarshal(doc, &compiled); err != nil {
		return compiled, err
	}
	return compiled, nil
}

// Tool parameter schemas follow JSON Schema Draft-07 (the dialect used by
// schemas/crowler-agent-schema.json). `$defs` is tolerated as an alias for
// `definitions`. Validation is purely structural and local: `$ref` targets
// must resolve inside the same parameters document, and external references
// are rejected without any network access. Schemas are declarative data;
// nothing here executes calls or pre-validates model-generated arguments
// (runtime argument checking belongs to a later phase).

// validSchemaTypes enumerates Draft-07 primitive type names.
var validSchemaTypes = map[string]bool{
	"string": true, "number": true, "integer": true, "boolean": true,
	"object": true, "array": true, "null": true,
}

// checkParameterSchema validates one function parameters schema document.
// The root must already be an object map; toolIndex scopes error paths.
func checkParameterSchema(params map[string]any, toolIndex int) error {
	// Dialect well-formedness via the project's JSON Schema implementation:
	// malformed keyword shapes (properties/required/items/type spellings)
	// fail compilation here.
	encoded, err := json.Marshal(params)
	if err != nil {
		return schemaError(toolIndex, "", "cannot serialize parameters")
	}
	if _, err := compileJSONSchema(encoded); err != nil {
		return schemaError(toolIndex, "", "malformed schema (%s)", shortSchemaError(err))
	}
	return checkSchemaNode(params, params, toolIndex, "")
}

// schemaError builds an index- and path-scoped validation error. Messages
// name tools, paths, and keywords only; prompts and secrets never appear.
func schemaError(toolIndex int, path, format string, args ...any) error {
	where := fmt.Sprintf("tools[%d].function.parameters", toolIndex)
	if path != "" {
		where += "." + path
	}
	return fmt.Errorf("invalid %s: %s", where, fmt.Sprintf(format, args...))
}

// shortSchemaError keeps compiler diagnostics to one line.
func shortSchemaError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if idx := strings.Index(msg, "\n"); idx >= 0 {
		msg = msg[:idx]
	}
	const maxLen = 160
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "..."
	}
	return msg
}

// asSchemaMap normalizes one schema node to a string-keyed map.
func asSchemaMap(node any) (map[string]any, bool) {
	switch t := node.(type) {
	case map[string]any:
		return t, true
	case map[interface{}]interface{}:
		converted, ok := normalizeStringMap(t)
		return converted, ok
	default:
		return nil, false
	}
}

// checkSchemaNode validates one schema object. root is the parameters
// document for local $ref resolution; path scopes error locations.
func checkSchemaNode(node any, root map[string]any, toolIndex int, path string) error {
	schema, ok := asSchemaMap(node)
	if !ok {
		return schemaError(toolIndex, path, "expected schema object")
	}

	if err := checkSchemaType(schema, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaRequired(schema, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaProperties(schema, root, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaItems(schema, root, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaCombinators(schema, root, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaDefinitions(schema, root, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaDependencies(schema, root, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaAdditional(schema, root, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaScalars(schema, toolIndex, path); err != nil {
		return err
	}
	if err := checkSchemaRef(schema, root, toolIndex, path); err != nil {
		return err
	}
	return nil
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func checkSchemaType(schema map[string]any, toolIndex int, path string) error {
	raw, present := schema["type"]
	if !present || raw == nil {
		return nil
	}
	if name, ok := raw.(string); ok {
		if !validSchemaTypes[name] {
			return schemaError(toolIndex, joinPath(path, "type"), "unknown type %q", name)
		}
		return nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return schemaError(toolIndex, joinPath(path, "type"), "expected type name or non-empty array")
	}
	for _, entry := range list {
		name, ok := entry.(string)
		if !ok || !validSchemaTypes[name] {
			return schemaError(toolIndex, joinPath(path, "type"), "unknown type %q", fmt.Sprintf("%v", entry))
		}
	}
	return nil
}

func checkSchemaRequired(schema map[string]any, toolIndex int, path string) error {
	raw, present := schema["required"]
	if !present || raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return schemaError(toolIndex, joinPath(path, "required"), "expected array of strings")
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(list))
	for _, entry := range list {
		name, ok := entry.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return schemaError(toolIndex, joinPath(path, "required"), "expected array of nonempty strings")
		}
		if seen[name] {
			return schemaError(toolIndex, joinPath(path, "required"), "duplicate entry %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	// A nonempty required list is only meaningful against declared
	// properties; tool schemas must stay self-describing.
	propsRaw, hasProps := schema["properties"]
	props, propsOK := asSchemaMap(propsRaw)
	if !hasProps || !propsOK {
		return schemaError(toolIndex, joinPath(path, "required"), "entries %q have no declared properties", strings.Join(names, ", "))
	}
	for _, name := range names {
		if _, declared := props[name]; !declared {
			return schemaError(toolIndex, joinPath(path, "required"), "unknown property %q", name)
		}
	}
	return nil
}

func checkSchemaProperties(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	raw, present := schema["properties"]
	if !present || raw == nil {
		return nil
	}
	props, ok := asSchemaMap(raw)
	if !ok {
		return schemaError(toolIndex, joinPath(path, "properties"), "expected object")
	}
	for name, subschema := range props {
		if strings.TrimSpace(name) == "" {
			return schemaError(toolIndex, joinPath(path, "properties"), "empty property name")
		}
		if err := checkSchemaNode(subschema, root, toolIndex, joinPath(path, "properties."+name)); err != nil {
			return err
		}
	}
	return nil
}

func checkSchemaItems(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	raw, present := schema["items"]
	if !present || raw == nil {
		return nil
	}
	if list, ok := raw.([]any); ok {
		for i, subschema := range list {
			if err := checkSchemaNode(subschema, root, toolIndex, joinPath(path, "items["+strconv.Itoa(i)+"]")); err != nil {
				return err
			}
		}
		return nil
	}
	return checkSchemaNode(raw, root, toolIndex, joinPath(path, "items"))
}

func checkSchemaCombinators(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		raw, present := schema[keyword]
		if !present || raw == nil {
			continue
		}
		list, ok := raw.([]any)
		if !ok || len(list) == 0 {
			return schemaError(toolIndex, joinPath(path, keyword), "expected non-empty array of schemas")
		}
		for i, subschema := range list {
			if err := checkSchemaNode(subschema, root, toolIndex, joinPath(path, keyword+"["+strconv.Itoa(i)+"]")); err != nil {
				return err
			}
		}
	}
	if raw, present := schema["not"]; present && raw != nil {
		if err := checkSchemaNode(raw, root, toolIndex, joinPath(path, "not")); err != nil {
			return err
		}
	}
	return nil
}

func checkSchemaDefinitions(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	for _, keyword := range []string{"definitions", "$defs"} {
		raw, present := schema[keyword]
		if !present || raw == nil {
			continue
		}
		defs, ok := asSchemaMap(raw)
		if !ok {
			return schemaError(toolIndex, joinPath(path, keyword), "expected object")
		}
		for name, subschema := range defs {
			if err := checkSchemaNode(subschema, root, toolIndex, joinPath(path, keyword+"."+name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkSchemaDependencies(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	raw, present := schema["dependencies"]
	if !present || raw == nil {
		return nil
	}
	deps, ok := asSchemaMap(raw)
	if !ok {
		return schemaError(toolIndex, joinPath(path, "dependencies"), "expected object")
	}
	for name, rule := range deps {
		switch v := rule.(type) {
		case []any:
			for _, entry := range v {
				if s, ok := entry.(string); !ok || strings.TrimSpace(s) == "" {
					return schemaError(toolIndex, joinPath(path, "dependencies."+name), "expected array of nonempty strings or a schema")
				}
			}
		default:
			if err := checkSchemaNode(rule, root, toolIndex, joinPath(path, "dependencies."+name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkSchemaAdditional(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	raw, present := schema["additionalProperties"]
	if !present || raw == nil {
		return nil
	}
	if _, ok := raw.(bool); ok {
		return nil
	}
	return checkSchemaNode(raw, root, toolIndex, joinPath(path, "additionalProperties"))
}

func checkSchemaScalars(schema map[string]any, toolIndex int, path string) error {
	if raw, present := schema["enum"]; present && raw != nil {
		if _, ok := raw.([]any); !ok {
			return schemaError(toolIndex, joinPath(path, "enum"), "expected array")
		}
	}
	for _, keyword := range []string{"format", "pattern"} {
		if raw, present := schema[keyword]; present && raw != nil {
			if _, ok := raw.(string); !ok {
				return schemaError(toolIndex, joinPath(path, keyword), "expected text")
			}
		}
	}
	for _, keyword := range []string{
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
		"multipleOf", "minLength", "maxLength", "minItems", "maxItems",
		"minProperties", "maxProperties",
	} {
		if raw, present := schema[keyword]; present && raw != nil {
			if !isSchemaNumber(raw) {
				return schemaError(toolIndex, joinPath(path, keyword), "expected number")
			}
		}
	}
	if raw, present := schema["uniqueItems"]; present && raw != nil {
		if _, ok := raw.(bool); !ok {
			return schemaError(toolIndex, joinPath(path, "uniqueItems"), "expected boolean")
		}
	}
	return nil
}

func isSchemaNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	default:
		return false
	}
}

func checkSchemaRef(schema map[string]any, root map[string]any, toolIndex int, path string) error {
	raw, present := schema["$ref"]
	if !present || raw == nil {
		return nil
	}
	target, ok := raw.(string)
	if !ok || strings.TrimSpace(target) == "" {
		return schemaError(toolIndex, joinPath(path, "$ref"), "expected local reference")
	}
	target = strings.TrimSpace(target)
	if !strings.HasPrefix(target, "#") {
		return schemaError(toolIndex, joinPath(path, "$ref"), "external references are not supported")
	}
	if target == "#" || target == "#/" {
		return nil
	}
	if !strings.HasPrefix(target, "#/") {
		return schemaError(toolIndex, joinPath(path, "$ref"), "malformed local reference")
	}
	if _, found := resolveLocalRef(root, strings.TrimPrefix(target, "#/")); !found {
		return schemaError(toolIndex, joinPath(path, "$ref"), "dangling reference %q", target)
	}
	return nil
}

// resolveLocalRef walks a JSON-pointer path inside the parameters document.
func resolveLocalRef(root map[string]any, pointer string) (any, bool) {
	var current any = root
	for _, segment := range strings.Split(pointer, "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[segment]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			if converted, ok := normalizeStringMap(current); ok {
				next, ok := converted[segment]
				if !ok {
					return nil, false
				}
				current = next
				continue
			}
			return nil, false
		}
	}
	return current, true
}
