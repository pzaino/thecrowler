package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunAgentsLint(t *testing.T) {
	if err := run([]string{"agents", "lint", filepath.Clean("../../pkg/agent/testdata/legacy.valid.yaml")}); err != nil {
		t.Fatalf("lint command should succeed: %v", err)
	}
}

func TestRunAgentsValidateStrict(t *testing.T) {
	if err := run([]string{"agents", "validate", "--strict", filepath.Clean("../../pkg/agent/testdata/identity.valid.yaml")}); err != nil {
		t.Fatalf("strict validation command should succeed: %v", err)
	}
}

func TestRunAgentsConvert(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "converted.yaml")
	err := run([]string{"agents", "convert", "json2yaml", filepath.Clean("../../pkg/agent/testdata/identity.valid.json"), out})
	if err != nil {
		t.Fatalf("convert command should succeed: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected output file: %v", err)
	}
}
