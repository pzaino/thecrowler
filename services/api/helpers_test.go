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
