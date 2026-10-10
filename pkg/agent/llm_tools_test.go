package agent

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func validToolEntries() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_forecast",
				"description": "Look up weather",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       "ping",
				"parameters": map[string]any{"type": "object"},
			},
		},
	}
}

// TestToolDefinitionValidation pins the declaration allowlist.
func TestToolDefinitionValidation(t *testing.T) {
	defs, err := validateToolDefinitions(validToolEntries())
	if err != nil {
		t.Fatalf("valid tools rejected: %v", err)
	}
	if len(defs) != 2 || defs[0].Function.Name != "get_forecast" {
		t.Fatalf("unexpected defs: %+v", defs)
	}
	// JSON-safe round trip.
	encoded, err := json.Marshal(defs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded []LLMToolDefinition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded[1].Function.Name != "ping" || decoded[0].Function.Parameters["type"] != "object" {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}

	bad := []struct {
		name  string
		tools []any
		want  string
	}{
		{"non-array entry", []any{"nope"}, "expected mapping"},
		{"bad type", []any{map[string]any{"type": "mcp", "function": map[string]any{}}}, "only \"function\""},
		{"empty name", []any{map[string]any{"type": "function", "function": map[string]any{
			"name": " ", "parameters": map[string]any{"type": "object"}}}}, "empty"},
		{"duplicate names", []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "Dup", "parameters": map[string]any{"type": "object"}}},
			map[string]any{"type": "function", "function": map[string]any{"name": "dup", "parameters": map[string]any{"type": "object"}}},
		}, "duplicate"},
		{"non-object parameters", []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "f", "parameters": []any{}}}}, "expected object"},
		{"missing parameters type", []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "f", "parameters": map[string]any{}}}}, "must be \"object\""},
		{"bad required", []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "f", "parameters": map[string]any{"type": "object", "required": "city"}}}}, "array of strings"},
		{"too many", make([]any, maxLLMToolsPerRequest+1), "at most"},
	}
	for _, tc := range bad {
		if tc.name == "too many" {
			for i := range tc.tools {
				tc.tools[i] = map[string]any{"type": "function", "function": map[string]any{
					"name": strings.Repeat("f", i+1), "parameters": map[string]any{"type": "object"}}}
			}
		}
		if _, err := validateToolDefinitions(tc.tools); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: expected %q, got %v", tc.name, tc.want, err)
		}
	}
}

// TestToolChoiceParsing pins the choice representation.
func TestToolChoiceParsing(t *testing.T) {
	for _, mode := range []string{"auto", "none", "required"} {
		choice, set, err := parseToolChoice(mode)
		if err != nil || !set || choice.Mode != mode {
			t.Fatalf("%s: got %+v, %v, %v", mode, choice, set, err)
		}
		if choice.toolChoiceWire() != mode {
			t.Fatalf("%s: bad wire form", mode)
		}
	}
	choice, set, err := parseToolChoice(map[string]any{
		"type":     "function",
		"function": map[string]any{"name": "get_forecast"},
	})
	if err != nil || !set || choice.Mode != "function" || choice.Name != "get_forecast" {
		t.Fatalf("function selector: %+v, %v, %v", choice, set, err)
	}
	wire, ok := choice.toolChoiceWire().(map[string]any)
	if !ok || wire["type"] != "function" {
		t.Fatalf("bad function wire form: %v", wire)
	}
	for _, bad := range []any{"sometimes", 42, map[string]any{"type": "tool"}} {
		if _, _, err := parseToolChoice(bad); err == nil {
			t.Fatalf("%v: expected rejection", bad)
		}
	}
	if _, set, err := parseToolChoice(nil); set || err != nil {
		t.Fatalf("nil must be unset without error")
	}
}

// TestToolArgumentsNormalization pins argument shapes.
func TestToolArgumentsNormalization(t *testing.T) {
	args, err := normalizeToolArguments(`{"city": "Oslo", "days": 3}`)
	if err != nil || args["city"] != "Oslo" {
		t.Fatalf("string args: %+v, %v", args, err)
	}
	args, err = normalizeToolArguments(map[string]any{"a": true})
	if err != nil || args["a"] != true {
		t.Fatalf("object args: %+v, %v", args, err)
	}
	for _, tt := range []any{nil, "", "  "} {
		args, err = normalizeToolArguments(tt)
		if err != nil || len(args) != 0 {
			t.Fatalf("%v: want empty object, got %+v, %v", tt, args, err)
		}
	}
	for _, tt := range []any{`{oops`, `[1,2]`, `42`, float64(1), []any{}} {
		if _, err := normalizeToolArguments(tt); err == nil {
			t.Fatalf("%v: expected rejection", tt)
		}
	}
}

// TestNormalizedResponseShape pins the typed envelope and its map form.
func TestNormalizedResponseShape(t *testing.T) {
	resp := LLMNormalizedResponse{
		Content:      "hi",
		ToolCalls:    []LLMToolCall{{ID: "c1", Index: 0, Type: "function", Name: "ping", Arguments: map[string]any{}}},
		FinishReason: "tool_calls",
		Model:        "m",
		Raw:          map[string]any{"id": "r"},
	}
	mapped := resp.ToMap()
	calls, ok := mapped["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("bad tool_calls shape: %v", mapped)
	}
	entry, ok := calls[0].(map[string]any)
	if !ok || entry["name"] != "ping" || entry["index"] != 0 {
		t.Fatalf("bad call entry: %v", calls)
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		t.Fatalf("not JSON-safe: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

// TestParameterSchemaValidation pins structural Draft-07 checks.
func TestParameterSchemaValidation(t *testing.T) {
	tool := func(params map[string]any) []any {
		return []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "f", "parameters": params}}}
	}
	valid := []map[string]any{
		{"type": "object"},
		{"type": "object", "properties": map[string]any{
			"city": map[string]any{"type": "string"},
			"geo": map[string]any{"type": "object", "properties": map[string]any{
				"lat": map[string]any{"type": "number"},
			}, "required": []any{"lat"}},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"mode": map[string]any{"oneOf": []any{
				map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
		}, "required": []any{"city"}},
		{"type": "object", "definitions": map[string]any{
			"addr": map[string]any{"type": "object", "properties": map[string]any{
				"zip": map[string]any{"type": "string"}}, "required": []any{"zip"}}},
			"properties": map[string]any{
				"home": map[string]any{"$ref": "#/definitions/addr"}}},
		{"type": "object", "properties": map[string]any{
			"pick": map[string]any{"anyOf": []any{
				map[string]any{"type": "string"}, map[string]any{"type": "integer"}}},
			"all": map[string]any{"allOf": []any{map[string]any{"type": "object"}}},
		}},
		{"type": "object", "properties": map[string]any{
			"level": map[string]any{"type": "string", "enum": []any{"a", "b"}}}},
	}
	for i, params := range valid {
		if _, err := validateToolDefinitions(tool(params)); err != nil {
			t.Fatalf("valid %d rejected: %v", i, err)
		}
	}
	invalid := []struct {
		name   string
		params string
		want   string
	}{
		{"unknown required", `{"type":"object","properties":{"a":{"type":"string"}},"required":["b"]}`, "unknown property"},
		{"required without properties", `{"type":"object","required":["a"]}`, "no declared properties"},
		{"duplicate required", `{"type":"object","properties":{"a":{"type":"string"}},"required":["a","a"]}`, "duplicate entry"},
		{"empty required name", `{"type":"object","properties":{"a":{"type":"string"}},"required":[" "]}`, "nonempty strings"},
		{"nested unknown required", `{"type":"object","properties":{"o":{"type":"object","properties":{"x":{"type":"string"}},"required":["y"]}}}`, "unknown property"},
		{"properties wrong type", `{"type":"object","properties":"nope"}`, "malformed schema"},
		{"required wrong type", `{"type":"object","required":42}`, "required.*expected array"},
		{"required number entry", `{"type":"object","properties":{"a":{"type":"string"}},"required":[42]}`, "array of strings"},
		{"items invalid", `{"type":"object","properties":{"a":{"type":"array","items":42}}}`, "malformed schema"},
		{"bad type name", `{"type":"object","properties":{"a":{"type":"fancy"}}}`, "malformed schema"},
		{"dangling ref", `{"type":"object","properties":{"a":{"$ref":"#/definitions/gone"}}}`, "dangling reference"},
		{"external ref", `{"type":"object","properties":{"a":{"$ref":"https://example.com/s.json"}}}`, "external references"},
		{"bad combinator", `{"type":"object","properties":{"a":{"oneOf":42}}}`, "malformed schema"},
		{"empty combinator", `{"type":"object","properties":{"a":{"oneOf":[]}}}`, "non-empty array"},
	}
	for _, tc := range invalid {
		var params map[string]any
		if err := json.Unmarshal([]byte(tc.params), &params); err != nil {
			t.Fatalf("%s: bad fixture: %v", tc.name, err)
		}
		_, err := validateToolDefinitions(tool(params))
		if err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
			continue
		}
		matched, matchErr := regexp.MatchString(tc.want, err.Error())
		if matchErr != nil || !matched {
			t.Fatalf("%s: expected %q, got %v", tc.name, tc.want, err)
		}
	}
}

// TestInvalidSchemaFailsBeforeTransport proves malformed declarations never
// reach the provider.
func TestInvalidSchemaFailsBeforeTransport(t *testing.T) {
	server, count := countingToolServer(t, `{"choices":[]}`, http.StatusOK)
	a := &AIInteractionAction{}
	_, err := a.Execute(map[string]interface{}{
		StrConfig:  map[string]interface{}{},
		StrRequest: "hi",
		"url":      server.URL,
		"model":    "m",
		"prompt":   "hi",
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "f", "parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "string"},
				},
				"required": []any{"ghost"},
			}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown property") {
		t.Fatalf("expected schema rejection, got %v", err)
	}
	if *count != 0 {
		t.Fatalf("invalid schema reached HTTP")
	}
}
