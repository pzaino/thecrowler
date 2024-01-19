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
	Api struct {
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

func getConfigFile(confName string) (Config, error) {
	data, err := os.ReadFile(confName)

	// If the configuration file has been found and is not empty, unmarshal it
	var config Config
	if data != nil && err == nil {
		err = yaml.Unmarshal(data, &config)
		if err != nil {
			return config, err
		}
	}
	return config, nil
}

// This function is responsible for loading the configuration file
// and return the Config struct
func LoadConfig(confName string) (Config, error) {

	// Get the configuration file
	config, _ := getConfigFile(confName)

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

	if config.Api.Host == "" {
		config.Api.Host = "localhost"
	}

	if config.Api.Port == 0 {
		config.Api.Port = 8080
	}

	if config.Api.Timeout == 0 {
		config.Api.Timeout = 10
	}

	if config.Selenium.Port == 0 {
		config.Selenium.Port = 4444
	}

	if config.Selenium.Host == "" {
		config.Selenium.Host = "localhost"
	}

	return config, nil
}
