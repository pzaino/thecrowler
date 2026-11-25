// Copyright 2023 Paolo Fabio Zaino
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

// Package proxycontrol implements the narrow, authenticated client for the
// microproxy data-plane sticky-session API. It deliberately has no knowledge
// of sources, browsers or VDI leases: callers supply one trusted API origin,
// one bearer token and one job-boundary idempotency key.
package proxycontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const (
	// RotatePath is the data-plane sticky-session rotation endpoint.
	RotatePath = "/api/v1/sessions/rotate"

	defaultMaxAttempts    = 3
	defaultDialTimeout    = 5 * time.Second
	defaultRequestTimeout = 15 * time.Second
	defaultHeaderTimeout  = 8 * time.Second
	defaultBackoff        = 200 * time.Millisecond
)

// MaxResponseBytes is the shared maximum accepted data-plane response size.
const MaxResponseBytes = int64(4 * 1024)

const defaultResponseLimit = MaxResponseBytes

// RotateRequest is one trusted rotation request. None of these values may be
// derived from model output or source-provided configuration.
type RotateRequest struct {
	APIURL         string
	Token          string
	IdempotencyKey string
	Provider       string
}

// RotateResponse is the validated successful response.
type RotateResponse struct {
	Status     string `json:"status"`
	Provider   string `json:"provider"`
	Generation int64  `json:"generation"`
	Replayed   bool   `json:"replayed,omitempty"`
}

// FailureKind is a stable, sanitized error category.
type FailureKind string

const (
	// FailureAuth is a 401 response.
	FailureAuth FailureKind = "auth_failure"
	// FailureInvalidRequest is a 400 response.
	FailureInvalidRequest FailureKind = "invalid_request"
	// FailureStaleKey is a 409 stale_idempotency_key response.
	FailureStaleKey FailureKind = "stale_key"
	// FailureConflict is any other 409 response.
	FailureConflict FailureKind = "conflict"
	// FailureUnavailable is a 503 or other server-side failure.
	FailureUnavailable FailureKind = "unavailable"
	// FailureInvalidResponse is a malformed or contract-violating 200 body.
	FailureInvalidResponse FailureKind = "invalid_response"
	// FailureProviderMismatch means the API rotated a different provider.
	FailureProviderMismatch FailureKind = "provider_mismatch"
	// FailureCancelled means the caller context was cancelled.
	FailureCancelled FailureKind = "cancelled"
	// FailureTimeout means the request or caller deadline expired.
	FailureTimeout FailureKind = "timeout"
	// FailureTransport means a network/TLS error before a response.
	FailureTransport FailureKind = "transport_error"
	// FailureUnexpectedStatus is any other HTTP status.
	FailureUnexpectedStatus FailureKind = "unexpected_status"
	// FailureInvalidInput is a local validation failure.
	FailureInvalidInput FailureKind = "invalid_input"
)

// Error is a sanitized rotation failure. It never contains the bearer token,
// the idempotency key, headers, response bodies or session credentials.
type Error struct {
	Kind       FailureKind
	HTTPStatus int
	Code       string
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "microproxy session rotation failed"
	}
	if e.HTTPStatus != 0 {
		if e.Code != "" {
			return fmt.Sprintf("microproxy session rotation failed: %s (HTTP %d, code %s)", e.Kind, e.HTTPStatus, e.Code)
		}
		return fmt.Sprintf("microproxy session rotation failed: %s (HTTP %d)", e.Kind, e.HTTPStatus)
	}
	return fmt.Sprintf("microproxy session rotation failed: %s", e.Kind)
}

// IsFailureKind reports whether err is a rotation Error of the given kind.
func IsFailureKind(err error, kind FailureKind) bool {
	var failure *Error
	return errors.As(err, &failure) && failure.Kind == kind
}

// IsDefinitive reports whether err is an authoritative API/contract rejection
// that must not be retried through another transport. Transport, timeout and
// cancellation outcomes are not definitive.
func IsDefinitive(err error) bool {
	var failure *Error
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Kind {
	case FailureTransport, FailureTimeout, FailureCancelled:
		return false
	default:
		return true
	}
}

// Rotator sends rotation requests. The zero value is not usable; call
// NewRotator. A Rotator is safe for concurrent use.
type Rotator struct {
	client        *http.Client
	maxAttempts   int
	responseLimit int64
	backoff       func(attempt int) time.Duration
}

// NewRotator returns a production rotator. Its transport ignores ambient
// proxy environment variables, verifies TLS normally, follows no redirects and
// bounds every phase of the request.
func NewRotator() *Rotator {
	transport := &http.Transport{
		Proxy:                 nil, // never send management credentials through an outbound proxy
		DialContext:           (&net.Dialer{Timeout: defaultDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   defaultDialTimeout,
		ResponseHeaderTimeout: defaultHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
	}
	return &Rotator{
		client: &http.Client{
			Transport: transport,
			Timeout:   defaultRequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// Return the 3xx response to the caller so it is classified as
				// an unexpected status without forwarding credentials.
				return http.ErrUseLastResponse
			},
		},
		maxAttempts:   defaultMaxAttempts,
		responseLimit: defaultResponseLimit,
		backoff: func(attempt int) time.Duration {
			if attempt < 1 {
				attempt = 1
			}
			return time.Duration(attempt) * defaultBackoff
		},
	}
}

// newRotatorWithClient is the deterministic test seam. Production callers use
// NewRotator.
func newRotatorWithClient(client *http.Client, maxAttempts int, limit int64) *Rotator {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &Rotator{
		client:        client,
		maxAttempts:   maxAttempts,
		responseLimit: limit,
		backoff:       func(int) time.Duration { return 0 },
	}
}

// Rotate sends one rotation request. Transport failures are retried with the
// same idempotency key; a definitive HTTP response is never retried.
func (r *Rotator) Rotate(ctx context.Context, request RotateRequest) (RotateResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.client == nil {
		return RotateResponse{}, &Error{Kind: FailureInvalidInput}
	}
	// A rotation boundary is short-lived. Close idle management connections
	// immediately instead of retaining one per VDI for the process lifetime.
	defer r.client.CloseIdleConnections()
	apiURL, err := cfg.NormalizeStickyAPIURL(request.APIURL)
	if err != nil {
		return RotateResponse{}, &Error{Kind: FailureInvalidInput}
	}
	if !validHeaderValue(request.Token) {
		return RotateResponse{}, &Error{Kind: FailureInvalidInput}
	}
	if !validIdempotencyKey(request.IdempotencyKey) {
		return RotateResponse{}, &Error{Kind: FailureInvalidInput}
	}
	if strings.TrimSpace(request.Provider) == "" {
		return RotateResponse{}, &Error{Kind: FailureInvalidInput}
	}
	endpoint := strings.TrimRight(apiURL, "/") + RotatePath

	attempts := r.maxAttempts
	if attempts < 1 {
		attempts = 1
	}
	backoff := r.backoff
	if backoff == nil {
		backoff = func(int) time.Duration { return 0 }
	}
	var lastTransport error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return RotateResponse{}, contextFailure(err)
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
		if reqErr != nil {
			return RotateResponse{}, &Error{Kind: FailureInvalidInput}
		}
		req.Header.Set("Authorization", "Bearer "+request.Token)
		req.Header.Set("Idempotency-Key", request.IdempotencyKey)

		resp, doErr := r.client.Do(req)
		if doErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return RotateResponse{}, contextFailure(ctxErr)
			}
			lastTransport = doErr
			if attempt < attempts {
				if err := waitBackoff(ctx, backoff(attempt)); err != nil {
					return RotateResponse{}, contextFailure(err)
				}
				continue
			}
			if isTimeoutError(doErr) {
				return RotateResponse{}, &Error{Kind: FailureTimeout}
			}
			return RotateResponse{}, &Error{Kind: FailureTransport}
		}

		body, readErr := readBounded(resp.Body, r.responseLimit)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if readErr != nil {
				body = nil
			}
			return RotateResponse{}, ClassifyHTTPFailure(resp.StatusCode, body)
		}
		if readErr != nil {
			if errors.Is(readErr, errResponseTooLarge) {
				return RotateResponse{}, &Error{Kind: FailureInvalidResponse, HTTPStatus: resp.StatusCode}
			}
			// The status line committed but the body did not arrive intact.
			// The outcome is uncertain; replaying the same key is safe.
			if attempt < attempts {
				if err := waitBackoff(ctx, backoff(attempt)); err != nil {
					return RotateResponse{}, contextFailure(err)
				}
				continue
			}
			if isTimeoutError(readErr) {
				return RotateResponse{}, &Error{Kind: FailureTimeout}
			}
			return RotateResponse{}, &Error{Kind: FailureInvalidResponse, HTTPStatus: resp.StatusCode}
		}

		return ValidateRotateResponse(resp.StatusCode, body, request.Provider)
	}
	if isTimeoutError(lastTransport) {
		return RotateResponse{}, &Error{Kind: FailureTimeout}
	}
	return RotateResponse{}, &Error{Kind: FailureTransport}
}

var errResponseTooLarge = errors.New("response exceeds limit")

func readBounded(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = defaultResponseLimit
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errResponseTooLarge
	}
	return data, nil
}

// ValidateRotateResponse applies the shared microproxy contract to a
// successful HTTP response. It is used by both the direct HTTP client and the
// CDP transport so the two paths cannot diverge.
func ValidateRotateResponse(status int, body []byte, expectedProvider string) (RotateResponse, error) {
	if status != http.StatusOK {
		return RotateResponse{}, ClassifyHTTPFailure(status, body)
	}
	if int64(len(body)) > MaxResponseBytes {
		return RotateResponse{}, &Error{Kind: FailureInvalidResponse, HTTPStatus: status}
	}
	var decoded RotateResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return RotateResponse{}, &Error{Kind: FailureInvalidResponse, HTTPStatus: status}
	}
	if decoded.Status != "rotated" || decoded.Generation < 1 {
		return RotateResponse{}, &Error{Kind: FailureInvalidResponse, HTTPStatus: status}
	}
	if decoded.Provider != expectedProvider {
		return RotateResponse{}, &Error{Kind: FailureProviderMismatch, HTTPStatus: status}
	}
	return decoded, nil
}

// ClassifyHTTPFailure maps a definitive non-200 response to a sanitized
// proxycontrol error.
func ClassifyHTTPFailure(status int, body []byte) error {
	code := responseErrorCode(body)
	failure := &Error{Kind: FailureUnexpectedStatus, HTTPStatus: status, Code: code}
	switch status {
	case http.StatusBadRequest:
		failure.Kind = FailureInvalidRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		failure.Kind = FailureAuth
	case http.StatusConflict:
		if code == "stale_idempotency_key" {
			failure.Kind = FailureStaleKey
		} else {
			failure.Kind = FailureConflict
		}
	case http.StatusServiceUnavailable:
		failure.Kind = FailureUnavailable
	default:
		if status >= 500 {
			failure.Kind = FailureUnavailable
		}
	}
	return failure
}

func responseErrorCode(body []byte) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	code := strings.TrimSpace(envelope.Error.Code)
	if !validErrorCode(code) {
		return ""
	}
	return code
}

func validErrorCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, r := range code {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

func contextFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: FailureTimeout}
	}
	return &Error{Kind: FailureCancelled}
}

func waitBackoff(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var urlErr interface{ Timeout() bool }
	return errors.As(err, &urlErr) && urlErr.Timeout()
}

// NewIdempotencyKey returns a cryptographically random, job-boundary key that
// satisfies the data-plane 1..256 printable-ASCII contract.
func NewIdempotencyKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating idempotency key")
	}
	return "crowler-" + hex.EncodeToString(buf), nil
}
