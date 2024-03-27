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

// Package main (API) implements the API server for the Crowler search engine.
package main

import (
	cfg "github.com/pzaino/thecrowler/pkg/config"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
)

var (
	config cfg.Config // Global variable to store the configuration
)

// ConsoleResponse represents the structure of the response
// returned by the console API (addSource/removeSOurce etc.).
type ConsoleResponse struct {
	Message string `json:"message"`
}

// addSourceRequest represents the structure of the add source request
type addSourceRequest struct {
	URL        string `json:"url"`
	Status     string `json:"status,omitempty"`
	Restricted int    `json:"restricted,omitempty"`
	Disabled   bool   `json:"disabled,omitempty"`
	Flags      int    `json:"flags,omitempty"`
	Config     string `json:"config,omitempty"`
}

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

// APIResponse represents a closer approximation to the Google Search API response structure.
// This should suffice for most of the integration use cases.
type APIResponse struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
	} `json:"queries"`
	Items []struct {
		Kind        string `json:"kind"`           // Type of the search result, e.g., "customsearch#result"
		Title       string `json:"title"`          // Title of the page
		HTMLTitle   string `json:"htmlTitle"`      // Title in HTML format
		Link        string `json:"link"`           // URL of the page
		DisplayLink string `json:"displayLink"`    // Displayed URL of the page
		Snippet     string `json:"snippet"`        // Snippet of the page
		HTMLSnippet string `json:"htmlSnippet"`    // Snippet in HTML format
		Mime        string `json:"mime,omitempty"` // MIME type of the result, if applicable
		Image       struct {
			ImageURL        string `json:"imageURL"`
			ContextLink     string `json:"contextLink"`
			Height          int    `json:"height"`
			Width           int    `json:"width"`
			ByteSize        int    `json:"byteSize"`
			ThumbnailLink   string `json:"thumbnailLink"`
			ThumbnailHeight int    `json:"thumbnailHeight"`
			ThumbnailWidth  int    `json:"thumbnailWidth"`
		} `json:"image,omitempty"` // Image details, if result is an image
	} `json:"items"` // List of results
	SearchInformation struct {
		TotalResults string `json:"totalResults"` // Total number of results for the query
	} `json:"searchInformation"`
}

// QueryRequest contains details about the search query made.
type QueryRequest struct {
	Title          string `json:"title"`          // Title of the search
	TotalResults   int    `json:"totalResults"`   // Total results for the search
	SearchTerms    string `json:"searchTerms"`    // The search terms that were used
	Count          int    `json:"count"`          // Number of results returned in this set
	StartIndex     int    `json:"startIndex"`     // Start index of the results
	InputEncoding  string `json:"inputEncoding"`  // Encoding of the input (e.g., "utf8")
	OutputEncoding string `json:"outputEncoding"` // Encoding of the output (e.g., "utf8")
	Safe           string `json:"safe"`           // Safe search setting
	Cx             string `json:"cx"`             // Custom search engine ID
}

// ScreenshotRequest represents the structure of the screenshot request POST
type ScreenshotRequest struct {
	URL        string `json:"url"`
	Resolution string `json:"resolution,omitempty"`
	FullPage   bool   `json:"fullPage,omitempty"`
	Delay      int    `json:"delay,omitempty"`
	Auth       struct {
		Type string `json:"type,omitempty"`
	} `json:"auth,omitempty"`
}

// ScreenshotResponse represents the structure of the screenshot response
type ScreenshotResponse struct {
	Link          string `json:"screenshot_link"`
	CreatedAt     string `json:"created_at"`
	LastUpdatedAt string `json:"updated_at"`
	Type          string `json:"type"`
	Format        string `json:"format"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	ByteSize      int    `json:"byte_size"`
}

type NetInfoRow struct {
	CreatedAt     string       `json:"created_at"`
	LastUpdatedAt string       `json:"last_updated_at"`
	Details       neti.NetInfo `json:"details"`
}

// NetInfoResponse represents the structure of the network information response
type NetInfoResponse struct {
	Items []NetInfoRow `json:"items"`
}

func (r *NetInfoResponse) isEmpty() bool {
	return len(r.Items) == 0
}

type HTTPInfoRow struct {
	CreatedAt     string            `json:"created_at"`
	LastUpdatedAt string            `json:"last_updated_at"`
	Details       httpi.HTTPDetails `json:"details"`
}

// NetInfoResponse represents the structure of the network information response
type HTTPInfoResponse struct {
	Items []HTTPInfoRow `json:"items"`
}

func (r *HTTPInfoResponse) isEmpty() bool {
	return len(r.Items) == 0
}

// Enum for qType (query type)
const (
	postQuery = 0
	getQuery  = 1
)
