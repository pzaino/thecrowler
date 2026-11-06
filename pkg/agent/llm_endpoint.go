// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// LLM provider endpoints are operator configuration, never model output.
// This validator is intentionally narrower than the general-purpose
// cmn.IsURLValid helper: inference endpoints are always HTTP(S), must carry a
// host, and must not smuggle credentials in the URL. Unlike IsURLValid it
// accepts single-label hosts (localhost, ollama, crowler-ollama) so local
// container deployments work without a DNS suffix.

// ValidateLLMEndpoint checks one operator-configured inference endpoint.
// It returns an error that never echoes the raw URL, because URLs may carry
// credentials or private hostnames.
func ValidateLLMEndpoint(raw string) error {
	_, err := parseLLMEndpoint(raw)
	return err
}

// parseLLMEndpoint parses and validates one inference endpoint.
func parseLLMEndpoint(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return nil, fmt.Errorf("endpoint contains whitespace")
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return nil, fmt.Errorf("endpoint contains control characters")
		}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("endpoint is not a valid URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("endpoint scheme must be http or https")
	}
	if parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("endpoint must carry a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("endpoint must not embed credentials")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("endpoint must not carry a fragment")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return nil, fmt.Errorf("endpoint has an empty port")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, fmt.Errorf("endpoint port must be in 1..65535")
		}
	}
	return parsed, nil
}
