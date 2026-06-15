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
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
)

var (
	config cfg.Config // Global variable to store the configuration
)

// ConsoleResponse represents the structure of the response
// returned by the console API (addSource/removeSOurce etc.).
type ConsoleResponse struct {
	Message string `json:"message" yaml:"message"`
}

// InformationSeedResponse represents a single information seed response.
type InformationSeedResponse struct {
	Message string             `json:"message"`
	Item    InformationSeedRow `json:"item"`
}

// InformationSeedListResponse represents the list response for information seeds.
type InformationSeedListResponse struct {
	Message string               `json:"message"`
	Items   []InformationSeedRow `json:"items"`
	Limit   int                  `json:"limit"`
	Offset  int                  `json:"offset"`
}

// InformationSeedLinkedSourceListResponse represents linked sources for one seed.
type InformationSeedLinkedSourceListResponse struct {
	Message           string                           `json:"message"`
	InformationSeedID uint64                           `json:"information_seed_id"`
	Items             []InformationSeedLinkedSourceRow `json:"items"`
	Limit             int                              `json:"limit"`
	Offset            int                              `json:"offset"`
}

// InformationSeedLinkedSourceRow represents one Source linked to an information seed.
type InformationSeedLinkedSourceRow struct {
	SourceID                   uint64                        `json:"source_id"`
	Priority                   string                        `json:"priority"`
	CategoryID                 uint64                        `json:"category_id"`
	Name                       string                        `json:"name"`
	UsrID                      uint64                        `json:"usr_id"`
	URL                        string                        `json:"url"`
	Restricted                 uint                          `json:"restricted"`
	Flags                      uint                          `json:"flags"`
	Config                     *json.RawMessage              `json:"config,omitempty"`
	Disabled                   bool                          `json:"disabled"`
	SourceInformationSeedIndex SourceInformationSeedIndexRow `json:"source_information_seed_index"`
}

// SourceInformationSeedIndexRow exposes source/seed discovery provenance.
type SourceInformationSeedIndexRow struct {
	ID                uint64           `json:"source_information_seed_id"`
	SourceID          uint64           `json:"source_id"`
	InformationSeedID uint64           `json:"information_seed_id"`
	DiscoveryProvider string           `json:"discovery_provider,omitempty"`
	DiscoveryQuery    string           `json:"discovery_query,omitempty"`
	DiscoveryRank     *int             `json:"discovery_rank,omitempty"`
	CandidateScore    *float64         `json:"candidate_score,omitempty"`
	CandidateReason   string           `json:"candidate_reason,omitempty"`
	DiscoveryMetadata *json.RawMessage `json:"discovery_metadata,omitempty"`
	CreatedAt         string           `json:"created_at,omitempty"`
	LastUpdatedAt     string           `json:"last_updated_at,omitempty"`
}

// InformationSeedEventListResponse represents discovery events for one seed.
type InformationSeedEventListResponse struct {
	Message           string                    `json:"message"`
	InformationSeedID uint64                    `json:"information_seed_id"`
	Items             []InformationSeedEventRow `json:"items"`
	Limit             int                       `json:"limit"`
	Offset            int                       `json:"offset"`
}

// InformationSeedEventRow exposes one persisted information-seed discovery event.
type InformationSeedEventRow struct {
	ID        string                 `json:"event_sha256"`
	SourceID  uint64                 `json:"source_id,omitempty"`
	Type      string                 `json:"event_type"`
	Severity  string                 `json:"event_severity"`
	Timestamp string                 `json:"event_timestamp"`
	Details   map[string]interface{} `json:"details"`
}

// InformationSeedDiagnosticsResponse exposes the latest redacted run diagnostics for one seed.
type InformationSeedDiagnosticsResponse struct {
	Message              string                    `json:"message"`
	InformationSeedID    uint64                    `json:"information_seed_id"`
	RunID                string                    `json:"run_id,omitempty"`
	RunAttempt           int                       `json:"run_attempt"`
	ProviderRequests     map[string]int            `json:"provider_requests"`
	RejectionStages      map[string]map[string]int `json:"rejection_stages"`
	ProviderFailures     []map[string]interface{}  `json:"provider_failures"`
	PluginFailures       []map[string]interface{}  `json:"plugin_failures"`
	ErrorSummaries       []string                  `json:"error_summaries"`
	LatestEventTimestamp string                    `json:"latest_event_timestamp,omitempty"`
}

// InformationSeedCandidateListResponse represents candidate decision evidence for one seed.
type InformationSeedCandidateListResponse struct {
	Message           string                        `json:"message"`
	InformationSeedID uint64                        `json:"information_seed_id"`
	Items             []InformationSeedCandidateRow `json:"items"`
	Limit             int                           `json:"limit"`
	Offset            int                           `json:"offset"`
}

// InformationSeedCandidateRow exposes persisted candidate decision evidence.
type InformationSeedCandidateRow struct {
	ID                uint64           `json:"information_seed_candidate_id"`
	InformationSeedID uint64           `json:"information_seed_id"`
	NormalizedURL     string           `json:"normalized_url"`
	Host              string           `json:"host,omitempty"`
	Provider          string           `json:"provider,omitempty"`
	Query             string           `json:"query,omitempty"`
	Rank              int              `json:"rank"`
	Score             float64          `json:"score"`
	DecisionStatus    string           `json:"decision_status"`
	RejectionReason   string           `json:"rejection_reason,omitempty"`
	Metadata          *json.RawMessage `json:"metadata,omitempty"`
	RunAttempt        int              `json:"run_attempt"`
	CreatedAt         string           `json:"created_at,omitempty"`
	LastUpdatedAt     string           `json:"last_updated_at,omitempty"`
}

// InformationSeedRow represents one information seed and aggregate stats
// exposed by the console API.
type InformationSeedRow struct {
	ID                    uint64           `json:"information_seed_id"`
	CreatedAt             string           `json:"created_at,omitempty"`
	LastUpdatedAt         string           `json:"last_updated_at,omitempty"`
	CategoryID            uint64           `json:"category_id"`
	UsrID                 uint64           `json:"usr_id"`
	InformationSeed       string           `json:"information_seed"`
	Status                string           `json:"status"`
	Priority              string           `json:"priority"`
	Engine                string           `json:"engine"`
	LastProcessedAt       string           `json:"last_processed_at,omitempty"`
	HasError              bool             `json:"has_error"`
	LastError             string           `json:"last_error,omitempty"`
	LastErrorAt           string           `json:"last_error_at,omitempty"`
	Disabled              bool             `json:"disabled"`
	Attempts              int              `json:"attempts"`
	Config                *json.RawMessage `json:"config,omitempty"`
	DiscoveredSourceCount uint64           `json:"discovered_source_count"`
}

// informationSeedAddRequest represents a request to create an information seed.
type informationSeedAddRequest struct {
	CategoryID      uint64           `json:"category_id,omitempty"`
	UsrID           uint64           `json:"usr_id,omitempty"`
	UserID          uint64           `json:"user_id,omitempty"`
	InformationSeed string           `json:"information_seed"`
	Status          string           `json:"status,omitempty"`
	Priority        string           `json:"priority,omitempty"`
	Engine          string           `json:"engine,omitempty"`
	Disabled        bool             `json:"disabled,omitempty"`
	Config          *json.RawMessage `json:"config,omitempty"`
}

// informationSeedIDRequest represents a request targeting one information seed.
type informationSeedIDRequest struct {
	InformationSeedID uint64 `json:"information_seed_id"`
}

type informationSeedIDGetRequest struct {
	InformationSeedID uint64 `json:"id"`
}

// informationSeedEnableRequest represents an enable request that can also queue the seed.
type informationSeedEnableRequest struct {
	InformationSeedID uint64 `json:"information_seed_id,omitempty"`
	Pending           bool   `json:"pending,omitempty"`
	QueuePending      bool   `json:"queue_pending,omitempty"`
}

// StatusResponse represents the structure of the status response
type StatusResponse struct {
	Message string              `json:"message"`
	Items   []StatusResponseRow `json:"items"`
}

// StatusResponseRow represents the structure of the status response row
type StatusResponseRow struct {
	SourceID      uint64               `json:"source_id"`
	URL           sql.NullString       `json:"url"`
	Status        sql.NullString       `json:"status"`
	Priority      sql.NullString       `json:"priority"`
	Engine        sql.NullString       `json:"engine"`
	CreatedAt     sql.NullString       `json:"created_at"`
	LastUpdatedAt sql.NullString       `json:"last_updated_at"`
	LastCrawledAt sql.NullString       `json:"last_crawled_at"`
	LastError     sql.NullString       `json:"last_error"`
	LastErrorAt   sql.NullString       `json:"last_error_at"`
	Restricted    int                  `json:"restricted"`
	Disabled      bool                 `json:"disabled"`
	Flags         int                  `json:"flags"`
	Config        SourceConfigResponse `json:"config,omitempty"`
	EmailStatus   *SourceEmailStatus   `json:"email_status,omitempty"`
}

// SourceEmailStatus is a privacy-safe operational summary for an email source.
// It excludes mailbox identifiers, cursor values, raw errors, content, and secrets.
type SourceEmailStatus struct {
	ListenerStatus     string                   `json:"listener_status"`
	LastSynchronizedAt string                   `json:"last_synchronized_at,omitempty"`
	CursorSummary      SourceEmailCursorSummary `json:"cursor_summary"`
	ProcessedCount     uint64                   `json:"processed_count"`
	FailedCount        uint64                   `json:"failed_count"`
	LastErrorCategory  string                   `json:"last_error_category,omitempty"`
}

// SourceEmailCursorSummary reveals only checkpoint coverage and cursor families,
// never provider cursor values or mailbox identities.
type SourceEmailCursorSummary struct {
	MailboxCount          uint64 `json:"mailbox_count"`
	CheckpointedMailboxes uint64 `json:"checkpointed_mailboxes"`
	HasTokenCursor        bool   `json:"has_token_cursor"`
	HasHistoryCursor      bool   `json:"has_history_cursor"`
	HasUIDCursor          bool   `json:"has_uid_cursor"`
}

// SourceConfigRequest is the source configuration accepted by source-management
// endpoints. The alias preserves cfg.SourceConfig's exact wire format and its
// historical email configuration envelope support.
type SourceConfigRequest = cfg.SourceConfig

// SourceConfigResponse is the source configuration returned by
// source-management endpoints. Its JSON shape matches cfg.SourceConfig, while
// MarshalJSON redacts secret values and secret references contained in email configuration.
type SourceConfigResponse cfg.SourceConfig

// MarshalJSON returns the existing source configuration shape with email
// secrets replaced by a stable redaction marker.
func (response SourceConfigResponse) MarshalJSON() ([]byte, error) {
	return marshalRedactedSourceConfig(cfg.SourceConfig(response))
}

// updateSourceRequest represents the structure of an update source request.
// Config is a pointer so an omitted configuration continues to preserve the
// stored value, while an explicitly supplied email configuration can be saved.
type updateSourceRequest struct {
	SourceID   int64                `json:"source_id,omitempty"`
	URL        string               `json:"url,omitempty"`
	Status     string               `json:"status,omitempty"`
	Restricted int                  `json:"restricted,omitempty"`
	Disabled   bool                 `json:"disabled,omitempty"`
	Flags      int                  `json:"flags,omitempty"`
	Config     *SourceConfigRequest `json:"config,omitempty"`
	Details    json.RawMessage      `json:"details,omitempty"`
}

// addSourceRequest represents the structure of the add source request
type addSourceRequest struct {
	URL        string              `json:"url"`
	CategoryID uint64              `json:"category_id,omitempty"`
	UsrID      uint64              `json:"usr_id,omitempty"`
	Status     string              `json:"status,omitempty"`
	Restricted int                 `json:"restricted,omitempty"`
	Disabled   bool                `json:"disabled,omitempty"`
	Flags      int                 `json:"flags,omitempty"`
	Config     SourceConfigRequest `json:"config,omitempty"`
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
		SourceUID string `json:"source_uid"`
		Title     string `json:"title"`   // Title of the page
		Link      string `json:"link"`    // URL of the page
		Summary   string `json:"summary"` // Summary of the page
		DocType   string `json:"type"`    // Type of the document (e.g., "text/html")
		Lang      string `json:"lang"`    // Language of the document (e.g., "en")
		Snippet   string `json:"snippet"` // Snippet of the page
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
	SourceUID     string `json:"source_uid"`
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
	SourceUID     string       `json:"source_uid"`
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
	SourceUID     string            `json:"source_uid"`
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
	URL    string `json:"url"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
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
	SourceUID     string          `json:"source_uid"`
	CreatedAt     string          `json:"created_at"`
	LastUpdatedAt string          `json:"last_updated_at"`
	ObjectLink    string          `json:"link"`
	ObjectType    string          `json:"type"`
	ObjectHash    string          `json:"hash"`
	ObjectContent string          `json:"content"`
	ObjectHTML    string          `json:"html"`
	Details       json.RawMessage `json:"details"`
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
	SourceID  uint64           `json:"source_id"`
	SourceUID string           `json:"source_uid"`
	URL       string           `json:"url"`
	WHOIS     []neti.WHOISData `json:"whois"`
	SSLInfo   httpi.SSLInfo    `json:"ssl_info"`
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
	SourceUID   string                 `json:"source_uid"`
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

// ReadyCheck is a struct that holds the readiness status of the application.
type ReadyCheck struct {
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

// TimeSeriesPaginationResponse describes deterministic offset pagination.
type TimeSeriesPaginationResponse struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	Count   int  `json:"count"`
	HasMore bool `json:"has_more"`
}

// TimeSeriesMetricDefinition is the public, selector-free metric definition.
type TimeSeriesMetricDefinition struct {
	ID               uint64                       `json:"id"`
	Key              string                       `json:"key"`
	DisplayName      string                       `json:"display_name"`
	Description      string                       `json:"description,omitempty"`
	SourceKind       cfg.TimeSeriesSourceKind     `json:"source_kind"`
	ValueType        cfg.TimeSeriesValueType      `json:"value_type"`
	DefaultAggregate cfg.TimeSeriesAggregate      `json:"default_aggregate"`
	Bucket           cfg.TimeSeriesBucketInterval `json:"bucket"`
	TimeBasis        cfg.TimeSeriesTimeBasis      `json:"time_basis"`
	Unit             string                       `json:"unit,omitempty"`
	DimensionKeys    []string                     `json:"dimension_keys"`
	Enabled          bool                         `json:"enabled"`
}

// TimeSeriesMetricListResponse is the stable metric-list envelope.
type TimeSeriesMetricListResponse struct {
	Items      []TimeSeriesMetricDefinition `json:"items"`
	Pagination TimeSeriesPaginationResponse `json:"pagination"`
}

// TimeSeriesScopeResponse identifies a public aggregate or observation scope.
type TimeSeriesScopeResponse struct {
	InformationSeedID          *uint64 `json:"information_seed_id,omitempty"`
	InformationSeedCandidateID *uint64 `json:"information_seed_candidate_id,omitempty"`
	SourceID                   *uint64 `json:"source_id,omitempty"`
	SourceInformationSeedID    *uint64 `json:"source_information_seed_id,omitempty"`
	IndexID                    *uint64 `json:"index_id,omitempty"`
	EntityID                   *uint64 `json:"entity_id,omitempty"`
	SubjectType                string  `json:"subject_type,omitempty"`
	SubjectID                  *uint64 `json:"subject_id,omitempty"`
	ObjectType                 string  `json:"object_type,omitempty"`
	ObjectID                   *uint64 `json:"object_id,omitempty"`
	CorrelationRuleID          *uint64 `json:"correlation_rule_id,omitempty"`
	CorrelationObjectType1     string  `json:"correlation_object_type_1,omitempty"`
	CorrelationObjectID1       *uint64 `json:"correlation_object_id_1,omitempty"`
	CorrelationObjectType2     string  `json:"correlation_object_type_2,omitempty"`
	CorrelationObjectID2       *uint64 `json:"correlation_object_id_2,omitempty"`
}

// TimeSeriesAggregateValues retains all materialized aggregate values.
type TimeSeriesAggregateValues struct {
	Count           int64       `json:"count"`
	OccurrenceTotal float64     `json:"occurrence_total"`
	DistinctCount   int64       `json:"distinct_count"`
	NumericCount    int64       `json:"numeric_count"`
	Sum             *float64    `json:"sum,omitempty"`
	Minimum         *float64    `json:"min,omitempty"`
	Maximum         *float64    `json:"max,omitempty"`
	Average         *float64    `json:"average,omitempty"`
	P50             *float64    `json:"p50,omitempty"`
	P75             *float64    `json:"p75,omitempty"`
	P90             *float64    `json:"p90,omitempty"`
	P95             *float64    `json:"p95,omitempty"`
	P99             *float64    `json:"p99,omitempty"`
	First           interface{} `json:"first,omitempty"`
	Last            interface{} `json:"last,omitempty"`
	ChangeCount     int64       `json:"change_count"`
}

// TimeSeriesAggregateBucket is one aggregate-first chart point.
type TimeSeriesAggregateBucket struct {
	AggregateHash   string                    `json:"aggregate_hash"`
	MetricID        uint64                    `json:"metric_id"`
	MetricKey       string                    `json:"metric_key"`
	BucketStart     string                    `json:"bucket_start"`
	BucketEnd       string                    `json:"bucket_end"`
	Aggregate       cfg.TimeSeriesAggregate   `json:"aggregate"`
	Value           interface{}               `json:"value"`
	Values          TimeSeriesAggregateValues `json:"values"`
	SampleCount     int64                     `json:"sample_count"`
	OccurrenceTotal float64                   `json:"occurrence_total"`
	Dimensions      map[string]interface{}    `json:"dimensions"`
	Scope           TimeSeriesScopeResponse   `json:"scope"`
}

// TimeSeriesAggregateResponse is the stable aggregate query envelope.
type TimeSeriesAggregateResponse struct {
	Items      []TimeSeriesAggregateBucket  `json:"items"`
	Pagination TimeSeriesPaginationResponse `json:"pagination"`
}

// TimeSeriesObservationResponse is a privacy-filtered raw observation.
type TimeSeriesObservationResponse struct {
	ID          uint64                  `json:"id"`
	MetricID    uint64                  `json:"metric_id"`
	MetricKey   string                  `json:"metric_key"`
	ObservedAt  string                  `json:"observed_at"`
	EffectiveAt *string                 `json:"effective_at,omitempty"`
	CollectedAt string                  `json:"collected_at"`
	BucketStart string                  `json:"bucket_start"`
	BucketEnd   string                  `json:"bucket_end"`
	Value       interface{}             `json:"value,omitempty"`
	IsChanged   bool                    `json:"is_changed"`
	ChangeType  string                  `json:"change_type,omitempty"`
	Dimensions  map[string]interface{}  `json:"dimensions"`
	Scope       TimeSeriesScopeResponse `json:"scope"`
}

// TimeSeriesObservationListResponse is the bounded raw-observation envelope.
type TimeSeriesObservationListResponse struct {
	Items      []TimeSeriesObservationResponse `json:"items"`
	Pagination TimeSeriesPaginationResponse    `json:"pagination"`
}

// TimeSeriesDrilldownResponse links an aggregate scope to matching observations.
type TimeSeriesDrilldownResponse struct {
	AggregateHash string                          `json:"aggregate_hash,omitempty"`
	Aggregate     *TimeSeriesAggregateBucket      `json:"aggregate,omitempty"`
	Observations  []TimeSeriesObservationResponse `json:"observations"`
	Pagination    TimeSeriesPaginationResponse    `json:"pagination"`
}

// TimeSeriesDimensionGroup is one bounded comparison group.
type TimeSeriesDimensionGroup struct {
	DimensionValue interface{}                 `json:"dimension_value"`
	Buckets        []TimeSeriesAggregateBucket `json:"buckets"`
}

// TimeSeriesDimensionComparisonResponse groups aggregate buckets by one dimension.
type TimeSeriesDimensionComparisonResponse struct {
	DimensionKey string                     `json:"dimension_key"`
	Groups       []TimeSeriesDimensionGroup `json:"groups"`
	Cardinality  int                        `json:"cardinality"`
	Limit        int                        `json:"limit"`
}

// TimeSeriesQuery documents composable time-series query parameters for OpenAPI.
type TimeSeriesQuery struct {
	MetricID                   uint64 `json:"metric_id,omitempty"`
	MetricKey                  string `json:"metric_key,omitempty"`
	InformationSeedID          uint64 `json:"information_seed_id,omitempty"`
	InformationSeedCandidateID uint64 `json:"information_seed_candidate_id,omitempty"`
	SourceID                   uint64 `json:"source_id,omitempty"`
	SourceInformationSeedID    uint64 `json:"source_information_seed_id,omitempty"`
	IndexID                    uint64 `json:"index_id,omitempty"`
	EntityID                   uint64 `json:"entity_id,omitempty"`
	SubjectType                string `json:"subject_type,omitempty"`
	SubjectID                  uint64 `json:"subject_id,omitempty"`
	Subject                    string `json:"subject,omitempty"`
	ObjectType                 string `json:"object_type,omitempty"`
	ObjectID                   uint64 `json:"object_id,omitempty"`
	CorrelationRuleID          uint64 `json:"correlation_rule_id,omitempty"`
	CorrelationObjectType1     string `json:"correlation_object_type_1,omitempty"`
	CorrelationObjectID1       uint64 `json:"correlation_object_id_1,omitempty"`
	CorrelationObjectType2     string `json:"correlation_object_type_2,omitempty"`
	CorrelationObjectID2       uint64 `json:"correlation_object_id_2,omitempty"`
	Dimensions                 string `json:"dimensions,omitempty"`
	DimensionKey               string `json:"dimension_key,omitempty"`
	AggregateHash              string `json:"aggregate_hash,omitempty"`
	Bucket                     string `json:"bucket,omitempty"`
	From                       string `json:"from,omitempty"`
	To                         string `json:"to,omitempty"`
	TimeBasis                  string `json:"time_basis,omitempty"`
	Aggregate                  string `json:"aggregate,omitempty"`
	Order                      string `json:"order,omitempty"`
	Limit                      int    `json:"limit,omitempty"`
	Offset                     int    `json:"offset,omitempty"`
}

// TimeSeriesErrorResponse documents the API's standard JSON error shape.
type TimeSeriesErrorResponse struct {
	ErrorCode int    `json:"error_code"`
	Error     string `json:"error"`
	Message   string `json:"message"`
}
