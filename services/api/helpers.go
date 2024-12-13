package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
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

	if err != nil {
		// Log the error and prepare an error response
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		response = map[string]interface{}{
			"error":   err.Error(),
			"message": errMsg,
		}
		w.Header().Set("Content-Type", "application/json")
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(successCode) // Send the success code
	}

	// Encode the response as JSON (always include a body)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// Log the error and send a fallback error response
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error encoding JSON response: %v", err)
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Original Results: %+v", results)

		fallbackResponse := map[string]string{"error": "Internal Server Error"}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(fallbackResponse)
	}
}

// extractQueryOrBody extracts the query parameter for GET requests or the body for POST requests.
func extractQueryOrBody(r *http.Request) (string, error) {
	if r.Method == "POST" {
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement
		if err != nil {
			return "", err
		}
		return string(body), nil
	}

	// Process it as GET request
	query := r.URL.Query().Get("q")
	if query == "" {
		return "", fmt.Errorf("query parameter 'q' is required")
	}
	offset := r.URL.Query().Get("offset")
	if offset != "" {
		query += "&offset:" + offset
	}
	limit := r.URL.Query().Get("limit")
	if limit != "" {
		query += "&limit:" + limit
	}
	details := r.URL.Query().Get("details")
	if details != "" {
		query += "&details:" + details
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

// normalizeURL normalizes a URL by trimming trailing slashes and converting it to lowercase.
func normalizeURL(url string) string {
	// Trim spaces
	url = strings.TrimSpace(url)
	// Trim trailing slash
	url = strings.TrimRight(url, "/")
	// Convert to lowercase
	url = strings.ToLower(url)
	return url
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
