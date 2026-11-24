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

package vdi

// CDP-first microproxy sticky-session rotation through a disposable browser on
// the already leased VDI. The temporary browser is created on the VDI's
// Selenium endpoint, bootstrapped on the trusted microproxy management origin,
// and closed before the source browser is allowed to start.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-auxiliaries/selenium"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
)

const (
	cdpBootstrapPath = "/__crowler_rotation_bootstrap"

	cdpSessionStageTimeout   = 60 * time.Second
	cdpBootstrapStageTimeout = 20 * time.Second
	cdpRotateStageTimeout    = 25 * time.Second
	cdpCleanupStageTimeout   = 10 * time.Second

	cdpControlResponseLimit = int64(1024 * 1024)
)

var (
	// ErrCDPStickyUnavailable means the CDP route could not be used or could
	// not reach the browser-visible management origin before any credential
	// was sent. The caller may use the engine-direct backup.
	ErrCDPStickyUnavailable = errors.New("CDP sticky rotation unavailable")
	// ErrCDPStickyUncertain means the browser may have sent the rotate POST but
	// no authoritative response was obtained. The caller may retry the exact
	// same key through the engine-direct backup.
	ErrCDPStickyUncertain = errors.New("CDP sticky rotation outcome uncertain")
	// ErrCDPStickyUnsafeCleanup means the temporary browser could not be
	// proven closed. The VDI must not be released for reuse.
	ErrCDPStickyUnsafeCleanup = errors.New("CDP temporary browser cleanup failed")
)

// SupportsCDPStickyRotation reports whether the selected VDI can use the
// Chrome DevTools Protocol management path. An empty type keeps the historical
// default of Chrome.
func SupportsCDPStickyRotation(sel SeleniumInstance) bool {
	switch strings.ToLower(strings.TrimSpace(sel.Config.Type)) {
	case "", BrowserChrome, BrowserChromium:
		return true
	default:
		return false
	}
}

// cdpStickySession is the narrow disposable-browser backend. Unit tests
// replace it with a deterministic fake.
type cdpStickySession interface {
	Bootstrap(ctx context.Context, origin string) error
	Rotate(ctx context.Context, endpoint, token, idempotencyKey string) (int, []byte, error)
	Quit(ctx context.Context) error
}

// cdpSessionFactory returns a disposable session. safeToFallback is true only
// when no server-side session can have been created by the failed attempt.
type cdpSessionFactory func(ctx context.Context, sel SeleniumInstance) (cdpStickySession, bool, error)

// CDPStickyRotator performs one CDP rotation on a disposable browser belonging
// to the leased VDI. It never creates a source browser.
type CDPStickyRotator struct {
	sel     SeleniumInstance
	factory cdpSessionFactory

	sessionTimeout   time.Duration
	bootstrapTimeout time.Duration
	rotateTimeout    time.Duration
	cleanupTimeout   time.Duration
}

// NewCDPStickyRotator returns the production CDP rotator for the leased VDI.
func NewCDPStickyRotator(sel SeleniumInstance) *CDPStickyRotator {
	return &CDPStickyRotator{
		sel:              sel,
		factory:          newHTTPCDPStickySession,
		sessionTimeout:   cdpSessionStageTimeout,
		bootstrapTimeout: cdpBootstrapStageTimeout,
		rotateTimeout:    cdpRotateStageTimeout,
		cleanupTimeout:   cdpCleanupStageTimeout,
	}
}

// Rotate runs the disposable browser, validates the microproxy response and
// closes the browser before returning. Definitive API rejections are returned
// as proxycontrol errors; only unavailable/uncertain CDP outcomes are eligible
// for the caller's direct fallback.
func (r *CDPStickyRotator) Rotate(ctx context.Context, apiURL string, request proxycontrol.RotateRequest) (proxycontrol.RotateResponse, error) {
	if r == nil {
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: rotator is nil", ErrCDPStickyUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	origin, err := cdpOrigin(apiURL)
	if err != nil {
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: %v", ErrCDPStickyUnavailable, err)
	}

	session, safe, createErr := r.createSession(ctx)
	if createErr != nil {
		if ctx.Err() != nil && safe {
			return proxycontrol.RotateResponse{}, ctx.Err()
		}
		if safe {
			return proxycontrol.RotateResponse{}, fmt.Errorf("%w: creating disposable browser: %v", ErrCDPStickyUnavailable, createErr)
		}
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: creating disposable browser: %v", ErrCDPStickyUnsafeCleanup, createErr)
	}
	if session == nil {
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: disposable browser factory returned no session", ErrCDPStickyUnavailable)
	}

	bootstrapCtx, cancel := cdpStageContext(ctx, r.bootstrapTimeout)
	started := time.Now()
	err = session.Bootstrap(bootstrapCtx, origin)
	cancel()
	logCDPStage(r.sel.Config.Name, "bootstrap", started, err)
	if err != nil {
		return r.cleanupAfterError(ctx, session, ErrCDPStickyUnavailable, "bootstrap", err)
	}

	rotateCtx, cancel := cdpStageContext(ctx, r.rotateTimeout)
	started = time.Now()
	status, body, rotateErr := session.Rotate(rotateCtx, origin+proxycontrol.RotatePath, request.Token, request.IdempotencyKey)
	cancel()
	logCDPStage(r.sel.Config.Name, "rotate", started, rotateErr)
	parentErr := ctx.Err()
	cleanupErr := r.cleanup(session)
	if cleanupErr != nil {
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: %v", ErrCDPStickyUnsafeCleanup, cleanupErr)
	}
	if parentErr != nil {
		return proxycontrol.RotateResponse{}, parentErr
	}
	if rotateErr != nil {
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: %v", ErrCDPStickyUncertain, rotateErr)
	}
	if status != http.StatusOK {
		return proxycontrol.RotateResponse{}, proxycontrol.ClassifyHTTPFailure(status, body)
	}
	return proxycontrol.ValidateRotateResponse(status, body, request.Provider)
}

func (r *CDPStickyRotator) createSession(ctx context.Context) (cdpStickySession, bool, error) {
	factory := r.factory
	if factory == nil {
		factory = newHTTPCDPStickySession
	}
	stageCtx, cancel := cdpStageContext(ctx, r.sessionTimeout)
	defer cancel()
	started := time.Now()
	session, safe, err := factory(stageCtx, r.sel)
	logCDPStage(r.sel.Config.Name, "session", started, err)
	return session, safe, err
}

func (r *CDPStickyRotator) cleanupAfterError(ctx context.Context, session cdpStickySession, kind error, stage string, stageErr error) (proxycontrol.RotateResponse, error) {
	parentErr := ctx.Err()
	cleanupErr := r.cleanup(session)
	if cleanupErr != nil {
		return proxycontrol.RotateResponse{}, fmt.Errorf("%w: %v", ErrCDPStickyUnsafeCleanup, cleanupErr)
	}
	if parentErr != nil {
		return proxycontrol.RotateResponse{}, parentErr
	}
	return proxycontrol.RotateResponse{}, fmt.Errorf("%w: %s: %v", kind, stage, stageErr)
}

func (r *CDPStickyRotator) cleanup(session cdpStickySession) error {
	if session == nil {
		return nil
	}
	// Cleanup deliberately detaches from the cancelled source/rotation context:
	// the temporary browser must still be closed (or the failure surfaced) even
	// when the crawl was superseded. It remains bounded by its own short timeout.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), r.cleanupTimeout)
	defer cancel()
	started := time.Now()
	err := session.Quit(cleanupCtx)
	logCDPStage(r.sel.Config.Name, "cleanup", started, err)
	return err
}

func cdpStageContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func logCDPStage(vdiName, stage string, started time.Time, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "failed"
	}
	cmn.DebugMsg(cmn.DbgLvlDebug2,
		"[sticky-cdp] VDI '%s' stage '%s' elapsed=%s outcome=%s",
		vdiName, stage, time.Since(started).Round(time.Millisecond), outcome)
}

// cdpOrigin normalizes the browser-visible management origin.
func cdpOrigin(raw string) (string, error) {
	normalized, err := cfg.NormalizeStickyAPIURL(raw)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid CDP management origin")
	}
	return originKey(parsed.Scheme, parsed.Hostname(), parsed.Port())
}

func originKey(scheme, host, port string) (string, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	host = strings.ToLower(strings.TrimSpace(host))
	port = strings.TrimSpace(port)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported CDP management origin scheme")
	}
	if host == "" {
		return "", fmt.Errorf("CDP management origin requires a host")
	}
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

func sameOrigin(configured, observed string) bool {
	configuredKey, err := originFromURL(configured)
	if err != nil {
		return false
	}
	observedKey, err := originFromURL(observed)
	if err != nil {
		return false
	}
	return configuredKey == observedKey
}

func originFromURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid origin")
	}
	return originKey(parsed.Scheme, parsed.Hostname(), parsed.Port())
}

// httpCDPStickySession is the production disposable-browser backend. It talks
// to the VDI's Selenium remote endpoint directly so every request is bounded by
// the caller's context and cleanup can always be attempted.
type httpCDPStickySession struct {
	baseURL   string
	sessionID string
	client    *http.Client
}

func newHTTPCDPStickySession(ctx context.Context, sel SeleniumInstance) (cdpStickySession, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if !SupportsCDPStickyRotation(sel) {
		return nil, true, fmt.Errorf("browser does not support CDP rotation")
	}
	browser := strings.ToLower(strings.TrimSpace(sel.Config.Type))
	if browser == "" {
		browser = BrowserChrome
	}
	baseURL, err := seleniumBaseURL(sel)
	if err != nil {
		return nil, true, err
	}
	client := clonedSeleniumHTTPClient()
	session := &httpCDPStickySession{baseURL: baseURL, client: client}

	payload := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": map[string]interface{}{
				"browserName": webdriverBrowserName(browser),
				"goog:chromeOptions": map[string]interface{}{
					"args": []string{
						"--headless=new",
						"--no-proxy-server",
						"--disable-gpu",
						"--disable-dev-shm-usage",
						"--disable-extensions",
						"--disable-background-networking",
						"--disable-sync",
						"--no-first-run",
						"--mute-audio",
					},
				},
			},
			"firstMatch": []interface{}{map[string]interface{}{}},
		},
	}
	status, body, err := session.postJSON(ctx, session.baseURL+"/session", payload)
	if err != nil {
		// The Selenium control request carries no credentials in its URL or
		// error text; retain the transport cause so a VDI namespace or DNS
		// problem is diagnosable without exposing the rotation token.
		return nil, safeSessionCreationFailure(err), fmt.Errorf("creating disposable browser session: %v", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, true, fmt.Errorf("creating disposable browser session: HTTP %d", status)
	}
	sessionID, err := parseSessionID(body)
	if err != nil {
		// A 2xx without a usable session id may still have created a browser.
		return nil, false, fmt.Errorf("parsing disposable browser session: %w", err)
	}
	session.sessionID = sessionID
	return session, true, nil
}

func clonedSeleniumHTTPClient() *http.Client {
	base := selenium.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	cloned := *base
	cloned.Timeout = 0 // all requests carry an explicit context deadline
	cloned.Jar = nil
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cloned
}

func seleniumBaseURL(sel SeleniumInstance) (string, error) {
	host := strings.TrimSpace(sel.Config.Host)
	if host == "" || sel.Config.Port < 1 || sel.Config.Port > 65535 {
		return "", fmt.Errorf("invalid VDI Selenium endpoint")
	}
	scheme := cfg.SeleniumEndpointScheme(sel.Config.SSLMode)
	return fmt.Sprintf("%s://%s/wd/hub", scheme, net.JoinHostPort(host, strconv.Itoa(sel.Config.Port))), nil
}

func safeSessionCreationFailure(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		// A dial failure (including a dial timeout) proves no session reached
		// the server. A read/write failure after dialing may have created one.
		return netErr.Op == "dial"
	}
	return false
}

func parseSessionID(body []byte) (string, error) {
	var envelope struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", err
	}
	if envelope.Value.SessionID != "" {
		return envelope.Value.SessionID, nil
	}
	if envelope.SessionID != "" {
		return envelope.SessionID, nil
	}
	return "", fmt.Errorf("missing session id")
}

func (s *httpCDPStickySession) Bootstrap(ctx context.Context, origin string) error {
	if s == nil || s.sessionID == "" {
		return fmt.Errorf("disposable browser session is not initialized")
	}
	bootstrapURL := origin + cdpBootstrapPath
	if err := s.navigate(ctx, bootstrapURL); err != nil {
		return err
	}
	current, err := s.currentURL(ctx)
	if err != nil {
		return err
	}
	if !sameOrigin(origin, current) {
		return fmt.Errorf("bootstrap navigation left the management origin")
	}
	if !sameBootstrapURL(bootstrapURL, current) {
		return fmt.Errorf("bootstrap navigation was redirected")
	}
	pageOrigin, err := s.evaluateOrigin(ctx)
	if err != nil {
		return err
	}
	if !sameOrigin(origin, pageOrigin) {
		return fmt.Errorf("bootstrap document origin mismatch")
	}
	return nil
}

func sameBootstrapURL(expected, observed string) bool {
	expectedURL, err := normalizeDocumentURL(expected)
	if err != nil {
		return false
	}
	observedURL, err := normalizeDocumentURL(observed)
	if err != nil {
		return false
	}
	return expectedURL == observedURL
}

func normalizeDocumentURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid document URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("document URL must not contain query or fragment")
	}
	key, err := originKey(parsed.Scheme, parsed.Hostname(), parsed.Port())
	if err != nil {
		return "", err
	}
	return key + parsed.EscapedPath(), nil
}

func (s *httpCDPStickySession) Rotate(ctx context.Context, endpoint, token, idempotencyKey string) (int, []byte, error) {
	if s == nil || s.sessionID == "" {
		return 0, nil, fmt.Errorf("disposable browser session is not initialized")
	}
	objectID, err := s.globalObjectID(ctx)
	if err != nil {
		return 0, nil, err
	}
	result, err := s.executeCDP(ctx, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId":            objectID,
		"functionDeclaration": cdpRotateFunction,
		"arguments": []interface{}{
			map[string]interface{}{"value": endpoint},
			map[string]interface{}{"value": token},
			map[string]interface{}{"value": idempotencyKey},
		},
		"awaitPromise":  true,
		"returnByValue": true,
	})
	if err != nil {
		return 0, nil, err
	}
	if exceptionDetails(result) {
		return 0, nil, fmt.Errorf("rotate JavaScript raised an exception")
	}
	resultMap, _ := result.(map[string]interface{})
	resultValue, _ := resultMap["result"].(map[string]interface{})
	value, _ := resultValue["value"].(map[string]interface{})
	if value == nil {
		return 0, nil, fmt.Errorf("rotate JavaScript returned an invalid result")
	}
	category, _ := value["error"].(string)
	statusValue, _ := value["status"].(float64)
	status := int(statusValue)
	body, _ := value["body"].(string)
	switch category {
	case "":
		if status < 100 || status > 599 {
			return 0, nil, fmt.Errorf("rotate JavaScript returned an invalid HTTP status")
		}
		return status, []byte(body), nil
	case "oversize":
		if status < 100 || status > 599 {
			return 0, nil, fmt.Errorf("rotate JavaScript returned an invalid HTTP status")
		}
		return status, nil, nil
	default:
		return 0, nil, fmt.Errorf("rotate browser fetch failed: %s", category)
	}
}

func (s *httpCDPStickySession) Quit(ctx context.Context) error {
	if s == nil || s.sessionID == "" {
		return nil
	}
	status, _, err := s.request(ctx, http.MethodDelete, s.baseURL+"/session/"+s.sessionID, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		// The server already has no such session; the browser is gone.
		s.sessionID = ""
		return nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("deleting disposable browser session returned HTTP %d", status)
	}
	s.sessionID = ""
	return nil
}

func (s *httpCDPStickySession) navigate(ctx context.Context, target string) error {
	status, _, err := s.postJSON(ctx, s.baseURL+"/session/"+s.sessionID+"/url", map[string]interface{}{"url": target})
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("bootstrap navigation returned HTTP %d", status)
	}
	return nil
}

func (s *httpCDPStickySession) currentURL(ctx context.Context) (string, error) {
	status, body, err := s.request(ctx, http.MethodGet, s.baseURL+"/session/"+s.sessionID+"/url", nil)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", fmt.Errorf("current URL query returned HTTP %d", status)
	}
	var envelope struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parsing current URL: %w", err)
	}
	return envelope.Value, nil
}

func (s *httpCDPStickySession) evaluateOrigin(ctx context.Context) (string, error) {
	result, err := s.executeCDP(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression":    "globalThis.location.origin",
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	if exceptionDetails(result) {
		return "", fmt.Errorf("origin evaluation raised an exception")
	}
	resultMap, _ := result.(map[string]interface{})
	resultValue, _ := resultMap["result"].(map[string]interface{})
	origin, _ := resultValue["value"].(string)
	if origin == "" {
		return "", fmt.Errorf("origin evaluation returned no string origin")
	}
	return origin, nil
}

func (s *httpCDPStickySession) globalObjectID(ctx context.Context) (string, error) {
	result, err := s.executeCDP(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression":    "globalThis",
		"returnByValue": false,
	})
	if err != nil {
		return "", err
	}
	if exceptionDetails(result) {
		return "", fmt.Errorf("global object evaluation raised an exception")
	}
	resultMap, _ := result.(map[string]interface{})
	resultValue, _ := resultMap["result"].(map[string]interface{})
	objectID, _ := resultValue["objectId"].(string)
	if objectID == "" {
		return "", fmt.Errorf("global object evaluation returned no object id")
	}
	return objectID, nil
}

func (s *httpCDPStickySession) executeCDP(ctx context.Context, command string, params map[string]interface{}) (interface{}, error) {
	payload := map[string]interface{}{"cmd": command, "params": params}
	status, body, err := s.postJSON(ctx, s.baseURL+"/session/"+s.sessionID+"/goog/cdp/execute", payload)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("CDP command returned HTTP %d", status)
	}
	var envelope struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parsing CDP response: %w", err)
	}
	if len(envelope.Value) == 0 {
		return nil, fmt.Errorf("CDP response contained no value")
	}
	var value interface{}
	if err := json.Unmarshal(envelope.Value, &value); err != nil {
		return nil, fmt.Errorf("parsing CDP value: %w", err)
	}
	return value, nil
}

func exceptionDetails(value interface{}) bool {
	resultMap, _ := value.(map[string]interface{})
	details, ok := resultMap["exceptionDetails"]
	return ok && details != nil
}

func (s *httpCDPStickySession) postJSON(ctx context.Context, target string, payload interface{}) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	return s.request(ctx, http.MethodPost, target, body)
}

func (s *httpCDPStickySession) request(ctx context.Context, method, target string, body []byte) (int, []byte, error) {
	if s == nil || s.client == nil {
		return 0, nil, fmt.Errorf("disposable browser HTTP client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, cdpControlResponseLimit+1))
	if err != nil {
		return 0, nil, err
	}
	if int64(len(data)) > cdpControlResponseLimit {
		return 0, nil, fmt.Errorf("disposable browser response exceeds limit")
	}
	return resp.StatusCode, data, nil
}

const cdpRotateFunction = `async function(endpoint, token, key) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 15000);
  try {
    const response = await fetch(endpoint, {
      method: 'POST',
      mode: 'same-origin',
      credentials: 'omit',
      cache: 'no-store',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      headers: {
        'Authorization': 'Bearer ' + token,
        'Idempotency-Key': key
      },
      signal: controller.signal
    });
    let body = '';
    if (response.body && response.body.getReader) {
      const reader = response.body.getReader();
      const chunks = [];
      let total = 0;
      for (;;) {
        const part = await reader.read();
        if (part.done) break;
        total += part.value.byteLength;
        if (total > 4096) {
          try { await reader.cancel(); } catch (e) {}
          return { status: response.status, body: '', error: 'oversize' };
        }
        chunks.push(part.value);
      }
      const merged = new Uint8Array(total);
      let offset = 0;
      for (const chunk of chunks) {
        merged.set(chunk, offset);
        offset += chunk.byteLength;
      }
      body = new TextDecoder('utf-8', { fatal: false }).decode(merged);
    } else {
      body = await response.text();
      if (new TextEncoder().encode(body).length > 4096) {
        return { status: response.status, body: '', error: 'oversize' };
      }
    }
    return { status: response.status, body: body, error: '' };
  } catch (e) {
    const name = e && e.name ? String(e.name) : '';
    if (name === 'AbortError') {
      return { status: 0, body: '', error: 'timeout' };
    }
    return { status: 0, body: '', error: 'transport' };
  } finally {
    clearTimeout(timer);
  }
}`
