package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateAgentFile validates a YAML/JSON agent file in the selected mode.
func ValidateAgentFile(path string, strict bool, registry *AgentRegistry) error {
	data, fileType, err := readAgentFile(path)
	if err != nil {
		return err
	}
	mode := ValidationModeLenient
	if strict {
		mode = ValidationModeStrict
	}
	return ValidateAgentConfig(data, fileType, mode, registry)
}

// LintAgentFile runs compatibility-friendly validation checks.
func LintAgentFile(path string) error {
	return ValidateAgentFile(path, false, nil)
}

// ConvertJSONToYAML converts an agent document from JSON to YAML.
func ConvertJSONToYAML(data []byte) ([]byte, error) {
	doc, _, err := decodeToJSONEquivalent(data, "json")
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize yaml: %w", err)
	}
	return out, nil
}

// ConvertYAMLToJSON converts an agent document from YAML to pretty JSON.
func ConvertYAMLToJSON(data []byte) ([]byte, error) {
	doc, _, err := decodeToJSONEquivalent(data, "yaml")
	if err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize json: %w", err)
	}
	return append(out, '\n'), nil
}

// ConvertAgentFile converts a source file to the requested target format and writes to outputPath.
func ConvertAgentFile(inputPath, outputPath, mode string) error {
	data, _, err := readAgentFile(inputPath)
	if err != nil {
		return err
	}
	var out []byte
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "json2yaml":
		out, err = ConvertJSONToYAML(data)
	case "yaml2json":
		out, err = ConvertYAMLToJSON(data)
	default:
		return fmt.Errorf("unsupported conversion mode: %s", mode)
	}
	if err != nil {
		return err
	}
	if outputPath == "" {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(outputPath, out, 0o600)
}

func readAgentFile(path string) ([]byte, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("file path is required")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, "", err
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "json", "yaml", "yml":
		return data, ext, nil
	default:
		return nil, "", fmt.Errorf("unsupported file extension: %s", ext)
	}
}
