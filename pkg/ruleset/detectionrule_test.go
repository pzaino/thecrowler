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
	"testing"
)

func TestGetRuleName(t *testing.T) {
	dr := DetectionRule{RuleName: " RuleName "}
	expected := "RuleName"
	if got := dr.GetRuleName(); got != expected {
		t.Errorf("GetRuleName() = %v, want %v", got, expected)
	}
}

func TestGetObjectName(t *testing.T) {
	dr := DetectionRule{ObjectName: " ObjectName "}
	expected := "ObjectName"
	if got := dr.GetObjectName(); got != expected {
		t.Errorf("GetObjectName() = %v, want %v", got, expected)
	}
}

func TestHTTPHeaderFieldGetKey(t *testing.T) {
	h := HTTPHeaderField{Key: " Content-Type "}
	expected := "Content-Type"
	if got := h.GetKey(); got != expected {
		t.Errorf("GetKey() = %v, want %v", got, expected)
	}
}

func TestHTTPHeaderFieldGetValue(t *testing.T) {
	h := HTTPHeaderField{Value: []string{
		" application/json ",
	},
	}
	expected := "application/json"
	if got := h.GetValue(0); got != expected {
		t.Errorf("GetValue() = %v, want %v", got, expected)
	}
}

func TestMetaTagGetName(t *testing.T) {
	m := MetaTag{Name: " viewport "}
	expected := "viewport"
	if got := m.GetName(); got != expected {
		t.Errorf("GetName() = %v, want %v", got, expected)
	}
}

func TestMetaTagGetContent(t *testing.T) {
	m := MetaTag{Content: " width=device-width, initial-scale=1 "}
	expected := "width=device-width, initial-scale=1"
	if got := m.GetContent(); got != expected {
		t.Errorf("GetContent() = %v, want %v", got, expected)
	}
}

func TestGetImplies(t *testing.T) {
	dr := DetectionRule{Implies: []string{"rule1", "rule2"}}
	expected := []string{"rule1", "rule2"}
	if got := dr.GetImplies(); !equalStringSlices(got, expected) {
		t.Errorf("GetImplies() = %v, want %v", got, expected)
	}
}

func TestGetPluginCalls(t *testing.T) {
	pluginCalls := []PluginCall{
		{PluginName: "plugin1", PluginArgs: []PluginParams{{ArgName: "param1", ArgValue: nil, Properties: PluginParamsProperties{}}}},
		{PluginName: "plugin2", PluginArgs: []PluginParams{{ArgName: "param2", ArgValue: nil, Properties: PluginParamsProperties{}}}},
	}
	dr := DetectionRule{PluginCalls: pluginCalls}
	expected := pluginCalls
	if got := dr.GetPluginCalls(); !equalPluginCalls(got, expected) {
		t.Errorf("GetPluginCalls() = %v, want %v", got, expected)
	}
}

// Helper function to compare two PluginCall structs
func equalPluginCall(a, b PluginCall) bool {
	if a.PluginName != b.PluginName {
		return false
	}
	if len(a.PluginArgs) != len(b.PluginArgs) {
		return false
	}
	for i := range a.PluginArgs {
		if !equalPluginParams(a.PluginArgs[i], b.PluginArgs[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two PluginParams structs
func equalPluginParams(a, b PluginParams) bool {
	if a.ArgName != b.ArgName {
		return false
	}
	if a.ArgValue != b.ArgValue {
		return false
	}
	if a.Properties != b.Properties {
		return false
	}
	return true
}

// Helper function to compare two slices of PluginCall
func equalPluginCalls(a, b []PluginCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].PluginName != b[i].PluginName {
			return false
		}
	}
	return true
}

// Helper function to compare two slices of strings
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGetExternalDetections(t *testing.T) {
	externalDetections := []ExternalDetection{
		{Provider: "detection1", DetectionParams: []DetectionParams{{ParamName: "param1", ParamValue: nil}}},
		{Provider: "detection2", DetectionParams: []DetectionParams{{ParamName: "param2", ParamValue: nil}}},
	}
	dr := DetectionRule{ExternalDetections: externalDetections}
	expected := externalDetections
	if got := dr.GetExternalDetections(); !equalExternalDetections(got, expected) {
		t.Errorf("GetExternalDetections() = %v, want %v", got, expected)
	}
}

func TestHTTPHeaderFieldGetAllValues(t *testing.T) {
	h := HTTPHeaderField{Value: []string{
		" application/json ",
		" text/html ",
		" text/plain ",
	}}
	expected := []string{"application/json", "text/html", "text/plain"}
	if got := h.GetAllValues(); !equalStringSlices(got, expected) {
		t.Errorf("GetAllValues() = %v, want %v", got, expected)
	}
}

func TestHTTPHeaderFieldGetConfidence(t *testing.T) {
	h := HTTPHeaderField{Confidence: 0.85}
	expected := float32(0.85)
	if got := h.GetConfidence(); got != expected {
		t.Errorf("GetConfidence() = %v, want %v", got, expected)
	}
}

func TestGetAllHTTPHeaderFields(t *testing.T) {
	httpHeaderFields := []HTTPHeaderField{
		{Key: "Content-Type", Value: []string{"application/json"}, Confidence: 0.9},
		{Key: "Accept", Value: []string{"text/html"}, Confidence: 0.8},
	}
	dr := DetectionRule{HTTPHeaderFields: httpHeaderFields}
	expected := httpHeaderFields
	if got := dr.GetAllHTTPHeaderFields(); !equalHTTPHeaderFields(got, expected) {
		t.Errorf("GetAllHTTPHeaderFields() = %v, want %v", got, expected)
	}
}

func TestGetAllPageContentPatterns(t *testing.T) {
	pageContentPatterns := []PageContentSignature{
		{
			Key:        " Key1 ",
			Signature:  []string{" Signature1 ", " Signature2 "},
			Text:       []string{" Text1 ", " Text2 "},
			Confidence: 0.9,
		},
		{
			Key:        " Key2 ",
			Signature:  []string{" Signature3 ", " Signature4 "},
			Text:       []string{" Text3 ", " Text4 "},
			Confidence: 0.8,
		},
	}
	expected := []PageContentSignature{
		{
			Key:        "Key1",
			Signature:  []string{"Signature1", "Signature2"},
			Text:       []string{" Text1 ", " Text2 "},
			Confidence: 0.9,
		},
		{
			Key:        "Key2",
			Signature:  []string{"Signature3", "Signature4"},
			Text:       []string{" Text3 ", " Text4 "},
			Confidence: 0.8,
		},
	}

	// Mock the cmn.PrepareSlice function
	/*
		prepareSlice := func(slice *[]string, flag int) []string {
			trimmed := []string{}
			for _, s := range *slice {
				trimmed = append(trimmed, strings.TrimSpace(s))
			}
			return trimmed
		}
	*/

	dr := DetectionRule{PageContentPatterns: pageContentPatterns}
	if got := dr.GetAllPageContentPatterns(); !equalPageContentSignatures(got, expected) {
		t.Errorf("GetAllPageContentPatterns() = %v, want %v", got, expected)
	}
}

func TestGetAllSSLSignatures(t *testing.T) {
	sslSignatures := []SSLSignature{
		{Key: "SSLSignature1",
			Value:      []string{"SSLValue1"},
			Confidence: 0.9},
		{Key: "SSLSignature2",
			Value:      []string{"SSLValue2"},
			Confidence: 0.8},
	}
	dr := DetectionRule{SSLSignatures: sslSignatures}
	expected := sslSignatures
	if got := dr.GetAllSSLSignatures(); !equalSSLSignatures(got, expected) {
		t.Errorf("GetAllSSLSignatures() = %v, want %v", got, expected)
	}
}

func TestGetAllURLMicroSignatures(t *testing.T) {
	urlMicroSignatures := []URLMicroSignature{
		{
			Signature:  " Signature1 ",
			Confidence: 0.9,
		},
		{
			Signature:  " Signature2 ",
			Confidence: 0.8,
		},
	}
	expected := []URLMicroSignature{
		{
			Signature:  "Signature1",
			Confidence: 0.9,
		},
		{
			Signature:  "Signature2",
			Confidence: 0.8,
		},
	}

	dr := DetectionRule{URLMicroSignatures: urlMicroSignatures}
	if got := dr.GetAllURLMicroSignatures(); !equalURLMicroSignatures(got, expected) {
		t.Errorf("GetAllURLMicroSignatures() = %v, want %v", got, expected)
	}
}

func TestGetAllMetaTags(t *testing.T) {
	metaTags := []MetaTag{
		{
			Name:       " viewport ",
			Content:    " width=device-width, initial-scale=1 ",
			Confidence: 0.9,
		},
		{
			Name:       " description ",
			Content:    " A sample description ",
			Confidence: 0.8,
		},
	}
	expected := []MetaTag{
		{
			Name:       "viewport",
			Content:    "width=device-width, initial-scale=1",
			Confidence: 0.9,
		},
		{
			Name:       "description",
			Content:    "A sample description",
			Confidence: 0.8,
		},
	}

	dr := DetectionRule{MetaTags: metaTags}
	if got := dr.GetAllMetaTags(); !equalMetaTags(got, expected) {
		t.Errorf("GetAllMetaTags() = %v, want %v", got, expected)
	}
}

func TestGetAllHTTPHeaderFieldsMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			HTTPHeaderFields: []HTTPHeaderField{
				{Key: "*", Value: []string{"application/json"}, Confidence: 0.9},
				{Key: "Content-Type", Value: []string{"text/html"}, Confidence: 0.8},
			},
		},
		{
			ObjectName: "Object2",
			HTTPHeaderFields: []HTTPHeaderField{
				{Key: "*", Value: []string{"text/plain"}, Confidence: 0.7},
			},
		},
	}

	expected := map[string]map[string]HTTPHeaderField{
		"object1": {
			"*": {Key: "*", Value: []string{"application/json"}, Confidence: 0.9},
		},
		"object2": {
			"*": {Key: "*", Value: []string{"text/plain"}, Confidence: 0.7},
		},
	}

	got := GetAllHTTPHeaderFieldsMap(&detectionRules)
	if !equalHTTPHeaderFieldsMap(got, expected) {
		t.Errorf("GetAllHTTPHeaderFieldsMap() = %v, want %v", got, expected)
	}
}

func TestGetHTTPHeaderFieldsMapByKey(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			HTTPHeaderFields: []HTTPHeaderField{
				{Key: "Content-Type", Value: []string{"application/json"}, Confidence: 0.9},
				{Key: "Accept", Value: []string{"text/html"}, Confidence: 0.8},
			},
		},
		{
			ObjectName: "Object2",
			HTTPHeaderFields: []HTTPHeaderField{
				{Key: "Content-Type", Value: []string{"text/plain"}, Confidence: 0.7},
			},
		},
		{
			ObjectName: "Object3",
			HTTPHeaderFields: []HTTPHeaderField{
				{Key: "Authorization", Value: []string{"Bearer token"}, Confidence: 0.95},
			},
		},
	}

	tests := []struct {
		name     string
		key      string
		expected map[string]map[string]HTTPHeaderField
	}{
		{
			name: "Match Content-Type",
			key:  "Content-Type",
			expected: map[string]map[string]HTTPHeaderField{
				"object1": {
					"content-type": {Key: "Content-Type", Value: []string{"application/json"}, Confidence: 0.9},
				},
				"object2": {
					"content-type": {Key: "Content-Type", Value: []string{"text/plain"}, Confidence: 0.7},
				},
			},
		},
		{
			name: "Match Authorization",
			key:  "Authorization",
			expected: map[string]map[string]HTTPHeaderField{
				"object3": {
					"authorization": {Key: "Authorization", Value: []string{"Bearer token"}, Confidence: 0.95},
				},
			},
		},
		{
			name:     "No Match",
			key:      "Non-Existent-Key",
			expected: map[string]map[string]HTTPHeaderField{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetHTTPHeaderFieldsMapByKey(&detectionRules, tt.key)
			if !equalHTTPHeaderFieldsMap(got, tt.expected) {
				t.Errorf("GetHTTPHeaderFieldsMapByKey() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetAllURLMicroSignaturesMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			URLMicroSignatures: []URLMicroSignature{
				{Signature: "Signature1", Confidence: 0.9},
				{Signature: "Signature2", Confidence: 0.8},
			},
		},
		{
			ObjectName: "Object2",
			URLMicroSignatures: []URLMicroSignature{
				{Signature: "Signature3", Confidence: 0.7},
			},
		},
		{
			ObjectName: "Object1", // Duplicate ObjectName to test appending
			URLMicroSignatures: []URLMicroSignature{
				{Signature: "Signature4", Confidence: 0.85},
			},
		},
	}

	expected := map[string][]URLMicroSignature{
		"object1": {
			{Signature: "Signature1", Confidence: 0.9},
			{Signature: "Signature2", Confidence: 0.8},
			{Signature: "Signature4", Confidence: 0.85},
		},
		"object2": {
			{Signature: "Signature3", Confidence: 0.7},
		},
	}

	got := GetAllURLMicroSignaturesMap(&detectionRules)
	if !equalURLMicroSignaturesMap(got, expected) {
		t.Errorf("GetAllURLMicroSignaturesMap() = %v, want %v", got, expected)
	}
}

func TestGetAllPageContentPatternsMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			PageContentPatterns: []PageContentSignature{
				{
					Key:        "Key1",
					Signature:  []string{"Signature1", "Signature2"},
					Text:       []string{"Text1", "Text2"},
					Confidence: 0.9,
				},
				{
					Key:        "Key2",
					Signature:  []string{"Signature3", "Signature4"},
					Text:       []string{"Text3", "Text4"},
					Confidence: 0.8,
				},
			},
		},
		{
			ObjectName: "Object2",
			PageContentPatterns: []PageContentSignature{
				{
					Key:        "Key3",
					Signature:  []string{"Signature5"},
					Text:       []string{"Text5"},
					Confidence: 0.7,
				},
			},
		},
		{
			ObjectName: "Object1", // Duplicate ObjectName to test appending
			PageContentPatterns: []PageContentSignature{
				{
					Key:        "Key4",
					Signature:  []string{"Signature6"},
					Text:       []string{"Text6"},
					Confidence: 0.85,
				},
			},
		},
	}

	expected := map[string][]PageContentSignature{
		"object1": {
			{
				Key:        "Key1",
				Signature:  []string{"Signature1", "Signature2"},
				Text:       []string{"Text1", "Text2"},
				Confidence: 0.9,
			},
			{
				Key:        "Key2",
				Signature:  []string{"Signature3", "Signature4"},
				Text:       []string{"Text3", "Text4"},
				Confidence: 0.8,
			},
			{
				Key:        "Key4",
				Signature:  []string{"Signature6"},
				Text:       []string{"Text6"},
				Confidence: 0.85,
			},
		},
		"object2": {
			{
				Key:        "Key3",
				Signature:  []string{"Signature5"},
				Text:       []string{"Text5"},
				Confidence: 0.7,
			},
		},
	}

	got := GetAllPageContentPatternsMap(&detectionRules)
	if !equalPageContentPatternsMap(got, expected) {
		t.Errorf("GetAllPageContentPatternsMap() = %v, want %v", got, expected)
	}
}

func TestGetAllSSLSignaturesMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			SSLSignatures: []SSLSignature{
				{Key: "SSLSignature1", Value: []string{"SSLValue1"}, Confidence: 0.9},
				{Key: "SSLSignature2", Value: []string{"SSLValue2"}, Confidence: 0.8},
			},
		},
		{
			ObjectName: "Object2",
			SSLSignatures: []SSLSignature{
				{Key: "SSLSignature3", Value: []string{"SSLValue3"}, Confidence: 0.7},
			},
		},
		{
			ObjectName: "Object1", // Duplicate ObjectName to test appending
			SSLSignatures: []SSLSignature{
				{Key: "SSLSignature4", Value: []string{"SSLValue4"}, Confidence: 0.85},
			},
		},
	}

	expected := map[string][]SSLSignature{
		"object1": {
			{Key: "SSLSignature1", Value: []string{"SSLValue1"}, Confidence: 0.9},
			{Key: "SSLSignature2", Value: []string{"SSLValue2"}, Confidence: 0.8},
			{Key: "SSLSignature4", Value: []string{"SSLValue4"}, Confidence: 0.85},
		},
		"object2": {
			{Key: "SSLSignature3", Value: []string{"SSLValue3"}, Confidence: 0.7},
		},
	}

	got := GetAllSSLSignaturesMap(&detectionRules)
	if !equalSSLSignaturesMap(got, expected) {
		t.Errorf("GetAllSSLSignaturesMap() = %v, want %v", got, expected)
	}
}

func TestGetAllMetaTagsMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			MetaTags: []MetaTag{
				{Name: "viewport", Content: "width=device-width, initial-scale=1", Confidence: 0.9},
				{Name: "description", Content: "A sample description", Confidence: 0.8},
			},
		},
		{
			ObjectName: "Object2",
			MetaTags: []MetaTag{
				{Name: "keywords", Content: "sample, test", Confidence: 0.7},
			},
		},
		{
			ObjectName: "Object1", // Duplicate ObjectName to test appending
			MetaTags: []MetaTag{
				{Name: "author", Content: "John Doe", Confidence: 0.85},
			},
		},
	}

	expected := map[string][]MetaTag{
		"object1": {
			{Name: "viewport", Content: "width=device-width, initial-scale=1", Confidence: 0.9},
			{Name: "description", Content: "A sample description", Confidence: 0.8},
			{Name: "author", Content: "John Doe", Confidence: 0.85},
		},
		"object2": {
			{Name: "keywords", Content: "sample, test", Confidence: 0.7},
		},
	}

	got := GetAllMetaTagsMap(&detectionRules)
	if !equalMetaTagsMap(got, expected) {
		t.Errorf("GetAllMetaTagsMap() = %v, want %v", got, expected)
	}
}

func TestGetAllPluginCallsMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			PluginCalls: []PluginCall{
				{PluginName: "Plugin1", PluginArgs: []PluginParams{{ArgName: "Arg1", ArgValue: "Value1"}}},
				{PluginName: "Plugin2", PluginArgs: []PluginParams{{ArgName: "Arg2", ArgValue: "Value2"}}},
			},
		},
		{
			ObjectName: "Object2",
			PluginCalls: []PluginCall{
				{PluginName: "Plugin3", PluginArgs: []PluginParams{{ArgName: "Arg3", ArgValue: "Value3"}}},
			},
		},
		{
			ObjectName: "Object1", // Duplicate ObjectName to test appending
			PluginCalls: []PluginCall{
				{PluginName: "Plugin4", PluginArgs: []PluginParams{{ArgName: "Arg4", ArgValue: "Value4"}}},
			},
		},
	}

	expected := map[string][]PluginCall{
		"object1": {
			{PluginName: "Plugin1", PluginArgs: []PluginParams{{ArgName: "Arg1", ArgValue: "Value1"}}},
			{PluginName: "Plugin2", PluginArgs: []PluginParams{{ArgName: "Arg2", ArgValue: "Value2"}}},
			{PluginName: "Plugin4", PluginArgs: []PluginParams{{ArgName: "Arg4", ArgValue: "Value4"}}},
		},
		"object2": {
			{PluginName: "Plugin3", PluginArgs: []PluginParams{{ArgName: "Arg3", ArgValue: "Value3"}}},
		},
	}

	got := GetAllPluginCallsMap(&detectionRules)
	if !equalPluginCallsMap(got, expected) {
		t.Errorf("GetAllPluginCallsMap() = %v, want %v", got, expected)
	}
}

func TestGetAllExternalDetectionsMap(t *testing.T) {
	detectionRules := []DetectionRule{
		{
			ObjectName: "Object1",
			ExternalDetections: []ExternalDetection{
				{Provider: "Provider1", DetectionParams: []DetectionParams{{ParamName: "Param1", ParamValue: "Value1"}}},
				{Provider: "Provider2", DetectionParams: []DetectionParams{{ParamName: "Param2", ParamValue: "Value2"}}},
			},
		},
		{
			ObjectName: "Object2",
			ExternalDetections: []ExternalDetection{
				{Provider: "Provider3", DetectionParams: []DetectionParams{{ParamName: "Param3", ParamValue: "Value3"}}},
			},
		},
		{
			ObjectName: "Object1", // Duplicate ObjectName to test appending
			ExternalDetections: []ExternalDetection{
				{Provider: "Provider4", DetectionParams: []DetectionParams{{ParamName: "Param4", ParamValue: "Value4"}}},
			},
		},
	}

	expected := map[string][]ExternalDetection{
		"object1": {
			{Provider: "Provider1", DetectionParams: []DetectionParams{{ParamName: "Param1", ParamValue: "Value1"}}},
			{Provider: "Provider2", DetectionParams: []DetectionParams{{ParamName: "Param2", ParamValue: "Value2"}}},
			{Provider: "Provider4", DetectionParams: []DetectionParams{{ParamName: "Param4", ParamValue: "Value4"}}},
		},
		"object2": {
			{Provider: "Provider3", DetectionParams: []DetectionParams{{ParamName: "Param3", ParamValue: "Value3"}}},
		},
	}

	got := GetAllExternalDetectionsMap(&detectionRules)
	if !equalExternalDetectionsMap(got, expected) {
		t.Errorf("GetAllExternalDetectionsMap() = %v, want %v", got, expected)
	}
}

// Helper function to compare two maps of ExternalDetection
func equalExternalDetectionsMap(a, b map[string][]ExternalDetection) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalExternalDetections(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two maps of PluginCall
func equalPluginCallsMap(a, b map[string][]PluginCall) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalPluginCalls(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two maps of MetaTag
func equalMetaTagsMap(a, b map[string][]MetaTag) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalMetaTags(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two maps of SSLSignature
func equalSSLSignaturesMap(a, b map[string][]SSLSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalSSLSignatures(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two maps of PageContentSignature
func equalPageContentPatternsMap(a, b map[string][]PageContentSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalPageContentSignatures(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two maps of URLMicroSignature
func equalURLMicroSignaturesMap(a, b map[string][]URLMicroSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalURLMicroSignatures(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two maps of HTTPHeaderField
func equalHTTPHeaderFieldsMap(a, b map[string]map[string]HTTPHeaderField) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if _, ok := b[key]; !ok {
			return false
		}
		if !equalHTTPHeaderFieldsMapInner(value, b[key]) {
			return false
		}
	}
	return true
}

// Helper function to compare two inner maps of HTTPHeaderField
func equalHTTPHeaderFieldsMapInner(a, b map[string]HTTPHeaderField) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if !equalHTTPHeaderField(b[key], value) {
			return false
		}
	}
	return true
}

// Helper function to compare two MetaTag structs
func equalMetaTag(a, b MetaTag) bool {
	if a.Name != b.Name {
		return false
	}
	if a.Content != b.Content {
		return false
	}
	if a.Confidence != b.Confidence {
		return false
	}
	return true
}

// Helper function to compare two slices of MetaTag
func equalMetaTags(a, b []MetaTag) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalMetaTag(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two URLMicroSignature structs
func equalURLMicroSignature(a, b URLMicroSignature) bool {
	if a.Signature != b.Signature {
		return false
	}
	if a.Confidence != b.Confidence {
		return false
	}
	return true
}

// Helper function to compare two slices of URLMicroSignature
func equalURLMicroSignatures(a, b []URLMicroSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalURLMicroSignature(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two SSLSignature structs
func equalSSLSignature(a, b SSLSignature) bool {
	if a.Key != b.Key {
		return false
	}
	if a.Confidence != b.Confidence {
		return false
	}
	return true
}

// Helper function to compare two slices of SSLSignature
func equalSSLSignatures(a, b []SSLSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalSSLSignature(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two PageContentSignature structs
func equalPageContentSignature(a, b PageContentSignature) bool {
	if a.Key != b.Key {
		return false
	}
	if !equalStringSlices(a.Signature, b.Signature) {
		return false
	}
	if !equalStringSlices(a.Text, b.Text) {
		return false
	}
	if a.Confidence != b.Confidence {
		return false
	}
	return true
}

// Helper function to compare two slices of PageContentSignature
func equalPageContentSignatures(a, b []PageContentSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalPageContentSignature(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two HTTPHeaderField structs
func equalHTTPHeaderField(a, b HTTPHeaderField) bool {
	if a.Key != b.Key {
		return false
	}
	if !equalStringSlices(a.Value, b.Value) {
		return false
	}
	if a.Confidence != b.Confidence {
		return false
	}
	return true
}

// Helper function to compare two slices of HTTPHeaderField
func equalHTTPHeaderFields(a, b []HTTPHeaderField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalHTTPHeaderField(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two ExternalDetection structs
func equalExternalDetection(a, b ExternalDetection) bool {
	if a.Name != b.Name {
		return false
	}
	if len(a.DetectionParams) != len(b.DetectionParams) {
		return false
	}
	for i := range a.DetectionParams {
		if !equalDetectionParams(a.DetectionParams[i], b.DetectionParams[i]) {
			return false
		}
	}
	return true
}

// Helper function to compare two DetectionParams structs
func equalDetectionParams(a, b DetectionParams) bool {
	if a.ParamName != b.ParamName {
		return false
	}
	if a.ParamValue != b.ParamValue {
		return false
	}
	return true
}

// Helper function to compare two slices of ExternalDetection
func equalExternalDetections(a, b []ExternalDetection) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalExternalDetection(a[i], b[i]) {
			return false
		}
	}
	return true
}
