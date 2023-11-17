package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	cfg "TheCrow/pkg/config"

	_ "github.com/lib/pq"
)

var (
	config cfg.Config
)

func insertWebsite(db *sql.DB, url string) error {
	// SQL statement to insert a new website
	stmt := `INSERT INTO Sources (url, last_crawled_at, status) VALUES ($1, NULL, 'pending')`

	// Execute the SQL statement
	_, err := db.Exec(stmt, url)
	if err != nil {
		return err
	}

	fmt.Println("Website inserted successfully:", url)
	return nil
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to the configuration file")
	url := flag.String("url", "", "URL of the website to add")
	flag.Parse()

	// Read the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	// Check if the URL is provided
	if *url == "" {
		log.Fatal("Please provide a URL of the website to add.")
	}

	// Database connection setup (replace with your actual database configuration)
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host, config.Database.Port,
		config.Database.User, config.Database.Password, config.Database.DBName)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Insert the website
	if err := insertWebsite(db, *url); err != nil {
		log.Fatalf("Error inserting website: %v", err)
	}
}
