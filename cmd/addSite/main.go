package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"gopkg.in/yaml.v2"
)

// Config represents the structure of the configuration file
type Config struct {
	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
	} `yaml:"database"`
	Workers  int `yaml:"workers"`
	Selenium struct {
		Path       string `yaml:"path"`
		DriverPath string `yaml:"driver_path"`
		Type       string `yaml:"type"`
		Port       int    `yaml:"port"`
		Host       string `yaml:"host"`
	} `yaml:"selenium"`
	OS string `yaml:"os"`
}

var (
	config Config
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
	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("Error parsing config file: %v", err)
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
