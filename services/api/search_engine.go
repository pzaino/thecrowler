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
	"strconv"
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
	searchLabel         = "Performing search for: %s"
	noQueryProvided     = "no query provided"
	queryExecTime       = "Query execution time: %v"
	dataEncapTime       = "Data encapsulation execution time: %v"
)

// SearchQuery represents the result of a processed search query.
// (Please note: NOT the result of the search itself)
type SearchQuery struct {
	sqlQuery  string
	sqlParams []interface{}
	limit     int
	offset    int
	details   Details
}

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

	Or:

	SELECT *
	FROM NetInfo
	WHERE EXISTS (
		SELECT 1
		FROM jsonb_array_elements(details->'dns') AS dns_element
		WHERE dns_element->>'domain' LIKE '%example.com%'
	);
*/

// tokens is a slice of tokens
type tokens []token

// token represents a single token in the query
type token struct {
	tValue string
	tType  string
}

// Details represents the details of the query
type Details struct {
	Path  []string
	Value string
}

// tokenize splits the input string into tokens.
// following the "dorking" query language's rules.
func tokenize(input string) tokens {
	var tokens tokens
	var currentToken strings.Builder

	inQuotes := false
	inEscape := false
	specType := 0 // 0: none, 1: JSON specifier, 2: Dorcking specifier
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
		/*
			case r == '@' && !inQuotes && !inEscape:
				// beginning of a JSON field specifier
				handleRemainingToken(&tokens, &currentToken, specType)
				specType = 1
		*/
		case r == ':' && !inQuotes:
			completeFieldSpecifier(&tokens, &currentToken, specType)
			specType = 0
		case unicode.IsSpace(r) && !inQuotes:
			handleSpace(&tokens, &currentToken)
			specType = 0
		case (r == '|' || r == '&') && !inQuotes:
			handlePipeAnd(&tokens, &currentToken, r, specType)
			specType = 0
		default:
			currentToken.WriteRune(r)
		}
	}
	handleRemainingToken(&tokens, &currentToken, specType)

	return tokens
}

// toggleQuotes toggles the inQuotes flag and appends the current token to the tokens slice.
func toggleQuotes(inQuotes bool, tokens *tokens, currentToken *strings.Builder) bool {
	inQuotes = !inQuotes
	if !inQuotes {
		*tokens = append(*tokens, token{tValue: currentToken.String(), tType: "0"})
		currentToken.Reset()
	}
	return inQuotes
}

// completeFieldSpecifier appends the current token to the tokens slice and adds a colon.
func completeFieldSpecifier(tokens *tokens, currentToken *strings.Builder, specType int) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: currentToken.String() + ":", tType: strconv.Itoa(specType)})
		currentToken.Reset()
	}
}

// handleSpace appends the current token to the tokens slice.
func handleSpace(tokens *tokens, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: currentToken.String(), tType: "0"})
		currentToken.Reset()
	}
}

// handlePipeAnd appends the current token to the tokens slice and adds the pipe (|) or and (&) operator.
func handlePipeAnd(tokens *tokens, currentToken *strings.Builder, r rune, specType int) {
	if currentToken.Len() > 0 {
		if currentToken.String() != "|" && currentToken.String() != "&" {
			*tokens = append(*tokens, token{tValue: currentToken.String(), tType: "0"})
			currentToken.Reset()
		} else {
			*tokens = append(*tokens, token{tValue: currentToken.String() + string(r), tType: strconv.Itoa(specType)})
			currentToken.Reset()
			return
		}
	}
	currentToken.WriteRune(r)
}

// handleRemainingToken appends the current token to the tokens slice.
func handleRemainingToken(tokens *tokens, currentToken *strings.Builder, specType int) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: currentToken.String(), tType: strconv.Itoa(specType)})
	}
}

// PrepareInput removes leading and trailing spaces from the input string.
func isFieldSpecifier(input string) bool {
	if strings.TrimSpace(input) == "" {
		return false
	}

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
	if field == "" {
		return false
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Field specifier: '%s'", field)

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
func parseAdvancedQuery(queryBody string, input string, parsingType string) (SearchQuery, error) {
	defaultFields := getDefaultFields()
	tokens := tokenize(input)
	var SQLQuery SearchQuery
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Query Body: %v", input)

	// Parse the tokens and generate the query parts
	// and query params:
	var queryParts [][]string
	var queryParams []interface{}
	paramCounter := 1
	var currentField string
	queryGroup := -1 // Initialize to -1, so the first group starts at index 0
	limit := 10      // Default limit
	offset := 0      // Default offset
	skipNextToken := false
	var details Details
	for i, token := range tokens {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Fetched token: %s", token)
		if skipNextToken {
			skipNextToken = false
			continue
		}
		switch {
		case token.tValue == "&limit:":
			// Check if the next token is a number
			if len(tokens) > i+1 {
				var err error
				limit, err = strconv.Atoi(tokens[i+1].tValue)
				if err != nil {
					return SearchQuery{}, errors.New("invalid limit value")
				}
			}
			skipNextToken = true
			continue

		case token.tValue == "&offset:":
			// Check if the next token is a number
			if len(tokens) > i+1 {
				var err error
				offset, err = strconv.Atoi(tokens[i+1].tValue)
				if err != nil {
					return SearchQuery{}, errors.New("invalid offset value")
				}
			}
			skipNextToken = true
			continue

		case strings.HasPrefix(token.tValue, "&details.") || token.tValue == "&details:":
			// Check if the next token is a number
			if len(tokens) > i+1 {
				details.Value = tokens[i+1].tValue
			}
			// Extract the path
			path := strings.Split(token.tValue, ".")
			details.Path = path[0:]
			skipNextToken = true
			continue

		case isFieldSpecifier(token.tValue):
			currentField = strings.TrimSuffix(token.tValue, ":")
			queryGroup++                                // Move to the next group
			queryParts = append(queryParts, []string{}) // Initialize the new group

		case token.tValue == "&", token.tValue == "|", token.tValue == "&&", token.tValue == "||":
			if queryGroup == -1 {
				queryGroup = 0
				queryParts = append(queryParts, []string{})
			}
			if token.tValue == "&&" {
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
				queryParams = append(queryParams, "%"+strings.ToLower(token.tValue)+"%")
				paramCounter++
			}

			if isQuotedString(token.tValue) {
				token.tValue = strings.Trim(token.tValue, `"`)
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

			if len(tokens) > i+1 && !isFieldSpecifier(tokens[i+1].tValue) && tokens[i+1].tValue != "&&" && tokens[i+1].tValue != "||" {
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
		return SearchQuery{}, errors.New("no valid query provided")
	}

	// Build the combined query
	var combinedQuery string
	if parsingType == "" {
		combinedQuery = buildCombinedQuery(queryBody, queryParts)
	} else if parsingType == "self-contained" {
		combinedQuery = queryBody
	}

	// Add the limit and offset to the list of parameters:
	queryParams = append(queryParams, limit, offset)

	SQLQuery.sqlQuery = combinedQuery
	SQLQuery.sqlParams = queryParams
	SQLQuery.limit = limit
	SQLQuery.offset = offset
	SQLQuery.details = details

	return SQLQuery, nil
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

func performSearch(query string, db *cdb.Handler) (SearchResult, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug, searchLabel, query)

	// Prepare the query body
	var queryBody string
	if config.API.ReturnContent {
		queryBody = `
		SELECT DISTINCT
			si.title, si.page_url, si.summary, wo.object_content AS content
		FROM
			SearchIndex si
		LEFT JOIN
			WebObjectsIndex woi ON si.index_id = woi.index_id
		LEFT JOIN
			WebObjects wo ON woi.object_id = wo.object_id
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
	SQLQuery, err := parseAdvancedQuery(queryBody, query, "")
	sqlQuery := SQLQuery.sqlQuery
	sqlParams := SQLQuery.sqlParams
	if err != nil {
		return SearchResult{}, err
	}

	sqlQuery = sqlQuery + ";"

	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take the current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := (*db).ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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

	results.Queries.Limit = SQLQuery.limit
	results.Queries.Offset = SQLQuery.offset
	return results, nil
}

func performScreenshotSearch(query string, qType int, db *cdb.Handler) (ScreenshotResponse, error) {
	var err error

	cmn.DebugMsg(cmn.DbgLvlDebug, searchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	var SQLQuery SearchQuery
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		SQLQuery, err := parseScreenshotGetQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return ScreenshotResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		SQLQuery, err = parseScreenshotQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return ScreenshotResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := (*db).ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return ScreenshotResponse{}, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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

	results.Limit = SQLQuery.limit
	results.Offset = SQLQuery.offset

	return results, nil
}

func parseScreenshotGetQuery(input string) (SearchQuery, error) {
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
	SQLQuery, err := parseAdvancedQuery(queryBody, input, "")
	sqlQuery := SQLQuery.sqlQuery
	sqlParams := SQLQuery.sqlParams
	if err != nil {
		return SearchQuery{}, err
	}

	sqlQuery = sqlQuery + " ORDER BY s.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	SQLQuery.sqlQuery = sqlQuery
	SQLQuery.sqlParams = sqlParams

	return SQLQuery, nil
}

func parseScreenshotQuery(input string) (SearchQuery, error) {
	var query string
	var err error
	var sqlParams []interface{}

	input = PrepareInput(input)

	// Unmarshal the JSON document
	var req ScreenshotRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling JSON: %v, %v", err, input)
		return SearchQuery{}, err
	}

	// Extract the query from the request
	if len(req.URL) > 0 {
		query = "%" + req.URL + "%"
	} else {
		return SearchQuery{}, errors.New(noQueryProvided)
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

	sqlQuery = sqlQuery + " ORDER BY s.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	return SearchQuery{sqlQuery, sqlParams, 10, 0, Details{}}, nil
}

func performWebObjectSearch(query string, qType int, db *cdb.Handler) (WebObjectResponse, error) {
	var err error
	cmn.DebugMsg(cmn.DbgLvlDebug, searchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	var SQLQuery SearchQuery
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		SQLQuery, err = parseWebObjectGetQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return WebObjectResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		SQLQuery, err = parseWebObjectQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return WebObjectResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := (*db).ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return WebObjectResponse{}, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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

	results.Queries.Limit = SQLQuery.limit
	results.Queries.Offset = SQLQuery.offset

	return results, nil
}

func parseWebObjectGetQuery(input string) (SearchQuery, error) {
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
		WebObjectsIndex AS wi ON wo.object_id = wi.object_id
	JOIN
		SearchIndex AS si ON wi.index_id = si.index_id
	LEFT JOIN
		KeywordIndex ki ON si.index_id = ki.index_id
	LEFT JOIN
		Keywords k ON ki.keyword_id = k.keyword_id
	WHERE
		wo.object_link != ''
		AND wo.object_link IS NOT NULL
		AND `

	SQLQuery, err := parseAdvancedQuery(queryBody, input, "")
	sqlQuery := SQLQuery.sqlQuery
	sqlParams := SQLQuery.sqlParams
	if err != nil {
		return SQLQuery, err
	}

	sqlQuery = sqlQuery + " ORDER BY wo.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	SQLQuery.sqlQuery = sqlQuery
	SQLQuery.sqlParams = sqlParams

	return SQLQuery, nil
}

func parseWebObjectQuery(input string) (SearchQuery, error) {
	var query string
	var err error
	var sqlParams []interface{}

	input = PrepareInput(input)

	// Unmarshal the JSON document
	var req WebObjectRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling JSON: %v, %v", err, input)
		return SearchQuery{}, err
	}

	// Extract the query from the request
	if len(req.URL) > 0 {
		query = "%" + req.URL + "%"
	} else {
		return SearchQuery{}, errors.New(noQueryProvided)
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
        WebObjectsIndex AS woi ON wo.object_id = woi.object_id
    JOIN
        SearchIndex AS si ON woi.index_id = si.index_id
	WHERE
		LOWER(si.page_url) LIKE LOWER($1)
	AND
		wo.object_link != '' AND wo.object_link IS NOT NULL;
	`
	sqlParams = append(sqlParams, query)

	sqlQuery = sqlQuery + " ORDER BY wo.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	return SearchQuery{sqlQuery, sqlParams, 10, 0, Details{}}, nil
}

func performCorrelatedSitesSearch(query string, qType int, db *cdb.Handler) (CorrelatedSitesResponse, error) {
	var err error
	cmn.DebugMsg(cmn.DbgLvlDebug, searchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	var SQLQuery SearchQuery
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		SQLQuery, err = parseCorrelatedSitesGetQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return CorrelatedSitesResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		SQLQuery, err = parseCorrelatedSitesQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return CorrelatedSitesResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := (*db).ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return CorrelatedSitesResponse{}, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Calculate the query execution time
	elapsed := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, queryExecTime, elapsed)

	// Take current timer (to monitor encapsulation performance)
	start = time.Now()

	var results CorrelatedSitesResponse
	for rows.Next() {
		var row CorrelatedSitesRow
		var detailsJSON1 []byte // Use a byte slice to hold the JSONB column data
		var detailsJSON2 []byte
		var createdAt string

		// Adjust Scan to match the expected columns returned by your query
		if err := rows.Scan(&row.SourceID, &row.URL, &createdAt, &detailsJSON1, &detailsJSON2); err != nil {
			return CorrelatedSitesResponse{}, err
		}

		// Unmarshal the JSON data (if not NULL)
		if detailsJSON1 != nil {
			if err := json.Unmarshal(detailsJSON1, &row.WHOIS); err != nil {
				return CorrelatedSitesResponse{}, err // Handle JSON unmarshal error
			}
		}
		if detailsJSON2 != nil {
			if err := json.Unmarshal(detailsJSON2, &row.SSLInfo); err != nil {
				return CorrelatedSitesResponse{}, err // Handle JSON unmarshal error
			}
		}

		// Append the row to the results
		results.Items = append(results.Items, row)
	}

	// Ensure to check rows.Err() after the loop to catch any error that occurred during iteration.
	if err := rows.Err(); err != nil {
		return CorrelatedSitesResponse{}, err
	}

	// Calculate the query execution time
	elapsed = time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug1, dataEncapTime, elapsed)

	results.Queries.Limit = SQLQuery.limit
	results.Queries.Offset = SQLQuery.offset

	return results, nil
}

func parseCorrelatedSitesGetQuery(input string) (SearchQuery, error) {
	// Prepare the query body
	queryBody := `
	WITH PartnerSources AS (
		SELECT * FROM find_correlated_sources_by_domain($1)
	),
	WhoisAndSSLInfo AS (
		SELECT
			ps.source_id,
			ps.url,
			ni.created_at,
			ni.details->'whois' AS whois_info,
			hi.details->'ssl_info' AS ssl_info
		FROM
			PartnerSources ps
		JOIN
			SourceSearchIndex ssi ON ps.source_id = ssi.source_id
		LEFT JOIN
			NetInfoIndex nii ON ssi.index_id = nii.index_id
		LEFT JOIN
			NetInfo ni ON nii.netinfo_id = ni.netinfo_id
		LEFT JOIN
			HTTPInfoIndex hii ON ssi.index_id = hii.index_id
		LEFT JOIN
			HTTPInfo hi ON hii.httpinfo_id = hi.httpinfo_id
		WHERE
			(ni.details->'whois' IS NOT NULL OR hi.details->'ssl_info' IS NOT NULL)
	)
	SELECT DISTINCT
		source_id,
		url,
		created_at,
		whois_info,
		ssl_info
	FROM
		WhoisAndSSLInfo
	`

	SQLQuery, err := parseAdvancedQuery(queryBody, input, "self-contained")
	sqlQuery := SQLQuery.sqlQuery
	sqlParams := SQLQuery.sqlParams
	if err != nil {
		return SearchQuery{}, err
	}

	sqlQuery = sqlQuery + "ORDER BY created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	SQLQuery.sqlQuery = sqlQuery
	SQLQuery.sqlParams = sqlParams

	return SQLQuery, nil
}

func parseCorrelatedSitesQuery(input string) (SearchQuery, error) {
	var query string
	var err error
	var sqlParams []interface{}

	// Unmarshal the JSON document
	var req CorrelatedSitesRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		return SearchQuery{}, err
	}

	// Extract the query from the request
	if len(req.URL) > 0 {
		query = req.URL
	} else {
		return SearchQuery{}, errors.New(noQueryProvided)
	}

	// Parse the user input
	sqlQuery := `
	WITH PartnerSources AS (
		SELECT * FROM find_correlated_sources_by_domain($1)
	),
	WhoisAndSSLInfo AS (
		SELECT
			ps.source_id,
			ps.url,
			ni.details->'whois' AS whois_info,
			hi.details->'ssl_info' AS ssl_info
		FROM
			PartnerSources ps
		JOIN
			SourceSearchIndex ssi ON ps.source_id = ssi.source_id
		LEFT JOIN
			NetInfoIndex nii ON ssi.index_id = nii.index_id
		LEFT JOIN
			NetInfo ni ON nii.netinfo_id = ni.netinfo_id
		LEFT JOIN
			HTTPInfoIndex hii ON ssi.index_id = hii.index_id
		LEFT JOIN
			HTTPInfo hi ON hii.httpinfo_id = hi.httpinfo_id
		WHERE
			(ni.details->'whois' IS NOT NULL OR hi.details->'ssl_info' IS NOT NULL)
	)
	SELECT DISTINCT
		source_id,
		url,
		created_at,
		whois_info,
		ssl_info
	FROM
		WhoisAndSSLInfo;
	`
	sqlParams = append(sqlParams, query)

	sqlQuery = sqlQuery + " ORDER BY created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	return SearchQuery{sqlQuery, sqlParams, 10, 0, Details{}}, nil
}

// performNetInfoSearch performs a search for network information.
func performNetInfoSearch(query string, qType int, db *cdb.Handler) (NetInfoResponse, error) {
	var err error
	cmn.DebugMsg(cmn.DbgLvlDebug, searchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	var SQLQuery SearchQuery
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		SQLQuery, err = parseNetInfoGetQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return NetInfoResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		SQLQuery, err = parseNetInfoQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return NetInfoResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := (*db).ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return NetInfoResponse{}, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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

		// log the detailsJSON for debugging purposes
		//cmn.DebugMsg(cmn.DbgLvlDebug5, "Details JSON: %s", detailsJSON)

		// Unmarshal the JSON data (if not NULL)
		if detailsJSON != nil {
			if err := json.Unmarshal(detailsJSON, &row.Details); err != nil {
				return NetInfoResponse{}, err // Handle JSON unmarshal error
			}
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

	results.Queries.Limit = SQLQuery.limit
	results.Queries.Offset = SQLQuery.offset

	return results, nil
}

func parseNetInfoGetQuery(input string) (SearchQuery, error) {
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
	SQLQuery, err := parseAdvancedQuery(queryBody, input, "")
	sqlQuery := SQLQuery.sqlQuery
	sqlParams := SQLQuery.sqlParams
	if err != nil {
		return SQLQuery, err
	}

	sqlQuery = sqlQuery + " ORDER BY ni.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	SQLQuery.sqlQuery = sqlQuery
	SQLQuery.sqlParams = sqlParams

	return SQLQuery, nil
}

func parseNetInfoQuery(input string) (SearchQuery, error) {
	var query string
	var err error
	var sqlParams []interface{}

	// Unmarshal the JSON document
	var req QueryRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		return SearchQuery{}, err
	}

	// Extract the query from the request
	if len(req.Title) > 0 {
		query = req.Title
	} else {
		return SearchQuery{}, errors.New(noQueryProvided)
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

	sqlQuery = sqlQuery + " ORDER BY ni.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	return SearchQuery{sqlQuery, sqlParams, 10, 0, Details{}}, nil
}

// performNetInfoSearch performs a search for network information.
func performHTTPInfoSearch(query string, qType int, db *cdb.Handler) (HTTPInfoResponse, error) {
	var err error
	cmn.DebugMsg(cmn.DbgLvlDebug, searchLabel, query)

	// Parse the user input
	var sqlQuery string
	var sqlParams []interface{}
	var SQLQuery SearchQuery
	if qType == getQuery {
		// it's a GET request, so we need to interpret the q parameter
		SQLQuery, err = parseHTTPInfoGetQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return HTTPInfoResponse{}, err
		}
	} else {
		// It's a POST request, so we can use the standard JSON parsing
		SQLQuery, err = parseHTTPInfoPostQuery(query)
		sqlQuery = SQLQuery.sqlQuery
		sqlParams = SQLQuery.sqlParams
		if err != nil {
			return HTTPInfoResponse{}, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryLabel, sqlQuery)
	cmn.DebugMsg(cmn.DbgLvlDebug1, sqlQueryParamsLabel, sqlParams)

	// Take current timer (to monitor query performance)
	start := time.Now()

	// Execute the query
	rows, err := (*db).ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return HTTPInfoResponse{}, err
	}
	defer rows.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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

		// Unmarshal the JSON data (if not NULL)
		if detailsJSON != nil {
			if err := json.Unmarshal(detailsJSON, &row.Details); err != nil {
				return HTTPInfoResponse{}, err // Handle JSON unmarshal error
			}
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

	results.Queries.Limit = SQLQuery.limit
	results.Queries.Offset = SQLQuery.offset

	return results, nil
}

func parseHTTPInfoGetQuery(input string) (SearchQuery, error) {
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
	SQLQuery, err := parseAdvancedQuery(queryBody, input, "")
	sqlQuery := SQLQuery.sqlQuery
	sqlParams := SQLQuery.sqlParams
	if err != nil {
		return SearchQuery{}, err
	}
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " ORDER BY hi.created_at DESC LIMIT "
	sqlQuery = sqlQuery + "$" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	SQLQuery.sqlQuery = sqlQuery
	SQLQuery.sqlParams = sqlParams

	return SQLQuery, nil
}

func parseHTTPInfoPostQuery(input string) (SearchQuery, error) {
	var query string
	var err error
	var sqlParams []interface{}

	// Unmarshal the JSON document
	var req QueryRequest
	err = json.Unmarshal([]byte(input), &req)
	if err != nil {
		return SearchQuery{}, err
	}

	// Extract the query from the request
	if len(req.Title) > 0 {
		query = req.Title
	} else {
		return SearchQuery{}, errors.New(noQueryProvided)
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

	sqlQuery = sqlQuery + " ORDER BY hi.created_at DESC"
	limit := len(sqlParams) - 1
	offset := len(sqlParams)
	sqlQuery = sqlQuery + " LIMIT $" + strconv.Itoa(limit) + " OFFSET $" + strconv.Itoa(offset) + ";"

	return SearchQuery{sqlQuery, sqlParams, 10, 0, Details{}}, nil
}
