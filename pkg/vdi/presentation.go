package vdi

import (
	"fmt"
	"strings"
)

// This file implements the read-only "effective browser presentation"
// diagnostic. It answers, for one selection event and one session:
//
//   - Requested:  what the user asked for (configuration / source override).
//   - Effective:  what CROWler resolved after inheritance, selection and
//     random choice — the deliberately chosen presentation.
//   - Applied:    what was sent to the driver / registered, and whether it
//     succeeded, failed or was unsupported.
//   - Observed:   what the browser (or a controlled HTTP recorder) actually
//     returned. Observations are never synthesized from configuration.
//
// Everything here is pure and allocation-light: building and serializing a
// report is gated to the most-verbose debug level, so an ordinary session pays
// no diagnostic cost. The consistency evaluator only compares already-gathered
// data; it never issues a browser or CDP call and never mutates a session.

// schemaVersionPresentation tags the report so a consumer can recognize its
// shape. It follows the versioned schema convention of the project's existing
// diagnostic payloads.
const schemaVersionPresentation = "vdi.presentation.v1"

// DiagnosticStatus records how a value was obtained so that an unavailable,
// not-yet-observed, or not-requested value is never mistaken for a
// deliberately empty one. It is assigned only by the build/observation layer.
type DiagnosticStatus string

const (
	// StatusKnown means the value was captured/observed successfully.
	StatusKnown DiagnosticStatus = "known"
	// StatusEmpty means the value was present but an empty/zero value.
	StatusEmpty DiagnosticStatus = "empty"
	// StatusNotObserved means no observation was taken at this time; a stale
	// observation from an earlier selection must not be reused.
	StatusNotObserved DiagnosticStatus = "not_observed"
	// StatusNotRequested means the surface was not requested by the session.
	StatusNotRequested DiagnosticStatus = "not_requested"
	// StatusUnavailable means the engine/interface does not provide the surface.
	StatusUnavailable DiagnosticStatus = "unavailable"
	// StatusApplied means the value was sent to the driver successfully.
	StatusApplied DiagnosticStatus = "applied"
	// StatusApplyFailed means the send or registration failed.
	StatusApplyFailed DiagnosticStatus = "apply_failed"
	// StatusUnsupported means the capability was requested but the engine lacks
	// it; this is an information gap, not an identity fault.
	StatusUnsupported DiagnosticStatus = "unsupported"
)

// --- action-status constructors ---

// ActionApplied / ActionApplyFailed / ActionNotRequested / ActionUnsupported
// build the applied-action statuses used to wire a report at a call site.
func ActionApplied() ApplyStatus { return ApplyStatus{Status: StatusApplied} }
func ActionApplyFailed(detail string) ApplyStatus {
	return ApplyStatus{Status: StatusApplyFailed, Detail: detail}
}
func ActionNotRequested() ApplyStatus { return ApplyStatus{Status: StatusNotRequested} }
func ActionUnsupported(detail string) ApplyStatus {
	return ApplyStatus{Status: StatusUnsupported, Detail: detail}
}

// RequestedPresentation is what the user asked for: the configured browser
// selector and the source/process overrides. Proxy values are always stored in
// their redacted form so the report is safe to emit or persist.
type RequestedPresentation struct {
	BrowserType  string `json:"browser_type"`
	DeviceClass  string `json:"device_class"`  // crawler.platform
	AdvertisedOS string `json:"advertised_os"` // crawler.browser_platform
	Language     string `json:"language"`
	Proxy        string `json:"proxy,omitempty"`
	GPUPatch     string `json:"gpu_patch,omitempty"`
}

// EffectivePresentation is the deliberately chosen presentation after
// per-source inheritance and random selection. It is the expectation that the
// consistency evaluator compares observations against. Proxy is redacted.
type EffectivePresentation struct {
	UserAgent             string             `json:"user_agent"`
	BrowserProduct        string             `json:"browser_product"`
	BrowserFamily         string             `json:"browser_family"`
	AdvertisedOSKey       string             `json:"advertised_os_key"`
	NavigatorPlatform     string             `json:"navigator_platform"`
	UserAgentDataPlatform string             `json:"user_agent_data_platform"`
	Mobile                bool               `json:"mobile"`
	ClientHints           ClientHintsSurface `json:"client_hints"`
	Proxy                 string             `json:"proxy,omitempty"`
	ProxyRequested        bool               `json:"proxy_requested"`
	GPUPatchRequested     bool               `json:"gpu_patch_requested"`
	FallbackUsed          bool               `json:"fallback_used"`
	DegradedProduct       bool               `json:"degraded_product"`
}

// ClientHintsSurface is the high/low-entropy Client Hints the session intends
// to advertise.
type ClientHintsSurface struct {
	Available       bool              `json:"available"`
	Brands          []ClientHintBrand `json:"brands,omitempty"`
	FullVersion     string            `json:"full_version,omitempty"`
	PlatformVersion string            `json:"platform_version,omitempty"`
	Architecture    string            `json:"architecture,omitempty"`
	Bitness         string            `json:"bitness,omitempty"`
	Model           string            `json:"model,omitempty"`
}

// ApplyStatus is the outcome of one applied action.
type ApplyStatus struct {
	Status DiagnosticStatus `json:"status"`
	Detail string           `json:"detail,omitempty"`
}

// AppliedPresentation records what was sent to the driver / registered and
// whether each action reached the engine. Proxy is redacted.
type AppliedPresentation struct {
	UserAgentOverride ApplyStatus `json:"user_agent_override"`
	IdentityScript    ApplyStatus `json:"identity_script"`
	GPUPatch          ApplyStatus `json:"gpu_patch"`
	Proxy             string      `json:"proxy,omitempty"`
	ProxyApplied      bool        `json:"proxy_applied"`
}

// ObservedPresentation is what the browser (or a controlled HTTP recorder)
// actually returns. It is never synthesized from configuration: every field is
// either present in the observation or reported not_observed / unavailable.
type ObservedPresentation struct {
	// Status is StatusKnown when an observation was taken, StatusNotObserved
	// when it was not, and StatusUnavailable when the engine could not answer.
	Status        DiagnosticStatus       `json:"status"`
	UserAgent     string                 `json:"user_agent,omitempty"`
	Platform      string                 `json:"platform,omitempty"`
	Languages     []string               `json:"languages,omitempty"`
	Webdriver     string                 `json:"webdriver,omitempty"`
	UserAgentData *ObservedUserAgentData `json:"user_agent_data,omitempty"`
	// HTTPClientHints, when non-nil, are the Sec-CH-UA* a controlled recorder
	// saw on the wire. They are distinct from a browser-side read and are the
	// authoritative cross-check for the Client Hints consistency check.
	HTTPClientHints *HTTPClientHints `json:"http_client_hints,omitempty"`
	// Capabilities carry requested vs observed native capability states so an
	// unavailable capability (e.g. a missing WebGL context) is never read as an
	// identity fault, while a broken (fabricated) implementation is.
	Capabilities []CapabilityReport `json:"capabilities,omitempty"`
}

// ObservedUserAgentData is the low-entropy navigator.userAgentData observation.
// High-entropy values are not read here (they are async and spoofed by the
// script); the presence of getHighEntropyValues is recorded instead.
type ObservedUserAgentData struct {
	Platform    string            `json:"platform,omitempty"`
	Mobile      bool              `json:"mobile"`
	Brands      []ClientHintBrand `json:"brands,omitempty"`
	HighEntropy bool              `json:"high_entropy,omitempty"`
}

// HTTPClientHints holds the Sec-CH-UA* headers a controlled recorder captured on
// a real HTTP request. UAFullVersion is the plain wire version (e.g. from
// Sec-CH-UA-Full-Version) used for comparison; UAFullVersionList is the raw
// Sec-CH-UA-Full-Version-List kept for diagnostics only.
type HTTPClientHints struct {
	UA                string `json:"sec_ch_ua,omitempty"`
	UAFullVersion     string `json:"sec_ch_ua_full_version,omitempty"`
	UAFullVersionList string `json:"sec_ch_ua_full_version_list,omitempty"`
	UAMobile          string `json:"sec_ch_ua_mobile,omitempty"`
	UAPlatform        string `json:"sec_ch_ua_platform,omitempty"`
	UAPlatformVersion string `json:"sec_ch_ua_platform_version,omitempty"`
	UAArch            string `json:"sec_ch_ua_arch,omitempty"`
	UAModel           string `json:"sec_ch_ua_model,omitempty"`
}

// CapabilityState is the observed state of one native capability.
type CapabilityState string

const (
	// CapabilityAvailable means the capability is present and behaves.
	CapabilityAvailable CapabilityState = "available"
	// CapabilityAbsent means the capability is not present (informational, not a
	// fault): a missing WebGL context on a GPU-less VDI, for example.
	CapabilityAbsent CapabilityState = "absent"
	// CapabilityBroken means the capability is claimed but its implementation
	// behaves wrongly (a fabricated or non-functional context).
	CapabilityBroken CapabilityState = "broken"
	// CapabilityNotObserved means the capability was not probed.
	CapabilityNotObserved CapabilityState = "not_observed"
)

// CapabilityReport is one requested/observed native capability.
type CapabilityReport struct {
	Name      string          `json:"name"`
	Requested bool            `json:"requested"`
	Applied   bool            `json:"applied"`
	Observed  CapabilityState `json:"observed"`
}

// PresentationReport is the typed, redacted, read-only snapshot distinguishing
// Requested, Effective, Applied and Observed.
type PresentationReport struct {
	// SchemaVersion identifies the structure for consumers.
	SchemaVersion string                `json:"schema_version"`
	Requested     RequestedPresentation `json:"requested"`
	Effective     EffectivePresentation `json:"effective"`
	Applied       AppliedPresentation   `json:"applied"`
	Observed      ObservedPresentation  `json:"observed"`
	// Rotated records that this report follows an intentional User-Agent
	// rotation, so the evaluator can judge rotation completeness.
	Rotated bool `json:"rotated,omitempty"`
}

// PresentationInput is the gathered data a report is built from. It is pure
// input: the builder performs no observation and issues no browser call.
type PresentationInput struct {
	// Requested / effective selection.
	BrowserType  string
	UserAgent    string
	Product      string
	Family       string
	ResolvedOS   string
	DeviceClass  string
	AdvertisedOS string
	Language     string
	Identity     OSIdentity
	Brands       []ClientHintBrand
	Details      ClientHintsDetails
	// Proxy is the source-effective selected proxy (raw); the builder redacts
	// it on the way into every section of the report.
	Proxy          string
	ProxyRequested bool
	// GPUPatchRequested records whether the session requested the GPU patch.
	GPUPatchRequested bool
	FallbackUsed      bool
	DegradedProduct   bool
	// Applied outcomes (StatusApplied / StatusApplyFailed / ...).
	UserAgentOverride ApplyStatus
	IdentityScript    ApplyStatus
	GPUPatch          ApplyStatus
	// Observation: the live getBrowserConfiguration map, or nil when no
	// observation was taken (in which case ObservedStatus is StatusNotObserved).
	Observation    map[string]interface{}
	ObservedStatus DiagnosticStatus
	// HTTP-observed Client Hints from a controlled recorder, when available.
	HTTPClientHints *HTTPClientHints
	// Capabilities: requested vs observed native capability states.
	Capabilities []CapabilityReport
	// Rotated marks a post-rotation report.
	Rotated bool
}

// BuildPresentationReport assembles a typed report from gathered data. It is
// pure and allocates only the report itself; every proxy value is redacted on
// the way in so the result is always safe to emit or persist.
func BuildPresentationReport(in PresentationInput) PresentationReport {
	prox := redactProxyURL(in.Proxy)
	hints := ClientHintsSurface{
		Available:       len(in.Brands) > 0,
		Brands:          in.Brands,
		FullVersion:     in.Details.FullVersion,
		PlatformVersion: in.Details.PlatformVersion,
		Architecture:    in.Details.Architecture,
		Bitness:         in.Details.Bitness,
		Model:           in.Details.Model,
	}
	gpuApply := in.GPUPatch
	if !in.GPUPatchRequested {
		gpuApply = ActionNotRequested()
	} else if gpuApply.Status == "" {
		gpuApply = ActionApplied()
	}
	obs, _ := observeFromMap(in.Observation, in.ObservedStatus, in.HTTPClientHints, in.Capabilities)
	return PresentationReport{
		SchemaVersion: schemaVersionPresentation,
		Requested: RequestedPresentation{
			BrowserType:  in.BrowserType,
			DeviceClass:  in.DeviceClass,
			AdvertisedOS: in.AdvertisedOS,
			Language:     in.Language,
			Proxy:        prox,
			GPUPatch:     gpuRequestString(in.GPUPatchRequested),
		},
		Effective: EffectivePresentation{
			UserAgent:             in.UserAgent,
			BrowserProduct:        in.Product,
			BrowserFamily:         in.Family,
			AdvertisedOSKey:       in.ResolvedOS,
			NavigatorPlatform:     in.Identity.NavigatorPlatform,
			UserAgentDataPlatform: in.Identity.UserAgentDataPlatform,
			Mobile:                in.Identity.Mobile,
			ClientHints:           hints,
			Proxy:                 prox,
			ProxyRequested:        in.ProxyRequested,
			GPUPatchRequested:     in.GPUPatchRequested,
			FallbackUsed:          in.FallbackUsed,
			DegradedProduct:       in.DegradedProduct,
		},
		Applied: AppliedPresentation{
			UserAgentOverride: in.UserAgentOverride,
			IdentityScript:    in.IdentityScript,
			GPUPatch:          gpuApply,
			Proxy:             prox,
			ProxyApplied:      in.Proxy != "",
		},
		Observed: obs,
		Rotated:  in.Rotated,
	}
}

// gpuRequestString renders the GPU-patch request for the Requested section.
func gpuRequestString(requested bool) string {
	if requested {
		return "set_vdi_gpu_patch"
	}
	return ""
}

// observeFromMap turns a getBrowserConfiguration map into an
// ObservedPresentation. A nil/empty observation yields StatusNotObserved so a
// not-yet-observed surface is never mistaken for an empty value; a recorded HTTP
// observation or a probed capability still makes the observation present even
// when the browser-side read was not taken.
func observeFromMap(m map[string]interface{}, status DiagnosticStatus, httpHints *HTTPClientHints, caps []CapabilityReport) (ObservedPresentation, []CapabilityReport) {
	obs := ObservedPresentation{
		Status:          status,
		HTTPClientHints: httpHints,
		Capabilities:    caps,
	}
	if status == StatusNotObserved && (len(m) == 0) {
		if httpHints != nil || len(caps) > 0 {
			obs.Status = StatusKnown
		}
		return obs, caps
	}
	obs.UserAgent, _ = firstString(m["userAgent"])
	obs.Platform, _ = firstString(m["platform"])
	obs.Webdriver, _ = firstString(m["webdriver"])
	if langs, ok := m["languages"].([]interface{}); ok {
		for _, l := range langs {
			if s, ok := l.(string); ok {
				obs.Languages = append(obs.Languages, s)
			}
		}
	}
	if uad := observeUserAgentData(m); uad != nil {
		obs.UserAgentData = uad
	}
	obs.Status = StatusKnown
	return obs, caps
}

// observeUserAgentData extracts the low-entropy navigator.userAgentData
// observation. It returns nil when the engine has no userAgentData (native
// Firefox identities) so the surface is reported unavailable rather than empty.
func observeUserAgentData(m map[string]interface{}) *ObservedUserAgentData {
	raw, ok := m["userAgentData"]
	if !ok || raw == nil {
		return nil
	}
	obj, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	o := &ObservedUserAgentData{}
	o.Platform, _ = firstString(obj["platform"])
	o.Mobile, _ = obj["mobile"].(bool)
	o.HighEntropy, _ = obj["highEntropy"].(bool)
	if bl, ok := obj["brands"].([]interface{}); ok {
		for _, b := range bl {
			if bm, ok := b.(map[string]interface{}); ok {
				brand, _ := firstString(bm["brand"])
				version, _ := firstString(bm["version"])
				if brand != "" {
					o.Brands = append(o.Brands, ClientHintBrand{Brand: brand, Version: version})
				}
			}
		}
	}
	return o
}

// firstString returns the value as a non-empty string.
func firstString(v interface{}) (string, bool) {
	if s, ok := v.(string); ok {
		return s, s != ""
	}
	return "", false
}

// ---- consistency evaluation --------------------------------------------

// ConsistencyVerdict is the outcome of one profile-aware check. The values match
// the outcomes the project uses: consistent, mismatch, failed to apply,
// unsupported/unavailable, not requested, informational.
type ConsistencyVerdict int

const (
	// VerdictConsistent: the observed surface agrees with the intended one.
	VerdictConsistent ConsistencyVerdict = iota
	// VerdictMismatch: a genuine contradiction with the intended presentation.
	VerdictMismatch
	// VerdictFailedToApply: a requested/needed action did not reach the engine.
	VerdictFailedToApply
	// VerdictUnsupported: the engine lacks the capability (not a fault).
	VerdictUnsupported
	// VerdictNotRequested: the check is not applicable to this session.
	VerdictNotRequested
	// VerdictNotObserved: no observation is available, so the surface is neither
	// confirmed nor denied (a stale observation is not reused).
	VerdictNotObserved
	// VerdictInformational: a noteworthy but non-failing observation.
	VerdictInformational
)

func (v ConsistencyVerdict) String() string {
	switch v {
	case VerdictConsistent:
		return "consistent"
	case VerdictMismatch:
		return "mismatch"
	case VerdictFailedToApply:
		return "failed_to_apply"
	case VerdictUnsupported:
		return "unsupported"
	case VerdictNotRequested:
		return "not_requested"
	case VerdictNotObserved:
		return "not_observed"
	case VerdictInformational:
		return "informational"
	default:
		return "unknown"
	}
}

// ConsistencyResult is one check's outcome with its expected/observed values.
type ConsistencyResult struct {
	Check    string             `json:"check"`
	Verdict  ConsistencyVerdict `json:"verdict"`
	Expected string             `json:"expected,omitempty"`
	Observed string             `json:"observed,omitempty"`
	Note     string             `json:"note,omitempty"`
}

// ConsistencyAssessment is the set of check outcomes for one report.
type ConsistencyAssessment struct {
	Results []ConsistencyResult
}

// HasFault reports whether any check found a genuine fault (a contradiction or a
// failed-to-apply action) that the user would need to act on.
func (a ConsistencyAssessment) HasFault() bool {
	return len(a.Failures()) > 0
}

// Failures returns the actionable results (mismatch and failed_to_apply).
func (a ConsistencyAssessment) Failures() []ConsistencyResult {
	var out []ConsistencyResult
	for _, r := range a.Results {
		if r.Verdict == VerdictMismatch || r.Verdict == VerdictFailedToApply {
			out = append(out, r)
		}
	}
	return out
}

// EvaluatePresentationConsistency runs the pure, profile-aware checks. It
// compares the report's Observed surface against its Effective (intended)
// surface. It never normalizes to the host, never reuses a stale observation,
// and never treats an unavailable capability as an identity fault. It performs
// no browser or CDP call.
func EvaluatePresentationConsistency(r PresentationReport) ConsistencyAssessment {
	checks := []func(PresentationReport) ConsistencyResult{
		checkClientHintsMatchUA,
		checkAdvertisedOSMatchesPlatform,
		checkUserAgentDataPlatform,
		checkDeviceClass,
		checkIdentityApplied,
		checkRotationCompleteness,
		checkProxy,
		checkGPUPatch,
		checkCapabilities,
	}
	results := make([]ConsistencyResult, 0, len(checks))
	for _, c := range checks {
		results = append(results, c(r))
	}
	return ConsistencyAssessment{Results: results}
}

func result(check, expected, observed, note string, v ConsistencyVerdict) ConsistencyResult {
	return ConsistencyResult{Check: check, Expected: expected, Observed: observed, Note: note, Verdict: v}
}

// checkClientHintsMatchUA compares the selected User-Agent's engine version
// against the Client Hints the session advertised. The authoritative source,
// when a controlled recorder captured the wire version, is the Sec-CH-UA
// full-version; otherwise the observed browser brands or User-Agent are used;
// when neither is present the check is reported not_observed rather than a
// mismatch.
func checkClientHintsMatchUA(r PresentationReport) ConsistencyResult {
	const check = "client_hints_match_useragent"
	eff := r.Effective
	if !eff.ClientHints.Available {
		return result(check, "", "", "no Client Hints intended", VerdictNotRequested)
	}
	want, ok := wantEngineVersion(eff)
	if !ok {
		return result(check, "", "", "could not derive an expected engine version", VerdictInformational)
	}
	if http := r.Observed.HTTPClientHints; http != nil && http.UAFullVersion != "" {
		got := leadingMajor(http.UAFullVersion)
		return compareVersions(check, want, got, "sec_ch_ua_full_version")
	}
	if uad := r.Observed.UserAgentData; uad != nil {
		for _, b := range uad.Brands {
			if strings.EqualFold(b.Brand, "Chromium") {
				return compareVersions(check, want, b.Version, "navigator.userAgentData.brands")
			}
		}
	}
	if r.Observed.UserAgent != "" {
		if major, ok := ChromeMajorVersion(r.Observed.UserAgent); ok {
			return compareVersions(check, want, major, "navigator.userAgent")
		}
	}
	return result(check, want, "", "no Client Hints observation available", VerdictNotObserved)
}

// checkAdvertisedOSMatchesPlatform compares the advertised OS to navigator.platform.
func checkAdvertisedOSMatchesPlatform(r PresentationReport) ConsistencyResult {
	const check = "advertised_os_matches_navigator_platform"
	if r.Effective.NavigatorPlatform == "" && r.Observed.Platform == "" {
		return result(check, "", "", "neither platform present", VerdictNotRequested)
	}
	return compareObservedString(check, r.Effective.NavigatorPlatform, r.Observed.Platform, r.Observed.Status, "navigator.platform")
}

// checkUserAgentDataPlatform compares the advertised OS to
// navigator.userAgentData.platform when that surface exists.
func checkUserAgentDataPlatform(r PresentationReport) ConsistencyResult {
	const check = "advertised_os_matches_userAgentData_platform"
	want := r.Effective.UserAgentDataPlatform
	uad := r.Observed.UserAgentData
	if uad == nil {
		if r.Observed.Status == StatusNotObserved {
			return result(check, want, "", "userAgentData not observed", VerdictNotObserved)
		}
		return result(check, want, "", "navigator.userAgentData unavailable on this engine", VerdictUnsupported)
	}
	if want == "" {
		return result(check, "", uad.Platform, "no expected userAgentData platform", VerdictInformational)
	}
	if uad.Platform == "" {
		return result(check, want, "", "userAgentData platform empty", VerdictNotObserved)
	}
	if uad.Platform == want {
		return result(check, want, uad.Platform, "matches", VerdictConsistent)
	}
	return result(check, want, uad.Platform, "navigator.userAgentData.platform disagrees", VerdictMismatch)
}

// checkDeviceClass ensures crawler.platform (device class) is not confused with
// the advertised OS, and that the observed mobile flag agrees with the device
// class.
func checkDeviceClass(r PresentationReport) ConsistencyResult {
	const check = "device_class_not_confused_with_os"
	class := strings.ToLower(strings.TrimSpace(r.Requested.DeviceClass))
	if class == "" {
		class = strings.ToLower(strings.TrimSpace(r.Requested.AdvertisedOS))
	}
	if class == "" {
		return result(check, "desktop|mobile", "unset", "no device class or advertised OS recorded", VerdictInformational)
	}
	// A device class is exactly desktop or mobile; anything else is treated as a
	// possible confusion with an OS token and reported informationally (we never
	// normalize it).
	if class != "desktop" && class != "mobile" {
		return result(check, "desktop|mobile", class, "crawler.platform is not a device class; may be confused with the OS token", VerdictInformational)
	}
	wantMobile := class == "mobile"
	if wantMobile != r.Effective.Mobile {
		return result(check, fmt.Sprintf("mobile=%v", wantMobile), fmt.Sprintf("effective.mobile=%v", r.Effective.Mobile),
			"device class disagrees with the selected mobile-ness", VerdictMismatch)
	}
	if uad := r.Observed.UserAgentData; uad != nil && !uad.HighEntropy {
		if uad.Mobile != wantMobile {
			return result(check, fmt.Sprintf("mobile=%v", wantMobile), fmt.Sprintf("observed.mobile=%v", uad.Mobile),
				"observed userAgentData.mobile disagrees with the device class", VerdictMismatch)
		}
	}
	return result(check, class, "", "device class consistent with mobile-ness", VerdictConsistent)
}

// checkIdentityApplied relates the "applied" identity registration to the
// observed document identity.
func checkIdentityApplied(r PresentationReport) ConsistencyResult {
	const check = "identity_script_applied_vs_observed"
	switch r.Applied.IdentityScript.Status {
	case StatusApplyFailed:
		return result(check, "applied", r.Applied.IdentityScript.Detail, "identity script registration failed to apply", VerdictFailedToApply)
	case StatusNotRequested:
		return result(check, "", "", "identity script was not requested", VerdictNotRequested)
	}
	if r.Observed.UserAgent == "" {
		if r.Observed.Status == StatusKnown {
			return result(check, r.Effective.UserAgent, "", "identity applied but no observed User-Agent", VerdictInformational)
		}
		return result(check, r.Effective.UserAgent, "", "identity applied; observation pending", VerdictNotObserved)
	}
	gotMajor, ok := ChromeMajorVersion(r.Observed.UserAgent)
	if !ok {
		return result(check, r.Effective.UserAgent, r.Observed.UserAgent, "observed User-Agent unparseable", VerdictInformational)
	}
	wantMajor, _ := ChromeMajorVersion(r.Effective.UserAgent)
	if wantMajor == "" {
		return result(check, r.Effective.UserAgent, gotMajor, "expected User-Agent unparseable", VerdictInformational)
	}
	if gotMajor == wantMajor {
		return result(check, wantMajor, gotMajor, "document reflects the applied identity", VerdictConsistent)
	}
	return result(check, wantMajor, gotMajor, "document does not reflect the applied identity", VerdictMismatch)
}

// checkRotationCompleteness detects an incomplete identity update after an
// intentional rotation: when rotated, a fresh observation must reflect the new
// selection; when none is available it is reported not_observed and a stale
// observation is never reused.
func checkRotationCompleteness(r PresentationReport) ConsistencyResult {
	const check = "rotation_completeness"
	if !r.Rotated {
		return result(check, "", "", "no rotation in this event", VerdictNotRequested)
	}
	if r.Observed.UserAgent == "" {
		return result(check, r.Effective.UserAgent, "", "rotated; no fresh observation (no stale value reused)", VerdictNotObserved)
	}
	gotMajor, ok := ChromeMajorVersion(r.Observed.UserAgent)
	if !ok {
		return result(check, r.Effective.UserAgent, r.Observed.UserAgent, "rotated; observed User-Agent unparseable", VerdictInformational)
	}
	wantMajor, _ := ChromeMajorVersion(r.Effective.UserAgent)
	if gotMajor == wantMajor {
		return result(check, wantMajor, gotMajor, "rotation reflected in the document", VerdictConsistent)
	}
	return result(check, wantMajor, gotMajor, "rotation incomplete: document still shows a prior identity", VerdictMismatch)
}

// checkProxy compares the source-effective proxy with the launched proxy.
func checkProxy(r PresentationReport) ConsistencyResult {
	const check = "source_proxy_vs_applied_proxy"
	eff, appl := r.Effective.Proxy, r.Applied.Proxy
	if !r.Effective.ProxyRequested && eff == "" && appl == "" {
		return result(check, "", "", "no proxy requested", VerdictNotRequested)
	}
	if eff == "" && appl == "" {
		return result(check, "", "", "no proxy in effect", VerdictNotRequested)
	}
	if eff == appl {
		return result(check, eff, appl, "effective proxy launched", VerdictConsistent)
	}
	return result(check, eff, appl, "source-effective proxy differs from launched proxy", VerdictMismatch)
}

// checkGPUPatch verifies a requested GPU patch actually applied.
func checkGPUPatch(r PresentationReport) ConsistencyResult {
	const check = "gpu_patch_applied"
	if !r.Effective.GPUPatchRequested {
		return result(check, "", "", "GPU patch not requested", VerdictNotRequested)
	}
	switch r.Applied.GPUPatch.Status {
	case StatusApplyFailed:
		return result(check, "applied", r.Applied.GPUPatch.Detail, "GPU patch failed to apply", VerdictFailedToApply)
	case StatusNotRequested, StatusUnsupported:
		return result(check, "applied", string(r.Applied.GPUPatch.Status), "GPU patch requested but not applied", VerdictInformational)
	case StatusNotObserved, StatusEmpty:
		return result(check, "applied", string(r.Applied.GPUPatch.Status), "GPU patch application not observed in this phase", VerdictNotObserved)
	}
	// An empty status with a request means this phase did not record the outcome
	// (the patch is applied in a later setup phase): report not_observed rather
	// than claiming a success the report did not observe.
	if r.Applied.GPUPatch.Status == "" {
		return result(check, "applied", "", "GPU patch application not observed in this phase", VerdictNotObserved)
	}
	return result(check, "applied", "applied", "GPU patch applied", VerdictConsistent)
}

// checkCapabilities ensures a requested/applied capability that is merely absent
// is not read as a fault, while a broken (fabricated) one is a mismatch.
func checkCapabilities(r PresentationReport) ConsistencyResult {
	const check = "native_capability_consistency"
	if len(r.Observed.Capabilities) == 0 {
		return result(check, "", "", "no capability probed", VerdictNotObserved)
	}
	var broken, absent []string
	for _, c := range r.Observed.Capabilities {
		if !c.Requested && !c.Applied {
			continue
		}
		switch c.Observed {
		case CapabilityBroken:
			broken = append(broken, c.Name)
		case CapabilityAbsent:
			absent = append(absent, c.Name)
		}
	}
	if len(broken) > 0 {
		return result(check, "available", strings.Join(broken, ","), "a requested/applied capability behaves incorrectly", VerdictMismatch)
	}
	if len(absent) > 0 {
		return result(check, "available", strings.Join(absent, ","), "requested capability unavailable (not an identity fault)", VerdictUnsupported)
	}
	return result(check, "available", "", "requested capabilities available", VerdictConsistent)
}

// --- comparison helpers ---

// wantEngineVersion returns the Chromium major version the intended User-Agent
// advertises for Client Hints comparison.
func wantEngineVersion(eff EffectivePresentation) (string, bool) {
	if maj, ok := ChromeMajorVersion(eff.UserAgent); ok {
		return maj, true
	}
	if eff.ClientHints.FullVersion != "" {
		if maj := leadingMajor(eff.ClientHints.FullVersion); maj != "" {
			return maj, true
		}
	}
	return "", false
}

// leadingMajor extracts the leading numeric major from a version like
// "132.0.0.0".
func leadingMajor(v string) string {
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	if end == 0 {
		return ""
	}
	return v[:end]
}

// compareObservedString compares an expected surface with an observed one,
// honoring the observation status so an unobserved side is reported
// not_observed rather than a mismatch or a false consistent.
func compareObservedString(check, expected, observed string, oStatus DiagnosticStatus, from string) ConsistencyResult {
	if oStatus == StatusNotObserved || observed == "" {
		return result(check, expected, observed, fmt.Sprintf("observation not available from %s", from), VerdictNotObserved)
	}
	if expected == "" {
		return result(check, expected, observed, "no expected value to compare", VerdictInformational)
	}
	if observed == expected {
		return result(check, expected, observed, "matches", VerdictConsistent)
	}
	return result(check, expected, observed, fmt.Sprintf("%s disagrees with intended", from), VerdictMismatch)
}

// compareVersions compares engine majors.
func compareVersions(check, want, got, from string) ConsistencyResult {
	if got == "" {
		return result(check, want, "", fmt.Sprintf("no observation from %s", from), VerdictNotObserved)
	}
	if got == want {
		return result(check, want, got, "engine version agrees", VerdictConsistent)
	}
	return result(check, want, got, fmt.Sprintf("%s engine version disagrees", from), VerdictMismatch)
}
