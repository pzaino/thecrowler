// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"encoding/json"
	"net"
	"net/url"
	"sort"
	"strings"
)

// Candidate is a normalized source candidate discovered from an information seed.
type Candidate struct {
	URL             string                 `json:"url"`
	Host            string                 `json:"host"`
	Title           string                 `json:"title"`
	Provider        string                 `json:"provider"`
	Query           string                 `json:"query"`
	Rank            int                    `json:"rank"`
	Score           float64                `json:"score"`
	Reason          string                 `json:"reason"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	SourceOverrides SourceOverrides        `json:"source_overrides,omitempty"`
}

// SourceOverrides contains the safe subset of Source fields and SourceConfig
// that candidate plugins may override for a single accepted candidate.
// Identity and ownership fields such as URL, category_id, and usr_id are
// intentionally excluded and always come from the seed/default candidate flow.
type SourceOverrides struct {
	Name         *string         `json:"name,omitempty"`
	Priority     *string         `json:"priority,omitempty"`
	Restricted   *uint           `json:"restricted,omitempty"`
	Flags        *uint           `json:"flags,omitempty"`
	SourceConfig json.RawMessage `json:"source_config,omitempty"`
}

const (
	CandidateRejectionStageNormalization            = "normalization"
	CandidateRejectionStageBuiltInFilters           = "built_in_filters"
	CandidateRejectionStageUserPlugins              = "user_candidate_plugins"
	CandidateRejectionStageSourceOverrideValidation = "source_override_validation"

	CandidateRejectionInvalidURL             = "invalid_url"
	CandidateRejectionDuplicateURL           = "duplicate_url"
	CandidateRejectionDuplicateHost          = "duplicate_host"
	CandidateRejectionCandidateLimit         = "candidate_limit"
	CandidateRejectionAllowedDomain          = "allowed_domain"
	CandidateRejectionDeniedDomain           = "denied_domain"
	CandidateRejectionRequiredScheme         = "required_scheme"
	CandidateRejectionMinimumScore           = "minimum_score"
	CandidateRejectionMaxCandidatesPerHost   = "max_candidates_per_host"
	CandidateRejectionMaxCandidatesPerDomain = "max_candidates_per_domain"
	CandidateRejectionCandidateProcessor     = "candidate_processor"
	CandidateRejectionPluginOutputInvalid    = "plugin_output_invalid"
	CandidateRejectionSourceOverrideInvalid  = "source_override_invalid"
)

// CandidateOptions controls URL normalization and de-duplication.
type CandidateOptions struct {
	TrackingParams  []string
	DeduplicateHost bool
}

type CandidateNormalizationResult struct {
	Candidates []Candidate
	Rejected   map[string]int
}

// CandidateFilters contains deterministic built-in candidate filter settings.
type CandidateFilters struct {
	AllowedDomains         []string
	DeniedDomains          []string
	RequiredSchemes        []string
	MinScore               *float64
	MaxCandidatesPerHost   int
	MaxCandidatesPerDomain int
	MaxCandidates          int
}

// CandidateFilterResult contains filtered candidates and stable rejection counts.
type CandidateFilterResult struct {
	Candidates []Candidate
	Rejected   map[string]int
}

// NormalizeURL canonicalizes a URL for source creation and de-duplication.
func NormalizeURL(raw string, trackingParams []string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", false
	}
	u.Scheme = scheme
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return "", "", false
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}
	u.Fragment = ""
	removeTrackingParams(u, trackingParams)
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), host, true
}

// NormalizeCandidates normalizes candidates, drops unsupported URLs, and
// de-duplicates by normalized URL and optionally by hostname.
func NormalizeCandidates(candidates []Candidate, options CandidateOptions) []Candidate {
	return NormalizeCandidatesWithRejections(candidates, options).Candidates
}

// NormalizeCandidatesWithRejections normalizes candidates and returns stable
// rejection reasons for every dropped input candidate.
func NormalizeCandidatesWithRejections(candidates []Candidate, options CandidateOptions) CandidateNormalizationResult {
	seenURL := map[string]struct{}{}
	seenHost := map[string]struct{}{}
	rejected := map[string]int{}
	normalized := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		normalizedURL, host, ok := NormalizeURL(candidate.URL, options.TrackingParams)
		if !ok {
			rejected[CandidateRejectionInvalidURL]++
			continue
		}
		if _, exists := seenURL[normalizedURL]; exists {
			rejected[CandidateRejectionDuplicateURL]++
			continue
		}
		if options.DeduplicateHost {
			if _, exists := seenHost[host]; exists {
				rejected[CandidateRejectionDuplicateHost]++
				continue
			}
			seenHost[host] = struct{}{}
		}
		seenURL[normalizedURL] = struct{}{}
		candidate.URL = normalizedURL
		candidate.Host = host
		normalized = append(normalized, candidate)
	}
	return CandidateNormalizationResult{Candidates: normalized, Rejected: rejected}
}

// ApplyBuiltInCandidateFilters applies deterministic candidate policy before
// user candidate plugins run.
func ApplyBuiltInCandidateFilters(candidates []Candidate, filters CandidateFilters) CandidateFilterResult {
	rejected := map[string]int{}
	allowedDomains := domainSet(filters.AllowedDomains)
	deniedDomains := domainSet(filters.DeniedDomains)
	requiredSchemes := schemeSet(filters.RequiredSchemes)
	perHost := map[string]int{}
	perDomain := map[string]int{}
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		u, err := url.Parse(candidate.URL)
		if (err != nil) || (u.Scheme == "") || (u.Host == "") {
			rejected[CandidateRejectionInvalidURL]++
			continue
		}
		scheme := strings.ToLower(u.Scheme)
		host := strings.ToLower(strings.TrimSpace(candidate.Host))
		if host == "" {
			host = strings.ToLower(u.Hostname())
			candidate.Host = host
		}
		domain := registrableDomain(host)
		if len(requiredSchemes) > 0 {
			if _, ok := requiredSchemes[scheme]; !ok {
				rejected[CandidateRejectionRequiredScheme]++
				continue
			}
		}
		if (len(allowedDomains) > 0) && !matchesDomainSet(host, allowedDomains) && !matchesDomainSet(domain, allowedDomains) {
			rejected[CandidateRejectionAllowedDomain]++
			continue
		}
		if matchesDomainSet(host, deniedDomains) || matchesDomainSet(domain, deniedDomains) {
			rejected[CandidateRejectionDeniedDomain]++
			continue
		}
		if (filters.MinScore != nil) && (candidate.Score < *filters.MinScore) {
			rejected[CandidateRejectionMinimumScore]++
			continue
		}
		if (filters.MaxCandidatesPerHost > 0) && (perHost[host] >= filters.MaxCandidatesPerHost) {
			rejected[CandidateRejectionMaxCandidatesPerHost]++
			continue
		}
		if (filters.MaxCandidatesPerDomain > 0) && (perDomain[domain] >= filters.MaxCandidatesPerDomain) {
			rejected[CandidateRejectionMaxCandidatesPerDomain]++
			continue
		}
		if (filters.MaxCandidates > 0) && (len(filtered) >= filters.MaxCandidates) {
			rejected[CandidateRejectionCandidateLimit]++
			continue
		}
		perHost[host]++
		perDomain[domain]++
		filtered = append(filtered, candidate)
	}
	return CandidateFilterResult{Candidates: filtered, Rejected: rejected}
}

func domainSet(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, ".")
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func schemeSet(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func matchesDomainSet(host string, set map[string]struct{}) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for domain := range set {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func registrableDomain(host string) string {
	parts := strings.Split(strings.Trim(strings.ToLower(host), "."), ".")
	if len(parts) <= 2 {
		return strings.Join(parts, ".")
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func removeTrackingParams(u *url.URL, trackingParams []string) {
	if len(trackingParams) == 0 || u.RawQuery == "" {
		return
	}
	remove := map[string]struct{}{}
	for _, param := range trackingParams {
		param = strings.ToLower(strings.TrimSpace(param))
		if param != "" {
			remove[param] = struct{}{}
		}
	}
	values := u.Query()
	for key := range values {
		if _, ok := remove[strings.ToLower(key)]; ok {
			values.Del(key)
		}
	}
	// url.Values.Encode sorts keys for stable candidate identity.
	u.RawQuery = values.Encode()
}

func defaultTrackingParams() []string {
	params := []string{"fbclid", "gclid", "mc_cid", "mc_eid", "msclkid", "utm_campaign", "utm_content", "utm_medium", "utm_source", "utm_term"}
	sort.Strings(params)
	return params
}
