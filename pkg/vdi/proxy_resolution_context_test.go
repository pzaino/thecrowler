package vdi

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type fakeProxyContext struct {
	source       *cdb.Source
	config       *cfg.Config
	instance     SeleniumInstance
	wd           WebDriver
	closed       bool
	returned     bool
	operationMu  sync.Mutex
	effective    string
	effectiveSet bool
	effectiveSrc string
}

func (f *fakeProxyContext) GetWebDriver() *WebDriver          { return &f.wd }
func (f *fakeProxyContext) GetConfig() *cfg.Config            { return f.config }
func (f *fakeProxyContext) GetSource() *cdb.Source            { return f.source }
func (f *fakeProxyContext) GetVDIClosedFlag() *bool           { return &f.closed }
func (f *fakeProxyContext) SetVDIClosedFlag(value bool)       { f.closed = value }
func (f *fakeProxyContext) GetVDIOperationMutex() *sync.Mutex { return &f.operationMu }
func (f *fakeProxyContext) GetVDIReturnedFlag() *bool         { return &f.returned }
func (f *fakeProxyContext) SetVDIReturnedFlag(value bool)     { f.returned = value }
func (f *fakeProxyContext) GetVDIInstance() *SeleniumInstance { return &f.instance }
func (f *fakeProxyContext) GetVDIAbortError() error           { return nil }
func (f *fakeProxyContext) GetEffectiveProxy() (string, bool) { return f.effective, f.effectiveSet }
func (f *fakeProxyContext) GetEffectiveProxySource() string   { return f.effectiveSrc }

func newFakeProxyContext(raw string, sel SeleniumInstance) *fakeProxyContext {
	var source *cdb.Source
	if raw != "" {
		message := json.RawMessage(raw)
		source = &cdb.Source{Config: &message}
	}
	config := &cfg.Config{Selenium: []cfg.Selenium{sel.Config}}
	return &fakeProxyContext{source: source, config: config, instance: sel}
}

func TestResolveSessionProxyUsesStoredSourceBoundaryDecision(t *testing.T) {
	sel := SeleniumInstance{Config: cfg.Selenium{Name: "vdi-1", Type: "chrome", ProxyURL: "http://vdi:8080"}}
	ctx := newFakeProxyContext(`{"crawling_config":{"proxy":"http://legacy-canonical:9999"}}`, sel)
	ctx.effective = "http://stored:3128"
	ctx.effectiveSet = true
	ctx.effectiveSrc = string(cfg.ProxySourceSticky)

	got, provenance, err := resolveSessionProxy(ctx, ctx.config, sel)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://stored:3128" || provenance != string(cfg.ProxySourceSticky) {
		t.Fatalf("resolved (%q,%q), want the stored sticky decision", got, provenance)
	}
	// Reconnect/ResetVDI paths must reuse the same decision without a new
	// rotation or a fallback to sel.Config.ProxyURL.
	again, againProvenance, err := resolveSessionProxy(ctx, ctx.config, sel)
	if err != nil {
		t.Fatal(err)
	}
	if again != got || againProvenance != provenance {
		t.Fatalf("second resolve = (%q,%q), want (%q,%q)", again, againProvenance, got, provenance)
	}
}

func TestResolveSessionProxyFallbackLayers(t *testing.T) {
	sel := SeleniumInstance{Config: cfg.Selenium{Name: "vdi-1", Type: "chrome", ProxyURL: "http://vdi:8080"}}

	t.Run("canonical", func(t *testing.T) {
		ctx := newFakeProxyContext(`{"crawling_config":{"proxy":"http://canonical:3128"}}`, sel)
		got, provenance, err := resolveSessionProxy(ctx, ctx.config, sel)
		if err != nil {
			t.Fatal(err)
		}
		if got != "http://canonical:3128" || provenance != string(cfg.ProxySourceCanonical) {
			t.Fatalf("resolved (%q,%q)", got, provenance)
		}
	})

	t.Run("legacy", func(t *testing.T) {
		ctx := newFakeProxyContext(`{"proxy_url":"http://legacy:8888"}`, sel)
		got, provenance, err := resolveSessionProxy(ctx, ctx.config, sel)
		if err != nil {
			t.Fatal(err)
		}
		if got != "http://legacy:8888" || provenance != string(cfg.ProxySourceLegacy) {
			t.Fatalf("resolved (%q,%q)", got, provenance)
		}
	})

	t.Run("empty stored config", func(t *testing.T) {
		ctx := newFakeProxyContext("   ", sel)
		got, provenance, err := resolveSessionProxy(ctx, ctx.config, sel)
		if err != nil {
			t.Fatal(err)
		}
		if got != "http://vdi:8080" || provenance != string(cfg.ProxySourceVDI) {
			t.Fatalf("resolved (%q,%q), want the VDI default", got, provenance)
		}
	})
}

func TestResolveSessionProxyStickyGuardAndBrowserCompatibility(t *testing.T) {
	t.Run("sticky without source-start boundary", func(t *testing.T) {
		sel := SeleniumInstance{Config: cfg.Selenium{Name: "vdi-1", Type: "chrome"}}
		ctx := newFakeProxyContext(`{"crawling_config":{"sticky_session":true,"site":"https://example.test/"}}`, sel)
		if _, _, err := resolveSessionProxy(ctx, ctx.config, sel); err == nil {
			t.Fatal("sticky source bypassed the source-start boundary")
		}
	})

	t.Run("firefox HTTPS listener", func(t *testing.T) {
		sel := SeleniumInstance{Config: cfg.Selenium{Name: "vdi-1", Type: "firefox"}}
		ctx := newFakeProxyContext(`{"crawling_config":{"proxy":"https://proxy:8443"}}`, sel)
		_, _, err := resolveSessionProxy(ctx, ctx.config, sel)
		if err == nil || !strings.Contains(err.Error(), "firefox") {
			t.Fatalf("error = %v, want Firefox HTTPS rejection", err)
		}
	})
}
