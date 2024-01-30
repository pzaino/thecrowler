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
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode"

	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

func tokenize(input string) []string {
	var tokens []string
	var currentToken strings.Builder

	inQuotes := false
	for _, r := range input {
		switch {
		case r == '"':
			inQuotes = toggleQuotes(inQuotes, &tokens, &currentToken)
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

func toggleQuotes(inQuotes bool, tokens *[]string, currentToken *strings.Builder) bool {
	inQuotes = !inQuotes
	if !inQuotes {
		*tokens = append(*tokens, currentToken.String())
		currentToken.Reset()
	}
	return inQuotes
}

func completeFieldSpecifier(tokens *[]string, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, currentToken.String()+":")
		currentToken.Reset()
	}
}

func handleSpace(tokens *[]string, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, currentToken.String())
		currentToken.Reset()
	}
}

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

func handleRemainingToken(tokens *[]string, currentToken *strings.Builder) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, currentToken.String())
	}
}

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
	if config.DebugLevel > 5 {
		log.Println("Field specifier:", field)
	}

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

func parseAdvancedQuery(input string) (string, []interface{}, error) {

	// Define default fields:
	defaultFields := getDefaultFields()

	// Tokenize the input string:
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

		case token == "&&", token == "||":
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
		keywordCondition := fmt.Sprintf("k.keyword = $%d", i)
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
	combinedQuery := buildCombinedQuery(queryParts)

	return combinedQuery, queryParams, nil
}

func buildCombinedQuery(queryParts [][]string) string {
	combinedQuery := `
	SELECT DISTINCT
		si.title, si.page_url, si.summary, si.content
	FROM
		SearchIndex si
	LEFT JOIN
		KeywordIndex ki ON si.index_id = ki.index_id
	LEFT JOIN
		Keywords k ON ki.keyword_id = k.keyword_id
	WHERE
		`

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
	combinedQuery += ";"

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
		log.Println(err)
		return SearchResult{}, err
	}
	defer db.Close()

	log.Println("Performing search for:", query)

	// Parse the user input
	sqlQuery, sqlParams, err := parseAdvancedQuery(query)
	if err != nil {
		// Handle error
		return SearchResult{}, err
	}
	if config.DebugLevel > 0 {
		log.Println("SQL query:", sqlQuery)
		log.Println("SQL params:", sqlParams)
	}

	// Execute the query
	rows, err := db.ExecuteQuery(sqlQuery, sqlParams...)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close()

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

	return results, nil
}
