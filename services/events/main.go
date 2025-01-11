// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	agt "github.com/pzaino/thecrowler/pkg/agent"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

const (
	errTooManyRequests = "Too Many Requests"
	errRateLimitExceed = "Rate limit exceeded"
)

var (
	configFile  *string
	config      cfg.Config
	configMutex = &sync.Mutex{}
	limiter     *rate.Limiter

	dbHandler cdb.Handler

	// PluginRegister is the plugin register
	PluginRegister *plg.JSPluginRegister

	// AgentsEngine is the agent engine
	AgentsEngine *agt.JobEngine
)

func main() {
	// Parse the command line arguments
	configFile = flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Initialize the logger
	cmn.InitLogger("TheCROWlerAPI")
	cmn.DebugMsg(cmn.DbgLvlInfo, "The CROWler API is starting...")

	// Setting up a channel to listen for termination signals
	cmn.DebugMsg(cmn.DbgLvlInfo, "Setting up termination signals listener...")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)

	// Define signal handling
	go func() {
		for {
			sig := <-signals
			switch sig {
			case syscall.SIGINT:
				// Handle SIGINT (Ctrl+C)
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGINT received, shutting down...")
				os.Exit(0)

			case syscall.SIGTERM:
				// Handle SIGTERM
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGTERM received, shutting down...")
				os.Exit(0)

			case syscall.SIGQUIT:
				// Handle SIGQUIT
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGQUIT received, shutting down...")
				os.Exit(0)

			case syscall.SIGHUP:
				// Handle SIGHUP
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGHUP received, will reload configuration as soon as all pending jobs are completed...")
				configMutex.Lock()
				err := initAll(configFile, &config, &limiter)
				if err != nil {
					configMutex.Unlock()
					cmn.DebugMsg(cmn.DbgLvlFatal, "Error initializing the crawler: %v", err)
				}
				configMutex.Unlock()
			}
		}
	}()

	// Initialize the configuration
	err := initAll(configFile, &config, &limiter)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error initializing the crawler: %v", err)
		os.Exit(-1)
	}

	// Start the event listener (on a separate go routine)
	//go listenForEvents(&dbHandler)
	go cdb.ListenForEvents(&dbHandler, handleNotification)

	srv := &http.Server{
		Addr: config.Events.Host + ":" + fmt.Sprintf("%d", config.Events.Port),

		// ReadHeaderTimeout is the amount of time allowed to read
		// request headers. The connection's read deadline is reset
		// after reading the headers and the Handler can decide what
		// is considered too slow for the body. If ReadHeaderTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		ReadHeaderTimeout: time.Duration(config.Events.ReadHeaderTimeout) * time.Second,

		// ReadTimeout is the maximum duration for reading the entire
		// request, including the body. A zero or negative value means
		// there will be no timeout.
		//
		// Because ReadTimeout does not let Handlers make per-request
		// decisions on each request body's acceptable deadline or
		// upload rate, most users will prefer to use
		// ReadHeaderTimeout. It is valid to use them both.
		ReadTimeout: time.Duration(config.Events.ReadTimeout) * time.Second,

		// WriteTimeout is the maximum duration before timing out
		// writes of the response. It is reset whenever a new
		// request's header is read. Like ReadTimeout, it does not
		// let Handlers make decisions on a per-request basis.
		// A zero or negative value means there will be no timeout.
		WriteTimeout: time.Duration(config.Events.WriteTimeout) * time.Second,

		// IdleTimeout is the maximum amount of time to wait for the
		// next request when keep-alive are enabled. If IdleTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		IdleTimeout: time.Duration(config.Events.Timeout) * time.Second,
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	// Set the handlers
	initAPIv1()

	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting server on %s:%d", config.Events.Host, config.Events.Port)
	if strings.ToLower(strings.TrimSpace(config.Events.SSLMode)) == cmn.EnableStr {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Server return: %v", srv.ListenAndServeTLS(config.Events.CertFile, config.Events.KeyFile))
	}
	cmn.DebugMsg(cmn.DbgLvlFatal, "Server return: %v", srv.ListenAndServe())
}

func initAll(configFile *string, config *cfg.Config, lmt **rate.Limiter) error {
	// Reading the configuration file
	var err error
	*config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error reading config file: %v", err)
	}
	if cfg.IsEmpty(*config) {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Config file is empty")
	}

	// Set the OS variable
	config.OS = runtime.GOOS

	// Set the rate limiter
	var rl, bl int
	if strings.TrimSpace(config.Events.RateLimit) == "" {
		config.Events.RateLimit = "10,10"
	}
	if !strings.Contains(config.Events.RateLimit, ",") {
		config.Events.RateLimit = config.Events.RateLimit + ",10"
	}
	rlStr := strings.Split(config.Events.RateLimit, ",")[0]
	if rlStr == "" {
		rlStr = "10"
	}
	rl, err = strconv.Atoi(rlStr)
	if err != nil {
		rl = 10
	}
	blStr := strings.Split(config.Events.RateLimit, ",")[1]
	if blStr == "" {
		blStr = "10"
	}
	bl, err = strconv.Atoi(blStr)
	if err != nil {
		bl = 10
	}
	*lmt = rate.NewLimiter(rate.Limit(rl), bl)

	// Reload Plugins
	PluginRegister = plg.NewJSPluginRegister().LoadPluginsFromConfig(config, "event_plugin")

	// Initialize the Agents Engine
	agt.AgentsEngine = agt.NewJobEngine()
	agt.RegisterActions(agt.AgentsEngine) // Use the initialized `agt.AgentsEngine`
	agtCfg := config.Agents
	agt.AgentsRegistry = agt.NewJobConfig()
	err = agt.AgentsRegistry.LoadConfig(agtCfg)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading agents configuration: %v", err)
	}

	// Initialize the database
	connected := false
	dbHandler, err = cdb.NewHandler(*config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error allocating database resources: %v", err)
	} else {
		for !connected {
			err = dbHandler.Connect(*config)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error opening database connection: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}
			connected = true
		}
		cmn.DebugMsg(cmn.DbgLvlInfo, "Database connection established")
	}

	return nil
}

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	// Health check
	healthCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(healthCheckHandler)))

	http.Handle("/v1/health", healthCheckWithMiddlewares)

	// Events API endpoints
	createEventWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(createEventHandler)))
	checkEventWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(checkEventHandler)))
	updateEventWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(updateEventHandler)))
	removeEventWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeEventHandler)))
	listEventsWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(listEventsHandler)))

	baseAPI := "/v1/event"

	http.Handle(baseAPI+"/create", createEventWithMiddlewares)
	http.Handle(baseAPI+"/status", checkEventWithMiddlewares)
	http.Handle(baseAPI+"/update", updateEventWithMiddlewares)
	http.Handle(baseAPI+"/remove", removeEventWithMiddlewares)
	http.Handle(baseAPI+"/list", listEventsWithMiddlewares)

}

// RateLimitMiddleware is a middleware for rate limiting
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			cmn.DebugMsg(cmn.DbgLvlDebug, errRateLimitExceed)
			http.Error(w, errTooManyRequests, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityHeadersMiddleware adds security-related headers to responses
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add various security headers here
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")

		next.ServeHTTP(w, r)
	})
}

func healthCheckHandler(w http.ResponseWriter, _ *http.Request) {
	// Create a JSON document with the health status
	healthStatus := HealthCheck{
		Status: "OK",
	}

	// Respond with the health status
	handleErrorAndRespond(w, nil, healthStatus, "Error in health Check: ", http.StatusInternalServerError, http.StatusOK)
}

func createEventHandler(w http.ResponseWriter, r *http.Request) {
	var event cdb.Event
	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid request body: ", http.StatusBadRequest, http.StatusOK)
		return
	}

	uid, err := cdb.CreateEvent(&dbHandler, event)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Failed to create event: ", http.StatusInternalServerError, http.StatusOK)
		return
	}

	response := map[string]string{"message": "Event created successfully", "event_id": uid}
	handleErrorAndRespond(w, nil, response, "Error creating event: ", http.StatusInternalServerError, http.StatusCreated)
}

func removeEventHandler(w http.ResponseWriter, r *http.Request) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		handleErrorAndRespond(w, errors.New("No event ID"), nil, "Missing event_id parameter: ", http.StatusBadRequest, http.StatusOK)
		return
	}

	_, err := dbHandler.Exec(`DELETE FROM Events WHERE event_sha256 = $1`, eventID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Failed to remove event: ", http.StatusInternalServerError, http.StatusOK)
		return
	}

	response := map[string]string{"message": "Event removed successfully"}
	handleErrorAndRespond(w, nil, response, "Error removing event: ", http.StatusInternalServerError, http.StatusOK)
}

func listEventsHandler(w http.ResponseWriter, _ *http.Request) {
	rows, err := dbHandler.ExecuteQuery(`SELECT event_sha256, created_at, last_updated_at, source_id, event_type, event_severity, event_timestamp, details FROM Events`)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Failed to retrieve events: ", http.StatusInternalServerError, http.StatusOK)
		return
	}
	defer rows.Close() //nolint:errcheck // We can't check returned error when using defer

	var events []cdb.Event
	for rows.Next() {
		var event cdb.Event
		var details string

		err := rows.Scan(&event.ID, &event.CreatedAt, &event.LastUpdatedAt, &event.SourceID, &event.Type, &event.Severity, &event.Timestamp, &details)
		if err != nil {
			handleErrorAndRespond(w, err, events, "Error scanning events: ", http.StatusInternalServerError, http.StatusOK)
			return
		}

		// Convert JSON details string back to map
		event.Details = cmn.ConvertStringToMap(details)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		handleErrorAndRespond(w, err, events, "Error reading events: ", http.StatusInternalServerError, http.StatusOK)
		return
	}

	handleErrorAndRespond(w, nil, events, "Error listing events: ", http.StatusInternalServerError, http.StatusOK)
}

func checkEventHandler(w http.ResponseWriter, r *http.Request) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		handleErrorAndRespond(w, errors.New("No event ID"), nil, "Missing event_id parameter: ", http.StatusBadRequest, http.StatusOK)
		return
	}

	rows, err := dbHandler.ExecuteQuery(`SELECT event_sha256, created_at, last_updated_at, source_id, event_type, event_severity, event_timestamp, details FROM Events WHERE event_sha256 = $1`, eventID)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Failed to retrieve events: ", http.StatusInternalServerError, http.StatusOK)
		return
	}
	defer rows.Close() //nolint:errcheck // We can't check returned error when using defer

	var events []cdb.Event
	for rows.Next() {
		var event cdb.Event
		var details string

		err := rows.Scan(&event.ID, &event.CreatedAt, &event.LastUpdatedAt, &event.SourceID, &event.Type, &event.Severity, &event.Timestamp, &details)
		if err != nil {
			handleErrorAndRespond(w, err, events, "Error scanning events: ", http.StatusInternalServerError, http.StatusOK)
			return
		}

		// Convert JSON details string back to map
		event.Details = cmn.ConvertStringToMap(details)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		handleErrorAndRespond(w, err, events, "Error reading events: ", http.StatusInternalServerError, http.StatusOK)
		return
	}

	handleErrorAndRespond(w, nil, events, "Error listing events: ", http.StatusInternalServerError, http.StatusOK)
}

func updateEventHandler(w http.ResponseWriter, r *http.Request) {
	eventID := r.URL.Query().Get("event_id")
	if eventID == "" {
		handleErrorAndRespond(w, errors.New("Missing event_id parameter"), nil, "Invalid request: ", http.StatusBadRequest, http.StatusOK)
		return
	}

	// Update the event details
	var event cdb.Event
	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Invalid request body: ", http.StatusBadRequest, http.StatusOK)
		return
	}

	_, err = dbHandler.Exec(`UPDATE Events SET event_type = $1, event_severity = $2, event_timestamp = $3, details = $4 WHERE event_sha256 = $5`, event.Type, event.Severity, event.Timestamp, cmn.ConvertMapToString(event.Details), eventID)

	handleErrorAndRespond(w, err, nil, "Error listing events: ", http.StatusInternalServerError, http.StatusOK)
}

// Handle the notification received
func handleNotification(payload string) {
	var event cdb.Event
	err := json.Unmarshal([]byte(payload), &event)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to decode notification payload: %v", err)
		return
	}

	// Log the event for debug purposes
	cmn.DebugMsg(cmn.DbgLvlDebug, "New Event Received: %+v", event)

	// Process the Event
	processEvent(event)
}

// Process the event
func processEvent(event cdb.Event) {
	p, pExists := PluginRegister.GetPluginsByEventType(event.Type)

	a, aExists := agt.AgentsRegistry.GetAgentsByEventType(event.Type)

	// Check if we have a Plugin for this event
	if !pExists && !aExists {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No Plugins or Agents found to handle event type '%s', ignoring event", event.Type)
		return
	}

	if pExists {
		// Convert the event struct to a map
		eventMap := make(map[string]interface{})
		eventMap["jsonData"] = event

		// leave it blank for now
		eventMap["currentURL"] = ""

		// Execute the plugin
		for _, plugin := range p {
			// Execute the plugin
			rval, err := plugin.Execute(nil, &dbHandler, 30, eventMap)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "executing plugin: %v", err)
			}

			// Parse the plugin response
			rvalStr := cmn.ConvertMapToString(rval)
			var pluginResp PluginResponse
			err = json.Unmarshal([]byte(rvalStr), &pluginResp)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "parsing plugin response: %v", err)
				continue
			}

			// Handle the parsed response
			handlePluginResponse(pluginResp)

			// Remove the event if needed
			if config.Events.EventRemoval == "" || config.Events.EventRemoval == cmn.AlwaysStr {
				removeHandledEVent(event.ID)
			} else if config.Events.EventRemoval == "on_success" && pluginResp.Success {
				removeHandledEVent(event.ID)
			} else if config.Events.EventRemoval == "on_failure" && !pluginResp.Success {
				removeHandledEVent(event.ID)
			}
		}
	}

	if aExists {
		// generate the minimal iCfg
		iCfg := make(map[string]interface{})
		iCfg["wd"] = nil
		iCfg["dbHandler"] = dbHandler
		iCfg["event"] = event

		for _, ac := range a {
			// Execute the agents
			err := agt.AgentsEngine.ExecuteJobs(ac, iCfg)
			if err != nil {
				// retrieve ac.Jobs names list:
				var jobs string
				for _, job := range ac.Jobs {
					if jobs != "" {
						jobs += ", " + strings.TrimSpace(job.Name)
					} else {
						jobs = strings.TrimSpace(job.Name)
					}
				}
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to execute agent '%s': %v", jobs, err)
			}
		}
	}
}

func removeHandledEVent(eventID string) {
	_, err := dbHandler.Exec(`DELETE FROM Events WHERE event_sha256 = $1`, eventID)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to remove event: %v", err)
	}
}

func handlePluginResponse(resp PluginResponse) {
	if resp.Success {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Plugin executed successfully: %s", resp.Message)

		// Optionally handle the API response if present
		if resp.APIResponse != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug5, "API response: %v", resp.APIResponse)
			// Add any specific handling logic for APIResponse here
		}
	} else {
		if resp.Message != "" {
			cmn.DebugMsg(cmn.DbgLvlError, "executing plugin: %s", resp.Message)
		}
		// Add any error handling logic here, such as retries or reporting
	}
}
