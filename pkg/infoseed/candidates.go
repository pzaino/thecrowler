// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"net"
	"net/url"
	"sort"
	"strings"
)

// Candidate is a normalized source candidate discovered from an information seed.
type Candidate struct {
	URL      string
	Host     string
	Title    string
	Provider string
	Query    string
	Rank     int
	Score    float64
	Reason   string
	Metadata map[string]interface{}
}

// CandidateOptions controls URL normalization and de-duplication.
type CandidateOptions struct {
	TrackingParams  []string
	DeduplicateHost bool
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
	seenURL := map[string]struct{}{}
	seenHost := map[string]struct{}{}
	normalized := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		normalizedURL, host, ok := NormalizeURL(candidate.URL, options.TrackingParams)
		if !ok {
			continue
		}
		if _, exists := seenURL[normalizedURL]; exists {
			continue
		}
		if options.DeduplicateHost {
			if _, exists := seenHost[host]; exists {
				continue
			}
			seenHost[host] = struct{}{}
		}
		seenURL[normalizedURL] = struct{}{}
		candidate.URL = normalizedURL
		candidate.Host = host
		normalized = append(normalized, candidate)
	}
	return normalized
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
