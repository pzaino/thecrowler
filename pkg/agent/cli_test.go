package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLintAgentFile(t *testing.T) {
	if err := LintAgentFile(filepath.Clean("testdata/legacy.valid.yaml")); err != nil {
		t.Fatalf("expected lint to pass legacy fixture: %v", err)
	}
}

func TestValidateAgentFileStrict(t *testing.T) {
	if err := ValidateAgentFile(filepath.Clean("testdata/identity.valid.yaml"), true, nil); err != nil {
		t.Fatalf("expected strict validate to pass identity fixture: %v", err)
	}
}

func TestValidateAgentFileStrictFailure(t *testing.T) {
	err := ValidateAgentFile(filepath.Clean("testdata/strict.invalid.decision.json"), true, nil)
	if err == nil {
		t.Fatal("expected strict validate to fail for invalid decision fixture")
	}
}

func TestConvertRoundTripParity(t *testing.T) {
	jsonInput, err := os.ReadFile(filepath.Clean("testdata/identity.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	yamlBytes, err := ConvertJSONToYAML(jsonInput)
	if err != nil {
		t.Fatalf("json2yaml conversion failed: %v", err)
	}
	jsonBytes, err := ConvertYAMLToJSON(yamlBytes)
	if err != nil {
		t.Fatalf("yaml2json conversion failed: %v", err)
	}

	var original any
	if err := json.Unmarshal(jsonInput, &original); err != nil {
		t.Fatal(err)
	}
	var roundTrip any
	if err := json.Unmarshal(jsonBytes, &roundTrip); err != nil {
		t.Fatal(err)
	}
	origNorm, _ := json.Marshal(original)
	roundNorm, _ := json.Marshal(roundTrip)
	if !bytes.Equal(origNorm, roundNorm) {
		t.Fatalf("expected round-trip semantics to match\noriginal: %s\nroundtrip: %s", string(origNorm), string(roundNorm))
	}
}

func TestPublishedYAMLTemplatesValidate(t *testing.T) {
	templates := []string{
		"../../agents/templates/legacy-job-only.yaml",
		"../../agents/templates/identity-enabled-agent.yaml",
		"../../agents/templates/multi-agent-delegation.yaml",
	}
	for _, p := range templates {
		p := filepath.Clean(p)
		t.Run(filepath.Base(p), func(t *testing.T) {
			if err := ValidateAgentFile(p, true, nil); err != nil {
				t.Fatalf("template should validate in strict mode: %v", err)
			}
		})
	}
}
