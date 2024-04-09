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

// Package ruleset implements the ruleset library for the Crowler and
// the scrapper.
package ruleset

import (
	"strings"
)

///// --------------------- DetectionRule ------------------------------- /////

// GetRuleName returns the rule name for the specified detection rule.
func (d *DetectionRule) GetRuleName() string {
	return strings.TrimSpace(d.RuleName)
}

// GetObjectName returns the object name targeted by the detection rule.
func (d *DetectionRule) GetObjectName() string {
	return strings.TrimSpace(d.ObjectName)
}

// GetObjectVersion returns the object version targeted by the detection rule.
func (d *DetectionRule) GetObjectVersion() string {
	return strings.TrimSpace(d.ObjectVersion)
}

// GetHTTPHeaderFields returns the HTTP header fields for the specified detection rule.
func (d *DetectionRule) GetHTTPHeaderFields() []HTTPHeaderField {
	return d.HTTPHeaderFields
}

// GetPageContentPatterns returns the page content patterns for the specified detection rule.
func (d *DetectionRule) GetPageContentPatterns() []string {
	trimmedPatterns := []string{}
	for _, pattern := range d.PageContentPatterns {
		trimmedPatterns = append(trimmedPatterns, strings.TrimSpace(pattern))
	}
	return trimmedPatterns
}

// GetURLMicroSignatures returns the URL micro-signatures for the specified detection rule.
func (d *DetectionRule) GetURLMicroSignatures() []string {
	trimmedSignatures := []string{}
	for _, signature := range d.URLMicroSignatures {
		trimmedSignatures = append(trimmedSignatures, strings.TrimSpace(signature))
	}
	return trimmedSignatures
}

// GetMetaTags returns the meta tags for the specified detection rule.
func (d *DetectionRule) GetMetaTags() []MetaTag {
	return d.MetaTags
}

///// --------------------- HTTPHeaderField ------------------------------- /////

// GetKey returns the key of the HTTP header field.
func (h *HTTPHeaderField) GetKey() string {
	return strings.TrimSpace(h.Key)
}

// GetValue returns the value of the HTTP header field.
func (h *HTTPHeaderField) GetValue() string {
	return strings.TrimSpace(h.Value)
}

///// --------------------- MetaTag ------------------------------- /////

// GetName returns the name attribute of the meta tag.
func (m *MetaTag) GetName() string {
	return strings.TrimSpace(m.Name)
}

// GetContent returns the content attribute of the meta tag.
func (m *MetaTag) GetContent() string {
	return strings.TrimSpace(m.Content)
}
