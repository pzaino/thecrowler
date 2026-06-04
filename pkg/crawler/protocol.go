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

package crawler

import "strings"

// SourceProtocol describes the internal source protocol family used by crawler dispatch.
type SourceProtocol string

const (
	// SourceProtocolWeb identifies sources that should use the web crawler.
	SourceProtocolWeb SourceProtocol = "web"
	// SourceProtocolNetwork identifies sources that should use network information collection only.
	SourceProtocolNetwork SourceProtocol = "network"
)

var allowedProtocols = strings.Split("http://,https://,ftp://,ftps://", ",")

func classifySourceProtocol(rawURL string) SourceProtocol {
	rawURL = strings.TrimSpace(rawURL)
	for _, proto := range allowedProtocols {
		if strings.HasPrefix(rawURL, proto) {
			return SourceProtocolWeb
		}
	}
	return SourceProtocolNetwork
}
