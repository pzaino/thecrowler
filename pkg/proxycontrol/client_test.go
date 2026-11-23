package proxycontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func successfulResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"status":"rotated","provider":"residential-sticky","generation":2}`)),
	}
}

func TestRotatorSuccessSendsTrustedHeadersAndValidatesResponse(t *testing.T) {
	var (
		mu       sync.Mutex
		method   string
		path     string
		auth     string
		key      string
		bodySeen string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		method, path, auth, key, bodySeen = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"rotated","provider":"residential-sticky","generation":2}`)
	}))
	defer server.Close()

	rotator := NewRotator()
	transport := rotator.client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("rotation transport must not inherit an ambient proxy")
	}
	if rotator.client.CheckRedirect == nil {
		t.Fatal("rotation client must disable redirects")
	}

	resp, err := rotator.Rotate(context.Background(), RotateRequest{
		APIURL:         server.URL,
		Token:          "secret-token",
		IdempotencyKey: "crowler-key-1",
		Provider:       "residential-sticky",
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if resp.Status != "rotated" || resp.Generation != 2 || resp.Provider != "residential-sticky" {
		t.Fatalf("response = %+v", resp)
	}

	mu.Lock()
	defer mu.Unlock()
	if method != http.MethodPost || path != RotatePath {
		t.Fatalf("request = %s %s", method, path)
	}
	if auth != "Bearer secret-token" {
		t.Fatalf("authorization = %q", auth)
	}
	if key != "crowler-key-1" {
		t.Fatalf("idempotency key = %q", key)
	}
	if bodySeen != "" {
		t.Fatalf("unexpected request body %q", bodySeen)
	}
}

func TestRotatorReplayIsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"rotated","provider":"residential-sticky","generation":7,"replayed":true}`)
	}))
	defer server.Close()
	resp, err := NewRotator().Rotate(context.Background(), RotateRequest{
		APIURL: server.URL, Token: "t", IdempotencyKey: "k", Provider: "residential-sticky",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Replayed || resp.Generation != 7 {
		t.Fatalf("replay response = %+v", resp)
	}
}

func TestRotatorHTTPFailuresAreClassified(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantKind FailureKind
		wantCode string
	}{
		{"bad request", http.StatusBadRequest, `{"error":{"code":"invalid_request"}}`, FailureInvalidRequest, "invalid_request"},
		{"unauthorized", http.StatusUnauthorized, ``, FailureAuth, ""},
		{"forbidden", http.StatusForbidden, ``, FailureAuth, ""},
		{"stale key", http.StatusConflict, `{"error":{"code":"stale_idempotency_key","message":"old"}}`, FailureStaleKey, "stale_idempotency_key"},
		{"other conflict", http.StatusConflict, `{"error":{"code":"conflict"}}`, FailureConflict, "conflict"},
		{"unavailable", http.StatusServiceUnavailable, `{"error":{"code":"identity_failure"}}`, FailureUnavailable, "identity_failure"},
		{"server error", http.StatusInternalServerError, ``, FailureUnavailable, ""},
		{"redirect", http.StatusFound, ``, FailureUnexpectedStatus, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.body != "" {
					fmt.Fprint(w, tc.body)
				}
			}))
			defer server.Close()
			_, err := NewRotator().Rotate(context.Background(), RotateRequest{
				APIURL: server.URL, Token: "secret", IdempotencyKey: "key", Provider: "p",
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			var failure *Error
			if !errors.As(err, &failure) {
				t.Fatalf("error type = %T, want *Error", err)
			}
			if failure.Kind != tc.wantKind || failure.Code != tc.wantCode {
				t.Fatalf("failure = %+v, want kind %q code %q", failure, tc.wantKind, tc.wantCode)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked credential material: %v", err)
			}
		})
	}
}

func TestRotatorInvalidSuccessBodiesAreRejected(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantKind FailureKind
	}{
		{"malformed json", `{`, FailureInvalidResponse},
		{"wrong status", `{"status":"pending","provider":"p","generation":1}`, FailureInvalidResponse},
		{"zero generation", `{"status":"rotated","provider":"p","generation":0}`, FailureInvalidResponse},
		{"negative generation", `{"status":"rotated","provider":"p","generation":-1}`, FailureInvalidResponse},
		{"wrong provider", `{"status":"rotated","provider":"other","generation":1}`, FailureProviderMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := NewRotator().Rotate(context.Background(), RotateRequest{
				APIURL: server.URL, Token: "t", IdempotencyKey: "k", Provider: "p",
			})
			if !IsFailureKind(err, tc.wantKind) {
				t.Fatalf("error = %v, want kind %q", err, tc.wantKind)
			}
		})
	}
}

func TestRotatorBoundsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 5000))
	}))
	defer server.Close()
	_, err := NewRotator().Rotate(context.Background(), RotateRequest{
		APIURL: server.URL, Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !IsFailureKind(err, FailureInvalidResponse) {
		t.Fatalf("error = %v, want invalid_response", err)
	}
}

func TestRotatorTLSCertificateFailureIsSanitized(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"rotated","provider":"p","generation":1}`)
	}))
	defer server.Close()
	_, err := NewRotator().Rotate(context.Background(), RotateRequest{
		APIURL: server.URL, Token: "very-secret-token", IdempotencyKey: "key", Provider: "p",
	})
	if !IsFailureKind(err, FailureTransport) {
		t.Fatalf("error = %v, want transport_error", err)
	}
	if strings.Contains(err.Error(), "very-secret-token") {
		t.Fatalf("error leaked the token: %v", err)
	}
}

func TestRotatorDoesNotFollowRedirects(t *testing.T) {
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		fmt.Fprint(w, `{"status":"rotated","provider":"p","generation":1}`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	_, err := NewRotator().Rotate(context.Background(), RotateRequest{
		APIURL: source.URL, Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !IsFailureKind(err, FailureUnexpectedStatus) {
		t.Fatalf("error = %v, want unexpected_status", err)
	}
	if targetHits != 0 {
		t.Fatalf("redirect target received %d requests, want 0", targetHits)
	}
}

type failingTransport struct {
	mu        sync.Mutex
	attempts  int
	keys      []string
	failCount int
}

func (f *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.attempts++
	f.keys = append(f.keys, req.Header.Get("Idempotency-Key"))
	attempt := f.attempts
	f.mu.Unlock()
	if attempt <= f.failCount {
		return nil, errors.New("simulated transport failure")
	}
	return successfulResponse(), nil
}

func TestRotatorRetriesTransportFailuresWithSameKey(t *testing.T) {
	transport := &failingTransport{failCount: 2}
	rotator := newRotatorWithClient(&http.Client{Transport: transport}, 3, 4096)
	resp, err := rotator.Rotate(context.Background(), RotateRequest{
		APIURL: "http://127.0.0.1:8094", Token: "t", IdempotencyKey: "same-key", Provider: "residential-sticky",
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if resp.Generation != 2 {
		t.Fatalf("response = %+v", resp)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.attempts != 3 {
		t.Fatalf("attempts = %d, want 3", transport.attempts)
	}
	for _, key := range transport.keys {
		if key != "same-key" {
			t.Fatalf("idempotency key changed across retries: %v", transport.keys)
		}
	}
}

func TestRotatorDoesNotRetryDefinitiveHTTPFailures(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	_, err := NewRotator().Rotate(context.Background(), RotateRequest{
		APIURL: server.URL, Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !IsFailureKind(err, FailureAuth) {
		t.Fatalf("error = %v, want auth_failure", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts)
	}
}

func TestRotatorCancellationAndTimeout(t *testing.T) {
	t.Run("cancelled before request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewRotator().Rotate(ctx, RotateRequest{
			APIURL: "http://127.0.0.1:8094", Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !IsFailureKind(err, FailureCancelled) {
			t.Fatalf("error = %v, want cancelled", err)
		}
	})

	t.Run("transport timeout retried and classified", func(t *testing.T) {
		transport := &timeoutTransport{}
		rotator := newRotatorWithClient(&http.Client{Transport: transport}, 2, 4096)
		_, err := rotator.Rotate(context.Background(), RotateRequest{
			APIURL: "http://127.0.0.1:8094", Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !IsFailureKind(err, FailureTimeout) {
			t.Fatalf("error = %v, want timeout", err)
		}
		if transport.calls != 2 {
			t.Fatalf("calls = %d, want 2", transport.calls)
		}
	})
}

type timeoutTransport struct {
	calls int
}

func (t *timeoutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, timeoutError{}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestRotatorRejectsInvalidLocalInputs(t *testing.T) {
	tests := []RotateRequest{
		{APIURL: "", Token: "t", IdempotencyKey: "k", Provider: "p"},
		{APIURL: "http://127.0.0.1:8094/path", Token: "t", IdempotencyKey: "k", Provider: "p"},
		{APIURL: "http://127.0.0.1:8094", Token: "", IdempotencyKey: "k", Provider: "p"},
		{APIURL: "http://127.0.0.1:8094", Token: "t", IdempotencyKey: "", Provider: "p"},
		{APIURL: "http://127.0.0.1:8094", Token: "t", IdempotencyKey: "bad key", Provider: "p"},
		{APIURL: "http://127.0.0.1:8094", Token: "t", IdempotencyKey: "k", Provider: ""},
		{APIURL: "http://127.0.0.1:8094", Token: "line\nbreak", IdempotencyKey: "k", Provider: "p"},
	}
	for _, request := range tests {
		if _, err := NewRotator().Rotate(context.Background(), request); !IsFailureKind(err, FailureInvalidInput) {
			t.Errorf("request %+v: error = %v, want invalid_input", request, err)
		}
	}
}

func TestNewIdempotencyKeyIsUniqueAndHeaderSafe(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 32; i++ {
		key, err := NewIdempotencyKey()
		if err != nil {
			t.Fatal(err)
		}
		if len(key) > 256 || !validIdempotencyKey(key) {
			t.Fatalf("unsafe key %q", key)
		}
		if seen[key] {
			t.Fatalf("duplicate key %q", key)
		}
		seen[key] = true
	}
}

func TestRotatorDefaultTimeoutIsBounded(t *testing.T) {
	if NewRotator().client.Timeout <= 0 || NewRotator().client.Timeout > 30*time.Second {
		t.Fatalf("client timeout = %v, want a bounded positive value", NewRotator().client.Timeout)
	}
}
