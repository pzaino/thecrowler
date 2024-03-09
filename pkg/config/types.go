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

import "time"

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
	SSLMode string `yaml:"sslmode"` // SSL mode for API connection (e.g., "disable")
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
	Workers            int    `yaml:"workers"`              // Number of crawler workers
	Interval           string `yaml:"interval"`             // Interval between crawler requests (in seconds)
	Timeout            int    `yaml:"timeout"`              // Timeout for crawler requests (in seconds)
	Maintenance        int    `yaml:"maintenance"`          // Interval between crawler maintenance tasks (in seconds)
	SourceScreenshot   bool   `yaml:"source_screenshot"`    // Whether to take a screenshot of the source page or not
	FullSiteScreenshot bool   `yaml:"full_site_screenshot"` // Whether to take a screenshot of the full site or not
	MaxDepth           int    `yaml:"max_depth"`            // Maximum depth to crawl
	MaxSources         int    `yaml:"max_sources"`          // Maximum number of sources to crawl
	Delay              string `yaml:"delay"`                // Delay between requests (in seconds)
}

// DNSConfig represents the DNS information gathering configuration
type DNSConfig struct {
	Enabled   bool `yaml:"enabled"`    // Whether to enable DNS information gathering or not
	Timeout   int  `yaml:"timeout"`    // Timeout for DNS requests (in seconds)
	RateLimit int  `yaml:"rate_limit"` // Rate limit for DNS requests (in milliseconds)
}

// WHOISConfig represents the WHOIS information gathering configuration
type WHOISConfig struct {
	Enabled   bool `yaml:"enabled"`
	Timeout   int  `yaml:"timeout"`
	RateLimit int  `yaml:"rate_limit"`
}

// NetLookupConfig represents the network information gathering configuration
type NetLookupConfig struct {
	Enabled   bool `yaml:"enabled"`
	Timeout   int  `yaml:"timeout"`
	RateLimit int  `yaml:"rate_limit"`
}

// GeoLookupConfig represents the network information gathering configuration
type GeoLookupConfig struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`    // "maxmind" or "ip2location"
	DBPath  string `yaml:"db_path"` // Used for MaxMind
	APIKey  string `yaml:"api_key"` // Used for IP2Location
}

// NetworkInfo represents the network information gathering configuration
type NetworkInfo struct {
	DNS         DNSConfig       `yaml:"dns"`
	WHOIS       WHOISConfig     `yaml:"whois"`
	NetLookup   NetLookupConfig `yaml:"netlookup"`
	Geolocation GeoLookupConfig `yaml:"geolocation"`
}

// API represents the API configuration
type API struct {
	Host          string `yaml:"host"`           // Hostname of the API server
	Port          int    `yaml:"port"`           // Port number of the API server
	Timeout       int    `yaml:"timeout"`        // Timeout for API requests (in seconds)
	ContentSearch bool   `yaml:"content_search"` // Whether to search in the content too or not
	ReturnContent bool   `yaml:"return_content"` // Whether to return the content or not
	SSLMode       string `yaml:"sslmode"`        // SSL mode for API connection (e.g., "disable")
	CertFile      string `yaml:"cert_file"`      // Path to the SSL certificate file
	KeyFile       string `yaml:"key_file"`       // Path to the SSL key file
	RateLimit     string `yaml:"rate_limit"`     // Rate limit values are tuples (for ex. "1,3") where 1 means allows 1 request per second with a burst of 3 requests
	EnableConsole bool   `yaml:"enable_console"` // Whether to enable the console or not
}

// Selenium represents the Selenium configuration
type Selenium struct {
	Path       string `yaml:"path"`        // Path to the Selenium executable
	DriverPath string `yaml:"driver_path"` // Path to the Selenium driver executable
	Type       string `yaml:"type"`        // Type of Selenium driver
	Port       int    `yaml:"port"`        // Port number for Selenium server
	Host       string `yaml:"host"`        // Hostname of the Selenium server
	Headless   bool   `yaml:"headless"`    // Whether to run Selenium in headless mode
	UseService bool   `yaml:"use_service"` // Whether to use Selenium service as well or not
	SSLMode    string `yaml:"sslmode"`     // SSL mode for Selenium connection (e.g., "disable")
}

// Rules represents the rules configuration sources for the crawler and the scrapper
type Rules struct {
	// Rules set location
	Path []string `yaml:"path"`
	// URL to fetch the rules from (in case they are distributed by a web server)
	URL []string `yaml:"url"`
	// Rules set update interval (in seconds)
	Interval int `yaml:"interval"`
}

// Remote represents a way to tell the CROWler to load a remote configuration
type Remote struct {
	Host    string `yaml:"host"`    // Hostname of the API server
	Path    string `yaml:"path"`    // Path to the storage (e.g., "/tmp/images" or Bucket name)
	Port    int    `yaml:"port"`    // Port number of the API server
	Region  string `yaml:"region"`  // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token   string `yaml:"token"`   // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret  string `yaml:"secret"`  // Secret for API authentication (e.g., AWS secret access key)
	Timeout int    `yaml:"timeout"` // Timeout for API requests (in seconds)
	Type    string `yaml:"type"`    // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode string `yaml:"sslmode"` // SSL mode for API connection (e.g., "disable")
}

// Ruleset represents the top-level structure of the rules YAML file
type Ruleset struct {
	Path    []string `yaml:"path"`    // Path to the ruleset files
	Host    string   `yaml:"host"`    // Hostname of the API server
	Port    int      `yaml:"port"`    // Port number of the API server
	Region  string   `yaml:"region"`  // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token   string   `yaml:"token"`   // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret  string   `yaml:"secret"`  // Secret for API authentication (e.g., AWS secret access key)
	Timeout int      `yaml:"timeout"` // Timeout for API requests (in seconds)
	Type    string   `yaml:"type"`    // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode string   `yaml:"sslmode"` // SSL mode for API connection (e.g., "disable")
	Refresh int      `yaml:"refresh"` // Refresh interval for the ruleset (in seconds)
}

// Config represents the structure of the configuration file
type Config struct {
	// Remote configuration
	Remote Remote `yaml:"remote"`

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

	// File storage API configuration (to store files on a separate server)
	FileStorageAPI FileStorageAPI `yaml:"file_storage"`

	// NetworkInfo configuration
	NetworkInfo NetworkInfo `yaml:"network_info"`

	// Rules configuration
	Rulesets []Ruleset `yaml:"rulesets"`

	OS         string `yaml:"os"`          // Operating system name
	DebugLevel int    `yaml:"debug_level"` // Debug level for logging
}

//// ----------- Source Config ------------ ////

type SourceConfig struct {
	FormatVersion  string              `json:"format_version"`
	Author         string              `json:"author"`
	CreatedAt      time.Time           `json:"created_at"`
	Description    string              `json:"description"`
	SourceName     string              `json:"source_name"`
	CrawlingConfig CrawlingConfig      `json:"crawling_config"`
	ExecutionPlan  []ExecutionPlanItem `json:"execution_plan"`
}

type CrawlingConfig struct {
	Site string `json:"site"`
}

type ExecutionPlanItem struct {
	Label                string                 `json:"label"`
	Conditions           Condition              `json:"conditions"`
	RuleGroups           []string               `json:"rule_groups,omitempty"`
	Rules                []string               `json:"rules,omitempty"`
	AdditionalConditions map[string]interface{} `json:"additional_conditions,omitempty"`
}

type Condition struct {
	UrlPatterns []string `json:"url_patterns"`
}
