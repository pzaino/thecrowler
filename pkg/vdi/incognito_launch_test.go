package vdi

import (
	"sync"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func incognitoPtr(v bool) *bool { return &v }

// launchArgsFor is the shared helper: it runs the real builder used by
// ConnectVDI so the assertions exercise production code, not a copy.
func launchArgsFor(t *testing.T, browser string, incognito *bool) []string {
	t.Helper()
	return buildLaunchArgs(browser, browser, "test-user-agent", cfg.Crawler{Incognito: incognito}, "")
}

// TestBuildLaunchArgsIncognitoDefaultPreserved proves the historical behaviour:
// an absent source override still launches Chrome/Chromium in private mode.
func TestBuildLaunchArgsIncognitoDefaultPreserved(t *testing.T) {
	for _, browser := range []string{BrowserChrome, BrowserChromium} {
		args := launchArgsFor(t, browser, nil)
		if countFlag(args, "--incognito") != 1 {
			t.Fatalf("%s: --incognito must be present exactly once by default, args=%v", browser, args)
		}
	}
}

// TestBuildLaunchArgsIncognitoExplicitTrue proves an explicit true keeps the
// flag.
func TestBuildLaunchArgsIncognitoExplicitTrue(t *testing.T) {
	for _, browser := range []string{BrowserChrome, BrowserChromium} {
		args := launchArgsFor(t, browser, incognitoPtr(true))
		if countFlag(args, "--incognito") != 1 {
			t.Fatalf("%s: --incognito missing for explicit true, args=%v", browser, args)
		}
	}
}

// TestBuildLaunchArgsIncognitoExplicitFalse proves an explicit false removes the
// flag from the real launch arguments.
func TestBuildLaunchArgsIncognitoExplicitFalse(t *testing.T) {
	for _, browser := range []string{BrowserChrome, BrowserChromium} {
		args := launchArgsFor(t, browser, incognitoPtr(false))
		if countFlag(args, "--incognito") != 0 {
			t.Fatalf("%s: --incognito must be absent for explicit false, args=%v", browser, args)
		}
		// The rest of the Chrome hardening must not disappear with the flag.
		for _, want := range []string{"--disable-client-side-phishing-detection", "--no-first-run", "--disable-component-update"} {
			if _, ok := findFlag(args, want); !ok {
				t.Fatalf("%s: %s missing when incognito is disabled: %v", browser, want, args)
			}
		}
	}
}

// TestBuildLaunchArgsIncognitoNeverForFirefox proves the Chrome-only flag is
// never passed to Firefox, regardless of the source value.
func TestBuildLaunchArgsIncognitoNeverForFirefox(t *testing.T) {
	for _, value := range []*bool{nil, incognitoPtr(true), incognitoPtr(false)} {
		args := launchArgsFor(t, BrowserFirefox, value)
		if countFlag(args, "--incognito") != 0 {
			t.Fatalf("Firefox must never receive --incognito (value=%v), args=%v", value, args)
		}
	}
}

// TestBuildLaunchArgsConcurrentSourcesIsolated runs the builder concurrently
// with opposite settings and proves each call sees only its own value: the
// decision is derived from the argument, never from shared mutable state.
func TestBuildLaunchArgsConcurrentSourcesIsolated(t *testing.T) {
	const iterations = 200
	var wg sync.WaitGroup
	errs := make(chan string, iterations*2)

	for i := 0; i < iterations; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			args := launchArgsFor(t, BrowserChrome, incognitoPtr(true))
			if countFlag(args, "--incognito") != 1 {
				errs <- "private source lost --incognito"
			}
		}()
		go func() {
			defer wg.Done()
			args := launchArgsFor(t, BrowserChrome, incognitoPtr(false))
			if countFlag(args, "--incognito") != 0 {
				errs <- "normal source gained --incognito"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

// TestBuildLaunchArgsNonIncognitoStillHardened guards the compensating
// controls: a normal session keeps the automation-hygiene flags and the
// per-session VDI isolation contract (one browser session per source).
func TestBuildLaunchArgsNonIncognitoStillHardened(t *testing.T) {
	args := launchArgsFor(t, BrowserChrome, incognitoPtr(false))
	for _, want := range []string{
		"--disable-client-side-phishing-detection",
		"--disable-component-update",
		"--no-first-run",
		"--use-fake-ui-for-media-stream",
		"--disable-geolocation",
		"--disable-notifications",
	} {
		if _, ok := findFlag(args, want); !ok {
			t.Fatalf("%s missing in a non-incognito session: %v", want, args)
		}
	}
	if countFlag(args, "--user-data-dir") != 0 {
		t.Fatalf("the builder must not force a shared profile directory: %v", args)
	}
}
