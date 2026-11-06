package agent

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const (
	secretPrompt = "PROMPT_SECRET_MARKER"
	secretBody   = "BODY_SECRET_MARKER"
	secretQuery  = "QUERY_SECRET_MARKER"
	secretAuth   = "AUTH_SECRET_MARKER"
)

// assertNoProviderSecrets guarantees that an error string carries none of the
// planted secrets or endpoint details.
func assertNoProviderSecrets(t *testing.T, err error, extra ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error")
	}
	text := err.Error()
	for _, marker := range append([]string{secretPrompt, secretBody, secretQuery, secretAuth, "127.0.0.1", "/v1/chat"}, extra...) {
		if marker == "" {
			continue
		}
		if strings.Contains(text, marker) {
			t.Fatalf("error leaked %q: %v", marker, err)
		}
	}
}

// TestProviderHTTPFailuresAreSanitized pins fixed categories and statuses
// while proving response bodies, prompts, query parameters, credentials and
// endpoint hosts never reach the error text.
func TestProviderHTTPFailuresAreSanitized(t *testing.T) {
	cases := []struct {
		status   int
		category string
	}{
		{http.StatusUnauthorized, "provider authentication failed"},
		{http.StatusForbidden, "provider authorization failed"},
		{http.StatusNotFound, "provider endpoint not found"},
		{http.StatusRequestTimeout, "provider request timed out"},
		{http.StatusTooManyRequests, "provider rate limited"},
		{http.StatusInternalServerError, "provider server error"},
		{http.StatusServiceUnavailable, "provider server error"},
	}
	for _, tc := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"` + secretBody +
				`","hint":"api_key=` + secretQuery +
				`","endpoint":"http://127.0.0.1:9999/v1/chat/completions"}`))
		}))
		provider := &OpenAICompatibleProvider{}
		_, err := provider.Execute(LLMRequest{
			URL:    server.URL + "/v1/chat/completions?api_key=" + secretQuery,
			Auth:   "Bearer " + secretAuth,
			Model:  "m",
			Prompt: secretPrompt,
		})
		server.Close()
		if err == nil {
			t.Fatalf("status %d: expected error", tc.status)
		}
		wantStatus := "status " + strconv.Itoa(tc.status)
		if !strings.Contains(err.Error(), wantStatus) {
			t.Fatalf("status %d: missing status in %v", tc.status, err)
		}
		if !strings.Contains(err.Error(), tc.category) {
			t.Fatalf("status %d: missing category %q in %v", tc.status, tc.category, err)
		}
		assertNoProviderSecrets(t, err)
	}
}

// TestProviderConnectionFailureIsSanitized pins the transport-failure
// category without leaking the endpoint host or port.
func TestProviderConnectionFailureIsSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	endpoint := server.URL + "/v1/chat/completions"
	server.Close()

	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{URL: endpoint, Model: "m", Prompt: secretPrompt})
	if err == nil {
		t.Fatalf("expected connection failure")
	}
	if !strings.Contains(err.Error(), "provider request failed") {
		t.Fatalf("unexpected category: %v", err)
	}
	assertNoProviderSecrets(t, err, endpoint, "connection refused")
}

// TestProviderTLSFailureIsSanitized pins the TLS categories without exposing
// the endpoint.
func TestProviderTLSFailureIsSanitized(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{URL: server.URL, Model: "m", Prompt: secretPrompt})
	if err == nil {
		t.Fatalf("expected TLS failure")
	}
	if !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("expected TLS category, got %v", err)
	}
	assertNoProviderSecrets(t, err, server.URL)
}

// TestProviderRedirectFailureIsSanitized proves the redirect category is
// preserved without exposing the redirect target.
func TestProviderRedirectFailureIsSanitized(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target must not be contacted")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/chat/completions?api_key="+secretQuery, http.StatusFound)
	}))
	defer redirector.Close()

	provider := &OpenAICompatibleProvider{}
	_, err := provider.Execute(LLMRequest{URL: redirector.URL, Model: "m", Prompt: secretPrompt})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect error, got %v", err)
	}
	assertNoProviderSecrets(t, err, target.URL, redirector.URL)
}

// TestAIInteractionProviderErrorIsSanitized proves the action's user-visible
// message is safe, not only the provider error.
func TestAIInteractionProviderErrorIsSanitized(t *testing.T) {
	resetLLMProvidersForTest()
	defer resetLLMProvidersForTest()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"` + secretBody + `","prompt":"` + secretPrompt + `"}`))
	}))
	defer server.Close()

	result, err := (&AIInteractionAction{}).Execute(map[string]any{
		"input":    map[string]any{},
		"provider": defaultLLMProvider,
		"url":      server.URL,
		"model":    "m",
		"prompt":   secretPrompt,
		"config":   map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected provider error")
	}
	message, _ := result[StrMessage].(string)
	if strings.Contains(message, secretBody) || strings.Contains(message, secretPrompt) {
		t.Fatalf("action message leaked provider data: %q", message)
	}
	if !strings.Contains(message, "502") || !strings.Contains(message, "server error") {
		t.Fatalf("action message lost status category: %q", message)
	}
}
