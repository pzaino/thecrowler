package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

func handleRequestWithDB(w http.ResponseWriter, r *http.Request, successCode int, action func(string, int, *cdb.Handler) (interface{}, error)) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Invalid query", http.StatusBadRequest, successCode)
			return
		}

		results, err := action(query, getQTypeFromName(r.Method), &dbHandler)
		handleErrorAndRespond(w, err, results, "Error performing action: %v", http.StatusInternalServerError, successCode)

	case <-time.After(5 * time.Second):
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// handleErrorAndRespond encapsulates common error handling and JSON response logic.
func handleErrorAndRespond(w http.ResponseWriter, err error, results interface{}, errMsg string, errCode int, successCode int) {
	var response interface{}

	if successCode == 0 {
		successCode = http.StatusOK
	} else if successCode == 204 {
		successCode = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		// Log the error and prepare an error response
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		response = map[string]interface{}{
			"error":   err.Error(),
			"message": errMsg,
		}
		w.WriteHeader(errCode) // Send the error code
	} else {
		// Prepare a success response
		if resp, ok := results.(ConsoleResponse); ok {
			if resp.Message == "" {
				resp.Message = "Success"
			}
			response = resp
		} else {
			response = results
		}
		w.WriteHeader(successCode) // Send the success code
	}

	// Encode the response as JSON (always include a body)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log the error and send a fallback error response
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error encoding JSON response: %v", err)
		cmn.DebugMsg(cmn.DbgLvlDebug4, "Original Results: %+v", results)

		fallbackResponse := map[string]string{"error": "Internal Server Error"}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(fallbackResponse)
	}
}

// extractQueryOrBody extracts the query parameter for GET requests or the body for POST requests.
func extractQueryOrBody(r *http.Request) (string, error) {
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close() // nolint:errcheck
		if err != nil {
			return "", err
		}
		return string(body), nil
	}

	// Handle GET requests
	query := r.URL.Query().Get("q")
	if query == "" {
		return "", fmt.Errorf("query parameter 'q' is required")
	}

	// Decode in case 'q' itself contains encoded data
	if decodedQuery, err := url.QueryUnescape(query); err == nil {
		query = decodedQuery
	}

	params := []string{}

	// Validate and append 'offset'
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil {
			return "", fmt.Errorf("invalid offset value: must be an integer")
		}
		if offset < 0 {
			return "", fmt.Errorf("invalid offset value: must be non-negative")
		}
		params = append(params, "offset:"+url.QueryEscape(offsetStr))
	}

	// Validate and append 'limit'
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil {
			return "", fmt.Errorf("invalid limit value: must be an integer")
		}
		if limit < 0 {
			return "", fmt.Errorf("invalid limit value: must be non-negative")
		}
		params = append(params, "limit:"+url.QueryEscape(limitStr))
	}

	// Append optional 'details'
	if details := r.URL.Query().Get("details"); details != "" {
		params = append(params, "details:"+url.QueryEscape(details))
	}

	// Combine query and validated params
	if len(params) > 0 {
		query = fmt.Sprintf("%s&%s", query, strings.Join(params, "&"))
	}

	return query, nil
}

func getQTypeFromName(name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "post" {
		return postQuery
	}
	return getQuery
}

// PrepareInput prepares the input string by removing all \" and trimming external quotes and spaces.
func PrepareInput(input string) string {
	// Remove all \" from the input
	input = strings.ReplaceAll(input, "\\\"", "\"")
	// trim external quotes (if any)
	input = strings.Trim(input, "\"")
	// trim spaces
	input = strings.TrimSpace(input)
	return input
}

func makeAPIPluginHandler(plugin plg.JSPlugin) http.HandlerFunc {
	allowed := map[string]bool{}
	if plugin.API != nil {
		for _, m := range plugin.API.Methods {
			allowed[strings.ToUpper(m)] = true
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Method] {
			w.Header().Set("Allow", strings.Join(plugin.API.Methods, ", "))
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		cmn.DebugMsg(
			cmn.DbgLvlDebug2,
			"API PLUGIN HIT: %s %s (%s)",
			r.Method,
			r.URL.Path,
			plugin.Name,
		)

		input, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(
				w,
				err,
				nil,
				"Invalid request",
				http.StatusBadRequest,
				0,
			)
			return
		}

		ctx := map[string]interface{}{
			"http": map[string]interface{}{
				"method": r.Method,
				"path":   r.URL.Path,
				"query":  r.URL.RawQuery,
				"header": r.Header,
			},
			"input": PrepareInput(input),
		}

		result, err := plugin.Execute(
			nil,
			nil,
			config.API.Timeout,
			ctx,
		)

		if err != nil {
			handleErrorAndRespond(
				w,
				err,
				nil,
				"Plugin execution failed",
				http.StatusInternalServerError,
				0,
			)
			return
		}

		handleErrorAndRespond(
			w,
			nil,
			result,
			"",
			0,
			http.StatusOK,
		)
	}
}
