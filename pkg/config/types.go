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

// Config represents the structure of the configuration file
type Config struct {
	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
	} `yaml:"database"`
	Crawler struct {
		Workers     int `yaml:"workers"`
		Interval    int `yaml:"interval"`
		Timeout     int `yaml:"timeout"`
		Maintenance int `yaml:"maintenance"`
	} `yaml:"crawler"`
	API struct {
		Host    string `yaml:"host"`
		Port    int    `yaml:"port"`
		Timeout int    `yaml:"timeout"`
	} `yaml:"api"`
	Selenium struct {
		Path       string `yaml:"path"`
		DriverPath string `yaml:"driver_path"`
		Type       string `yaml:"type"`
		Port       int    `yaml:"port"`
		Host       string `yaml:"host"`
		Headless   bool   `yaml:"headless"`
	} `yaml:"selenium"`
	OS         string `yaml:"os"`
	DebugLevel int    `yaml:"debug_level"`
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

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

// This function is responsible for loading the configuration file
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

// This function is responsible for returning true if the config is empty or false otherwise
func ConfigIsEmpty(config Config) bool {
	return config == Config{}
}
