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
	h := HTTPHeaderField{Value: " application/json "}
	expected := "application/json"
	if got := h.GetValue(); got != expected {
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
