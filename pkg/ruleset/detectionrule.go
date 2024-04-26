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
func (d *DetectionRule) GetAllHTTPHeaderFields() []HTTPHeaderField {
	return d.HTTPHeaderFields
}

// GetPageContentPatterns returns the page content patterns for the specified detection rule.
func (d *DetectionRule) GetAllPageContentPatterns() []PageContentSignature {
	trimmedPatterns := []PageContentSignature{}
	for _, pattern := range d.PageContentPatterns {
		trimmedPatterns = append(trimmedPatterns, PageContentSignature{
			Key:        strings.TrimSpace(pattern.Key),
			Signature:  PrepareSlice(pattern.Signature, 1), // flag = 1 only trim spaces
			Confidence: pattern.Confidence,
		},
		)
	}
	return trimmedPatterns
}

// GetAllSSLSignatures returns the SSL signatures for the specified detection rule.
func (d *DetectionRule) GetAllSSLSignatures() []SSLSignature {
	return d.SSLSignatures
}

// GetURLMicroSignatures returns the URL micro-signatures for the specified detection rule.
func (d *DetectionRule) GetAllURLMicroSignatures() []URLMicroSignature {
	trimmedSignatures := []URLMicroSignature{}
	for _, signature := range d.URLMicroSignatures {
		trimmedSignatures = append(trimmedSignatures, URLMicroSignature{
			Signature:  strings.TrimSpace(signature.Signature),
			Confidence: signature.Confidence,
		},
		)
	}
	return trimmedSignatures
}

// GetMetaTags returns the meta tags for the specified detection rule.
func (d *DetectionRule) GetAllMetaTags() []MetaTag {
	trimmedMetaTags := []MetaTag{}
	for _, tag := range d.MetaTags {
		trimmedMetaTags = append(trimmedMetaTags, MetaTag{
			Name:       strings.TrimSpace(tag.Name),
			Content:    strings.TrimSpace(tag.Content),
			Confidence: tag.Confidence,
		},
		)
	}
	return trimmedMetaTags
}

/// --- Special Getters --- ///

// GetAllHTTPHeaderFieldsMap returns a map of all HTTP header fields for the specified detection rules.
func GetAllHTTPHeaderFieldsMap(d *[]DetectionRule) map[string]map[string]HTTPHeaderField {
	headers := make(map[string]map[string]HTTPHeaderField)
	for _, rule := range *d {
		for _, header := range rule.HTTPHeaderFields {
			if header.GetKey() == "*" {
				item := make(map[string]HTTPHeaderField)
				item[strings.ToLower(header.GetKey())] = header
				headers[strings.ToLower(rule.ObjectName)] = item
			}
		}
	}

	return headers
}

// GetHTTPHeaderFieldsMapByKey returns a map of all HTTP header fields for the specified detection rules.
func GetHTTPHeaderFieldsMapByKey(d *[]DetectionRule, key string) map[string]map[string]HTTPHeaderField {
	headers := make(map[string]map[string]HTTPHeaderField)
	key = strings.ToLower(strings.TrimSpace(key))
	for _, rule := range *d {
		for _, header := range rule.HTTPHeaderFields {
			if strings.ToLower(strings.TrimSpace(header.GetKey())) != key {
				continue
			}
			item := make(map[string]HTTPHeaderField)
			item[strings.ToLower(header.GetKey())] = header
			headers[strings.ToLower(rule.ObjectName)] = item
		}
	}

	return headers
}

// GetAllMetaTagsMap returns a map of all meta tags for the specified detection rules.
func GetAllURLMicroSignaturesMap(d *[]DetectionRule) map[string][]URLMicroSignature {
	signatures := make(map[string][]URLMicroSignature)
	for _, rule := range *d {
		signatures[strings.ToLower(rule.ObjectName)] = rule.URLMicroSignatures
	}
	return signatures
}

// GetAllMetaTagsMap returns a map of all meta tags for the specified detection rules.
func GetAllPageContentPatternsMap(d *[]DetectionRule) map[string][]PageContentSignature {
	patterns := make(map[string][]PageContentSignature)
	for _, rule := range *d {
		patterns[strings.ToLower(rule.ObjectName)] = rule.PageContentPatterns
	}
	return patterns
}

// GetAllSSLSignaturesMap returns a map of all SSL signatures for the specified detection rules.
func GetAllSSLSignaturesMap(d *[]DetectionRule) map[string][]SSLSignature {
	signatures := make(map[string][]SSLSignature)
	for _, rule := range *d {
		signatures[strings.ToLower(rule.ObjectName)] = rule.SSLSignatures
	}
	return signatures
}

// GetAllMetaTagsMap returns a map of all meta tags for the specified detection rules.
func GetAllMetaTagsMap(d *[]DetectionRule) map[string][]MetaTag {
	tags := make(map[string][]MetaTag)
	for _, rule := range *d {
		tags[strings.ToLower(rule.ObjectName)] = rule.MetaTags
	}
	return tags
}

///// --------------------- HTTPHeaderField ------------------------------- /////

// GetKey returns the key of the HTTP header field.
func (h *HTTPHeaderField) GetKey() string {
	return strings.TrimSpace(h.Key)
}

// GetValue returns the value of the HTTP header field.
func (h *HTTPHeaderField) GetValue(index int) string {
	if index >= len(h.Value) {
		return ""
	}
	return strings.TrimSpace(h.Value[index])
}

// GetAllValues returns all the values of the HTTP header field.
func (h *HTTPHeaderField) GetAllValues() []string {
	trimmedValues := []string{}
	for _, value := range h.Value {
		trimmedValues = append(trimmedValues, strings.TrimSpace(value))
	}
	return trimmedValues
}

// GetConfidence returns the confidence level of the HTTP header field.
func (h *HTTPHeaderField) GetConfidence() float32 {
	return h.Confidence
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
