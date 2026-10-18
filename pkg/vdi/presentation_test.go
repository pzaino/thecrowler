package vdi

import (
	"encoding/json"
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
