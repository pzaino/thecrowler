package search

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// ParsedQuery represents the result of a processed search query.
// (Please note: NOT the result of the search itself)
type ParsedQuery struct {
	sqlQuery  string
	sqlParams []interface{}
	limit     int
	offset    int
	details   Details
}

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
	specType := 0          // 0: none, 1: JSON specifier, 2: Dorcking specifier
	runes := []rune(input) // Convert input to runes for indexed access
	for i := 0; i < len(runes); i++ {
		r := runes[i]

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
		case (r == '@') && (!inQuotes && !inEscape):
			// beginning of a JSON field specifier
			handleRemainingToken(&tokens, &currentToken, specType)
			specType = 1 // JSON field specifier
			currentToken.WriteRune(r)
		case (r == ':' || r == '=') && (!inQuotes && !inEscape):
			if isValidSpecifier(currentToken.String()) {
				// end of a Dorcking field specifier
				completeFieldSpecifier(&tokens, &currentToken, specType)
			} else {
				currentToken.WriteRune(r)
			}
			specType = 0
		case unicode.IsSpace(r) && !inQuotes:
			handleSpace(&tokens, &currentToken, specType)
			specType = 0
		case r == '|' && (!inQuotes && !inEscape):
			handlePipeAnd(&tokens, &currentToken, r, specType, runes, &i)
			specType = 0
		case r == '&' && (!inQuotes && !inEscape):
			// Peek next rune to decide whether this is logic AND or control specifier
			next := rune(0)
			if i+1 < len(runes) {
				next = runes[i+1]
			}

			// Handle "&&" as logical AND
			if next == '&' {
				handlePipeAnd(&tokens, &currentToken, r, specType, runes, &i)
				specType = 0
				continue
			}

			// Handle standalone "&" surrounded by spaces (logic AND)
			if (i > 0 && unicode.IsSpace(runes[i-1])) && (i+1 < len(runes) && unicode.IsSpace(next)) {
				handlePipeAnd(&tokens, &currentToken, r, specType, runes, &i)
				specType = 0
				continue
			}

			// Otherwise, treat as control specifier (e.g. &limit:10)
			handleRemainingToken(&tokens, &currentToken, specType)
			currentToken.Reset()
			currentToken.WriteRune(r)

			// Capture modifier until next space or operator
			if i+1 < len(runes) {
				j := i + 1
				for ; j < len(runes); j++ {
					if unicode.IsSpace(runes[j]) || runes[j] == '&' || runes[j] == '|' {
						break
					}
					currentToken.WriteRune(runes[j])
				}
				tokens = append(tokens, token{tValue: currentToken.String(), tType: strconv.Itoa(specType)})
				currentToken.Reset()
				i = j - 1
				specType = 0
				continue
			}
		case (r == ';' || r == '+') && (!inQuotes && !inEscape):
			handleLogicalAnd(&tokens, &currentToken, specType)
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

// handleLogicalAnd appends the current token to the tokens slice and adds the logical AND (";") operator.
func handleLogicalAnd(tokens *tokens, currentToken *strings.Builder, specType int) {
	var tok string
	if currentToken.String() == "+" || currentToken.String() == ";" {
		tok = "&"
	} else {
		tok = currentToken.String()
	}
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: tok, tType: "0"})
		currentToken.Reset()
	}
	*tokens = append(*tokens, token{tValue: ";", tType: strconv.Itoa(specType)})
}

// completeFieldSpecifier appends the current token to the tokens slice and adds a colon.
func completeFieldSpecifier(tokens *tokens, currentToken *strings.Builder, _ int) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: currentToken.String() + ":", tType: "2"})
		currentToken.Reset()
	}
}

func isValidSpecifier(spec string) bool {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Checking specifier: '%s'", spec)
	if strings.HasPrefix(spec, "@") {
		return true
	}
	//nolint:goconst
	return spec == "title" || spec == "summary" || spec == "content" || spec == "details" || spec == "&details" || spec == "offset" || spec == "&offset" || spec == "limit" || spec == "&limit" || spec == "file_type"
}

// handleSpace appends the current token to the tokens slice.
func handleSpace(tokens *tokens, currentToken *strings.Builder, specType int) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: currentToken.String(), tType: strconv.Itoa(specType)})
		currentToken.Reset()
	}
}

// handlePipeAnd appends the current token to the tokens slice and adds the logical OR (||) or AND (&&) operator if detected.
func handlePipeAnd(tokens *tokens, currentToken *strings.Builder, r rune, specType int, input []rune, i *int) {
	// Check if the next character is the same, to detect || or &&
	if *i+1 < len(input) && input[*i+1] == r {
		// Handle || or &&
		if currentToken.Len() > 0 {
			*tokens = append(*tokens, token{tValue: currentToken.String(), tType: "0"})
			currentToken.Reset()
		}
		currentToken.WriteRune(r)
		currentToken.WriteRune(r)
		*tokens = append(*tokens, token{tValue: currentToken.String(), tType: strconv.Itoa(specType)})
		currentToken.Reset()
		*i++ // Skip the next character as we've processed it
	} else {
		// Treat single | or & as part of the current token
		currentToken.WriteRune(r)
	}
}

// handleRemainingToken appends the current token to the tokens slice.
func handleRemainingToken(tokens *tokens, currentToken *strings.Builder, specType int) {
	if currentToken.Len() > 0 {
		*tokens = append(*tokens, token{tValue: currentToken.String(), tType: strconv.Itoa(specType)})
	}
}

// PrepareInput removes leading and trailing spaces from the input string.
func (s *Searcher) isFieldSpecifier(input string) bool {
	if strings.TrimSpace(input) == "" {
		return false
	}

	// Define allowed fields
	// TODO: I need to create a single source of truth for the allowed fields
	var allowedFields = map[string]bool{
		"title":   true,
		"summary": true,
		"content": s.Config.API.ContentSearch,
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

/*
func isQuotedString(input string) bool {
	matched, err := regexp.MatchString(`^".*"$`, input)
	if err != nil {
		return false
	}
	return matched
}
*/

func (s *Searcher) getDefaultFields() []string {
	var defaultFields []string
	if s.Config.API.ContentSearch {
		defaultFields = []string{"page_url", "title", "summary", "content"}
	} else {
		defaultFields = []string{"page_url", "title", "summary"}
	}
	return defaultFields
}

// parseAdvancedQuery interpret the "dorcking" query language and returns the SQL query and its parameters.
// queryBody represent the SQL query body, while input is the "raw" dorking input.
func (s *Searcher) parseAdvancedQuery(queryBody string, input string, parsingType string) (ParsedQuery, error) {
	defaultFields := s.getDefaultFields()
	tokensData := tokenize(input)
	var SQLQuery ParsedQuery
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Query Body: %v", input)

	// Parse the tokens and generate the query parts and query params:
	var queryParts [][]string
	var queryParams []interface{}
	var generalParamCounter = 1 // For general fields like example
	const jObjAccOp = "->"
	const jObjTxtAccOp = "->>"
	var currentField string
	queryGroup := -1
	limit := 10
	offset := 0
	skipNextToken := false
	var details Details
	isJSONField := false // Track whether we are handling a JSON field

	for i := 0; i < len(tokensData); i++ {
		tokenData := &tokensData[i]
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Fetched token: %s, n: %d", tokenData.tValue, i)
		if skipNextToken {
			skipNextToken = false
			continue
		}

		// Normalize combined tokens like "term&limit:20"
		if strings.Contains(tokenData.tValue, "&limit:") || strings.Contains(tokenData.tValue, "&offset=") {
			var parts []string
			if strings.Contains(tokenData.tValue, "&limit=") {
				parts = strings.SplitN(tokenData.tValue, "&limit=", 2)
			} else {
				parts = strings.SplitN(tokenData.tValue, "&limit:", 2)
			}
			tokenData.tValue = parts[0] // update the real slice element
			if len(parts) == 2 {
				if n, err := strconv.Atoi(parts[1]); err == nil {
					limit = n
				}
			}
		}

		if strings.Contains(tokenData.tValue, "&offset:") || strings.Contains(tokenData.tValue, "&offset=") {
			var parts []string
			if strings.Contains(tokenData.tValue, "&offset=") {
				parts = strings.SplitN(tokenData.tValue, "&offset=", 2)
			} else {
				parts = strings.SplitN(tokenData.tValue, "&offset:", 2)
			}
			tokenData.tValue = parts[0]
			if len(parts) == 2 {
				if n, err := strconv.Atoi(parts[1]); err == nil {
					offset = n
				}
			}
		}

		// Split numeric+specifier combos like "3&limit:5"
		if strings.Contains(tokenData.tValue, "&") && !strings.HasPrefix(tokenData.tValue, "&") {
			parts := strings.SplitN(tokenData.tValue, "&", 2)
			tokenData.tValue = parts[0]
			if len(parts) > 1 {
				newToken := token{tValue: "&" + parts[1]}
				// Safe insert: extend slice with the new token
				tokensData = append(tokensData[:i+1], append([]token{newToken}, tokensData[i+1:]...)...)
				// Re-run loop for the new token
				continue
			}
		}

		// Process the token
		switch {
		case tokenData.tValue == "":
			// Skip empty tokens
			continue

		case tokenData.tValue == ";":
			// Move to the next group
			queryGroup++
			queryParts = append(queryParts, []string{})

		case strings.HasPrefix(tokenData.tValue, "@"):
			// Handling the JSON field (@test_field:)
			isJSONField = true
			jsonPath := strings.TrimSuffix(strings.TrimPrefix(tokenData.tValue, "@"), ":")
			jsonPath = strings.Trim(jsonPath, " ")

			// Split the JSON path into components
			// Split the JSON path into components, handling both '->' and '->>'
			pathComponents := []string{}
			currentPath := jsonPath

			// Split the path into parts and identify the operators
			for strings.Contains(currentPath, jObjTxtAccOp) || strings.Contains(currentPath, jObjAccOp) {
				if strings.Contains(currentPath, jObjTxtAccOp) {
					// Split by '->>' operator
					parts := strings.SplitN(currentPath, jObjTxtAccOp, 2)
					pathComponents = append(pathComponents, parts[0])
					pathComponents = append(pathComponents, jObjTxtAccOp)
					currentPath = parts[1]
				} else {
					// Split by '->' operator
					parts := strings.SplitN(currentPath, jObjAccOp, 2)
					pathComponents = append(pathComponents, parts[0])
					pathComponents = append(pathComponents, jObjAccOp)
					currentPath = parts[1]
				}
			}
			// Add the last part of the path
			if currentPath != "" {
				pathComponents = append(pathComponents, currentPath)
			}

			// Build the JSON path query (e.g., details->'json_field'->>'nested_field')
			jsonFieldQuery := "details"
			if len(pathComponents) > 1 {
				jsonFieldQuery += jObjAccOp
			} else {
				jsonFieldQuery += jObjTxtAccOp
			}
			// Iterate over components and place operators and fields correctly
			for i := 0; i < len(pathComponents); i++ {
				component := pathComponents[i]

				// Append the operators directly, outside of quotes
				if component == jObjAccOp || component == "->>" {
					jsonFieldQuery += component
				} else {
					// Append field names inside quotes
					jsonFieldQuery += fmt.Sprintf("'%s'", component)
				}
			}

			currentField = jsonFieldQuery // Set the JSON field as the current field

		case tokenData.tValue == "&offset:":
			if len(tokensData) > i+1 {
				nextVal := tokensData[i+1].tValue
				if strings.Contains(nextVal, "&") {
					parts := strings.SplitN(nextVal, "&", 2)
					if len(parts) > 0 {
						if n, err := strconv.Atoi(parts[0]); err == nil {
							offset = n
						}
					}
					if len(parts) == 2 {
						tokensData[i+1].tValue = "&" + parts[1]
					} else {
						tokensData[i+1].tValue = ""
					}
					skipNextToken = false
				} else {
					if n, err := strconv.Atoi(nextVal); err == nil {
						offset = n
					}
					tokensData[i+1].tValue = ""
					skipNextToken = true
				}
			}
			continue

		case tokenData.tValue == "&limit:":
			if len(tokensData) > i+1 {
				nextVal := tokensData[i+1].tValue
				if strings.Contains(nextVal, "&") {
					parts := strings.SplitN(nextVal, "&", 2)
					if len(parts) > 0 {
						if n, err := strconv.Atoi(parts[0]); err == nil {
							limit = n
						}
					}
					if len(parts) == 2 {
						tokensData[i+1].tValue = "&" + parts[1]
					} else {
						tokensData[i+1].tValue = ""
					}
					skipNextToken = false
				} else {
					if n, err := strconv.Atoi(nextVal); err == nil {
						limit = n
					}
					tokensData[i+1].tValue = ""
					skipNextToken = true
				}
			}
			continue

		case strings.HasPrefix(tokenData.tValue, "&details.") || tokenData.tValue == "&details:":
			// Check if the next token is a number
			if len(tokensData) > i+1 {
				details.Value = tokensData[i+1].tValue
			}
			// Extract the path
			path := strings.Split(tokenData.tValue, ".")
			details.Path = path[0:]
			skipNextToken = true
			continue

		case s.isFieldSpecifier(tokenData.tValue):
			// Handle general fields (e.g., title:)
			currentField = strings.TrimSuffix(tokenData.tValue, ":")
			queryGroup++
			queryParts = append(queryParts, []string{}) // Initialize the new group
			isJSONField = false                         // Reset the JSON field flag

		case tokenData.tValue == "&", tokenData.tValue == "|", tokenData.tValue == "&&", tokenData.tValue == "||":
			// Handle logical operators AND/OR
			if queryGroup == -1 {
				queryGroup = 0
				queryParts = append(queryParts, []string{})
			}
			if tokenData.tValue == "&&" {
				queryParts[queryGroup] = append(queryParts[queryGroup], "AND")
			} else {
				queryParts[queryGroup] = append(queryParts[queryGroup], "OR")

			}

		default:
			// Handle field values (either for general fields or JSON fields)
			addCondition := func(condition string) {
				if queryGroup == -1 {
					queryGroup = 0
					queryParts = append(queryParts, []string{condition})
				} else {
					queryParts[queryGroup] = append(queryParts[queryGroup], condition)
				}
			}

			if isJSONField {
				// Handle JSON field value (use a separate jsonParamCounter)
				condition := fmt.Sprintf("%s LIKE $%d", currentField, generalParamCounter)
				addCondition(condition)
				queryParams = append(queryParams, "%"+tokenData.tValue+"%")
				generalParamCounter++ // Increase the JSON parameter counter
			} else {
				// Handle non-JSON field value
				var conditions []string
				for _, field := range defaultFields {
					condition := fmt.Sprintf("LOWER(%s) LIKE $%d", field, generalParamCounter)
					conditions = append(conditions, condition)
				}
				combinedCondition := "(" + strings.Join(conditions, " OR ") + ")"
				addCondition(combinedCondition)
				queryParams = append(queryParams, "%"+strings.ToLower(tokenData.tValue)+"%")
				generalParamCounter++ // Increase the parameter counter for general fields
			}
		}
	}

	// Add a separate group for keyword conditions (only use the first parameter set for keywords)
	var keywordConditions []string
	for i := 1; i < generalParamCounter; i++ {
		keywordCondition := fmt.Sprintf("k.keyword LIKE $%d", i)
		keywordConditions = append(keywordConditions, keywordCondition)
	}

	// Append the keyword group at the end
	if len(keywordConditions) > 0 {
		keywordGroup := "(" + strings.Join(keywordConditions, " OR ") + ")"
		queryParts = append(queryParts, []string{keywordGroup})
	}
	if len(queryParts) == 0 {
		return ParsedQuery{}, errors.New("no valid query provided")
	}

	// Build the combined query
	var combinedQuery string
	switch strings.ToLower(strings.TrimSpace(parsingType)) {
	case "self-contained":
		combinedQuery = queryBody
	case "combined":
		combinedQuery = buildCombinedQuery(queryBody, queryParts)
	default:
		// Default to "combined" behavior
		combinedQuery = buildCombinedQuery(queryBody, queryParts)
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
		if len(group) == 0 {
			continue
		}

		if i > 0 {
			// Use 'OR' before the keyword group
			if i == len(queryParts)-1 {
				combinedQuery += " OR "
			} else {
				combinedQuery += " AND "
			}
		}
		// Clean up possible AND or OR at the end of the group
		if isLogicalOperator(group[len(group)-1]) {
			group = group[:len(group)-1]
		}
		combinedQuery += "(" + strings.Join(group, " ") + ")"
	}
	return combinedQuery
}

// Helper function to determine if a string is a logical operator
func isLogicalOperator(op string) bool {
	return op == "AND" || op == "OR"
}
