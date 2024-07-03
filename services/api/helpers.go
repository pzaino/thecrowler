package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// handleErrorAndRespond encapsulates common error handling and JSON response logic.
func handleErrorAndRespond(w http.ResponseWriter, err error, results interface{}, errMsg string, errCode int, successCode int) {
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		http.Error(w, err.Error(), errCode)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(successCode) // Explicitly set the success status code
	if err := json.NewEncoder(w).Encode(results); err != nil {
		// Log the error and send a generic error message to the client
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error encoding JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// extractQueryOrBody extracts the query parameter for GET requests or the body for POST requests.
func extractQueryOrBody(r *http.Request) (string, error) {
	if r.Method == "POST" {
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			return "", err
		}
		return string(body), nil
	} else {
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

func PrepareInput(input string) string {
	// Remove all \" from the input
	input = strings.ReplaceAll(input, "\\\"", "\"")
	// trim external quotes (if any)
	input = strings.Trim(input, "\"")
	// trim spaces
	input = strings.TrimSpace(input)
	return input
}
