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
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	errFailedToInitializeDBHandler = "Failed to initialize database handler"
	errFailedToConnectToDB         = "Error connecting to the database"
	errFailedToStartTransaction    = "Failed to start transaction"
	errFailedToCommitTransaction   = "Failed to commit transaction"

	infoAllSourcesStatus = "All Sources status"
	//infoSourceStatus     = "Source status"
	infoSourceRemoved = "Source and related data removed successfully"
)

func performAddSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var sqlQuery string
	var sqlParams addSourceRequest
	if qType == getQuery {
		sqlParams.URL = cmn.NormalizeURL(query)
		//sqlQuery = "INSERT INTO Sources (url, last_crawled_at, status) VALUES ($1, NULL, 'pending')"
		sqlQuery = "INSERT INTO Sources (url, last_crawled_at, category_id, usr_id, status, restricted, disabled, flags, config) VALUES ($1, NULL, 0, 0, 'pending', 2, false, 0, '{}')"
	} else {
		// extract the parameters from the query
		extractAddSourceParams(query, &sqlParams)
		// Normalize the URL
		sqlParams.URL = cmn.NormalizeURL(sqlParams.URL)
		// Prepare the SQL query
		sqlQuery = "INSERT INTO Sources (url, last_crawled_at, status, restricted, disabled, flags, config, category_id, usr_id) VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8) RETURNING source_id;"
	}

	if sqlParams.URL == "" {
		return ConsoleResponse{Message: "Invalid URL"}, fmt.Errorf("invalid URL")
	}

	// Perform the addSource operation
	results, err := addSource(sqlQuery, sqlParams, db)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "adding the source: %v", err)
		return results, err
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, results.Message)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Website inserted with: %s", query)
	return results, nil
}

func extractAddSourceParams(query string, params *addSourceRequest) {
	params.Restricted = -1

	// Unmarshal query into params
	err := json.Unmarshal([]byte(query), &params)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling the query: %v", err)
	}

	// Check for missing parameters
	if params.Status == "" {
		params.Status = "pending"
	}
	if params.Restricted < 0 || params.Restricted > 4 {
		params.Restricted = 0
	}

	if !params.Config.IsEmpty() {
		// Validate and potentially reformat the existing Config JSON
		// First, marshal the params.Config struct to JSON
		configJSON, err := json.Marshal(params.Config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "marshalling the Config field: %v", err)
		}

		// Unmarshal the JSON into a map to check for invalid JSON
		var jsonRaw map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &jsonRaw); err != nil {
			// Handle invalid JSON
			cmn.DebugMsg(cmn.DbgLvlError, "Config field contains invalid JSON: %v", err)
		}

		// Re-marshal to ensure the JSON is in a standardized format (optional)
		configJSONChecked, err := json.Marshal(jsonRaw)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "re-marshalling the Config field: %v", err)
		}
		if err := json.Unmarshal(configJSONChecked, &params.Config); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling the Config field: %v", err)
		}
	}
}

func addSource(sqlQuery string, params addSourceRequest, db *cdb.Handler) (ConsoleResponse, error) {
	var results ConsoleResponse
	results.Message = "Failed to add the source"

	// Check if Config is empty and set to default JSON if it is
	if !params.Config.IsEmpty() {
		// Validate and potentially reformat the existing Config JSON
		err := validateAndReformatConfig(&params.Config)
		if err != nil {
			return results, fmt.Errorf("failed to validate and reformat Config: %w", err)
		}
	}

	// Get the JSON string for the Config field
	configJSON, err := json.Marshal(params.Config)
	if err != nil {
		return results, err
	}

	// Execute the SQL statement
	qResults, err := (*db).ExecuteQuery(sqlQuery, params.URL, params.Status, params.Restricted, params.Disabled, params.Flags, string(configJSON), params.CategoryID, params.UsrID)
	if err != nil {
		return results, err
	}

	// Get the ID of the inserted website
	var id uint64
	for qResults.Next() {
		err = qResults.Scan(&id)
		if err != nil {
			results.Message = "Failed to get the ID of the inserted website"
			return results, err
		}
	}

	// Create the response message adding the id of the inserted source
	msg := fmt.Sprintf("Website inserted successfully with ID: %d", id)

	results.Message = msg
	return results, nil
}

/*
func getDefaultConfig() cfg.SourceConfig {
	defaultConfig := map[string]string{}
	defaultConfigJSON, _ := json.Marshal(defaultConfig)
	var config cfg.SourceConfig
	_ = json.Unmarshal(defaultConfigJSON, &config)
	return config
}
*/

func validateAndReformatConfig(config *cfg.SourceConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config to JSON: %w", err)
	}

	var jsonRaw map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &jsonRaw); err != nil {
		return fmt.Errorf("config field contains invalid JSON: %w", err)
	}

	configJSONChecked, err := json.Marshal(jsonRaw)
	if err != nil {
		return fmt.Errorf("failed to marshal Config field: %w", err)
	}

	if err := json.Unmarshal(configJSONChecked, config); err != nil {
		return fmt.Errorf("failed to unmarshal validated JSON back to Config struct: %w", err)
	}

	return nil
}

func performRemoveSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var results ConsoleResponse
	var sourceURL string // Assuming the source URL is passed. Adjust as necessary based on input.

	if qType == getQuery {
		// Direct extraction from query if it's a simple GET request
		sourceURL = query
	} else {
		// Handle extraction from a JSON document or other POST data
		// Assuming you have a method or logic to extract the URL from the POST body
		return ConsoleResponse{Message: "Invalid request"}, nil
	}

	// Start a transaction
	tx, err := (*db).Begin()
	if err != nil {
		return ConsoleResponse{Message: errFailedToStartTransaction}, err
	}

	// Proceed with deleting the source using the obtained source_id
	results, err = removeSource(tx, sourceURL)
	if err != nil {
		return ConsoleResponse{Message: "Failed to remove source and related data"}, err
	}

	// If everything went well, commit the transaction
	err = tx.Commit()
	if err != nil {
		return ConsoleResponse{Message: errFailedToCommitTransaction}, err
	}

	results.Message = infoSourceRemoved
	return results, nil
}

func removeSource(tx *sql.Tx, sourceURL string) (ConsoleResponse, error) {
	var results ConsoleResponse
	results.Message = "Failed to remove the source"

	// First, get the source_id for the given URL to ensure it exists and to use in cascading deletes if necessary
	var sourceID uint64
	err := tx.QueryRow("SELECT source_id FROM Sources WHERE url = $1", sourceURL).Scan(&sourceID)
	if err != nil {
		return results, err
	}

	// Proceed with deleting the source using the obtained source_id
	_, err = tx.Exec("DELETE FROM Sources WHERE source_id = $1", sourceID)
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to delete source"}, err2
		}
		return ConsoleResponse{Message: "Failed to delete source and related data"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_httpinfo();")
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_netinfo();")
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err
	}

	results.Message = infoSourceRemoved
	return results, nil
}

func performGetURLStatus(query string, qType int, db *cdb.Handler) (StatusResponse, error) {
	var results StatusResponse
	var sourceURL string // Assuming the source URL is passed. Adjust as necessary based on input.

	if qType == getQuery {
		// Direct extraction from query if it's a simple GET request
		sourceURL = query
	} else {
		// Handle extraction from a JSON document or other POST data
		// Assuming you have a method or logic to extract the URL from the POST body
		return StatusResponse{Message: "Invalid request"}, nil
	}

	// Start a transaction
	tx, err := (*db).Begin()
	if err != nil {
		return StatusResponse{Message: errFailedToStartTransaction}, err
	}

	// Proceed with getting the status
	results, err = getURLStatus(tx, sourceURL)
	if err != nil {
		return StatusResponse{Message: "Failed to get the status"}, err
	}

	// If everything went well, commit the transaction
	err = tx.Commit()
	if err != nil {
		return StatusResponse{Message: errFailedToCommitTransaction}, err
	}

	return results, nil
}

func getURLStatus(tx *sql.Tx, sourceURL string) (StatusResponse, error) {
	var results StatusResponse
	results.Message = "Failed to get the status"

	sourceURL = cmn.NormalizeURL(sourceURL)
	sourceURL = fmt.Sprintf("%%%s%%", sourceURL)
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Source URL: %s", sourceURL)

	query := `
		SELECT source_id,
			   url,
			   status,
			   priority,
			   engine,
			   created_at,
			   last_updated_at,
			   last_crawled_at,
			   last_error,
			   last_error_at,
			   restricted,
			   disabled,
			   flags,
			   config
		FROM Sources
		WHERE url LIKE $1`

	// Get the status
	rows, err := tx.Query(query, sourceURL)
	if err != nil {
		return results, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	var statuses []StatusResponseRow
	for rows.Next() {
		var row StatusResponseRow
		var configJSON []byte
		err = rows.Scan(&row.SourceID, &row.URL, &row.Status, &row.Priority, &row.Engine, &row.CreatedAt, &row.LastUpdatedAt, &row.LastCrawledAt, &row.LastError, &row.LastErrorAt, &row.Restricted, &row.Disabled, &row.Flags, &configJSON)
		if err != nil {
			return results, err
		}
		if configJSON != nil {
			if err := json.Unmarshal(configJSON, &row.Config); err != nil {
				return results, err
			}
		}

		statuses = append(statuses, row)
	}

	results.Message = infoAllSourcesStatus
	results.Items = statuses
	return results, nil
}

func performGetAllURLStatus(_ int, db *cdb.Handler) (StatusResponse, error) {
	// using _ instead of qType because for now we don't need it

	// Start a transaction
	tx, err := (*db).Begin()
	if err != nil {
		return StatusResponse{Message: errFailedToStartTransaction}, err
	}

	// Proceed with getting all statuses
	results, err := getAllURLStatus(tx)
	if err != nil {
		return StatusResponse{Message: "Failed to get all statuses"}, err
	}

	// If everything went well, commit the transaction
	err = tx.Commit()
	if err != nil {
		return StatusResponse{Message: errFailedToCommitTransaction}, err
	}

	return results, nil
}

func getAllURLStatus(tx *sql.Tx) (StatusResponse, error) {
	var results StatusResponse
	results.Message = "Failed to get all statuses"

	// Proceed with getting all statuses
	rows, err := tx.Query("SELECT source_id, url, status, priority, engine, created_at, last_updated_at, last_crawled_at, last_error, last_error_at, restricted, disabled, flags, config FROM Sources")
	if err != nil {
		return results, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	var statuses []StatusResponseRow
	for rows.Next() {
		var row StatusResponseRow
		var configJSON []byte
		err = rows.Scan(&row.SourceID, &row.URL, &row.Status, &row.Priority, &row.Engine, &row.CreatedAt, &row.LastUpdatedAt, &row.LastCrawledAt, &row.LastError, &row.LastErrorAt, &row.Restricted, &row.Disabled, &row.Flags, &configJSON)
		if err != nil {
			return results, err
		}
		if configJSON != nil {
			if err := json.Unmarshal(configJSON, &row.Config); err != nil {
				return results, err
			}
		}

		statuses = append(statuses, row)
	}

	results.Message = infoAllSourcesStatus
	results.Items = statuses
	return results, nil
}

func performUpdateSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var sqlParams cdb.UpdateSourceRequest
	var sourceConfig *string
	var sourceDetails *string

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		sqlParams.URL = cmn.NormalizeURL(query)
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &sqlParams)
		if err != nil {
			return ConsoleResponse{Message: "Invalid update request"}, fmt.Errorf("invalid JSON: %w", err)
		}
	}

	// Resolve sourceID if only URL is provided
	if sqlParams.SourceID == 0 && sqlParams.URL != "" {
		sourceID, err := cdb.GetSourceID(cdb.SourceFilter{URL: sqlParams.URL}, db)
		if err != nil {
			return ConsoleResponse{Message: "Failed to resolve Source ID"}, err
		}
		sqlParams.SourceID = int64(sourceID) //nolint:gosec // This is a controlled value
	} else if sqlParams.SourceID == 0 {
		return ConsoleResponse{Message: "Source ID or URL must be provided"}, fmt.Errorf("missing Source ID or URL")
	}

	// Retrieve existing data for the source
	var existingData cdb.UpdateSourceRequest
	selectQuery := `
        SELECT url, status, restricted, disabled, flags, config, details
        FROM Sources
        WHERE source_id = $1
    `
	err := (*db).QueryRow(selectQuery, sqlParams.SourceID).Scan(
		&existingData.URL,
		&existingData.Status,
		&existingData.Restricted,
		&existingData.Disabled,
		&existingData.Flags,
		&sourceConfig,
		&sourceDetails,
	)
	if err != nil {
		return ConsoleResponse{Message: "Failed to retrieve source data"}, fmt.Errorf("error querying existing source data: %w", err)
	}
	if sourceConfig != nil {
		existingData.Config = json.RawMessage(*sourceConfig)
	} else {
		existingData.Config = json.RawMessage("{}")
	}
	if sourceDetails != nil {
		existingData.Details = json.RawMessage(*sourceDetails)
	} else {
		existingData.Details = json.RawMessage("{}")
	}

	// Merge existing data with provided updates
	mergedData := cdb.UpdateSourceRequest{
		SourceID:   sqlParams.SourceID,
		URL:        coalesce(sqlParams.URL, existingData.URL),
		Status:     coalesce(sqlParams.Status, existingData.Status),
		Restricted: coalesceInt(sqlParams.Restricted, existingData.Restricted),
		Disabled:   coalesceBool(sqlParams.Disabled, existingData.Disabled),
		Flags:      coalesceInt(sqlParams.Flags, existingData.Flags),
		Config:     coalesceJSON(sqlParams.Config, existingData.Config),
		Details:    coalesceJSON(sqlParams.Details, existingData.Details),
	}

	// Perform the update
	updateQuery := `
        UPDATE Sources
        SET url = $1,
            status = $2,
            restricted = $3,
            disabled = $4,
            flags = $5,
            config = $6::jsonb,
            details = $7::jsonb
        WHERE source_id = $8
    `
	_, err = (*db).Exec(updateQuery,
		cmn.NormalizeURL(mergedData.URL),
		mergedData.Status,
		mergedData.Restricted,
		mergedData.Disabled,
		mergedData.Flags,
		mergedData.Config,
		mergedData.Details,
		mergedData.SourceID,
	)
	if err != nil {
		return ConsoleResponse{Message: "Failed to update source"}, err
	}

	return ConsoleResponse{Message: "Source updated successfully"}, nil
}

func coalesce(newValue, existingValue string) string {
	if newValue != "" {
		return newValue
	}
	return existingValue
}

func coalesceInt(newValue, existingValue int) int {
	if newValue != 0 {
		return newValue
	}
	return existingValue
}

func coalesceBool(newValue, existingValue bool) bool {
	// In case of boolean, use a specific value (e.g., a pointer or extra logic)
	// Here, assuming `false` is not a valid new value
	if newValue {
		return newValue
	}
	return existingValue
}

func coalesceJSON(newValue, existingValue json.RawMessage) json.RawMessage {
	if len(newValue) > 0 {
		return newValue
	}
	return existingValue
}

func performVacuumSource(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var filter cdb.SourceFilter

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		filter.URL = cmn.NormalizeURL(query)
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &filter)
		if err != nil {
			return ConsoleResponse{Message: "Invalid vacuum request"}, fmt.Errorf("invalid JSON: %w", err)
		}
	}

	// Resolve sourceID if only URL is provided
	if filter.SourceID == 0 && filter.URL != "" {
		sourceID, err := cdb.GetSourceID(filter, db)
		if err != nil {
			return ConsoleResponse{Message: "Failed to resolve Source ID"}, err
		}
		filter.SourceID = int64(sourceID) //nolint:gosec // This is a controlled value
	} else if filter.SourceID == 0 {
		return ConsoleResponse{Message: "Source ID or URL must be provided"}, fmt.Errorf("missing Source ID or URL")
	}

	tx, err := (*db).Begin()
	if err != nil {
		return ConsoleResponse{Message: "Failed to start transaction"}, err
	}

	// Deleting indexed data
	queries := []string{
		"DELETE FROM KeywordIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM MetaTagsIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM WebObjectsIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM NetInfoIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM HTTPInfoIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM SourceSearchIndex WHERE source_id = $1",
	}

	for _, query := range queries {
		_, err := tx.Exec(query, filter.SourceID)
		if err != nil {
			err2 := tx.Rollback() // Rollback if any query fails
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to rollback transaction: %v", err2)
			}
			return ConsoleResponse{Message: "Failed to vacuum source data"}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return ConsoleResponse{Message: "Failed to commit transaction"}, err
	}

	return ConsoleResponse{Message: "Source vacuumed successfully"}, nil
}

func performAddOwner(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var owner cdb.OwnerRequest // Define a struct for owner if not already present

	if qType == getQuery {
		// Create a JSON document with the owner name
		jDoc := fmt.Sprintf(`{"name": "%s"}`, strings.ReplaceAll(strings.TrimSpace(query), "\"", ""))
		err := json.Unmarshal([]byte(jDoc), &owner.Details)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner name"}, fmt.Errorf("failed to parse owner name: %w", err)
		}
	} else {
		// Parse POST request JSON
		err := json.Unmarshal([]byte(query), &owner)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner data"}, fmt.Errorf("failed to parse owner data: %w", err)
		}
	}

	// Insert owner into the database
	queryStr := `
		INSERT INTO Owners (parent_id, details)
		VALUES ($1, $2)
		RETURNING owner_id
	`
	var ownerID int64
	err := (*db).QueryRow(queryStr, owner.Details).Scan(&ownerID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to add owner"}, fmt.Errorf("error adding owner: %w", err)
	}

	return ConsoleResponse{Message: fmt.Sprintf("Owner added successfully with ID %d", ownerID)}, nil
}

func performAddCategory(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var category cdb.CategoryRequest // Define a struct for category if not already present

	if qType == getQuery {
		// For GET requests, assume `query` is a simple name
		category.Name = strings.TrimSpace(query)
		if category.Name == "" {
			return ConsoleResponse{Message: "Invalid category name"}, fmt.Errorf("category name is required")
		}
	} else {
		// Parse POST request JSON
		err := json.Unmarshal([]byte(query), &category)
		if err != nil {
			return ConsoleResponse{Message: "Invalid category data"}, fmt.Errorf("failed to parse category data: %w", err)
		}
	}

	// Insert category into the database
	queryStr := `
		INSERT INTO Categories (name, parent_id, description)
		VALUES ($1, $2, $3)
		RETURNING category_id
	`
	var categoryID int64
	err := (*db).QueryRow(queryStr, category.Name, category.ParentID, category.Description).Scan(&categoryID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to add category"}, fmt.Errorf("error adding category: %w", err)
	}

	return ConsoleResponse{Message: fmt.Sprintf("Category added successfully with ID %d", categoryID)}, nil
}

func performUpdateOwner(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var owner cdb.OwnerRequest

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		jDoc := fmt.Sprintf(`{"details": %s}`, strings.ReplaceAll(strings.TrimSpace(query), "\"", ""))
		err := json.Unmarshal([]byte(jDoc), &owner)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner data"}, fmt.Errorf("failed to parse owner data: %w", err)
		}
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &owner)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner data"}, fmt.Errorf("failed to parse owner data: %w", err)
		}
	}

	if owner.OwnerID == 0 {
		return ConsoleResponse{Message: "Owner ID must be provided"}, fmt.Errorf("missing Owner ID")
	}

	// Update owner in the database
	queryStr := `
		UPDATE Owners
		SET parent_id = COALESCE($1, parent_id),
		    details = COALESCE($2, details::jsonb)
		WHERE owner_id = $3
	`
	_, err := (*db).Exec(queryStr, owner.DetailsHash, owner.Details, owner.OwnerID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to update owner"}, fmt.Errorf("error updating owner: %w", err)
	}

	return ConsoleResponse{Message: "Owner updated successfully"}, nil
}

func performRemoveOwner(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var ownerID int64

	if qType == getQuery {
		// Parse the query as a GET request
		id, err := strconv.ParseInt(query, 10, 64)
		if err != nil {
			return ConsoleResponse{Message: "Invalid owner ID"}, fmt.Errorf("failed to parse owner ID: %w", err)
		}
		ownerID = id
	} else {
		// Parse the query as a POST request (JSON payload)
		var req map[string]int64
		err := json.Unmarshal([]byte(query), &req)
		if err != nil || req["owner_id"] == 0 {
			return ConsoleResponse{Message: "Invalid owner ID"}, fmt.Errorf("missing or invalid owner ID")
		}
		ownerID = req["owner_id"]
	}

	// Remove owner from the database
	queryStr := `DELETE FROM Owners WHERE owner_id = $1`
	_, err := (*db).Exec(queryStr, ownerID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to remove owner"}, fmt.Errorf("error removing owner: %w", err)
	}

	return ConsoleResponse{Message: "Owner removed successfully"}, nil
}

func performUpdateCategory(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var category cdb.CategoryRequest

	if qType == getQuery {
		// Parse the query as a GET request (direct parameters)
		category.Name = strings.TrimSpace(query)
		if category.Name == "" {
			return ConsoleResponse{Message: "Invalid category name"}, fmt.Errorf("category name is required")
		}
	} else {
		// Parse the query as a POST request (JSON payload)
		err := json.Unmarshal([]byte(query), &category)
		if err != nil {
			return ConsoleResponse{Message: "Invalid category data"}, fmt.Errorf("failed to parse category data: %w", err)
		}
	}

	if category.CategoryID == 0 {
		return ConsoleResponse{Message: "Category ID must be provided"}, fmt.Errorf("missing Category ID")
	}

	// Update category in the database
	queryStr := `
		UPDATE Categories
		SET name = COALESCE($1, name),
		    parent_id = COALESCE($2, parent_id),
		    description = COALESCE($3, description)
		WHERE category_id = $4
	`
	_, err := (*db).Exec(queryStr, category.Name, category.ParentID, category.Description, category.CategoryID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to update category"}, fmt.Errorf("error updating category: %w", err)
	}

	return ConsoleResponse{Message: "Category updated successfully"}, nil
}

func performRemoveCategory(query string, qType int, db *cdb.Handler) (ConsoleResponse, error) {
	var categoryID int64

	if qType == getQuery {
		// Parse the query as a GET request
		id, err := strconv.ParseInt(query, 10, 64)
		if err != nil {
			return ConsoleResponse{Message: "Invalid category ID"}, fmt.Errorf("failed to parse category ID: %w", err)
		}
		categoryID = id
	} else {
		// Parse the query as a POST request (JSON payload)
		var req map[string]int64
		err := json.Unmarshal([]byte(query), &req)
		if err != nil || req["category_id"] == 0 {
			return ConsoleResponse{Message: "Invalid category ID"}, fmt.Errorf("missing or invalid category ID")
		}
		categoryID = req["category_id"]
	}

	// Remove category from the database
	queryStr := `DELETE FROM Categories WHERE category_id = $1`
	_, err := (*db).Exec(queryStr, categoryID)
	if err != nil {
		return ConsoleResponse{Message: "Failed to remove category"}, fmt.Errorf("error removing category: %w", err)
	}

	return ConsoleResponse{Message: "Category removed successfully"}, nil
}
