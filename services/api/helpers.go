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
		response = cmn.StdAPIError{
			ErrCode: errCode,
			Err:     err.Error(),
			Message: errMsg,
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

		// Streaming (SSE) mode
		if r.Header.Get("Accept") == "text/event-stream" {
			handleStreamingAPIPlugin(w, r, plugin)
			return
		}

		// Normal request-response mode
		handleNormalAPIPlugin(w, r, plugin)
	}
}

func handleNormalAPIPlugin(w http.ResponseWriter, r *http.Request, plugin plg.JSPlugin) {
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
		&dbHandler,
		config.API.Plugins.Timeout,
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

func setWriteDeadline(w http.ResponseWriter, d time.Duration) {
	if wd, ok := w.(interface {
		SetWriteDeadline(time.Time) error
	}); ok {
		_ = wd.SetWriteDeadline(time.Now().Add(d))
	}
}

func handleStreamingAPIPlugin(w http.ResponseWriter, r *http.Request, plugin plg.JSPlugin) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	input, err := extractQueryOrBody(r)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	input = PrepareInput(input)

	_, err = io.Copy(io.Discard, r.Body)
	if (err != nil) && (err != io.EOF) {
		cmn.DebugMsg(cmn.DbgLvlError, "Error reading request body: %v", err)
	}
	err = r.Body.Close()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error closing request body: %v", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.WriteHeader(http.StatusOK)

	setWriteDeadline(w, 5*time.Second)
	_, err = fmt.Fprintf(w, "event: status\ndata: started\n\n")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error writing to SSE: %v", err)
	}
	flusher.Flush()

	progressCh := make(chan map[string]interface{}, 16)
	resultCh := make(chan interface{}, 1)
	errCh := make(chan error, 1)

	pluginDone := make(chan struct{})

	go func(input string) {
		defer close(pluginDone)
		ctx := map[string]interface{}{
			"http": map[string]interface{}{
				"method": r.Method,
				"path":   r.URL.Path,
				"query":  r.URL.RawQuery,
				"header": r.Header,
			},
			"input": input,
			"progress": func(msg map[string]interface{}) {
				select {
				case progressCh <- msg:
				default:
				}
			},
		}

		result, err := plugin.Execute(
			nil,
			&dbHandler,
			config.API.Plugins.Timeout,
			ctx,
		)
		if err != nil {
			errCh <- err
			return
		}

		resultCh <- result
	}(input)

	keepAlive := time.NewTicker(10 * time.Second)
	defer keepAlive.Stop()
	exitReason := "completed"

loop:
	for {
		select {
		case msg := <-progressCh:
			b, _ := json.Marshal(msg)
			setWriteDeadline(w, 5*time.Second)
			_, err = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error writing to SSE: %v", err)
			}
			flusher.Flush()

		case result := <-resultCh:
			b, _ := json.Marshal(result)
			setWriteDeadline(w, 5*time.Second)
			_, err = fmt.Fprintf(w, "event: result\ndata: %s\n\n", b)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error writing to SSE: %v", err)
			}
			flusher.Flush()

			setWriteDeadline(w, 5*time.Second)
			_, err = fmt.Fprintf(w, "event: done\ndata: completed\n\n")
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error writing to SSE: %v", err)
			}
			flusher.Flush()
			break loop

		case err := <-errCh:
			setWriteDeadline(w, 5*time.Second)
			_, err = fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error writing to SSE: %v", err)
			}
			flusher.Flush()
			exitReason = "error"
			break loop

		case <-keepAlive.C:
			setWriteDeadline(w, 5*time.Second)
			_, err = fmt.Fprint(w, ": ping\n\n")
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error writing to SSE: %v", err)
			}
			flusher.Flush()

		case <-r.Context().Done():
			cmn.DebugMsg(
				cmn.DbgLvlDebug2,
				"SSE client disconnected: %s (%s): %v",
				r.RemoteAddr,
				plugin.Name,
				r.Context().Err(),
			)
			exitReason = "client_disconnect"
			break loop
		}
	}

	<-pluginDone // 2. wait until plugin is 100% done

	cmn.DebugMsg(
		cmn.DbgLvlDebug2,
		"SSE handler exit (%s): %s",
		exitReason,
		plugin.Name,
	)
}
