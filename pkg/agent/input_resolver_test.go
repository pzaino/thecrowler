package agent

import (
	"reflect"
	"strings"
	"testing"
)

func testInputContext() InputContext {
	return InputContext{
		Response: map[string]interface{}{
			"status_code": float64(200),
			"user_id":     float64(7),
			"active":      true,
			"name":        "ada",
			"nothing":     nil,
			"tags":        []interface{}{"a", "b"},
			"items": []interface{}{
				map[string]interface{}{"id": float64(1)},
				map[string]interface{}{"id": float64(2)},
			},
			"nested": map[string]interface{}{
				"deep": map[string]interface{}{"value": "found"},
			},
			"unicode": "héllo wörld ✓",
		},
		Event: map[string]interface{}{
			"type": "crawl_completed",
			"meta": map[string]interface{}{"source": float64(3)},
		},
	}
}

// TestResolveValueTypeMatrix checks exact-token type preservation and
// interpolation across JSON types.
func TestResolveValueTypeMatrix(t *testing.T) {
	ctx := testInputContext()
	cases := []struct {
		name  string
		value any
		want  any
	}{
		{"whole payload", "$response", ctx.Response},
		{"number exact", "$response.user_id", float64(7)},
		{"bool exact", "$response.active", true},
		{"string exact", "$response.name", "ada"},
		{"null exact", "$response.nothing", nil},
		{"array exact", "$response.tags", []interface{}{"a", "b"}},
		{"nested path", "$response.nested.deep.value", "found"},
		{"array index", "$response.items[0].id", float64(1)},
		{"array index bare", "$response.items[1]", map[string]interface{}{"id": float64(2)}},
		{"number interp", "id=$response.user_id!", "id=7!"},
		{"bool interp", "on=$response.active", "on=true"},
		{"null interp", "v=$response.nothing", "v=null"},
		{"unicode passthrough", "$response.unicode", "héllo wörld ✓"},
		{"unicode interp", "hi $response.unicode", "hi héllo wörld ✓"},
		{"quoted interp", `say "$response.name"`, `say "ada"`},
		{"event path", "$event.meta.source", float64(3)},
		{"event whole", "$event", ctx.Event},
		{"plain string", "no tokens here", "no tokens here"},
		{"plain number", float64(9), float64(9)},
	}
	for _, tc := range cases {
		got, err := ResolveValue(ctx, tc.value)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

// TestResolveNestedContainers checks deep resolution inside maps and slices.
func TestResolveNestedContainers(t *testing.T) {
	ctx := testInputContext()
	in := map[string]interface{}{
		"body": map[string]interface{}{
			"uid":    "$response.user_id",
			"label":  "user-$response.name",
			"flags":  []interface{}{"$response.active", "x"},
			"nested": map[string]interface{}{"deep": "$response.nested.deep.value"},
		},
	}
	got, err := ResolveValue(ctx, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := got.(map[string]interface{})["body"].(map[string]interface{})
	if body["uid"] != float64(7) {
		t.Fatalf("typed exact token lost: %#v", body["uid"])
	}
	if body["label"] != "user-ada" {
		t.Fatalf("interpolation failed: %#v", body["label"])
	}
	if flags := body["flags"].([]interface{}); flags[0] != true || flags[1] != "x" {
		t.Fatalf("slice resolution failed: %#v", flags)
	}
	if deep := body["nested"].(map[string]interface{})["deep"]; deep != "found" {
		t.Fatalf("nested map resolution failed: %#v", deep)
	}
}

// TestResolveMissingPaths demands explicit errors, never <nil> magic.
func TestResolveMissingPaths(t *testing.T) {
	ctx := testInputContext()
	for _, value := range []any{
		"$response.absent",
		"$response.nested.absent.deeper",
		"prefix-$response.absent-suffix",
		"$response.items[9]",
		"$response.user_id.deeper",
		"$event.absent",
	} {
		if _, err := ResolveValue(ctx, value); err == nil {
			t.Fatalf("expected error for %v", value)
		} else if strings.Contains(err.Error(), "<nil>") || strings.Contains(err.Error(), "%!s") {
			t.Fatalf("error must not leak magic strings: %v", err)
		}
		if s, err := ResolveString(ctx, value.(string)); err == nil || s == "<nil>" || strings.Contains(s, "<nil>") {
			t.Fatalf("expected clean error for %v, got %q, %v", value, s, err)
		}
	}
}

// TestResolveDeprecatedAlias keeps wrapper-era manifests working.
func TestResolveDeprecatedAlias(t *testing.T) {
	ctx := testInputContext()
	for _, token := range []string{"$response.input.user_id", "$response.request.user_id"} {
		got, err := ResolveValue(ctx, token)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", token, err)
		}
		if got != float64(7) {
			t.Fatalf("%s: got %#v, want 7", token, got)
		}
	}
	// Canonical paths win over the alias when both could match.
	aliased := InputContext{Response: map[string]interface{}{"input": "canonical"}}
	got, err := ResolveValue(aliased, "$response.input")
	if err != nil || got != "canonical" {
		t.Fatalf("canonical path must win, got %#v, %v", got, err)
	}
}

// TestResolveDoesNotMutate ensures repeated runs see pristine inputs.
func TestResolveDoesNotMutate(t *testing.T) {
	payload := map[string]interface{}{"user_id": float64(7)}
	params := map[string]interface{}{
		"body": map[string]interface{}{"uid": "$response.user_id"},
		"list": []interface{}{"$response.user_id"},
	}
	ctx := InputContext{Response: payload}
	for i := 0; i < 2; i++ {
		if _, err := ResolveValue(ctx, params["body"]); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := ResolveValue(ctx, params["list"]); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if params["body"].(map[string]interface{})["uid"] != "$response.user_id" {
		t.Fatalf("input map was mutated: %#v", params["body"])
	}
	if params["list"].([]interface{})[0] != "$response.user_id" {
		t.Fatalf("input slice was mutated: %#v", params["list"])
	}
	if payload["user_id"] != float64(7) {
		t.Fatalf("payload was mutated: %#v", payload)
	}
	// Exact-token results must not alias the payload either.
	got, err := ResolveValue(ctx, "$response")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got.(map[string]interface{})["user_id"] = float64(999)
	if payload["user_id"] != float64(7) {
		t.Fatalf("resolved whole payload aliases stored state")
	}
}
