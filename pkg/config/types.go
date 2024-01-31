// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config contains the configuration file parsing logic.
package config

// FileStorageAPI is a generic File Storage API configuration
type FileStorageAPI struct {
	Host    string `yaml:"host"`    // Hostname of the API server
	Path    string `yaml:"path"`    // Path to the storage (e.g., "/tmp/images" or Bucket name)
	Port    int    `yaml:"port"`    // Port number of the API server
	Region  string `yaml:"region"`  // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token   string `yaml:"token"`   // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret  string `yaml:"secret"`  // Secret for API authentication (e.g., AWS secret access key)
	Timeout int    `yaml:"timeout"` // Timeout for API requests (in seconds)
	Type    string `yaml:"type"`    // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
}

// Database represents the database configuration
type Database struct {
	Type      string `yaml:"type"`       // Type of database (e.g., "postgres", "mysql", "sqlite")
	Host      string `yaml:"host"`       // Hostname of the database server
	Port      int    `yaml:"port"`       // Port number of the database server
	User      string `yaml:"user"`       // Username for database authentication
	Password  string `yaml:"password"`   // Password for database authentication
	DBName    string `yaml:"dbname"`     // Name of the database
	RetryTime int    `yaml:"retry_time"` // Time to wait before retrying to connect to the database (in seconds)
	PingTime  int    `yaml:"ping_time"`  // Time to wait before retrying to ping the database (in seconds)
	SSLMode   string `yaml:"sslmode"`    // SSL mode for database connection (e.g., "disable")
}

// Crawler represents the crawler configuration
type Crawler struct {
	Workers            int  `yaml:"workers"`              // Number of crawler workers
	Interval           int  `yaml:"interval"`             // Interval between crawler requests (in seconds)
	Timeout            int  `yaml:"timeout"`              // Timeout for crawler requests (in seconds)
	Maintenance        int  `yaml:"maintenance"`          // Interval between crawler maintenance tasks (in seconds)
	SourceScreenshot   bool `yaml:"source_screenshot"`    // Whether to take a screenshot of the source page or not
	FullSiteScreenshot bool `yaml:"full_site_screenshot"` // Whether to take a screenshot of the full site or not
	MaxDepth           int  `yaml:"max_depth"`            // Maximum depth to crawl
	Delay              int  `yaml:"delay"`                // Delay between requests (in seconds)
}

// API represents the API configuration
type API struct {
	Host          string `yaml:"host"`           // Hostname of the API server
	Port          int    `yaml:"port"`           // Port number of the API server
	Timeout       int    `yaml:"timeout"`        // Timeout for API requests (in seconds)
	ContentSearch bool   `yaml:"content_search"` // Whether to search in the content too or not
}

// Selenium represents the Selenium configuration
type Selenium struct {
	Path       string `yaml:"path"`        // Path to the Selenium executable
	DriverPath string `yaml:"driver_path"` // Path to the Selenium driver executable
	Type       string `yaml:"type"`        // Type of Selenium driver
	Port       int    `yaml:"port"`        // Port number for Selenium server
	Host       string `yaml:"host"`        // Hostname of the Selenium server
	Headless   bool   `yaml:"headless"`    // Whether to run Selenium in headless mode
}

// Config represents the structure of the configuration file
type Config struct {
	// Database configuration
	Database Database `yaml:"database"`

	// Crawler configuration
	Crawler Crawler `yaml:"crawler"`

	// API configuration
	API API `yaml:"api"`

	// Selenium configuration
	Selenium []Selenium `yaml:"selenium"`

	// Image storage API configuration (to store images on a separate server)
	ImageStorageAPI FileStorageAPI `yaml:"image_storage"`

	OS         string `yaml:"os"`          // Operating system name
	DebugLevel int    `yaml:"debug_level"` // Debug level for logging
}
