package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// handleErrorAndRespond encapsulates common error handling and JSON response logic.
func handleErrorAndRespond(w http.ResponseWriter, err error, results interface{}, errMsg string, errCode int) {
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		http.Error(w, err.Error(), errCode)
		return
	}
	w.Header().Set("Content-Type", "application/json")
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
		return query, nil
	}
}

func getQType(expr bool) int {
	if expr {
		return 1
	}
	return 0
}
