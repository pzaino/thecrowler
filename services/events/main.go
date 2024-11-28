// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"encoding/json"
	"flag"
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
)

var (
	configFile  *string
	config      cfg.Config
	configMutex = &sync.Mutex{}
	limiter     *rate.Limiter

	dbHandler cdb.Handler
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

	// Start the event listener
	listenForEvents(&dbHandler)
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
	if strings.TrimSpace(config.API.RateLimit) == "" {
		config.API.RateLimit = "10,10"
	}
	if !strings.Contains(config.API.RateLimit, ",") {
		config.API.RateLimit = config.API.RateLimit + ",10"
	}
	rlStr := strings.Split(config.API.RateLimit, ",")[0]
	if rlStr == "" {
		rlStr = "10"
	}
	rl, err = strconv.Atoi(rlStr)
	if err != nil {
		rl = 10
	}
	blStr := strings.Split(config.API.RateLimit, ",")[1]
	if blStr == "" {
		blStr = "10"
	}
	bl, err = strconv.Atoi(blStr)
	if err != nil {
		bl = 10
	}
	*lmt = rate.NewLimiter(rate.Limit(rl), bl)

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
	/*
		// Connect using the new ConnectWithDBHandler method
		err := listener.ConnectWithDBHandler(db, "new_event")
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to connect to the database listener: %v", err)
			return
		}
	*/
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

	// Process the event
	cmn.DebugMsg(cmn.DbgLvlInfo, "New Event Received: %+v", event)
	// Add your event processing logic here
}
