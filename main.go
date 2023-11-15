package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
	"github.com/tebeka/selenium"
)

const (
	sleepTime = 1 * time.Minute // Time to sleep when no URLs are found
)

func checkSources(db *sql.DB, wd selenium.WebDriver) {
	for {
		// Define the SQL query to find URLs not crawled in the last 3 days
		query := `SELECT url FROM Sources WHERE last_crawled_at IS NULL OR last_crawled_at < NOW() - INTERVAL '3 days'`

		// Execute the query
		rows, err := db.Query(query)
		if err != nil {
			log.Println("Error querying database:", err)
			time.Sleep(sleepTime)
			continue
		}

		var urlsToCrawl []string
		for rows.Next() {
			var url string
			if err := rows.Scan(&url); err != nil {
				log.Println("Error scanning rows:", err)
				continue
			}
			urlsToCrawl = append(urlsToCrawl, url)
		}
		rows.Close()

		// Check if there are URLs to crawl
		if len(urlsToCrawl) == 0 {
			fmt.Println("No URLs to crawl, sleeping...")
			time.Sleep(sleepTime)
			continue
		}

		// Crawl each URL
		for _, url := range urlsToCrawl {
			fmt.Println("Crawling URL:", url)
			crawlWebsite(db, url, wd)
		}
	}
}

func main() {
	// Database connection setup
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
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

	// Start the checkSources function in a goroutine
	go checkSources(db, wd)

	// Keep the main function alive
	select {} // Infinite empty select block to keep the main goroutine running
}
