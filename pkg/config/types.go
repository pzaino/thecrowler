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

package config

import (
	"fmt"
	"os"
	"runtime"

	"gopkg.in/yaml.v2"
)

// Generic File Storage API configuration
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

// Config represents the structure of the configuration file
type Config struct {
	// Database configuration
	Database struct {
		Host     string `yaml:"host"`     // Hostname of the database server
		Port     int    `yaml:"port"`     // Port number of the database server
		User     string `yaml:"user"`     // Username for database authentication
		Password string `yaml:"password"` // Password for database authentication
		DBName   string `yaml:"dbname"`   // Name of the database
	} `yaml:"database"`

	// Crawler configuration
	Crawler struct {
		Workers     int `yaml:"workers"`     // Number of crawler workers
		Interval    int `yaml:"interval"`    // Interval between crawler requests (in seconds)
		Timeout     int `yaml:"timeout"`     // Timeout for crawler requests (in seconds)
		Maintenance int `yaml:"maintenance"` // Interval between crawler maintenance tasks (in seconds)
	} `yaml:"crawler"`

	// API configuration
	API struct {
		Host    string `yaml:"host"`    // Hostname of the API server
		Port    int    `yaml:"port"`    // Port number of the API server
		Timeout int    `yaml:"timeout"` // Timeout for API requests (in seconds)
	} `yaml:"api"`

	// Selenium configuration
	Selenium struct {
		Path       string `yaml:"path"`        // Path to the Selenium executable
		DriverPath string `yaml:"driver_path"` // Path to the Selenium driver executable
		Type       string `yaml:"type"`        // Type of Selenium driver
		Port       int    `yaml:"port"`        // Port number for Selenium server
		Host       string `yaml:"host"`        // Hostname of the Selenium server
		Headless   bool   `yaml:"headless"`    // Whether to run Selenium in headless mode
	} `yaml:"selenium"`

	// Image storage API configuration (to store images on a separate server)
	ImageStorageAPI FileStorageAPI `yaml:"image_storage_api"`

	OS         string `yaml:"os"`          // Operating system name
	DebugLevel int    `yaml:"debug_level"` // Debug level for logging
}

// fileExists checks if a file exists at the given filename.
// It returns true if the file exists and is not a directory, and false otherwise.
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// getConfigFile reads and unmarshals a configuration file with the given name.
// It checks if the file exists, reads its contents, and unmarshals it into a Config struct.
// If the file does not exist or an error occurs during reading or unmarshaling, an error is returned.
func getConfigFile(confName string) (Config, error) {

	// Check if the configuration file exists
	if !fileExists(confName) {
		return Config{}, fmt.Errorf("file does not exist: %s", confName)
	}

	// Read the configuration file
	data, err := os.ReadFile(confName)

	// If the configuration file has been found and is not empty, unmarshal it
	var config Config
	if (data != nil) && (err == nil) {
		err = yaml.Unmarshal(data, &config)
	}
	return config, err
}

// LoadConfig is responsible for loading the configuration file
// and return the Config struct
func LoadConfig(confName string) (Config, error) {

	// Get the configuration file
	config, err := getConfigFile(confName)

	// Set the OS variable
	config.OS = runtime.GOOS

	// Set default values
	if config.Database.Host == "" {
		config.Database.Host = "localhost"
	}

	if config.Database.Port == 0 {
		config.Database.Port = 5432
	}

	if config.Database.User == "" {
		config.Database.User = "postgres"
	}

	if config.Database.Password == "" {
		config.Database.Password = "postgres"
	}

	if config.Database.DBName == "" {
		config.Database.DBName = "SitesIndex"
	}

	if config.Crawler.Workers == 0 {
		config.Crawler.Workers = 1
	}

	if config.Crawler.Interval == 0 {
		config.Crawler.Interval = 2
	}

	if config.Crawler.Timeout == 0 {
		config.Crawler.Timeout = 10
	}

	if config.Crawler.Maintenance == 0 {
		config.Crawler.Maintenance = 60
	}

	if config.API.Host == "" {
		config.API.Host = "localhost"
	}

	if config.API.Port == 0 {
		config.API.Port = 8080
	}

	if config.API.Timeout == 0 {
		config.API.Timeout = 10
	}

	if config.Selenium.Port == 0 {
		config.Selenium.Port = 4444
	}

	if config.Selenium.Host == "" {
		config.Selenium.Host = "localhost"
	}

	return config, err
}

// IsEmpty checks if the given config is empty.
// It returns true if the config is empty, false otherwise.
func IsEmpty(config Config) bool {
	return config == Config{}
}
