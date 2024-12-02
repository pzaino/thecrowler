// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"encoding/json"
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
	go listenForEvents(&dbHandler)

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
	if strings.ToLower(strings.TrimSpace(config.Events.SSLMode)) == "enable" {
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
	}

	return nil
}

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	// Health check
	healthCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(healthCheckHandler)))

	http.Handle("/v1/health", healthCheckWithMiddlewares)

	// Events
	//eventsWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(eventsHandler)))

	//http.Handle("/v1/events", eventsWithMiddlewares)

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

func listenForEvents(db *cdb.Handler) {
	// Listener for PostgreSQL notifications
	listener := (*db).NewListener()

	// _ = event
	err := listener.Connect(cfg.Config{}, 10, 90, func(_ cdb.ListenerEventType, err error) {
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Listener error: %v", err)
		}
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to connect to the database listener: %v", err)
		return
	}
	defer listener.Close() //nolint:errcheck // We can't check returned error when using defer

	err = listener.Listen("new_event")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to listen on 'new_event': %v", err)
		return
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "Listening for new events...")

	// Verify connection
	if err := listener.Ping(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to ping the listener: %v", err)
		return
	}

	// Graceful shutdown on interrupt
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			select {
			case n := <-listener.Notify():
				if n != nil {
					handleNotification(n.Extra())
				}
			case <-stop:
				cmn.DebugMsg(cmn.DbgLvlInfo, "Shutting down the events handler...")
				err = listener.UnlistenAll()
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to unlisten: %v", err)
				}
				return
			}
		}
	}()

	<-stop // Wait for shutdown signal
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
	p, exists := PluginRegister.GetPluginsByEventType(event.Type)

	// Check if we have a Plugin for this event
	if !exists {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No plugins found to handle event type '%s', ignoring event", event.Type)
		return
	}

	// Convert the event struct to a map
	eventMap := make(map[string]interface{})
	eventMap["event"] = event

	// Execute the plugin
	for _, plugin := range p {
		// Execute the plugin
		_, err := plugin.Execute(nil, &dbHandler, 30, eventMap)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error executing plugin: %v", err)
		}
	}
}
