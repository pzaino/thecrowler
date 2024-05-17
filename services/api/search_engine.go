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
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

const (
	sqlQueryLabel       = "SQL query: %s"
	sqlQueryParamsLabel = "SQL params: %v"
	dbConnErrorLabel    = "Error connecting to the database: %v"
	SearchLabel         = "Performing search for: %s"
	noQueryProvided     = "no query provided"
	queryExecTime       = "Query execution time: %v"
	dataEncapTime       = "Data encapsulation execution time: %v"
)

// TODO: Improve Tokenizer and query generator to support more complex queries
//       like:
/*
	SELECT *
	FROM NetInfo
	WHERE EXISTS (
		SELECT 1
		FROM jsonb_array_elements(details -> 'service_scout' -> 'hosts') AS hosts
		WHERE hosts ->> 'ip' = '$1'
	);
*/

// tokenize splits the input string into tokens.
// following the "dorking" query language's rules.
func tokenize(input string) []string {
	var tokens []string
	var currentToken strings.Builder

	inQuotes := false
	inEscape := false
	for _, r := range input {
		switch {
		case r == '\\':
			inEscape = true
		case r == '"':
			if !inEscape {
				inQuotes = toggleQuotes(inQuotes, &tokens, &currentToken)
			} else {
				currentToken.WriteRune(r)
				inEscape = false
			}
		case r == ':' && !inQuotes:
			completeFieldSpecifier(&tokens, &currentToken)
		case unicode.IsSpace(r) && !inQuotes:
			handleSpace(&tokens, &currentToken)
		case (r == '|' || r == '&') && !inQuotes:
			handlePipeAnd(&tokens, &currentToken, r)
		default:
			currentToken.WriteRune(r)
		}
	}
	handleRemainingToken(&tokens, &currentToken)

	return tokens
}

// toggleQuotes toggles the inQuotes flag and appends the current token to the tokens slice.
func toggleQuotes(inQuotes bool, tokens *[]string, currentToken *strings.Builder) bool {
	inQuotes = !inQuotes
	if !inQuotes {
		*tokens = append(*tokens, currentToken.String())
		currentToken.Reset()
	}
	return inQuotes
}

// completeFieldSpecifier appends the current token to the tokens slice and adds a colon.
func completeFieldSpecifier(tokens *[]string, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, currentToken.String()+":")
		currentToken.Reset()
	}
}

// handleSpace appends the current token to the tokens slice.
func handleSpace(tokens *[]string, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, currentToken.String())
		currentToken.Reset()
	}
}

// handlePipeAnd appends the current token to the tokens slice and adds the pipe or and operator.
func handlePipeAnd(tokens *[]string, currentToken *strings.Builder, r rune) {
	if currentToken.Len() > 0 {
		if currentToken.String() != "|" && currentToken.String() != "&" {
			*tokens = append(*tokens, currentToken.String())
			currentToken.Reset()
		} else {
			*tokens = append(*tokens, currentToken.String()+string(r))
			currentToken.Reset()
			return
		}
	}
	currentToken.WriteRune(r)
}

// handleRemainingToken appends the current token to the tokens slice.
func handleRemainingToken(tokens *[]string, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, currentToken.String())
	}
}

// PrepareInput removes leading and trailing spaces from the input string.
func isFieldSpecifier(input string) bool {
	// Define allowed fields
	var allowedFields = map[string]bool{
		"title":   true,
		"summary": true,
		"content": config.API.ContentSearch,
	}

	// Split the input string at the colon
	parts := strings.SplitN(input, ":", 2)
	if len(parts) != 2 {
		return false
	}

	field := strings.ToLower(strings.TrimSpace(parts[0]))
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Field specifier: %s", field)

	// Check if the extracted field is a valid field
	_, ok := allowedFields[field]
	return ok
}

func isQuotedString(input string) bool {
	matched, err := regexp.MatchString(`^".*"$`, input)
	if err != nil {
		return false
	}
	return matched
}

func getDefaultFields() []string {
	var defaultFields []string
	if config.API.ContentSearch {
		defaultFields = []string{"page_url", "title", "summary", "content"}
	} else {
		defaultFields = []string{"page_url", "title", "summary"}
	}
	return defaultFields
}

// parseAdvancedQuery interpret the "dorcking" query language and returns the SQL query and its parameters.
// queryBody represent the SQL query body, while input is the "raw" dorking input.
func parseAdvancedQuery(queryBody string, input string) (string, []interface{}, error) {
	defaultFields := getDefaultFields()
	tokens := tokenize(input)

	// Parse the tokens and generate the query parts
	// and query params:
	var queryParts [][]string
	var queryParams []interface{}
	paramCounter := 1
	var currentField string
	queryGroup := -1 // Initialize to -1, so the first group starts at index 0
	for i, token := range tokens {
		switch {
		case isFieldSpecifier(token):
			currentField = strings.TrimSuffix(token, ":")
			queryGroup++                                // Move to the next group
			queryParts = append(queryParts, []string{}) // Initialize the new group

		case token == "&", token == "|", token == "&&", token == "||":
			if queryGroup == -1 {
				queryGroup = 0
				queryParts = append(queryParts, []string{})
			}
			if token == "&&" {
				queryParts[queryGroup] = append(queryParts[queryGroup], "AND")
			} else {
				queryParts[queryGroup] = append(queryParts[queryGroup], "OR")
			}

		default:
			addCondition := func(condition string) {
				if queryGroup == -1 {
					queryGroup = 0
					queryParts = append(queryParts, []string{condition})
				} else {
					queryParts[queryGroup] = append(queryParts[queryGroup], condition)
				}
				queryParams = append(queryParams, "%"+strings.ToLower(token)+"%")
				paramCounter++
			}

			if isQuotedString(token) {
				token = strings.Trim(token, `"`)
			}

			if isFieldSpecifier(currentField + ":") {
				condition := fmt.Sprintf("LOWER(%s) LIKE $%d", currentField, paramCounter)
				addCondition(condition)
			} else {
				var conditions []string
				for _, field := range defaultFields {
					condition := fmt.Sprintf("LOWER(%s) LIKE $%d", field, paramCounter)
					conditions = append(conditions, condition)
				}
				combinedCondition := "(" + strings.Join(conditions, " OR ") + ")"
				addCondition(combinedCondition)
			}

			if len(tokens) > i+1 && !isFieldSpecifier(tokens[i+1]) && tokens[i+1] != "&&" && tokens[i+1] != "||" {
				queryParts[queryGroup] = append(queryParts[queryGroup], "AND")
			}
		}
	}

	// Add a separate group for keyword conditions
	var keywordConditions []string
	for i := 1; i < paramCounter; i++ {
		keywordCondition := fmt.Sprintf("k.keyword LIKE $%d", i)
		keywordConditions = append(keywordConditions, keywordCondition)
	}

	// Append the keyword group at the end
	if len(keywordConditions) > 0 {
		keywordGroup := "(" + strings.Join(keywordConditions, " OR ") + ")"
		queryParts = append(queryParts, []string{keywordGroup})
	}
	if len(queryParts) == 0 {
		return "", nil, errors.New("no valid query provided")
	}

	// Build the combined query
	combinedQuery := buildCombinedQuery(queryBody, queryParts)

	return combinedQuery, queryParams, nil
}

func buildCombinedQuery(queryBody string, queryParts [][]string) string {
	var combinedQuery string
	combinedQuery += queryBody
	for i, group := range queryParts {
		if i > 0 {
			// Use 'OR' before the keyword group
			if i == len(queryParts)-1 {
				combinedQuery += " OR "
			} else {
				combinedQuery += " AND "
			}
		}
		// let's clean up possible AND or OR at the end of the group
		if isLogicalOperator(group[len(group)-1]) {
			group = group[:len(group)-1]
		}

		combinedQuery += "(" + strings.Join(group, " ") + ")"
	}
	//combinedQuery += ";"

	return combinedQuery
}

// Helper function to determine if a string is a logical operator
func isLogicalOperator(op string) bool {
	return op == "AND" || op == "OR"
}

func performSearch(query string) (SearchResult, error) {
	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return SearchResult{}, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnErrorLabel, err)
		return SearchResult{}, err
	}
	defer db.Close()

	cmn.DebugMsg(cmn.DbgLvlDebug, SearchLabel, query)

	// Prepare the query body
	var queryBody string
	if config.API.ReturnContent {
		queryBody = `
		SELECT DISTINCT
			si.title, si.page_url, si.summary, wo.object_content AS content
		FROM
			SearchIndex si
		LEFT JOIN
			PageWebObjectsIndex pwoi ON si.index_id = pwoi.index_id
		LEFT JOIN
			WebObjects wo ON pwoi.object_id = wo.object_id
		LEFT JOIN
			KeywordIndex ki ON si.index_id = ki.index_id
		LEFT JOIN
			Keywords k ON ki.keyword_id = k.keyword_id
		WHERE
		`
	} else {
		queryBody = `
		SELECT DISTINCT
			si.title, si.page_url, si.summary, '' as content
		FROM
			SearchIndex si
		LEFT JOIN
			KeywordIndex ki ON si.index_id = ki.index_id
		LEFT JOIN
			Keywords k ON ki.keyword_id = k.keyword_id
		WHERE
		`
	}

	// Parse the user input
	sqlQuery, sqlParams, err := parseAdvancedQuery(queryBody, query)
	if err != nil {
		return SearchResult{}, err
	}

	sqlQuery = sqlQuery + ";"

	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take the current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := db.ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close()

	// Calculate the query execution time
	elapsed := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, queryExecTime, elapsed)

	// Take the current timer (to monitor encapsulation performance)
	start = time.Now()

	// Iterate over the results
	var results SearchResult
	for rows.Next() {
		var title, link, summary, snippet string
		if err := rows.Scan(&title, &link, &summary, &snippet); err != nil {
			return SearchResult{}, err
		}
		results.Items = append(results.Items, struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Summary string `json:"summary"`
			Snippet string `json:"snippet"`
		}{
			Title:   title,
			Link:    link,
			Summary: summary,
			Snippet: snippet,
		})
	}

	// Calculate the query execution time
	elapsed = time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, dataEncapTime, elapsed)

	return results, nil
}

func performScreenshotSearch(query string, qType int) (ScreenshotResponse, error) {
	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return ScreenshotResponse{}, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnErrorLabel, err)
		return ScreenshotResponse{}, err
	}
	defer db.Close()

	cmn.DebugMsg(cmn.DbgLvlDebug, SearchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		sqlQuery, sqlParams, err = parseScreenshotGetQuery(query)
		if err != nil {
			return ScreenshotResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		sqlQuery, sqlParams, err = parseScreenshotQuery(query)
		if err != nil {
			return ScreenshotResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := db.ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return ScreenshotResponse{}, err
	}
	defer rows.Close()

	// Calculate the query execution time
	elapsed := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, queryExecTime, elapsed)

	// Take current timer (to monitor encapsulation performance)
	start = time.Now()

	// Iterate over the results
	var results ScreenshotResponse
	for rows.Next() {
		var link, createdAt, updatedAt, sType, sFormat string
		var width, height, byteSize int
		if err := rows.Scan(&link, &createdAt, &updatedAt, &sType, &sFormat, &width, &height, &byteSize); err != nil {
			return ScreenshotResponse{}, err
		}
		results = ScreenshotResponse{
			Link:          link,
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
			Type:          sType,
			Format:        sFormat,
			Width:         width,
			Height:        height,
			ByteSize:      byteSize,
		}
	}

	// Calculate the query execution time
	elapsed = time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, dataEncapTime, elapsed)

	return results, nil
}

func parseScreenshotGetQuery(input string) (string, []interface{}, error) {
	// Prepare the query body
	queryBody := `
	SELECT DISTINCT
		s.screenshot_link,
		s.created_at,
		s.last_updated_at,
		s.type,
		s.format,
		s.width,
		s.height,
		s.byte_size
	FROM
		Screenshots AS s
	JOIN
		SearchIndex AS si ON s.index_id = si.index_id
	LEFT JOIN
		KeywordIndex ki ON si.index_id = ki.index_id
	LEFT JOIN
		Keywords k ON ki.keyword_id = k.keyword_id
	WHERE
		s.screenshot_link != '' AND s.screenshot_link IS NOT NULL AND
	`

	sqlQuery, sqlParams, err := parseAdvancedQuery(queryBody, input)
	if err != nil {
		return "", nil, err
	}

	sqlQuery = sqlQuery + " ORDER BY s.created_at DESC;"

	return sqlQuery, sqlParams, nil
}

func parseScreenshotQuery(input string) (string, []interface{}, error) {
	var query string
	var err error
	var sqlParams []interface{}

	input = PrepareInput(input)

	// Unmarshal the JSON document
	var req ScreenshotRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshalling JSON: %v, %v", err, input)
		return "", nil, err
	}

	// Extract the query from the request
	if len(req.URL) > 0 {
		query = "%" + req.URL + "%"
	} else {
		return "", nil, errors.New(noQueryProvided)
	}

	// Parse the user input
	sqlQuery := `
	SELECT DISTINCT
		s.screenshot_link,
		s.created_at,
		s.last_updated_at,
		s.type,
		s.format,
		s.width,
		s.height,
		s.byte_size
	FROM
		Screenshots AS s
	JOIN
		SearchIndex AS si ON s.index_id = si.index_id
	WHERE
		LOWER(si.page_url) LIKE LOWER($1)
	AND
		s.screenshot_link != '' AND s.screenshot_link IS NOT NULL;
	`
	sqlParams = append(sqlParams, query)

	return sqlQuery, sqlParams, nil
}

func performWebObjectSearch(query string, qType int) (WebObjectResponse, error) {
	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return WebObjectResponse{}, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnErrorLabel, err)
		return WebObjectResponse{}, err
	}
	defer db.Close()

	cmn.DebugMsg(cmn.DbgLvlDebug, SearchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		sqlQuery, sqlParams, err = parseWebObjectGetQuery(query)
		if err != nil {
			return WebObjectResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		sqlQuery, sqlParams, err = parseWebObjectQuery(query)
		if err != nil {
			return WebObjectResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := db.ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return WebObjectResponse{}, err
	}
	defer rows.Close()

	// Calculate the query execution time
	elapsed := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, queryExecTime, elapsed)

	// Take current timer (to monitor encapsulation performance)
	start = time.Now()

	// Iterate over the results
	var results WebObjectResponse
	for rows.Next() {
		var row WebObjectRow
		var detailsJSON []byte

		// Read rows and unmarshal the JSON data
		if err := rows.Scan(&row.ObjectLink, &row.CreatedAt, &row.LastUpdatedAt, &row.ObjectType, &row.ObjectHash, &row.ObjectContent, &row.ObjectHTML, &detailsJSON); err != nil {
			return WebObjectResponse{}, err
		}
		if err := json.Unmarshal(detailsJSON, &row.Details); err != nil {
			return WebObjectResponse{}, err
		}

		// Append the row to the results
		results.Items = append(results.Items, row)
	}

	// Calculate the query execution time
	elapsed = time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, dataEncapTime, elapsed)

	return results, nil
}

func parseWebObjectGetQuery(input string) (string, []interface{}, error) {
	// Prepare the query body
	queryBody := `
	SELECT DISTINCT
		wo.object_link,
		wo.created_at,
		wo.last_updated_at,
		wo.object_type,
		wo.object_hash,
		wo.object_content,
		wo.object_html,
		wo.details
	FROM
		WebObjects AS wo
	JOIN
		PageWebObjectsIndex AS pwi ON wo.object_id = pwi.object_id
	JOIN
		SearchIndex AS si ON pwi.index_id = si.index_id
	LEFT JOIN
		KeywordIndex ki ON si.index_id = ki.index_id
	LEFT JOIN
		Keywords k ON ki.keyword_id = k.keyword_id
	WHERE
		wo.object_link != ''
		AND wo.object_link IS NOT NULL
		AND `

	sqlQuery, sqlParams, err := parseAdvancedQuery(queryBody, input)
	if err != nil {
		return "", nil, err
	}

	sqlQuery = sqlQuery + " ORDER BY wo.created_at DESC;"

	return sqlQuery, sqlParams, nil
}

func parseWebObjectQuery(input string) (string, []interface{}, error) {
	var query string
	var err error
	var sqlParams []interface{}

	input = PrepareInput(input)

	// Unmarshal the JSON document
	var req WebObjectRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshalling JSON: %v, %v", err, input)
		return "", nil, err
	}

	// Extract the query from the request
	if len(req.URL) > 0 {
		query = "%" + req.URL + "%"
	} else {
		return "", nil, errors.New(noQueryProvided)
	}

	// Parse the user input
	sqlQuery := `
	SELECT DISTINCT
		wo.object_link,
		wo.created_at,
		wo.last_updated_at,
		wo.object_type,
		wo.object_hash,
		wo.object_content,
		wo.object_html,
		wo.details
	FROM
		WebObjects AS wo
	JOIN
        PageWebObjectsIndex AS pwi ON wo.object_id = pwi.object_id
    JOIN
        SearchIndex AS si ON pwi.index_id = si.index_id
	WHERE
		LOWER(si.page_url) LIKE LOWER($1)
	AND
		wo.object_link != '' AND wo.object_link IS NOT NULL;
	`
	sqlParams = append(sqlParams, query)

	return sqlQuery, sqlParams, nil
}

// performNetInfoSearch performs a search for network information.
func performNetInfoSearch(query string, qType int) (NetInfoResponse, error) {
	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return NetInfoResponse{}, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnErrorLabel, err)
		return NetInfoResponse{}, err
	}
	defer db.Close()

	cmn.DebugMsg(cmn.DbgLvlDebug, SearchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		sqlQuery, sqlParams, err = parseNetInfoGetQuery(query)
		if err != nil {
			return NetInfoResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		sqlQuery, sqlParams, err = parseNetInfoQuery(query)
		if err != nil {
			return NetInfoResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := db.ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return NetInfoResponse{}, err
	}
	defer rows.Close()

	// Calculate the query execution time
	elapsed := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, queryExecTime, elapsed)

	// Take current timer (to monitor encapsulation performance)
	start = time.Now()

	var results NetInfoResponse
	for rows.Next() {
		var row NetInfoRow
		var detailsJSON []byte // Use a byte slice to hold the JSONB column data

		// Adjust Scan to match the expected columns returned by your query
		if err := rows.Scan(&row.CreatedAt, &row.LastUpdatedAt, &detailsJSON); err != nil {
			return NetInfoResponse{}, err
		}

		// Assuming that detailsJSON contains an array of neti.NetInfo,
		// you need to unmarshal the JSON into the NetInfo struct.
		if err := json.Unmarshal(detailsJSON, &row.Details); err != nil {
			return NetInfoResponse{}, err // Handle JSON unmarshal error
		}

		// Append the row to the results
		results.Items = append(results.Items, row)
	}

	// Ensure to check rows.Err() after the loop to catch any error that occurred during iteration.
	if err := rows.Err(); err != nil {
		return NetInfoResponse{}, err
	}

	// Calculate the query execution time
	elapsed = time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, dataEncapTime, elapsed)

	return results, nil
}

func parseNetInfoGetQuery(input string) (string, []interface{}, error) {
	// Prepare the query body
	queryBody := `
	SELECT DISTINCT
		ni.created_at,
		ni.last_updated_at,
		ni.details
	FROM
		NetInfo ni
	JOIN
        NetInfoIndex nii ON ni.netinfo_id = nii.netinfo_id
	JOIN
        SearchIndex si ON nii.index_id = si.index_id
	LEFT JOIN
		KeywordIndex ki ON si.index_id = ki.index_id
	LEFT JOIN
		Keywords k ON ki.keyword_id = k.keyword_id
	WHERE
	`
	sqlQuery, sqlParams, err := parseAdvancedQuery(queryBody, input)
	if err != nil {
		return "", nil, err
	}

	sqlQuery = sqlQuery + " ORDER BY ni.created_at DESC;"

	return sqlQuery, sqlParams, nil
}

func parseNetInfoQuery(input string) (string, []interface{}, error) {
	var query string
	var err error
	var sqlParams []interface{}

	// Unmarshal the JSON document
	var req QueryRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		return "", nil, err
	}

	// Extract the query from the request
	if len(req.Title) > 0 {
		query = req.Title
	} else {
		return "", nil, errors.New(noQueryProvided)
	}

	// Parse the user input
	sqlQuery := `
	SELECT DISTINCT
		ni.created_at,
		ni.last_updated_at,
		ni.details
	FROM
		NetInfo ni
	JOIN
        NetInfoIndex nii ON ni.netinfo_id = nii.netinfo_id
	JOIN
        SearchIndex si ON nii.index_id = si.index_id
	WHERE
		si.page_url LIKE $1;
	`
	sqlParams = append(sqlParams, query)

	return sqlQuery, sqlParams, nil
}

// performNetInfoSearch performs a search for network information.
func performHTTPInfoSearch(query string, qType int) (HTTPInfoResponse, error) {
	// Initialize the database handler
	db, err := cdb.NewHandler(config)
	if err != nil {
		return HTTPInfoResponse{}, err
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnErrorLabel, err)
		return HTTPInfoResponse{}, err
	}
	defer db.Close()

	cmn.DebugMsg(cmn.DbgLvlDebug, SearchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		sqlQuery, sqlParams, err = parseHTTPInfoGetQuery(query)
		if err != nil {
			return HTTPInfoResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		sqlQuery, sqlParams, err = parseHTTPInfoQuery(query)
		if err != nil {
			return HTTPInfoResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := db.ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return HTTPInfoResponse{}, err
	}
	defer rows.Close()

	// Calculate the query execution time
	elapsed := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, queryExecTime, elapsed)

	// Take current timer (to monitor encapsulation performance)
	start = time.Now()

	var results HTTPInfoResponse
	for rows.Next() {
		var row HTTPInfoRow
		var detailsJSON []byte // Use a byte slice to hold the JSONB column data

		// Adjust Scan to match the expected columns returned by your query
		if err := rows.Scan(&row.CreatedAt, &row.LastUpdatedAt, &detailsJSON); err != nil {
			return HTTPInfoResponse{}, err
		}

		// Assuming that detailsJSON contains an array of neti.NetInfo,
		// you need to unmarshal the JSON into the NetInfo struct.
		if err := json.Unmarshal(detailsJSON, &row.Details); err != nil {
			return HTTPInfoResponse{}, err // Handle JSON unmarshal error
		}

		// Append the row to the results
		results.Items = append(results.Items, row)
	}

	// Ensure to check rows.Err() after the loop to catch any error that occurred during iteration.
	if err := rows.Err(); err != nil {
		return HTTPInfoResponse{}, err
	}

	// Calculate the query execution time
	elapsed = time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, dataEncapTime, elapsed)

	return results, nil
}

func parseHTTPInfoGetQuery(input string) (string, []interface{}, error) {
	// Prepare the query body
	queryBody := `
	SELECT DISTINCT
		hi.created_at,
		hi.last_updated_at,
		hi.details
	FROM
		HTTPInfo hi
	JOIN
		HTTPInfoIndex hii ON hi.httpinfo_id = hii.httpinfo_id
	JOIN
		SearchIndex si ON hii.index_id = si.index_id
	LEFT JOIN
		KeywordIndex ki ON si.index_id = ki.index_id
	LEFT JOIN
		Keywords k ON ki.keyword_id = k.keyword_id
	WHERE
	`
	sqlQuery, sqlParams, err := parseAdvancedQuery(queryBody, input)
	if err != nil {
		return "", nil, err
	}

	sqlQuery = sqlQuery + " ORDER BY hi.created_at DESC;"

	return sqlQuery, sqlParams, nil
}

func parseHTTPInfoQuery(input string) (string, []interface{}, error) {
	var query string
	var err error
	var sqlParams []interface{}

	// Unmarshal the JSON document
	var req QueryRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		return "", nil, err
	}

	// Extract the query from the request
	if len(req.Title) > 0 {
		query = req.Title
	} else {
		return "", nil, errors.New(noQueryProvided)
	}

	// Parse the user input
	sqlQuery := `
	SELECT DISTINCT
		hi.created_at,
		hi.last_updated_at,
		hi.details
	FROM
		HTTPInfo hi
	JOIN
        HTTPInfoIndex hii ON hi.httpinfo_id = hii.httpinfo_id
	JOIN
        SearchIndex si ON hii.index_id = si.index_id
	WHERE
		si.page_url LIKE $1;
	`
	sqlParams = append(sqlParams, query)

	return sqlQuery, sqlParams, nil
}
