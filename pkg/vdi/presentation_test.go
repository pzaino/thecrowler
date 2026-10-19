package vdi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// findCheck is a small helper to pull one check's result out of an assessment.
func findCheck(a ConsistencyAssessment, name string) (ConsistencyResult, bool) {
	for _, r := range a.Results {
		if r.Check == name {
			return r, true
		}
	}
	return ConsistencyResult{}, false
}

// consistentProfile builds a fully-consistent report for a Windows/132 desktop
// Chromium session observed on a browser that reports the matching identity.
func consistentProfile(t *testing.T) PresentationReport {
	t.Helper()
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64 x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	ident := MapAdvertisedOSIdentity("windows", false)
	rep := BuildPresentationReport(PresentationInput{
		BrowserType:       "chrome",
		UserAgent:         ua,
		Product:           "chrome",
		Family:            "chromium",
		ResolvedOS:        "windows",
		DeviceClass:       "desktop",
		AdvertisedOS:      "windows",
		Language:          "en-US",
		Identity:          ident,
		Brands:            ClientHintBrandsFor("chrome", "132", "132"),
		Details:           NewClientHintsDetails(ua, "chrome", ident),
		Proxy:             "http://proxy:8080",
		ProxyRequested:    true,
		UserAgentOverride: ActionApplied(),
		IdentityScript:    ActionApplied(),
		GPUPatch:          ActionNotRequested(),
		Observation: map[string]interface{}{
			"userAgent": ua,
			"platform":  ident.NavigatorPlatform,
			"languages": []interface{}{"en-US", "en"},
			"webdriver": "",
			"userAgentData": map[string]interface{}{
				"platform":    ident.UserAgentDataPlatform,
				"mobile":      false,
				"highEntropy": true,
				"brands": []interface{}{
					map[string]interface{}{"brand": "Chromium", "version": "132"},
					map[string]interface{}{"brand": "Not-A.Brand", "version": "8"},
				},
			},
		},
		ObservedStatus:  StatusKnown,
		HTTPClientHints: &HTTPClientHints{UAFullVersion: "132.0.0.0", UAPlatform: "Windows"},
	})
	return rep
}

func TestReportRedactsProxy(t *testing.T) {
	rep := consistentProfile(t)
	// Every proxy field must be the redacted form.
	for _, p := range []string{rep.Requested.Proxy, rep.Effective.Proxy, rep.Applied.Proxy} {
		if strings.Contains(p, "proxy:8080") == false {
			t.Errorf("proxy host/port expected present but not scrubbed: %q", p)
		}
		// A non-credential proxy has no userinfo to scrub; assert no "user:" leak
		// is present for the credential case below.
	}
	// A proxy WITH credentials must be scrubbed on every section.
	cred := BuildPresentationReport(PresentationInput{
		UserAgent:      "ua",
		Proxy:          "http://user:s3cr3t@proxy.local:8080",
		ProxyRequested: true,
		Observation:    map[string]interface{}{"userAgent": "ua", "platform": "Win32"},
		ObservedStatus: StatusKnown,
	})
	for _, p := range []string{cred.Requested.Proxy, cred.Effective.Proxy, cred.Applied.Proxy} {
		if strings.Contains(p, "s3cr3t") || strings.Contains(p, "user:") {
			t.Errorf("credential leaked into %q", p)
		}
		if !strings.Contains(p, "proxy.local:8080") {
			t.Errorf("proxy host/port lost in %q", p)
		}
	}
}

func TestReportJSONSafe(t *testing.T) {
	cred := BuildPresentationReport(PresentationInput{
		UserAgent:      "ua",
		Proxy:          "http://user:s3cr3t@proxy.local:8080",
		ProxyRequested: true,
		Observation:    map[string]interface{}{"userAgent": "ua", "platform": "Win32"},
		ObservedStatus: StatusKnown,
	})
	b, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "s3cr3t") || strings.Contains(s, "user:") {
		t.Errorf("marshaled report leaks credentials: %s", s)
	}
	if !strings.Contains(s, "schema_version") {
		t.Errorf("marshaled report missing schema_version: %s", s)
	}
}

func TestReportNotObservedDistinction(t *testing.T) {
	// No observation at all: the observed UA must be not_observed, not empty-mismatch.
	rep := BuildPresentationReport(PresentationInput{
		UserAgent:      "Mozilla/5.0 Chrome/132.0.0.0 Safari/537.36",
		Product:        "chrome",
		Identity:       MapAdvertisedOSIdentity("windows", false),
		Brands:         ClientHintBrandsFor("chrome", "132", "132"),
		Details:        NewClientHintsDetails("Mozilla/5.0 Chrome/132.0.0.0", "chrome", MapAdvertisedOSIdentity("windows", false)),
		Observation:    nil,
		ObservedStatus: StatusNotObserved,
	})
	if rep.Observed.Status != StatusNotObserved {
		t.Errorf("expected not_observed status, got %q", rep.Observed.Status)
	}
	assess := EvaluatePresentationConsistency(rep)
	// Identity-script check should be not_observed (no fresh observation).
	if r, ok := findCheck(assess, "identity_script_applied_vs_observed"); ok && r.Verdict == VerdictMismatch {
		t.Errorf("a missing observation must not be a mismatch: %v", r)
	}
}

func TestReportUnknownStatusIsIgnored(t *testing.T) {
	// A report whose ObservedStatus is StatusKnown but whose IdentityScript action
	// failed must surface a failed_to_apply fault.
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64 x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36"
	ident := MapAdvertisedOSIdentity("windows", false)
	rep := BuildPresentationReport(PresentationInput{
		UserAgent:         ua,
		Product:           "chrome",
		Family:            "chromium",
		ResolvedOS:        "windows",
		DeviceClass:       "desktop",
		AdvertisedOS:      "windows",
		Identity:          ident,
		Brands:            ClientHintBrandsFor("chrome", "132", "132"),
		Details:           NewClientHintsDetails(ua, "chrome", ident),
		UserAgentOverride: ActionApplied(),
		IdentityScript:    ActionApplyFailed("driver refused addScriptToEvaluateOnNewDocument"),
		Observation:       map[string]interface{}{"userAgent": "Chrome/99.0.0.0", "platform": "Linux"},
		ObservedStatus:    StatusKnown,
	})
	assess := EvaluatePresentationConsistency(rep)
	if !assess.HasFault() {
		t.Fatalf("expected a fault for the failed identity script, got: %+v", assess.Results)
	}
	if r, ok := findCheck(assess, "identity_script_applied_vs_observed"); ok && r.Verdict != VerdictFailedToApply {
		t.Errorf("expected failed_to_apply, got %v", r.Verdict)
	}
}

func TestConsistencyAllChecksConsistent(t *testing.T) {
	rep := consistentProfile(t)
	assess := EvaluatePresentationConsistency(rep)
	if len(assess.Failures()) != 0 {
		t.Fatalf("expected no faults for a consistent profile, got: %+v", assess.Failures())
	}
	// Every check must be present and consistent (or a benign informational /
	// not_requested).
	names := map[string]bool{}
	for _, r := range assess.Results {
		names[r.Check] = true
		if r.Verdict == VerdictMismatch || r.Verdict == VerdictFailedToApply {
			t.Errorf("unexpected fault: %s -> %s (%s)", r.Check, r.Verdict, r.Note)
		}
	}
	for _, want := range []string{
		"client_hints_match_useragent",
		"advertised_os_matches_navigator_platform",
		"advertised_os_matches_userAgentData_platform",
		"device_class_not_confused_with_os",
		"identity_script_applied_vs_observed",
		"rotation_completeness",
		"source_proxy_vs_applied_proxy",
		"gpu_patch_applied",
		"native_capability_consistency",
	} {
		if !names[want] {
			t.Errorf("expected check %q present", want)
		}
	}
}

func TestHostOSNeverNormolizesAdvertisedOS(t *testing.T) {
	// A deliberately-Windows profile observed reporting Win32 must be consistent
	// even though the host is a Linux box. The checker has NO host input, so a
	// Windows-on-Linux host can never be flagged as a mismatch.
	rep := consistentProfile(t)
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "advertised_os_matches_navigator_platform"); ok && r.Verdict == VerdictMismatch {
		t.Errorf("Windows-on-Linux must not produce a platform mismatch: %v", r)
	}
}

func TestPlatformMismatchFlagged(t *testing.T) {
	// Intended Win32 but the observation reports "Linux": the patch's platform
	// override did not take; that is a genuine contradiction.
	rep := consistentProfile(t)
	rep.Observed.Platform = "Linux"
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "advertised_os_matches_navigator_platform"); !ok || r.Verdict != VerdictMismatch {
		t.Errorf("expected platform mismatch, got %+v", r)
	}
}

func TestNotObservedIsNotMismatch(t *testing.T) {
	rep := consistentProfile(t)
	rep.Observed.Status = StatusNotObserved
	rep.Observed.Platform = ""
	rep.Observed.UserAgent = ""
	assess := EvaluatePresentationConsistency(rep)
	for _, name := range []string{
		"advertised_os_matches_navigator_platform",
		"identity_script_applied_vs_observed",
		"rotation_completeness",
	} {
		if r, ok := findCheck(assess, name); ok && r.Verdict == VerdictMismatch {
			t.Errorf("check %q must not be a mismatch when not observed: %v", name, r)
		}
	}
	if assess.HasFault() {
		t.Errorf("a fully unobserved profile must have no fault, got %v", assess.Failures())
	}
}

func TestRotationNoStaleReuse(t *testing.T) {
	// After a rotation with no fresh observation, the checker must report
	// not_observed rather than reusing a stale prior observation as a mismatch.
	rep := consistentProfile(t)
	rep.Rotated = true
	rep.Observed.Status = StatusNotObserved
	rep.Observed.UserAgent = ""
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "rotation_completeness"); ok && r.Verdict == VerdictMismatch {
		t.Errorf("rotated-without-observation must be not_observed, not a mismatch: %v", r)
	}
	if !assess.HasFault() {
		// not_observed is not a fault, which is correct here.
	}
}

func TestRotationStaleIsMismatch(t *testing.T) {
	// After a rotation, the document still shows the prior (131.x) identity:
	// the new-document script did not take — a rotation-incomplete fault.
	rep := consistentProfile(t)
	rep.Rotated = true
	rep.Observed.UserAgent = "Mozilla/5.0 Chrome/131.0.0.0 Safari/537.36"
	rep.Observed.Status = StatusKnown
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "rotation_completeness"); ok && r.Verdict != VerdictMismatch {
		t.Errorf("expected rotation mismatch when document still shows the prior identity, got %v", r.Verdict)
	}
}

func TestProxyChecks(t *testing.T) {
	// Divergent effective vs applied proxy is a fault.
	divergent := PresentationReport{
		SchemaVersion: schemaVersionPresentation,
		Effective:     EffectivePresentation{Proxy: "http://***@a:1", ProxyRequested: true},
		Applied:       AppliedPresentation{Proxy: "http://***@b:2"},
	}
	assess := EvaluatePresentationConsistency(divergent)
	if r, ok := findCheck(assess, "source_proxy_vs_applied_proxy"); ok && r.Verdict != VerdictMismatch {
		t.Errorf("divergent proxy should be a mismatch, got %v", r)
	}
	// No proxy requested is not a fault.
	notProxy := PresentationReport{SchemaVersion: schemaVersionPresentation}
	assess2 := EvaluatePresentationConsistency(notProxy)
	if r, ok := findCheck(assess2, "source_proxy_vs_applied_proxy"); ok && r.Verdict == VerdictMismatch {
		t.Errorf("no proxy should not be a mismatch, got %v", r)
	}
}

func TestGPUPatchChecks(t *testing.T) {
	// Requested but not applied is a fault.
	needed := PresentationReport{
		SchemaVersion: schemaVersionPresentation,
		Applied:       AppliedPresentation{GPUPatch: ActionApplyFailed("no GPU device")},
		Effective:     EffectivePresentation{GPUPatchRequested: true},
	}
	assess := EvaluatePresentationConsistency(needed)
	if r, ok := findCheck(assess, "gpu_patch_applied"); ok && r.Verdict != VerdictFailedToApply {
		t.Errorf("expected gpu patch failed_to_apply, got %v", r)
	}
	// Not requested is not a fault.
	notReq := PresentationReport{SchemaVersion: schemaVersionPresentation}
	assess2 := EvaluatePresentationConsistency(notReq)
	if r, ok := findCheck(assess2, "gpu_patch_applied"); ok && r.Verdict == VerdictMismatch {
		t.Errorf("gpu patch not requested should not be a mismatch, got %v", r)
	}
	// Requested but the phase did not record the outcome must be not_observed,
	// not a false "applied" or fault (the patch is applied in a later phase).
	pending := PresentationReport{
		SchemaVersion: schemaVersionPresentation,
		Effective:     EffectivePresentation{GPUPatchRequested: true},
		Applied:       AppliedPresentation{},
	}
	assess3 := EvaluatePresentationConsistency(pending)
	if r, ok := findCheck(assess3, "gpu_patch_applied"); ok && r.Verdict == VerdictConsistent {
		t.Errorf("an unrecorded GPU patch request must not claim applied/consistent, got %v", r)
	}
	if r, ok := findCheck(assess3, "gpu_patch_applied"); ok && (r.Verdict == VerdictMismatch || r.Verdict == VerdictFailedToApply) {
		t.Errorf("an unrecorded GPU patch request must not be a fault, got %v", r)
	}
}

// TestGPUPatchApplicationMatrix exercises BuildPresentationReport end to end so a
// requested GPU patch whose outcome has not yet been observed can never be
// reported as a fabricated success, while the explicit outcomes (applied,
// failed-to-apply, unsupported) are preserved. Each case asserts the builder's
// applied status, the consistency verdict, and that the status survives a JSON
// round-trip.
func TestGPUPatchApplicationMatrix(t *testing.T) {
	cases := []struct {
		name        string
		requested   bool
		apply       ApplyStatus
		wantStatus  DiagnosticStatus
		wantVerdict ConsistencyVerdict
	}{
		{"disabled_not_requested", false, ApplyStatus{}, StatusNotRequested, VerdictNotRequested},
		{"enabled_unobserved", true, ApplyStatus{}, StatusNotObserved, VerdictNotObserved},
		{"enabled_explicit_applied", true, ActionApplied(), StatusApplied, VerdictConsistent},
		{"enabled_explicit_failed", true, ActionApplyFailed("no GPU device"), StatusApplyFailed, VerdictFailedToApply},
		{"enabled_unsupported", true, ActionUnsupported("engine lacks the patch"), StatusUnsupported, VerdictInformational},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := BuildPresentationReport(PresentationInput{
				GPUPatchRequested: c.requested,
				GPUPatch:          c.apply,
			})
			if got := rep.Applied.GPUPatch.Status; got != c.wantStatus {
				t.Errorf("builder status = %q, want %q", got, c.wantStatus)
			}
			// The core invariant: a requested-but-unobserved patch is never a
			// fabricated "applied" (the regression this fix guards).
			if c.requested && c.wantStatus != StatusApplied && rep.Applied.GPUPatch.Status == StatusApplied {
				t.Fatalf("a requested GPU patch (%s) was reported as applied", c.name)
			}
			r, ok := findCheck(EvaluatePresentationConsistency(rep), "gpu_patch_applied")
			if !ok {
				t.Fatal("gpu_patch_applied check missing from assessment")
			}
			if r.Verdict != c.wantVerdict {
				t.Errorf("verdict = %v, want %v", r.Verdict, c.wantVerdict)
			}
			// Serialization preserves the correct status.
			b, err := json.Marshal(rep)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var back PresentationReport
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back.Applied.GPUPatch.Status != c.wantStatus {
				t.Errorf("round-tripped status = %q, want %q", back.Applied.GPUPatch.Status, c.wantStatus)
			}
			if !strings.Contains(string(b), `"`+string(c.wantStatus)+`"`) {
				t.Errorf("status %q absent from serialized report: %s", c.wantStatus, b)
			}
		})
	}
}

func TestCapabilityAbsentNotFaultBrokenIs(t *testing.T) {
	// A requested WebGL that is simply absent is unsupported (NOT a fault), while
	// a broken (fabricated) one is a mismatch.
	absent := PresentationReport{
		SchemaVersion: schemaVersionPresentation,
		Observed: ObservedPresentation{
			Capabilities: []CapabilityReport{{Name: "webgl", Requested: true, Observed: CapabilityAbsent}},
		},
	}
	assess := EvaluatePresentationConsistency(absent)
	if r, ok := findCheck(assess, "native_capability_consistency"); ok && r.Verdict != VerdictUnsupported {
		t.Errorf("absent capability should be unsupported, got %v", r)
	}
	if assess.HasFault() {
		t.Errorf("absent capability must not be a fault, got %v", assess.Failures())
	}
	broken := PresentationReport{
		SchemaVersion: schemaVersionPresentation,
		Observed: ObservedPresentation{
			Capabilities: []CapabilityReport{{Name: "webgl", Requested: true, Observed: CapabilityBroken}},
		},
	}
	assess2 := EvaluatePresentationConsistency(broken)
	if r, ok := findCheck(assess2, "native_capability_consistency"); ok && r.Verdict != VerdictMismatch {
		t.Errorf("broken capability should be a mismatch, got %v", r)
	}
	if !assess2.HasFault() {
		t.Errorf("a broken capability must be a fault")
	}
}

func TestDeviceClassNotConfusedWithOS(t *testing.T) {
	// A device class of "windows" (an OS token mislabeled as a device class) is
	// reported informationally, not a mismatch, and is never normalized.
	rep := BuildPresentationReport(PresentationInput{
		DeviceClass:    "windows",
		AdvertisedOS:   "windows",
		Observation:    map[string]interface{}{"userAgent": "Chrome/132.0.0.0", "platform": "Win32"},
		ObservedStatus: StatusKnown,
	})
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "device_class_not_confused_with_os"); ok && r.Verdict == VerdictMismatch {
		t.Errorf("a mislabeled device class must be informational, not a mismatch: %v", r)
	}
}

func TestClientHintsMismatch(t *testing.T) {
	// The wire-observed full version disagrees with the intended engine version.
	rep := consistentProfile(t)
	rep.Observed.HTTPClientHints = &HTTPClientHints{UAFullVersion: "99.0.0.0"}
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "client_hints_match_useragent"); ok && r.Verdict != VerdictMismatch {
		t.Errorf("expected client-hints mismatch, got %v", r)
	}
}

func TestClientHintsNotObserved(t *testing.T) {
	rep := BuildPresentationReport(PresentationInput{
		UserAgent:      "Mozilla/5.0 Chrome/132.0.0.0 Safari/537.36",
		Product:        "chrome",
		Identity:       MapAdvertisedOSIdentity("windows", false),
		Brands:         ClientHintBrandsFor("chrome", "132", "132"),
		Details:        NewClientHintsDetails("Mozilla/5.0 Chrome/132.0.0.0", "chrome", MapAdvertisedOSIdentity("windows", false)),
		Observation:    map[string]interface{}{"userAgent": "Mozilla/5.0 Chrome/132.0.0.0 Safari/537.36", "platform": "Win32"},
		ObservedStatus: StatusKnown,
	})
	assess := EvaluatePresentationConsistency(rep)
	if r, ok := findCheck(assess, "client_hints_match_useragent"); !ok || r.Verdict != VerdictConsistent {
		t.Errorf("observed UA major should make client-hints consistent, got %+v", r)
	}
}

func TestNotRequestedChecks(t *testing.T) {
	// An empty report (nothing requested) must yield only not_requested /
	// not_observed / informational, never a mismatch.
	assess := EvaluatePresentationConsistency(PresentationReport{SchemaVersion: schemaVersionPresentation})
	if assess.HasFault() {
		t.Errorf("an empty report must have no fault, got %v", assess.Failures())
	}
}

func TestObserveUserAgentDataReadsAllKeys(t *testing.T) {
	// Regression for a producer/reader key drift: the live observation emitted by
	// getBrowserConfiguration and the reader observeUserAgentData must agree on
	// every key. A key the reader does not recognise is silently lost (e.g. the
	// high-entropy flag would read false), so this pins the exact contract the
	// live producer emits, without needing a browser to run the JS.
	m := map[string]interface{}{
		"userAgentData": map[string]interface{}{
			"platform":    "Windows",
			"mobile":      true,
			"highEntropy": true,
			"brands": []interface{}{
				map[string]interface{}{"brand": "Chromium", "version": "132"},
			},
		},
	}
	o := observeUserAgentData(m)
	if o == nil {
		t.Fatal("observeUserAgentData returned nil for a populated map")
	}
	if o.Platform != "Windows" {
		t.Errorf("platform = %q, want Windows", o.Platform)
	}
	if !o.Mobile {
		t.Errorf("mobile = %v, want true", o.Mobile)
	}
	if !o.HighEntropy {
		t.Errorf("HighEntropy = %v, want true (a reader/producer key drift yields false)", o.HighEntropy)
	}
	if len(o.Brands) != 1 || o.Brands[0].Brand != "Chromium" || o.Brands[0].Version != "132" {
		t.Errorf("brands = %+v, want a single Chromium/132 brand", o.Brands)
	}

	// A null or absent userAgentData (native Firefox, or no observation) must be
	// unavailable rather than an empty fake, so the surface is not normalised.
	if g := observeUserAgentData(map[string]interface{}{"userAgentData": nil}); g != nil {
		t.Errorf("a null userAgentData must yield a nil observation, got %+v", g)
	}
	if g := observeUserAgentData(map[string]interface{}{}); g != nil {
		t.Errorf("an absent userAgentData must yield a nil observation, got %+v", g)
	}
}

// --- PR7 additive diagnostic coverage --------------------------------------
//
// These tests extend the existing suite for the new, gated diagnostic surfaces.
// They prove the known/unavailable distinction, credential redaction in the
// launch summary, correct section separation of requested/effective/applied/
// observed values, and that no field is ever synthesized from configuration.

// TestObservedViewportParsedFromMap proves viewport and device surfaces are
// parsed from the getBrowserConfiguration map the session already captured (no
// new browser call). Numbers arrive as float64 (JSON-decoded), so toInt must
// coerce float64, int and int64; absent keys stay zero/omitted.
func TestObservedViewportParsedFromMap(t *testing.T) {
	obs, _ := observeFromMap(map[string]interface{}{
		"userAgent":           "Chrome/132.0.0.0",
		"platform":            "Win32",
		"screenWidth":         float64(1920),
		"screenHeight":        float64(1080),
		"innerWidth":          float64(1920),
		"innerHeight":         float64(1032),
		"outerWidth":          float64(1920),
		"outerHeight":         float64(1080),
		"deviceMemory":        float64(8),
		"hardwareConcurrency": float64(4),
		"plugins":             float64(2),
	}, StatusKnown, nil, nil)
	if obs.Viewport == nil {
		t.Fatalf("viewport not parsed from a populated observation")
	}
	if obs.Viewport.ScreenWidth != 1920 || obs.Viewport.ScreenHeight != 1080 ||
		obs.Viewport.InnerWidth != 1920 || obs.Viewport.InnerHeight != 1032 ||
		obs.Viewport.OuterWidth != 1920 || obs.Viewport.OuterHeight != 1080 {
		t.Errorf("viewport = %+v, want 1920x1080 / 1920x1032 inner", obs.Viewport)
	}
	if obs.DeviceMemory != 8 || obs.HardwareConcurrency != 4 || obs.Plugins != 2 {
		t.Errorf("device = mem=%d hc=%d plugins=%d, want 8/4/2", obs.DeviceMemory, obs.HardwareConcurrency, obs.Plugins)
	}
	// toInt must coerce the numeric kinds and reject non-numbers.
	if n, ok := toInt(int(10)); !ok || n != 10 {
		t.Errorf("toInt(int 10) = %d,%t", n, ok)
	}
	if n, ok := toInt(int64(11)); !ok || n != 11 {
		t.Errorf("toInt(int64 11) = %d,%t", n, ok)
	}
	if n, ok := toInt(float64(12.9)); !ok || n != 12 {
		t.Errorf("toInt(float64 12.9) = %d,%t (want truncation to 12)", n, ok)
	}
	if _, ok := toInt("nope"); ok {
		t.Errorf("toInt(non-number) reported ok")
	}
}

// TestObservedViewportAbsentIsNotFabricated proves an observation that lacks
// dimension/device keys does not show a fabricated empty surface — the viewport
// stays nil (omitted) and the device fields stay zero, and a nil observation is
// not_observed.
func TestObservedViewportAbsentIsNotFabricated(t *testing.T) {
	obs, _ := observeFromMap(map[string]interface{}{"userAgent": "Chrome/132", "platform": "Win32"}, StatusKnown, nil, nil)
	if obs.Viewport != nil {
		t.Errorf("a populated-but-undimensioned observation must not fabricate a viewport: %+v", obs.Viewport)
	}
	if obs.DeviceMemory != 0 || obs.HardwareConcurrency != 0 || obs.Plugins != 0 {
		t.Errorf("device surfaces must stay zero/omitted when unobserved: %+v", obs)
	}
	obs2, _ := observeFromMap(nil, StatusNotObserved, nil, nil)
	if obs2.Viewport != nil || obs2.Status != StatusNotObserved {
		t.Errorf("nil observation must be not_observed with no viewport: %+v", obs2)
	}
}

// TestLaunchOptionsSummaryRedactsProxy proves the launch-option summary scrubs
// the proxy credential while preserving the host/port (so it stays diagnostic),
// detects DNS-prefetch presence, and is JSON-safe. Benign switches are untouched.
func TestLaunchOptionsSummaryRedactsProxy(t *testing.T) {
	args := []string{
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
		"--proxy-server=http://user:s3cr3t@corp.local:8080",
		"--dns-prefetch-disable",
		"--headless=new",
	}
	s := buildLaunchOptionsSummary(args)
	if s == nil {
		t.Fatalf("summary nil for a non-empty argv")
	}
	var sawProxy bool
	for _, a := range s.Redacted {
		if strings.Contains(a, "--proxy-server=") {
			sawProxy = true
			if strings.Contains(a, "s3cr3t") || strings.Contains(a, "user:s3cr3t") {
				t.Errorf("proxy credential leaked into the launch summary: %q", a)
			}
			if !strings.Contains(a, "corp.local:8080") {
				t.Errorf("proxy host/port lost in redaction: %q", a)
			}
		}
	}
	if !sawProxy {
		t.Errorf("proxy-server switch missing from the summary: %v", s.Redacted)
	}
	if !hasLaunchFlag(s.Redacted, "--user-agent") {
		t.Errorf("benign --user-agent was altered: %v", s.Redacted)
	}
	if !s.DNSPrefetchDisabled {
		t.Errorf("--dns-prefetch-disable present but not detected")
	}
	if len(s.Duplicates) != 0 {
		t.Errorf("unexpected duplicate switches: %v", s.Duplicates)
	}
	// The summary is safe to serialize end to end.
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "s3cr3t") {
		t.Errorf("serialized summary leaks a credential: %s", b)
	}
	// An empty argv yields a nil summary, never a fabricated empty slice.
	if buildLaunchOptionsSummary(nil) != nil {
		t.Errorf("nil argv must yield a nil summary")
	}
	if buildLaunchOptionsSummary([]string{}) != nil {
		t.Errorf("empty argv must yield a nil summary")
	}
}

// TestLaunchOptionsDuplicateSwitches proves a repeated switch is a recorded
// coherence signal in first-seen order, while unique switches are not flagged.
func TestLaunchOptionsDuplicateSwitches(t *testing.T) {
	s := buildLaunchOptionsSummary([]string{
		"--headless=new", "--no-sandbox", "--headless=new", "--disable-gpu", "--no-sandbox",
	})
	if s == nil {
		t.Fatalf("summary nil")
	}
	want := []string{"--headless=new", "--no-sandbox"}
	if !reflect.DeepEqual(s.Duplicates, want) {
		t.Errorf("duplicates = %v, want %v (first-seen order)", s.Duplicates, want)
	}
	if got := buildLaunchOptionsSummary([]string{"--a", "--b", "--c"}).Duplicates; len(got) != 0 {
		t.Errorf("unique switches must not be flagged: %v", got)
	}
	// A single occurrence is not a duplicate; a double occurrence with an
	// intervening switch must still report first-seen order.
	if got := buildLaunchOptionsSummary([]string{"--a", "--b", "--a"}).Duplicates; !reflect.DeepEqual(got, []string{"--a"}) {
		t.Errorf("spaced duplicates = %v, want [--a]", got)
	}
}

// TestReportWithLaunchOptionsJSONSafe proves a credentialed --proxy-server launch
// argument cannot leak a proxy credential through JSON serialization of the whole
// report, while the host/port survive for diagnosis.
func TestReportWithLaunchOptionsJSONSafe(t *testing.T) {
	rep := BuildPresentationReport(PresentationInput{
		Proxy:          "http://user:s3cr3t@corp.local:8080",
		ProxyRequested: true,
		LaunchOptions:  []string{"--proxy-server=http://user:s3cr3t@corp.local:8080", "--headless=new"},
		Observation:    map[string]interface{}{"userAgent": "Chrome/132", "platform": "Win32"},
		ObservedStatus: StatusKnown,
	})
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "s3cr3t") || strings.Contains(s, "user:s3cr3t") {
		t.Errorf("report leaks a proxy credential through launch options: %s", s)
	}
	if !strings.Contains(s, "corp.local:8080") {
		t.Errorf("proxy host/port lost in launch-summary redaction: %s", s)
	}
}

// TestProxyProvenanceLayer pins the proxy-provenance attribution against the real
// effectiveSessionProxy composition for every precedence branch, so a proxy layer
// is never attributed to the wrong setting. The layer helper is a pure mirror of
// the composition, so its output is always reconcilable with the effective value.
func TestProxyProvenanceLayer(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		sourceOK   bool
		custom     string
		vdiDefault string
		wantLayer  string
	}{
		{"source_wins_over_custom_and_vdi", "http://src:1", true, "http://custom:2", "http://vdi:3", "source"},
		{"source_no_proxy_forces_direct_over_custom", "no_proxy", true, "http://custom:2", "http://vdi:3", "no_proxy"},
		{"source_empty_falls_to_custom", "   ", true, "http://custom:2", "http://vdi:3", "custom_vdi"},
		{"source_absent_custom", "", false, "http://custom:2", "http://vdi:3", "custom_vdi"},
		{"custom_no_proxy_forces_direct", "", false, "no_proxy", "http://vdi:3", "no_proxy"},
		{"custom_empty_falls_to_vdi", "   ", false, "  ", "http://vdi:3", "vdi_default"},
		{"all_empty_none", "", false, "", "", "none"},
		{"source_present_but_not_ok_ignored", "http://src:1", false, "", "", "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff := effectiveSessionProxy(c.source, c.sourceOK, c.custom, c.vdiDefault)
			got := proxyProvenanceLayer(c.source, c.sourceOK, c.custom, eff)
			if got != c.wantLayer {
				t.Errorf("layer = %q for effective=%q, want %q", got, redactProxyURL(eff), c.wantLayer)
			}
		})
	}
}

// TestRegistrationScopePassesThrough proves the registration scope is a structural
// fact that the builder passes through verbatim (never inferred from config): a
// CDP-applied script reports a session-scoped new-document registration, a
// non-CDP engine reports not_applied, and an unscoped input fabricates nothing.
func TestRegistrationScopePassesThrough(t *testing.T) {
	applied := BuildPresentationReport(PresentationInput{
		IdentityScript:      ActionApplied(),
		IdentityScriptScope: "new_document_session",
	})
	if applied.Applied.RegistrationScope != "new_document_session" {
		t.Errorf("registration scope = %q, want new_document_session", applied.Applied.RegistrationScope)
	}
	none := BuildPresentationReport(PresentationInput{
		IdentityScript:      ActionNotRequested(),
		IdentityScriptScope: "not_applied",
	})
	if none.Applied.RegistrationScope != "not_applied" {
		t.Errorf("registration scope = %q, want not_applied", none.Applied.RegistrationScope)
	}
	if got := BuildPresentationReport(PresentationInput{}).Applied.RegistrationScope; got != "" {
		t.Errorf("an unscoped input must not fabricate a registration scope, got %q", got)
	}
}

// TestDiagnosticGapsSurfaceUntrackedRequirements proves the explicitly-unavailable
// v2.1 diagnostics (config generation, per-setting ownership, rotation lifecycle)
// are surfaced as documented gaps rather than fabricated, and that an input with
// no gap data emits no gap section.
func TestDiagnosticGapsSurfaceUntrackedRequirements(t *testing.T) {
	const note = "config_generation: untracked — no reload generation counter"
	rep := BuildPresentationReport(PresentationInput{
		ProxyProvenance:    "custom_vdi",
		DiagnosticGapNotes: []string{note},
	})
	if rep.DiagnosticGaps == nil {
		t.Fatalf("diagnostic gaps not surfaced")
	}
	if rep.DiagnosticGaps.ProxyProvenance != "custom_vdi" {
		t.Errorf("proxy provenance = %q, want custom_vdi", rep.DiagnosticGaps.ProxyProvenance)
	}
	if len(rep.DiagnosticGaps.Notes) != 1 || rep.DiagnosticGaps.Notes[0] != note {
		t.Errorf("gap notes = %v, want [%q]", rep.DiagnosticGaps.Notes, note)
	}
	// No provenance and no notes -> no gap section (not a fabricated one).
	if got := BuildPresentationReport(PresentationInput{}).DiagnosticGaps; got != nil {
		t.Errorf("an input with no gap data must carry no DiagnosticGaps section, got %+v", got)
	}

	// The gap notes must not alias the caller's slice: a consumer may mutate a
	// report's notes without corrupting the source or other reports.
	src := []string{}
	repA := BuildPresentationReport(PresentationInput{DiagnosticGapNotes: append(src, "a")})
	// Append to the source slice; the built report must be unaffected.
	src = append(src, "corrupted-after-build")
	if len(repA.DiagnosticGaps.Notes) != 1 || repA.DiagnosticGaps.Notes[0] != "a" {
		t.Errorf("report notes must not alias the source slice: %+v", repA.DiagnosticGaps.Notes)
	}
	// Mutating the built report must not affect a subsequent build.
	if repA.DiagnosticGaps.Notes != nil {
		repA.DiagnosticGaps.Notes[0] = "mutated"
	}
	repB := BuildPresentationReport(PresentationInput{DiagnosticGapNotes: append([]string{}, "b")})
	if repB.DiagnosticGaps.Notes[0] != "b" {
		t.Errorf("subsequent report mutated through repA's build: %+v", repB.DiagnosticGaps.Notes)
	}
}

// TestReportSectionSeparation proves the new PR7 fields land in exactly their
// Requested / Effective / Applied / Observed sections, so a consumer can never
// confuse a requested value with an effective, an applied registration with an
// observed surface, or a diagnostic gap with a section value. (The type system
// enforces each field lives on exactly one section struct; this pins the wiring.)
func TestReportSectionSeparation(t *testing.T) {
	rep := BuildPresentationReport(PresentationInput{
		BrowserType:  "chrome",
		DeviceClass:  "desktop",
		AdvertisedOS: "windows",
		Language:     "en-US",
		UserAgent:    "Mozilla/5.0 Chrome/132.0.0.0",
		Product:      "chrome",
		Family:       "chromium",
		ResolvedOS:   "windows",
		Identity:     MapAdvertisedOSIdentity("windows", false),
		Brands:       ClientHintBrandsFor("chrome", "132", "132"),
		Details:      NewClientHintsDetails("Mozilla/5.0 Chrome/132.0.0.0", "chrome", MapAdvertisedOSIdentity("windows", false)),
		// Requested: the configured rotation policy.
		UAChangePolicy: "on_start",
		// Effective: the (redacted) launch argv.
		LaunchOptions: []string{"--user-agent=Mozilla/5.0 Chrome/132.0.0.0", "--dns-prefetch-disable"},
		// Applied: the registration scope.
		IdentityScriptScope: "new_document_session",
		IdentityScript:      ActionApplied(),
		// Observed: viewport / device from the already-taken observation.
		Observation: map[string]interface{}{
			"userAgent":    "Mozilla/5.0 Chrome/132.0.0.0",
			"platform":     "Win32",
			"screenWidth":  float64(1920),
			"screenHeight": float64(1080),
			"deviceMemory": float64(8),
			"plugins":      float64(2),
		},
		ObservedStatus: StatusKnown,
		// Diagnostics.
		ProxyProvenance:    "custom_vdi",
		DiagnosticGapNotes: []string{"setting_ownership: unrecorded"},
	})
	if rep.Requested.UAChangePolicy != "on_start" {
		t.Errorf("UAChangePolicy not wired into Requested: %q", rep.Requested.UAChangePolicy)
	}
	if rep.Effective.LaunchOptions == nil || len(rep.Effective.LaunchOptions.Redacted) != 2 {
		t.Errorf("LaunchOptions not wired into Effective: %+v", rep.Effective.LaunchOptions)
	}
	if rep.Applied.RegistrationScope != "new_document_session" {
		t.Errorf("RegistrationScope not wired into Applied: %q", rep.Applied.RegistrationScope)
	}
	if rep.Observed.Viewport == nil || rep.Observed.Viewport.ScreenWidth != 1920 || rep.Observed.DeviceMemory != 8 {
		t.Errorf("viewport/device not wired into Observed: %+v / mem=%d", rep.Observed.Viewport, rep.Observed.DeviceMemory)
	}
	if rep.DiagnosticGaps == nil || rep.DiagnosticGaps.ProxyProvenance != "custom_vdi" {
		t.Errorf("DiagnosticGaps not surfaced: %+v", rep.DiagnosticGaps)
	}
}

// TestNewFieldsJSONRoundTrip proves the additive diagnostic fields survive a JSON
// round-trip and that a credentialed launch argument cannot leak through them.
func TestNewFieldsJSONRoundTrip(t *testing.T) {
	rep := BuildPresentationReport(PresentationInput{
		UAChangePolicy:      "always",
		LaunchOptions:       []string{"--dns-prefetch-disable", "--proxy-server=http://u:s3cr3t@h:8080"},
		IdentityScriptScope: "new_document_session",
		IdentityScript:      ActionApplied(),
		ProxyProvenance:     "vdi_default",
		DiagnosticGapNotes:  []string{"lifecycle_state: unobserved at connection time"},
		Observation: map[string]interface{}{
			"userAgent":           "Chrome/132",
			"platform":            "Win32",
			"screenWidth":         float64(1280),
			"screenHeight":        float64(720),
			"deviceMemory":        float64(4),
			"hardwareConcurrency": float64(8),
		},
		ObservedStatus: StatusKnown,
	})
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back PresentationReport
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Requested.UAChangePolicy != "always" {
		t.Errorf("UAChangePolicy round-trip = %q, want always", back.Requested.UAChangePolicy)
	}
	if back.Applied.RegistrationScope != "new_document_session" {
		t.Errorf("RegistrationScope round-trip = %q, want new_document_session", back.Applied.RegistrationScope)
	}
	if back.Effective.LaunchOptions == nil || !back.Effective.LaunchOptions.DNSPrefetchDisabled ||
		len(back.Effective.LaunchOptions.Redacted) != 2 {
		t.Errorf("LaunchOptions round-trip = %+v", back.Effective.LaunchOptions)
	}
	if back.DiagnosticGaps == nil || back.DiagnosticGaps.ProxyProvenance != "vdi_default" {
		t.Errorf("DiagnosticGaps round-trip = %+v", back.DiagnosticGaps)
	}
	if back.Observed.Viewport == nil || back.Observed.Viewport.ScreenWidth != 1280 {
		t.Errorf("viewport round-trip = %+v", back.Observed.Viewport)
	}
	if back.Observed.HardwareConcurrency != 8 || back.Observed.DeviceMemory != 4 {
		t.Errorf("device round-trip = hc %d mem %d, want 8/4", back.Observed.HardwareConcurrency, back.Observed.DeviceMemory)
	}
	// The scrubbed credential must not survive the round-trip.
	if strings.Contains(string(b), "s3cr3t") || strings.Contains(string(b), "u:s3cr3t") {
		t.Errorf("credential leaked through the launch-options round-trip: %s", b)
	}
}
