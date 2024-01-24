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

package main

import cfg "github.com/pzaino/thecrowler/pkg/config"

var (
	config cfg.Config // Global variable to store the configuration
)

// SearchResult represents the structure of the search result
// returned by the search engine. It designed to be similar
// to the one returned by Google.
type SearchResult struct {
	Items []struct {
		Title   string `json:"title"`   // Title of the page
		Link    string `json:"link"`    // URL of the page
		Summary string `json:"summary"` // Summary of the page
		Snippet string `json:"snippet"` // Snippet of the page
	} `json:"items"` // List of results
}
