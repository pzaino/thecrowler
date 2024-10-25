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

// Package detection implements the detection library for the Crowler.
package detection

import (
	"reflect"
	"testing"

	ruleset "github.com/pzaino/thecrowler/pkg/ruleset"
)

func TestDetectionEntityDetailsIsEmpty(t *testing.T) {
	emptyDetails := detectionEntityDetails{}
	nonEmptyDetails := detectionEntityDetails{
		entityType:      "test",
		matchedPatterns: []string{"pattern1", "pattern2"},
		confidence:      0.5,
		pluginResult:    map[string]interface{}{"key": "value"},
	}

	if !emptyDetails.IsEmpty() {
		t.Errorf("Expected empty details to be empty")
	}

	if nonEmptyDetails.IsEmpty() {
		t.Errorf("Expected non-empty details to not be empty")
	}
}

func TestProcessImpliedTechnologies(t *testing.T) {
	detectedTech := map[string]detectionEntityDetails{
		"tech1": {
			entityType:      "implied",
			confidence:      0.8,
			matchedPatterns: []string{"implied by tech2"},
		},
		"tech2": {
			entityType:      "implied",
			confidence:      0.6,
			matchedPatterns: []string{"implied by tech3"},
		},
	}
	patterns := []ruleset.DetectionRule{
		{
			ObjectName: "tech1",
			Implies:    []string{"tech2"},
		},
		{
			ObjectName: "tech2",
			Implies:    []string{"tech3"},
		},
		{
			ObjectName: "tech3",
			Implies:    []string{"tech2"},
		},
	}
	expectedDetectedTech := map[string]detectionEntityDetails{
		"tech1": {
			entityType:      "implied",
			confidence:      0.8,
			matchedPatterns: []string{"implied by tech2"},
		},
		"tech2": {
			entityType:      "implied",
			confidence:      0.6,
			matchedPatterns: []string{"implied by tech3"},
		},
		"tech3": {
			entityType:      "implied",
			confidence:      0.6,
			matchedPatterns: []string{"implied by tech2"},
		},
	}

	processImpliedTechnologies(&detectedTech, &patterns)

	if !reflect.DeepEqual(detectedTech, expectedDetectedTech) {
		t.Errorf("Unexpected detected technologies. Got %v, want %v", detectedTech, expectedDetectedTech)
	}
}

func TestCalculateConfidence(t *testing.T) {
	x := float32(0.5)
	noise := float32(0.1)
	maybe := float32(0.3)
	detected := float32(0.7)
	expected := float32(70)

	result := calculateConfidence(x, noise, maybe, detected)

	if result != expected {
		t.Errorf("Unexpected confidence value. Got %f, want %f", result, expected)
	}
}

func TestUpdateDetectedTech(t *testing.T) {
	detectedTech := make(map[string]detectionEntityDetails)
	sig := "tech1"
	confidence := float32(0.8)
	matchedSig := "matchedSig1"

	updateDetectedTech(&detectedTech, sig, confidence, matchedSig)

	expectedDetectedTech := map[string]detectionEntityDetails{
		"tech1": {
			entityType:      "",
			confidence:      0.8,
			matchedPatterns: []string{"matchedSig1"},
			pluginResult:    nil,
		},
	}

	if !reflect.DeepEqual(detectedTech, expectedDetectedTech) {
		t.Errorf("Unexpected detected technologies. Got %v, want %v", detectedTech, expectedDetectedTech)
	}
}

func TestUpdateDetectedTechCustom(t *testing.T) {
	detectedTech := make(map[string]detectionEntityDetails)
	sig := "tech1"
	confidence := float32(0.8)
	matchedSig := "matchedSig1"
	custom := `{"key": "value"}`
	updateDetectedTechCustom(&detectedTech, sig, confidence, matchedSig, custom)
	expectedDetectedTech := map[string]detectionEntityDetails{
		"tech1": {
			entityType:      "",
			confidence:      0.8,
			matchedPatterns: []string{"matchedSig1"},
			pluginResult:    map[string]interface{}{"key": "value"},
		},
	}
	if !reflect.DeepEqual(detectedTech, expectedDetectedTech) {
		t.Errorf("Unexpected detected technologies. Got %v, want %v", detectedTech, expectedDetectedTech)
	}
}

func TestUpdateDetectedType(t *testing.T) {
	detectedTech := map[string]detectionEntityDetails{
		"tech1": {
			entityType:      "",
			confidence:      0.8,
			matchedPatterns: []string{"matchedSig1"},
			pluginResult:    nil,
		},
	}
	sig := "tech1"
	detectionType := "html"
	updateDetectedType(&detectedTech, sig, detectionType)
	expectedDetectedTech := map[string]detectionEntityDetails{
		"tech1": {
			entityType:      "html",
			confidence:      0.8,
			matchedPatterns: []string{"matchedSig1"},
			pluginResult:    nil,
		},
	}
	if !reflect.DeepEqual(detectedTech, expectedDetectedTech) {
		t.Errorf("Unexpected detected technologies. Got %v, want %v", detectedTech, expectedDetectedTech)
	}
}
