// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"encoding/json"
	"net/http"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// handleErrorAndRespond encapsulates common error handling and JSON response logic.
func handleErrorAndRespond(w http.ResponseWriter, err error, results interface{}, errMsg string, errCode int, successCode int) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		// escape " in the error message
		errMsg = strings.ReplaceAll(err.Error(), "\"", "\\\"")
		// Encapsulate the error message in a JSON string
		results := "{\n  \"error\": \"" + errMsg + "\"\n}"
		http.Error(w, results, errCode)
		return
	}
	w.WriteHeader(successCode) // Explicitly set the success status code
	if err := json.NewEncoder(w).Encode(results); err != nil {
		// Log the error and send a generic error message to the client
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error encoding JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
