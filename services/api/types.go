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
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	crawler "github.com/pzaino/thecrowler/pkg/crawler"
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

// StatusResponse represents the structure of the status response
type StatusResponse struct {
	Message string              `json:"message"`
	Items   []StatusResponseRow `json:"items"`
}

// StatusResponseRow represents the structure of the status response row
type StatusResponseRow struct {
	SourceID      uint64           `json:"source_id"`
	URL           sql.NullString   `json:"url"`
	Status        sql.NullString   `json:"status"`
	Engine        sql.NullString   `json:"engine"`
	CreatedAt     sql.NullString   `json:"created_at"`
	LastUpdatedAt sql.NullString   `json:"last_updated_at"`
	LastCrawledAt sql.NullString   `json:"last_crawled_at"`
	LastError     sql.NullString   `json:"last_error"`
	LastErrorAt   sql.NullString   `json:"last_error_at"`
	Restricted    int              `json:"restricted"`
	Disabled      bool             `json:"disabled"`
	Flags         int              `json:"flags"`
	Config        cfg.SourceConfig `json:"config,omitempty"`
}

// addSourceRequest represents the structure of the add source request
type addSourceRequest struct {
	URL        string           `json:"url"`
	CategoryID uint64           `json:"category_id,omitempty"`
	UsrID      uint64           `json:"usr_id,omitempty"`
	Status     string           `json:"status,omitempty"`
	Restricted int              `json:"restricted,omitempty"`
	Disabled   bool             `json:"disabled,omitempty"`
	Flags      int              `json:"flags,omitempty"`
	Config     cfg.SourceConfig `json:"config,omitempty"`
}

// SearchResult represents the structure of the search result
// returned by the search engine. It designed to be similar
// to the one returned by Google.
type SearchResult struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
	} `json:"queries"`
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
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
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
	Limit         int    `json:"limit"`  // Limit of results
	Offset        int    `json:"offset"` // Offset of results
}

// NetInfoRow represents the structure of the network information response
type NetInfoRow struct {
	CreatedAt     string       `json:"created_at"`
	LastUpdatedAt string       `json:"last_updated_at"`
	Details       neti.NetInfo `json:"details"`
}

// NetInfoResponse represents the structure of the network information response
type NetInfoResponse struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
	} `json:"queries"`
	Items []NetInfoRow `json:"items"`
}

func (r *NetInfoResponse) isEmpty() bool {
	return len(r.Items) == 0
}

// HTTPInfoRow represents the structure of the HTTP information response
type HTTPInfoRow struct {
	CreatedAt     string            `json:"created_at"`
	LastUpdatedAt string            `json:"last_updated_at"`
	Details       httpi.HTTPDetails `json:"details"`
}

// HTTPInfoResponse represents the structure of the network information response
type HTTPInfoResponse struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
	} `json:"queries"`
	Items []HTTPInfoRow `json:"items"`
}

// WebObjectRequest represents the structure of the WebObject request POST
type WebObjectRequest struct {
	URL string `json:"url"`
}

// WebObjectResponse represents the structure of the WebObject response
type WebObjectResponse struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
	} `json:"queries"`
	Items []WebObjectRow `json:"items"`
}

// WebObjectRow represents the structure of the WebObject response
type WebObjectRow struct {
	CreatedAt     string                   `json:"created_at"`
	LastUpdatedAt string                   `json:"last_updated_at"`
	ObjectLink    string                   `json:"link"`
	ObjectType    string                   `json:"type"`
	ObjectHash    string                   `json:"hash"`
	ObjectContent string                   `json:"content"`
	ObjectHTML    string                   `json:"html"`
	Details       crawler.WebObjectDetails `json:"details"`
}

// CorrelatedSitesRequest represents the structure of the Correlated Sites request POST
type CorrelatedSitesRequest struct {
	URL string `json:"url"`
}

// CorrelatedSitesResponse represents the structure of the correlated sites response
type CorrelatedSitesResponse struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
	} `json:"queries"`
	Items []CorrelatedSitesRow `json:"items"`
}

// CorrelatedSitesRow represents the structure of the correlated sites response
type CorrelatedSitesRow struct {
	SourceID uint64           `json:"source_id"`
	URL      string           `json:"url"`
	WHOIS    []neti.WHOISData `json:"whois"`
	SSLInfo  httpi.SSLInfo    `json:"ssl_info"`
}

// ScrapedDataRequest represents the structure of the Correlated Sites request POST
type ScrapedDataRequest struct {
	URL string `json:"url"`
}

// ScrapedDataResponse represents the structure of the correlated sites response
type ScrapedDataResponse struct {
	Kind string `json:"kind"` // Identifier of the API's service
	URL  struct {
		Type     string `json:"type"`     // Type of the request (e.g., "application/json")
		Template string `json:"template"` // URL template for requests
	} `json:"url"`
	Queries struct {
		Request  []QueryRequest `json:"request"`  // The request that was made
		NextPage []QueryRequest `json:"nextPage"` // Information for the next page of results
		Limit    int            `json:"limit"`    // Limit of results
		Offset   int            `json:"offset"`   // Offset of results
	} `json:"queries"`
	Items []ScrapedDataRow `json:"items"`
}

// ScrapedDataRow represents the structure of the correlated sites response
type ScrapedDataRow struct {
	SourceID    uint64                 `json:"source_id"`
	URL         string                 `json:"url"`
	CollectedAt string                 `json:"collected_at"`
	Details     map[string]interface{} `json:"details"`
}

// IsEmpty returns true if the response is empty
func (r *ScrapedDataResponse) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *ScrapedDataResponse) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// SearchResponse is an interface that defines the methods that
// a search response should implement.
type SearchResponse interface {
	IsEmpty() bool
	SetHeaderFields(kind, urlType, urlTemplate string, requests, nextPages []QueryRequest)
	Populate(data []byte) error // Assuming data comes as a byte array of JSON
}

// HealthCheck is a struct that holds the health status of the application.
type HealthCheck struct {
	Status string `json:"status"`
}

// IsEmpty returns true if the response is empty
func (r *HTTPInfoResponse) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *HTTPInfoResponse) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// Populate populates the response with the data
func (r *HTTPInfoResponse) Populate(data []byte) error {
	if err := json.Unmarshal(data, &r.Items); err != nil {
		return err
	}
	return nil
}

// IsEmpty returns true if the response is empty
func (r *NetInfoResponse) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *NetInfoResponse) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// Populate populates the response with the data
func (r *NetInfoResponse) Populate(data []byte) error {
	if err := json.Unmarshal(data, &r.Items); err != nil {
		return err
	}
	return nil
}

// IsEmpty returns true if the response is empty
func (r *APIResponse) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *APIResponse) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// Populate populates the response with the data
func (r *APIResponse) Populate(data []byte) error {
	if err := json.Unmarshal(data, &r.Items); err != nil {
		return err
	}
	return nil
}

// IsEmpty returns true if the response is empty
func (r *WebObjectResponse) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *WebObjectResponse) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// Populate populates the response with the data
func (r *WebObjectResponse) Populate(data []byte) error {
	if err := json.Unmarshal(data, &r.Items); err != nil {
		return err
	}
	return nil
}

// IsEmpty returns true if the response is empty
func (r *CorrelatedSitesResponse) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *CorrelatedSitesResponse) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// Populate populates the response with the data
func (r *CorrelatedSitesResponse) Populate(data []byte) error {
	if err := json.Unmarshal(data, &r.Items); err != nil {
		return err
	}
	return nil
}

// IsEmpty returns true if the response is empty
func (r *SearchResult) IsEmpty() bool {
	return len(r.Items) == 0
}

// SetHeaderFields sets the header fields of the response
func (r *SearchResult) SetHeaderFields(kind, urlType, urlTemplate string, requests []QueryRequest) {
	r.Kind = kind
	r.URL.Type = urlType
	r.URL.Template = urlTemplate
	r.Queries.Request = requests
}

// Populate populates the response with the data
func (r *SearchResult) Populate(data []byte) error {
	if err := json.Unmarshal(data, &r.Items); err != nil {
		return err
	}
	return nil
}

// GetQueryTemplate returns the query template for the given kind, version, and method
func GetQueryTemplate(kind string, version string, method string) string {
	if method == "GET" {
		return fmt.Sprintf("%s http(s)://%s/%s/%s?q={q}", method, config.API.Host+":"+strconv.Itoa(config.API.Port), version, kind)
	}

	// return the template for POST requests
	return fmt.Sprintf("%s http(s)://%s/%s/%s", method, config.API.Host+":"+strconv.Itoa(config.API.Port), version, kind)
}

// Enum for qType (query type)
const (
	postQuery    = 0
	getQuery     = 1
	jsonResponse = "application/json"
)
