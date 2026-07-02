package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Example results struct for success scenario
type ExampleResults struct {
	Message string `json:"message"`
}

func TestExtractQueryOrBody(t *testing.T) {
	t.Run("POST request with body", func(t *testing.T) {
		body := "test body"
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		res, err := extractQueryOrBody(req)
		defer req.Body.Close() // nolint: errcheck // we don't care about this error code
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if res != body {
			t.Errorf("expected body %q, got %q", body, res)
		}
	})

	t.Run("GET request with query parameter", func(t *testing.T) {
		query := "test query"
		req := httptest.NewRequest(http.MethodGet, "/?q="+url.QueryEscape(query), nil)
		res, err := extractQueryOrBody(req)
		defer req.Body.Close() // nolint: errcheck // we don't care about this error code
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if res != query {
			t.Errorf("expected query %q, got %q", query, res)
		}
	})

	t.Run("GET request without query parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		_, err := extractQueryOrBody(req)
		defer req.Body.Close() // nolint: errcheck // we don't care about this error code
		if err == nil {
			t.Error("expected error, got nil")
		}
		expectedErr := fmt.Errorf("query parameter 'q' is required")
		if err.Error() != expectedErr.Error() {
			t.Errorf("expected error %q, got %q", expectedErr, err)
		}
	})
}

func TestGetQTypeFromName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "Post",
			input:    "Post",
			expected: postQuery,
		},
		{
			name:     "Get",
			input:    "Get",
			expected: getQuery,
		},
		{
			name:     "Unknown",
			input:    "Unknown",
			expected: getQuery,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := getQTypeFromName(test.input)
			if res != test.expected {
				t.Errorf("expected %d, got %d", test.expected, res)
			}
		})
	}
}

func TestPrepareInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "TrimSpaces",
			input:    "  test input  ",
			expected: "test input",
		},
		{
			name:     "NoChange",
			input:    "test input",
			expected: "test input",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := PrepareInput(test.input)
			if res != test.expected {
				t.Errorf("expected %q, got %q", test.expected, res)
			}
		})
	}
}

// TestHandleErrorAndRespond tests the handleErrorAndRespond function for various scenarios
func TestHandleErrorAndRespond(t *testing.T) {
	t.Run("with error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		errMsg := "an error occurred"
		testErr := fmt.Errorf("test error")

		handleErrorAndRespond(rec, testErr, nil, errMsg, http.StatusBadRequest, http.StatusOK)

		res := rec.Result()
		defer res.Body.Close()

		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, res.StatusCode)
		}

		// You can also read and check the response body if needed
	})

	t.Run("redacts mail authentication material", func(t *testing.T) {
		rec := httptest.NewRecorder()
		testErr := fmt.Errorf("password=hunter2 access_token=access-value refresh_token=refresh-value client_secret=client-value Authorization: Bearer bearer-value credential_ref=secret/mail/archive")

		handleErrorAndRespond(rec, testErr, nil, "request failed: %v", http.StatusBadRequest, http.StatusOK)

		body := rec.Body.String()
		for _, secret := range []string{"hunter2", "access-value", "refresh-value", "client-value", "bearer-value", "secret/mail/archive"} {
			if strings.Contains(body, secret) {
				t.Fatalf("API error response leaked %q: %s", secret, body)
			}
		}
	})

	t.Run("success with results", func(t *testing.T) {
		rec := httptest.NewRecorder()
		results := ExampleResults{Message: "success"}

		handleErrorAndRespond(rec, nil, results, "", http.StatusBadRequest, http.StatusOK)

		res := rec.Result()
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, res.StatusCode)
		}

		var responseResults ExampleResults
		if err := json.NewDecoder(res.Body).Decode(&responseResults); err != nil {
			t.Errorf("Failed to decode response body: %v", err)
		}

		if responseResults.Message != results.Message {
			t.Errorf("Expected result message %q, got %q", results.Message, responseResults.Message)
		}
	})

	t.Run("error during JSON encoding", func(t *testing.T) {
		// This test might require a custom type that fails on json.Marshal.
		// Since json.Encoder does not expose direct control over the encoding process to trigger an error easily,
		// this test scenario might be more challenging to implement directly without custom types or interfaces
		// that can simulate an error during JSON encoding.
	})
}

func TestExtractAPIPluginData(t *testing.T) {
	t.Run("GET request uses declared plugin query parameters without requiring q", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/plugin/ping?input=hello", nil)

		data, httpCtx, err := extractAPIPluginData(req)
		defer req.Body.Close() // nolint: errcheck // we don't care about this error code
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		object, ok := data.(map[string]interface{})
		if !ok {
			t.Fatalf("expected object data, got %T", data)
		}
		if object["input"] != "hello" {
			t.Fatalf("expected input query value, got %#v", object["input"])
		}
		jsonHTTP, ok := object["http"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected jsonData.http to contain HTTP context, got %#v", object["http"])
		}
		if jsonHTTP["method"] != httpCtx["method"] || jsonHTTP["path"] != httpCtx["path"] || jsonHTTP["query"] != httpCtx["query"] {
			t.Fatalf("expected jsonData.http to match HTTP context: %#v vs %#v", jsonHTTP, httpCtx)
		}
		if httpCtx["method"] != http.MethodGet || httpCtx["path"] != "/v1/plugin/ping" || httpCtx["query"] != "input=hello" {
			t.Fatalf("unexpected HTTP context: %#v", httpCtx)
		}
	})

	t.Run("GET request with repeated query values keeps all values", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/plugin/ping?tag=a&tag=b", nil)

		data, _, err := extractAPIPluginData(req)
		defer req.Body.Close() // nolint: errcheck // we don't care about this error code
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		object, ok := data.(map[string]interface{})
		if !ok {
			t.Fatalf("expected object data, got %T", data)
		}
		values, ok := object["tag"].([]string)
		if !ok {
			t.Fatalf("expected repeated tag values, got %#v", object["tag"])
		}
		if len(values) != 2 || values[0] != "a" || values[1] != "b" {
			t.Fatalf("unexpected tag values: %#v", values)
		}
	})

	t.Run("POST request attaches HTTP context to JSON object body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/plugin/ping", strings.NewReader(`{"input":"hello"}`))

		data, httpCtx, err := extractAPIPluginData(req)
		defer req.Body.Close() // nolint: errcheck // we don't care about this error code
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		object, ok := data.(map[string]interface{})
		if !ok {
			t.Fatalf("expected object data, got %T", data)
		}
		if object["input"] != "hello" {
			t.Fatalf("expected input body value, got %#v", object["input"])
		}
		jsonHTTP, ok := object["http"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected jsonData.http to contain HTTP context, got %#v", object["http"])
		}
		if jsonHTTP["method"] != httpCtx["method"] || jsonHTTP["path"] != httpCtx["path"] || jsonHTTP["query"] != httpCtx["query"] {
			t.Fatalf("expected jsonData.http to match HTTP context: %#v vs %#v", jsonHTTP, httpCtx)
		}
	})
}
