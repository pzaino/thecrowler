package common

import (
	"encoding/json"
	"testing"
)

func TestSchemaAnyFromJSON_AllowsBooleanAdditionalProperties(t *testing.T) {
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		},
		"additionalProperties": true
	}`

	schema := schemaAnyFromJSON(schemaJSON)
	if schema == nil {
		t.Fatal("schemaAnyFromJSON returned nil")
	}

	v, ok := schema.AdditionalProperties.(bool)
	if !ok {
		t.Fatalf("additionalProperties type = %T, expected bool", schema.AdditionalProperties)
	}

	if !v {
		t.Fatal("additionalProperties bool value = false, expected true")
	}
}

func TestSchemaAnyFromJSON_AllowsObjectAdditionalProperties(t *testing.T) {
	schemaJSON := `{
		"type": "object",
		"additionalProperties": {"type": "string"}
	}`

	schema := schemaAnyFromJSON(schemaJSON)
	if schema == nil {
		t.Fatal("schemaAnyFromJSON returned nil")
	}

	additionalSchema, ok := schema.AdditionalProperties.(map[string]any)
	if !ok {
		t.Fatalf("additionalProperties type = %T, expected map[string]any", schema.AdditionalProperties)
	}

	if typ, ok := additionalSchema["type"].(string); !ok || typ != "string" {
		t.Fatalf("additionalProperties.type = %v, expected string", additionalSchema["type"])
	}
}

func TestBuildOpenAPISpec_PreservesBooleanAdditionalProperties(t *testing.T) {
	routes := []APIRoute{{
		Path:          "/v1/example",
		Methods:       []string{"POST"},
		Description:   "example route",
		SuccessStatus: 200,
		BodyType:      schemaAnyFromJSON(`{"type":"object","additionalProperties":true}`),
		ResponseType:  schemaAnyFromJSON(`{"type":"object","additionalProperties":false}`),
	}}

	spec := BuildOpenAPISpec(routes, OpenAPIOptions{Title: "t", Version: "v"})

	post, ok := spec.Paths["/v1/example"]["post"]
	if !ok {
		t.Fatal("missing POST operation")
	}

	bodySchema, ok := post.RequestBody.Content["application/json"].Schema.(*OpenAPISchema)
	if !ok {
		t.Fatalf("request schema type = %T, expected *OpenAPISchema", post.RequestBody.Content["application/json"].Schema)
	}

	bodyAdditional, ok := bodySchema.AdditionalProperties.(bool)
	if !ok || !bodyAdditional {
		t.Fatalf("request additionalProperties = %#v, expected true bool", bodySchema.AdditionalProperties)
	}

	responseSchema, ok := post.Responses["200"].Content["application/json"].Schema.(*OpenAPISchema)
	if !ok {
		t.Fatalf("response schema type = %T, expected *OpenAPISchema", post.Responses["200"].Content["application/json"].Schema)
	}

	responseAdditional, ok := responseSchema.AdditionalProperties.(bool)
	if !ok || responseAdditional {
		t.Fatalf("response additionalProperties = %#v, expected false bool", responseSchema.AdditionalProperties)
	}

	if _, err := json.Marshal(spec); err != nil {
		t.Fatalf("generated spec failed to marshal: %v", err)
	}
}
