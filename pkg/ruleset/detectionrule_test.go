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
