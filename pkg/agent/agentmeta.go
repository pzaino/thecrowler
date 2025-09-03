// Package agent provides the agent functionality for the CROWler.
package agent

// This file defines the manifest structure for user-defined agents in the CROWler.

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest represents a YAML or JSON declaration of a user-defined agent.
type Manifest struct {
	Name         string            `yaml:"name" json:"name"`
	Version      string            `yaml:"version" json:"version"`
	Description  string            `yaml:"description" json:"description"`
	Author       string            `yaml:"author" json:"author"`
	Tags         []string          `yaml:"tags" json:"tags"`
	Capabilities []string          `yaml:"capabilities" json:"capabilities"`
	Trigger      TriggerDefinition `yaml:"trigger" json:"trigger"`
	Inputs       []IOField         `yaml:"inputs" json:"inputs"`
	Outputs      []IOField         `yaml:"outputs" json:"outputs"`
	Dependencies []string          `yaml:"dependencies" json:"dependencies"`
	Sandbox      *SandboxConfig    `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
}

// TriggerDefinition defines the trigger for the agent.
type TriggerDefinition struct {
	Type string `yaml:"type" json:"type"` // event, api, cron
	Name string `yaml:"name" json:"name"`
}

// IOField represents an input/output field in the agent's manifest.
type IOField struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

// SandboxConfig defines the sandboxing options for the agent.
type SandboxConfig struct {
	AllowedNetwork bool   `yaml:"allowed_network" json:"allowed_network"`
	MaxDuration    string `yaml:"max_duration" json:"max_duration"`
}

// LoadManifest loads a manifest file from disk (YAML only for now)
func LoadManifest(path string) (*Manifest, error) {
	f, err := os.Open(path) //nolint:gosec // this path is controlled by the system owner
	if err != nil {
		return nil, err
	}
	defer f.Close() // nolint:errcheck // ignore close error

	var manifest Manifest
	yamlDecoder := yaml.NewDecoder(f)
	if err := yamlDecoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest file %s: %w", path, err)
	}
	return &manifest, nil
}

// LoadAllManifests walks a directory and loads all manifest.yaml or .yml files
func LoadAllManifests(dir string) ([]*Manifest, error) {
	var manifests []*Manifest
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".yaml" || ext == ".yml" {
			m, err := LoadManifest(path)
			if err != nil {
				return fmt.Errorf("failed loading manifest %s: %w", path, err)
			}
			manifests = append(manifests, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifests, nil
}
