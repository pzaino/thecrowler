package crawler

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-auxiliaries/selenium"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// fakeSetupDriver fakes the narrow WebDriver surface setupBrowser exercises.
// Unimplemented methods come from the embedded (nil) interface and panic if
// called, failing the test by design.
type fakeSetupDriver struct {
	selenium.WebDriver
	sessionID string
	cdpErr    error
	scriptErr error
}

func (f *fakeSetupDriver) SessionID() string { return f.sessionID }

func (f *fakeSetupDriver) ExecuteChromeDPCommand(string, map[string]interface{}) (interface{}, error) {
	if f.cdpErr != nil {
		return nil, f.cdpErr
	}
	return map[string]interface{}{"identifier": "test-id"}, nil
}

func (f *fakeSetupDriver) ExecuteScript(string, []interface{}) (interface{}, error) {
	if f.scriptErr != nil {
		return nil, f.scriptErr
	}
	return nil, nil
}

func (f *fakeSetupDriver) Get(string) error { return nil }

func setupTestContext(browserType string) *ProcessContext {
	return &ProcessContext{
		config: cfg.Config{
			Crawler: cfg.Crawler{
				Platform:           "desktop",
				BrowserPlatform:    "linux",
				ResetCookiesPolicy: "always",
			},
			Selenium: []cfg.Selenium{{Type: browserType}},
		},
		SelID: 0,
	}
}

func TestSetupBrowserPropagatesIdentityFailure(t *testing.T) {
	// Follow-up P1: identity-refresh failure on a CDP-capable browser must
	// reach the caller so navigation is refused. CDP fails here while the
	// JS fallback succeeds, isolating the refresh path.
	fake := &fakeSetupDriver{sessionID: "sess-setup", cdpErr: errors.New("boom")}
	ctx := setupTestContext("chrome")
	ctx.wd = fake
	if err := setupBrowser(fake, ctx); err == nil {
		t.Fatal("setupBrowser = nil, want identity-refresh error")
	} else if !strings.Contains(err.Error(), "browser identity") {
		t.Errorf("setupBrowser err = %v, want browser-identity context", err)
	}
}

func TestSetupBrowserFirefoxRefreshStaysBestEffort(t *testing.T) {
	// Firefox has no CDP bridge and therefore no registered script to go
	// stale: the same CDP failure must not block setup.
	fake := &fakeSetupDriver{sessionID: "sess-setup-ff", cdpErr: errors.New("boom")}
	ctx := setupTestContext("firefox")
	ctx.wd = fake
	if err := setupBrowser(fake, ctx); err != nil {
		t.Errorf("setupBrowser (firefox) = %v, want nil (best-effort refresh)", err)
	}
}

func TestSetupBrowserGuards(t *testing.T) {
	if err := setupBrowser(nil, setupTestContext("chrome")); err == nil {
		t.Error("setupBrowser(nil) = nil, want error")
	}
	ctx := setupTestContext("chrome")
	ctx.VDIReturned = true
	fake := &fakeSetupDriver{cdpErr: errors.New("boom"), scriptErr: errors.New("boom")}
	if err := setupBrowser(fake, ctx); err != nil {
		t.Errorf("setupBrowser (returned VDI) = %v, want nil short-circuit", err)
	}
}
