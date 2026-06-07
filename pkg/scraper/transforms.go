package scraper

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// TransformRequest describes a pure post-processing transformation.
type TransformRequest struct {
	Data    []byte
	Details map[string]interface{}
}

// TransformResult contains transformed bytes.
type TransformResult struct{ Data []byte }

// Replace applies the target/replacement transformation.
func Replace(req TransformRequest) (TransformResult, error) {
	target, ok := req.Details["target"].(string)
	if !ok {
		return TransformResult{}, fmt.Errorf("replace target must be a string")
	}
	replacement, ok := req.Details["replacement"].(string)
	if !ok {
		return TransformResult{}, fmt.Errorf("replace replacement must be a string")
	}
	return TransformResult{Data: []byte(strings.ReplaceAll(string(req.Data), target, replacement))}, nil
}

// Remove removes every target occurrence.
func Remove(req TransformRequest) (TransformResult, error) {
	target, ok := req.Details["target"].(string)
	if !ok {
		return TransformResult{}, fmt.Errorf("remove target must be a string")
	}
	return TransformResult{Data: []byte(strings.ReplaceAll(string(req.Data), target, ""))}, nil
}

// Validate checks JSON validity and required keys without mutating the input.
func Validate(req TransformRequest) (TransformResult, error) {
	if !json.Valid(req.Data) {
		return TransformResult{Data: req.Data}, fmt.Errorf("data is not valid JSON")
	}
	rawKeys, ok := req.Details["keys"]
	if !ok {
		return TransformResult{Data: req.Data}, nil
	}
	keys, ok := rawKeys.([]string)
	if !ok {
		return TransformResult{Data: req.Data}, fmt.Errorf("validate keys must be []string")
	}
	for _, key := range keys {
		if !strings.Contains(string(req.Data), key) {
			return TransformResult{Data: req.Data}, fmt.Errorf("key %q is missing from the data", key)
		}
	}
	return TransformResult{Data: req.Data}, nil
}

// Clean applies side-effect-free cleanup options.
func Clean(req TransformRequest) (TransformResult, error) {
	data := string(req.Data)
	for key, raw := range req.Details {
		enabled, ok := raw.(bool)
		if !ok {
			return TransformResult{}, fmt.Errorf("clean option %q must be a bool", key)
		}
		if !enabled {
			continue
		}
		switch key {
		case "remove_html":
			data = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(data, "")
		case "remove_whitespace":
			data = strings.ReplaceAll(data, " ", "")
		case "remove_extra_whitespace":
			data = strings.Join(strings.Fields(data), " ")
		case "remove_newlines":
			data = strings.ReplaceAll(data, "\n", "")
		case "remove_special_chars":
			data = regexp.MustCompile(`[^a-zA-Z0-9\s]`).ReplaceAllString(data, "")
		case "remove_numbers":
			data = regexp.MustCompile(`[0-9]`).ReplaceAllString(data, "")
		case "decode_html_entities":
			data = html.UnescapeString(data)
		}
	}
	return TransformResult{Data: []byte(data)}, nil
}
