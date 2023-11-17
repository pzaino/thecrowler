package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/tebeka/selenium"
	"gopkg.in/yaml.v2"
)

const (
	sleepTime = 1 * time.Minute // Time to sleep when no URLs are found
)

func checkSources(db *sql.DB, wd selenium.WebDriver) {
	fmt.Println("Checking sources...")
	for {
		// Update the SQL query to fetch all necessary fields
		query := `SELECT url, restricted FROM Sources WHERE (last_crawled_at IS NULL OR last_crawled_at < NOW() - INTERVAL '3 days') OR (status = 'error' AND last_crawled_at < NOW() - INTERVAL '15 minutes') OR (status = 'completed' AND last_crawled_at < NOW() - INTERVAL '1 week') OR (status = 'pending') ORDER BY last_crawled_at ASC`

		// Execute the query
		rows, err := db.Query(query)
		if err != nil {
			log.Println("Error querying database:", err)
			time.Sleep(sleepTime)
			continue
		}

		var sourcesToCrawl []Source
		for rows.Next() {
			var src Source
			if err := rows.Scan(&src.URL, &src.Restricted); err != nil {
				log.Println("Error scanning rows:", err)
				continue
			}
			sourcesToCrawl = append(sourcesToCrawl, src)
		}
		rows.Close()

		// Check if there are sources to crawl
		if len(sourcesToCrawl) == 0 {
			fmt.Println("No sources to crawl, sleeping...")
			time.Sleep(sleepTime)
			continue
		}

		// Crawl each source
		for _, source := range sourcesToCrawl {
			fmt.Println("Crawling URL:", source.URL)
			crawlWebsite(db, source, wd)
		}
	}
}

func main() {

	configFile := flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Reading the configuration file
	config = Config{}
	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}

	// Set the OS variable
	config.OS = runtime.GOOS

	// Database connection setup
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host, config.Database.Port,
		config.Database.User, config.Database.Password, config.Database.DBName)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Check database connection
	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Successfully connected to the database!")

	sel, err := startSelenium()
	if err != nil {
		log.Fatal("Error starting Selenium:", err)
	}
	defer stopSelenium(sel)

	wd, err := connectSelenium(sel)
	if err != nil {
		log.Fatal("Error connecting to Selenium:", err)
	}
	defer quitSelenium(wd)

	// Setting up a channel to listen for termination signals
	signals := make(chan os.Signal, 1)
	// Catch SIGINT (Ctrl+C) and SIGTERM signals
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Use a select statement to block until a signal is received
	go func() {
		sig := <-signals
		fmt.Printf("Received %v signal, shutting down...\n", sig)

		// Close resources
		closeResources(db, wd) // Assuming db is your DB connection and wd is the WebDriver

		os.Exit(0)
	}()

	// Start the checkSources function in a goroutine
	go checkSources(db, wd)

	// Keep the main function alive
	select {} // Infinite empty select block to keep the main goroutine running
}

func closeResources(db *sql.DB, wd selenium.WebDriver) {
	// Close the database connection
	if db != nil {
		db.Close()
		fmt.Println("Database connection closed.")
	}

	// Close the WebDriver
	if wd != nil {
		wd.Quit()
		fmt.Println("WebDriver closed.")
	}
}
