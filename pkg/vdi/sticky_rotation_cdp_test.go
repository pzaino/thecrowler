package vdi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"github.com/pzaino/thecrowler/pkg/proxycontrol"
)

type fakeCDPSession struct {
	bootstrapErr error
	rotateStatus int
	rotateBody   []byte
	rotateErr    error
	quitErr      error

	bootstrapCalls int
	rotateCalls    int
	quitCalls      int
	lastOrigin     string
	lastEndpoint   string
	lastToken      string
	lastKey        string
}

func (f *fakeCDPSession) Bootstrap(_ context.Context, origin string) error {
	f.bootstrapCalls++
	f.lastOrigin = origin
	return f.bootstrapErr
}

func (f *fakeCDPSession) Rotate(_ context.Context, endpoint, token, key string) (int, []byte, error) {
	f.rotateCalls++
	f.lastEndpoint = endpoint
	f.lastToken = token
	f.lastKey = key
	return f.rotateStatus, f.rotateBody, f.rotateErr
}

func (f *fakeCDPSession) Quit(context.Context) error {
	f.quitCalls++
	return f.quitErr
}

type fakeCDPSessionFactory struct {
	session cdpStickySession
	safe    bool
	err     error
	calls   int
}

func (f *fakeCDPSessionFactory) new(context.Context, SeleniumInstance) (cdpStickySession, bool, error) {
	f.calls++
	if f.err != nil {
		return nil, f.safe, f.err
	}
	return f.session, f.safe, nil
}

func testCDPRotator(session cdpStickySession, safe bool, createErr error) (*CDPStickyRotator, *fakeCDPSessionFactory) {
	if session == nil {
		session = &fakeCDPSession{}
	}
	factory := &fakeCDPSessionFactory{session: session, safe: safe, err: createErr}
	rotator := NewCDPStickyRotator(SeleniumInstance{Config: cfg.Selenium{Name: "vdi-1", Type: "chrome"}})
	rotator.factory = factory.new
	rotator.sessionTimeout = 100 * time.Millisecond
	rotator.bootstrapTimeout = 100 * time.Millisecond
	rotator.rotateTimeout = 100 * time.Millisecond
	rotator.cleanupTimeout = 100 * time.Millisecond
	return rotator, factory
}

func validRotateBody() []byte {
	return []byte(`{"status":"rotated","provider":"p","generation":2}`)
}

func TestCDPRotatorSuccessClosesBrowserBeforeReturning(t *testing.T) {
	session := &fakeCDPSession{rotateStatus: http.StatusOK, rotateBody: validRotateBody()}
	rotator, factory := testCDPRotator(session, true, nil)
	response, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "token-1", IdempotencyKey: "key-1", Provider: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Generation != 2 {
		t.Fatalf("response = %+v", response)
	}
	if factory.calls != 1 || session.bootstrapCalls != 1 || session.rotateCalls != 1 || session.quitCalls != 1 {
		t.Fatalf("calls = factory:%d bootstrap:%d rotate:%d quit:%d",
			factory.calls, session.bootstrapCalls, session.rotateCalls, session.quitCalls)
	}
	if session.lastOrigin != "http://127.0.0.1:8094" {
		t.Fatalf("origin = %q", session.lastOrigin)
	}
	if session.lastEndpoint != "http://127.0.0.1:8094"+proxycontrol.RotatePath {
		t.Fatalf("endpoint = %q", session.lastEndpoint)
	}
	if session.lastToken != "token-1" || session.lastKey != "key-1" {
		t.Fatalf("credential binding lost")
	}
}

func TestCDPRotatorCleanupFailureIsUnsafe(t *testing.T) {
	session := &fakeCDPSession{rotateStatus: http.StatusOK, rotateBody: validRotateBody(), quitErr: errors.New("quit failed")}
	rotator, _ := testCDPRotator(session, true, nil)
	_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !errors.Is(err, ErrCDPStickyUnsafeCleanup) {
		t.Fatalf("error = %v, want unsafe cleanup", err)
	}
}

func TestCDPRotatorBootstrapFailureIsUnavailableAndCleansUp(t *testing.T) {
	session := &fakeCDPSession{bootstrapErr: errors.New("bootstrap failed")}
	rotator, _ := testCDPRotator(session, true, nil)
	_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !errors.Is(err, ErrCDPStickyUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if session.rotateCalls != 0 || session.quitCalls != 1 {
		t.Fatalf("rotate=%d quit=%d", session.rotateCalls, session.quitCalls)
	}
}

func TestCDPRotatorBootstrapCleanupFailureIsUnsafe(t *testing.T) {
	session := &fakeCDPSession{bootstrapErr: errors.New("bootstrap failed"), quitErr: errors.New("quit failed")}
	rotator, _ := testCDPRotator(session, true, nil)
	_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !errors.Is(err, ErrCDPStickyUnsafeCleanup) {
		t.Fatalf("error = %v, want unsafe cleanup", err)
	}
}

func TestCDPRotatorFetchFailureIsUncertainAndCleansUp(t *testing.T) {
	session := &fakeCDPSession{rotateErr: errors.New("network")}
	rotator, _ := testCDPRotator(session, true, nil)
	_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !errors.Is(err, ErrCDPStickyUncertain) {
		t.Fatalf("error = %v, want uncertain", err)
	}
	if session.quitCalls != 1 {
		t.Fatalf("quit calls = %d", session.quitCalls)
	}
}

func TestCDPRotatorDefinitiveAPIRejections(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   []byte
		kind   proxycontrol.FailureKind
	}{
		{"400", http.StatusBadRequest, nil, proxycontrol.FailureInvalidRequest},
		{"401", http.StatusUnauthorized, nil, proxycontrol.FailureAuth},
		{"403", http.StatusForbidden, nil, proxycontrol.FailureAuth},
		{"405", http.StatusMethodNotAllowed, nil, proxycontrol.FailureUnexpectedStatus},
		{"409 stale", http.StatusConflict, []byte(`{"error":{"code":"stale_idempotency_key"}}`), proxycontrol.FailureStaleKey},
		{"409 other", http.StatusConflict, nil, proxycontrol.FailureConflict},
		{"503", http.StatusServiceUnavailable, nil, proxycontrol.FailureUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			session := &fakeCDPSession{rotateStatus: tc.status, rotateBody: tc.body}
			rotator, _ := testCDPRotator(session, true, nil)
			_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
				Token: "t", IdempotencyKey: "k", Provider: "p",
			})
			if !proxycontrol.IsFailureKind(err, tc.kind) {
				t.Fatalf("error = %v, want kind %q", err, tc.kind)
			}
			if !proxycontrol.IsDefinitive(err) {
				t.Fatalf("error %v is not definitive", err)
			}
			if session.quitCalls != 1 {
				t.Fatalf("quit calls = %d", session.quitCalls)
			}
		})
	}
}

func TestCDPRotatorMalformedSuccessIsDefinitive(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"malformed", []byte(`{`)},
		{"wrong provider", []byte(`{"status":"rotated","provider":"q","generation":1}`)},
		{"zero generation", []byte(`{"status":"rotated","provider":"p","generation":0}`)},
		{"oversized", bytes.Repeat([]byte("x"), int(proxycontrol.MaxResponseBytes)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &fakeCDPSession{rotateStatus: http.StatusOK, rotateBody: tc.body}
			rotator, _ := testCDPRotator(session, true, nil)
			_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
				Token: "t", IdempotencyKey: "k", Provider: "p",
			})
			if !proxycontrol.IsDefinitive(err) {
				t.Fatalf("error = %v, want definitive", err)
			}
		})
	}
}

func TestCDPRotatorSessionCreationFallbackSafety(t *testing.T) {
	t.Run("safe creation failure is unavailable", func(t *testing.T) {
		rotator, factory := testCDPRotator(nil, true, errors.New("dial failed"))
		_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
			Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !errors.Is(err, ErrCDPStickyUnavailable) {
			t.Fatalf("error = %v", err)
		}
		if factory.calls != 1 {
			t.Fatalf("factory calls = %d", factory.calls)
		}
	})
	t.Run("unsafe creation failure is cleanup unsafe", func(t *testing.T) {
		rotator, _ := testCDPRotator(nil, false, errors.New("timeout"))
		_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
			Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !errors.Is(err, ErrCDPStickyUnsafeCleanup) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCDPRotatorCancellationDoesNotBecomeFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rotator, _ := testCDPRotator(nil, true, context.Canceled)
	_, err := rotator.Rotate(ctx, "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "t", IdempotencyKey: "k", Provider: "p",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestCDPHelpers(t *testing.T) {
	if got, err := cdpOrigin("HTTP://127.0.0.1:8094/"); err != nil || got != "http://127.0.0.1:8094" {
		t.Fatalf("cdpOrigin = %q err=%v", got, err)
	}
	if !sameOrigin("http://127.0.0.1:8094", "http://127.0.0.1:8094") {
		t.Fatal("same origin not recognized")
	}
	if sameOrigin("http://127.0.0.1:8094", "http://127.0.0.1:8095") {
		t.Fatal("different port treated as same origin")
	}
	if sameOrigin("http://127.0.0.1:8094", "chrome-error://chromewebdata/") {
		t.Fatal("chrome error page treated as same origin")
	}
	if !sameBootstrapURL("http://h:8094/x", "http://h:8094/x") {
		t.Fatal("bootstrap URL not matched")
	}
	if sameBootstrapURL("http://h:8094/x", "http://h:8094/y") {
		t.Fatal("redirected bootstrap URL matched")
	}
	if id, err := parseSessionID([]byte(`{"value":{"sessionId":"abc"}}`)); err != nil || id != "abc" {
		t.Fatalf("session id = %q err=%v", id, err)
	}
	if _, err := parseSessionID([]byte(`{"value":{}}`)); err == nil {
		t.Fatal("missing session id accepted")
	}
	if !safeSessionCreationFailure(&net.OpError{Op: "dial", Err: errors.New("connection refused")}) {
		t.Fatal("dial failure should be safe to fall back")
	}
	if safeSessionCreationFailure(context.DeadlineExceeded) {
		t.Fatal("deadline must not be treated as safe")
	}
}

func TestCDPRedactionMasksArgumentsAndKey(t *testing.T) {
	const token = "super-secret-token"
	const key = "super-secret-key"
	redacted := redactCDPParams(map[string]interface{}{
		"objectId":            "handle-1",
		"functionDeclaration": "async function() {}",
		"arguments": []interface{}{
			map[string]interface{}{"value": "http://127.0.0.1:8094/api/v1/sessions/rotate"},
			map[string]interface{}{"value": token},
			map[string]interface{}{"value": key},
		},
	})
	encoded := fmt.Sprint(redacted)
	if strings.Contains(encoded, token) || strings.Contains(encoded, key) {
		t.Fatalf("redacted CDP params leaked credentials: %v", redacted)
	}
}

func TestCDPHTTPBackendProtocolFlow(t *testing.T) {
	var (
		mu            sync.Mutex
		sessionCreate map[string]interface{}
		bootstrapURL  string
		rotateArgs    []interface{}
		deleted       bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/wd/hub/session":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &sessionCreate)
			fmt.Fprint(w, `{"value":{"sessionId":"sess-1"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/wd/hub/session/sess-1/url":
			var payload struct {
				URL string `json:"url"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &payload)
			bootstrapURL = payload.URL
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodGet && r.URL.Path == "/wd/hub/session/sess-1/url":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"value": bootstrapURL})
		case r.Method == http.MethodPost && r.URL.Path == "/wd/hub/session/sess-1/goog/cdp/execute":
			var payload struct {
				Cmd    string                 `json:"cmd"`
				Params map[string]interface{} `json:"params"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &payload)
			if payload.Cmd == "Runtime.evaluate" {
				switch payload.Params["expression"] {
				case "globalThis.location.origin":
					origin := "http://" + r.Host
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"value": map[string]interface{}{
							"result": map[string]interface{}{"type": "string", "value": origin},
						},
					})
				case "globalThis":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"value": map[string]interface{}{
							"result": map[string]interface{}{"type": "object", "objectId": "obj-1"},
						},
					})
				default:
					t.Errorf("unexpected Runtime.evaluate expression %v", payload.Params["expression"])
				}
				return
			}
			if payload.Cmd != "Runtime.callFunctionOn" {
				t.Errorf("unexpected CDP command %q", payload.Cmd)
				return
			}
			rotateArgs, _ = payload.Params["arguments"].([]interface{})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"value": map[string]interface{}{
					"result": map[string]interface{}{
						"type": "object",
						"value": map[string]interface{}{
							"status": 200,
							"body":   `{"status":"rotated","provider":"p","generation":1}`,
							"error":  "",
						},
					},
				},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/wd/hub/session/sess-1":
			deleted = true
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected Selenium fixture request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(parsed.Port())
	sel := SeleniumInstance{Config: cfg.Selenium{
		Name: "vdi-1", Type: "chrome", Host: parsed.Hostname(), Port: port, SSLMode: "disable",
	}}
	rotator := NewCDPStickyRotator(sel)
	response, err := rotator.Rotate(context.Background(), server.URL, proxycontrol.RotateRequest{
		Token: "protocol-token", IdempotencyKey: "protocol-key", Provider: "p",
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if response.Generation != 1 {
		t.Fatalf("response = %+v", response)
	}
	mu.Lock()
	defer mu.Unlock()
	if sessionCreate == nil {
		t.Fatal("session creation request was not observed")
	}
	caps, _ := sessionCreate["capabilities"].(map[string]interface{})
	always, _ := caps["alwaysMatch"].(map[string]interface{})
	if always["browserName"] != "chrome" {
		t.Fatalf("browserName = %v", always["browserName"])
	}
	options, _ := always["goog:chromeOptions"].(map[string]interface{})
	args, _ := options["args"].([]interface{})
	joined := fmt.Sprint(args)
	if !strings.Contains(joined, "--no-proxy-server") {
		t.Fatalf("management browser args missing proxy suppression: %v", args)
	}
	if strings.Contains(joined, "--no-sandbox") || strings.Contains(joined, "--disable-web-security") {
		t.Fatalf("management browser weakened security: %v", args)
	}
	if bootstrapURL != server.URL+cdpBootstrapPath {
		t.Fatalf("bootstrap URL = %q", bootstrapURL)
	}
	if len(rotateArgs) != 3 {
		t.Fatalf("rotate arguments = %v", rotateArgs)
	}
	if rotateArgs[1].(map[string]interface{})["value"] != "protocol-token" ||
		rotateArgs[2].(map[string]interface{})["value"] != "protocol-key" {
		t.Fatalf("rotate arguments lost credentials")
	}
	if !deleted {
		t.Fatal("temporary browser session was not deleted")
	}
}

type blockingCDPSession struct {
	blockBootstrap bool
	blockRotate    bool
	quitCalls      int
}

func (b *blockingCDPSession) Bootstrap(ctx context.Context, _ string) error {
	if b.blockBootstrap {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (b *blockingCDPSession) Rotate(ctx context.Context, _, _, _ string) (int, []byte, error) {
	if b.blockRotate {
		<-ctx.Done()
		return 0, nil, ctx.Err()
	}
	return http.StatusOK, validRotateBody(), nil
}

func (b *blockingCDPSession) Quit(context.Context) error {
	b.quitCalls++
	return nil
}

func TestCDPRotatorCancellationDuringStagesCleansUp(t *testing.T) {
	t.Run("bootstrap", func(t *testing.T) {
		session := &blockingCDPSession{blockBootstrap: true}
		rotator, _ := testCDPRotator(session, true, nil)
		rotator.bootstrapTimeout = 50 * time.Millisecond
		_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
			Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !errors.Is(err, ErrCDPStickyUnavailable) {
			t.Fatalf("error = %v, want unavailable", err)
		}
		if session.quitCalls != 1 {
			t.Fatalf("quit calls = %d", session.quitCalls)
		}
	})
	t.Run("rotate", func(t *testing.T) {
		session := &blockingCDPSession{blockRotate: true}
		rotator, _ := testCDPRotator(session, true, nil)
		rotator.rotateTimeout = 50 * time.Millisecond
		_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
			Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !errors.Is(err, ErrCDPStickyUncertain) {
			t.Fatalf("error = %v, want uncertain", err)
		}
		if session.quitCalls != 1 {
			t.Fatalf("quit calls = %d", session.quitCalls)
		}
	})
	t.Run("session creation", func(t *testing.T) {
		rotator, _ := testCDPRotator(nil, true, nil)
		rotator.sessionTimeout = 50 * time.Millisecond
		rotator.factory = func(ctx context.Context, _ SeleniumInstance) (cdpStickySession, bool, error) {
			<-ctx.Done()
			return nil, false, ctx.Err()
		}
		_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
			Token: "t", IdempotencyKey: "k", Provider: "p",
		})
		if !errors.Is(err, ErrCDPStickyUnsafeCleanup) {
			t.Fatalf("error = %v, want unsafe cleanup", err)
		}
	})
}

func TestCDPRotatorDebug5DoesNotLogSecrets(t *testing.T) {
	var buf bytes.Buffer
	oldWriter := log.Writer()
	oldLevel := cmn.GetDebugLevel()
	log.SetOutput(&buf)
	cmn.SetDebugLevel(cmn.DbgLvlDebug5)
	defer func() {
		log.SetOutput(oldWriter)
		cmn.SetDebugLevel(oldLevel)
	}()

	session := &fakeCDPSession{rotateStatus: http.StatusOK, rotateBody: validRotateBody()}
	rotator, _ := testCDPRotator(session, true, nil)
	_, err := rotator.Rotate(context.Background(), "http://127.0.0.1:8094", proxycontrol.RotateRequest{
		Token: "debug5-secret-token", IdempotencyKey: "debug5-secret-key", Provider: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "debug5-secret-token") || strings.Contains(buf.String(), "debug5-secret-key") {
		t.Fatalf("Debug5 log leaked credentials: %s", buf.String())
	}
}
