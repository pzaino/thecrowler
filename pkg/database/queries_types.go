package database

import "encoding/json"

// SourceFilter is a struct to filter sources based on URL and/or SourceID.
type SourceFilter struct {
	URL      string `json:"url,omitempty" yaml:"url,omitempty"`             // Optional, used if no SourceID is provided
	SourceID int64  `json:"source_id,omitempty" yaml:"source_id,omitempty"` // Optional, used if no URL is provided
}

// UpdateSourceRequest represents the structure of the update source request
type UpdateSourceRequest struct {
	SourceID   int64           `json:"source_id,omitempty"`  // Optional, used if no URL is provided
	URL        string          `json:"url,omitempty"`        // The URL of the source
	Status     string          `json:"status,omitempty"`     // The status of the source (e.g., 'completed', 'pending')
	Restricted int             `json:"restricted,omitempty"` // Restriction level (0-4)
	Disabled   bool            `json:"disabled,omitempty"`   // Whether the source is disabled
	Flags      int             `json:"flags,omitempty"`      // Bitwise flags for the source
	Config     json.RawMessage `json:"config,omitempty"`     // JSON configuration for the source
	Details    json.RawMessage `json:"details,omitempty"`    // JSON details about the source's internal state
}

// OwnerRequest represents the structure of the owner request
type OwnerRequest struct {
	OwnerID       int64           `json:"owner_id"`        // The ID of the owner
	CreatedAt     string          `json:"created_at"`      // The creation date of the owner
	LastUpdatedAt string          `json:"last_updated_at"` // The last update date of the owner
	UserID        int64           `json:"user_id"`         // The ID of the user
	DetailsHash   string          `json:"details_hash"`    // The SHA256 hash of the details
	Details       json.RawMessage `json:"details"`         // The details of the owner
}

// CategoryRequest represents the structure of the category request
type CategoryRequest struct {
	CategoryID    int64  `json:"category_id"`     // The ID of the category
	CreatedAt     string `json:"created_at"`      // The creation date of the category
	LastUpdatedAt string `json:"last_updated_at"` // The last update date of the category
	Name          string `json:"name"`            // The name of the category
	Description   string `json:"description"`     // The description of the category
	ParentID      int64  `json:"parent_id"`       // The ID of the parent category
}
