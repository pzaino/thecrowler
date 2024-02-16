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
	"encoding/json"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func performAddSource(query string, qType int) (ConsoleResponse, error) {
	var sqlQuery string
	var sqlParams addSourceRequest
	if qType == 1 {
		sqlParams.URL = query
		sqlQuery = "INSERT INTO Sources (url, last_crawled_at, status) VALUES ($1, NULL, 'pending')"
	} else {
		// extract the parameters from the query
		extractAddSourceParams(query, &sqlParams)
		sqlQuery = "INSERT INTO Sources (url, last_crawled_at, status, restricted, disabled, flags, config) VALUES ($1, NULL, $2, $3, $4, $5, $6)"
	}

	// Perform the addSource operation
	results, err := addSource(sqlQuery, sqlParams)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error adding the source: %v", err)
		return results, err
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Website inserted successfully: %s", query)
	return results, nil
}

func extractAddSourceParams(query string, params *addSourceRequest) {
	params.Restricted = -1

	// Unmarshal query into params
	err := json.Unmarshal([]byte(query), &params)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshalling the query: %v", err)
	}

	// Check for missing parameters
	if params.Status == "" {
		params.Status = "pending"
	}
	if params.Restricted < 0 || params.Restricted > 4 {
		params.Restricted = 2
	}
	if params.Config == "" {
		params.Config = "NULL"
	}
}

func addSource(sqlQuery string, params addSourceRequest) (ConsoleResponse, error) {
	var results ConsoleResponse
	results.Message = "Failed to add the source"

	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return results, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error connecting to the database: %v", err)
		return results, err
	}
	defer db.Close()

	// Execute the SQL statement
	_, err = db.Exec(sqlQuery, params)
	if err != nil {
		return results, err
	}

	results.Message = "Website inserted successfully"
	return results, nil
}

func performRemoveSource(query string, qType int) (ConsoleResponse, error) {
	var results ConsoleResponse
	var sourceURL string // Assuming the source URL is passed. Adjust as necessary based on input.

	if qType == 1 {
		// Direct extraction from query if it's a simple GET request
		sourceURL = query
	} else {
		// Handle extraction from a JSON document or other POST data
		// Assuming you have a method or logic to extract the URL from the POST body
		return ConsoleResponse{Message: "Invalid request"}, nil
	}

	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return ConsoleResponse{Message: "Failed to initialize database handler"}, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		return ConsoleResponse{Message: "Error connecting to the database"}, err
	}
	defer db.Close()

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return ConsoleResponse{Message: "Failed to start transaction"}, err
	}

	// First, get the source_id for the given URL to ensure it exists and to use in cascading deletes if necessary
	var sourceID int64
	err = tx.QueryRow("SELECT source_id FROM Sources WHERE url = $1", sourceURL).Scan(&sourceID)
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to find source with provided URL"}, err2
		}
		return ConsoleResponse{Message: "Failed to find source with provided URL"}, err
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
	_, err = tx.Exec("SELECT cleanup_orphaned_httpinfo();", sourceID)
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_netinfo();", sourceID)
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err
	}

	// If everything went well, commit the transaction
	err = tx.Commit()
	if err != nil {
		return ConsoleResponse{Message: "Failed to commit transaction"}, err
	}

	results.Message = "Source and related data removed successfully"
	return results, nil
}
