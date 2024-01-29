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

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gopkg.in/yaml.v2"
)

// fileExists checks if a file exists at the given filename.
// It returns true if the file exists and is not a directory, and false otherwise.
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// interpolateEnvVars replaces occurrences of `${VAR}` or `$VAR` in the input string
// with the value of the VAR environment variable.
func interpolateEnvVars(input string) string {
	envVarPattern := regexp.MustCompile(`\$\{?(\w+)\}?`)
	return envVarPattern.ReplaceAllStringFunc(input, func(varName string) string {
		// Trim ${ and } from varName
		trimmedVarName := varName
		trimmedVarName = strings.TrimPrefix(trimmedVarName, "${")
		trimmedVarName = strings.TrimSuffix(trimmedVarName, "}")

		// Return the environment variable value
		return os.Getenv(trimmedVarName)
	})
}

// recursiveInclude processes the "include" directives in YAML files.
// It supports environment variable interpolation in file paths.
func recursiveInclude(yamlContent string, baseDir string) (string, error) {
	includePattern := regexp.MustCompile(`include:\s*["']?([^"'\s]+)["']?`)
	matches := includePattern.FindAllStringSubmatch(yamlContent, -1)

	for _, match := range matches {
		includePath := interpolateEnvVars(match[1])
		includePath = filepath.Join(baseDir, includePath)

		includedContentBytes, err := os.ReadFile(includePath)
		if err != nil {
			return "", err
		}

		includedContent := string(includedContentBytes)
		if strings.Contains(includedContent, "include:") {
			includedContent, err = recursiveInclude(includedContent, filepath.Dir(includePath))
			if err != nil {
				return "", err
			}
		}

		yamlContent = strings.Replace(yamlContent, match[0], includedContent, 1)
	}

	return yamlContent, nil
}

// getConfigFile reads and unmarshals a configuration file with the given name.
// It checks if the file exists, reads its contents, and unmarshals it into a Config struct.
// If the file does not exist or an error occurs during reading or unmarshaling, an error is returned.
func getConfigFile(confName string) (Config, error) {

	// Create a new configuration object
	config := NewConfig()

	// Check if the configuration file exists
	if !fileExists(confName) {
		return Config{}, fmt.Errorf("file does not exist: %s", confName)
	}

	// Read the configuration file
	data, err := os.ReadFile(confName)
	if err != nil {
		return Config{}, err
	}

	baseDir := filepath.Dir(confName)

	// Interpolate environment variables and process includes
	interpolatedData := interpolateEnvVars(string(data))

	finalData, err := recursiveInclude(interpolatedData, baseDir)
	if err != nil {
		return Config{}, err
	}

	// If the configuration file has been found and is not empty, unmarshal it
	if (finalData != "") && (finalData != "\n") && (finalData != "\r\n") {
		err = yaml.Unmarshal([]byte(finalData), &config)
	}
	return config, err
}

// NewConfig returns a new Config struct with default values.
func NewConfig() Config {
	return Config{
		Database: struct {
			Type      string `yaml:"type"`
			Host      string `yaml:"host"`
			Port      int    `yaml:"port"`
			User      string `yaml:"user"`
			Password  string `yaml:"password"`
			DBName    string `yaml:"dbname"`
			RetryTime int    `yaml:"retry_time"`
			PingTime  int    `yaml:"ping_time"`
			SSLMode   string `yaml:"sslmode"`
		}{
			Type:      "postgres",
			Host:      "localhost",
			Port:      5432,
			User:      "postgres",
			Password:  "postgres",
			DBName:    "SitesIndex",
			RetryTime: 5,
			PingTime:  5,
			SSLMode:   "disable",
		},
		Crawler: struct {
			Workers            int  `yaml:"workers"`
			Interval           int  `yaml:"interval"`
			Timeout            int  `yaml:"timeout"`
			Maintenance        int  `yaml:"maintenance"`
			SourceScreenshot   bool `yaml:"source_screenshot"`
			FullSiteScreenshot bool `yaml:"full_site_screenshot"`
			MaxDepth           int  `yaml:"max_depth"`
		}{
			Workers:            1,
			Interval:           2,
			Timeout:            10,
			Maintenance:        60,
			SourceScreenshot:   false,
			FullSiteScreenshot: false,
			MaxDepth:           0,
		},
		API: struct {
			Host          string `yaml:"host"`
			Port          int    `yaml:"port"`
			Timeout       int    `yaml:"timeout"`
			ContentSearch bool   `yaml:"content_search"`
		}{
			Host:          "localhost",
			Port:          8080,
			Timeout:       10,
			ContentSearch: false,
		},
		Selenium: struct {
			Path       string `yaml:"path"`
			DriverPath string `yaml:"driver_path"`
			Type       string `yaml:"type"`
			Port       int    `yaml:"port"`
			Host       string `yaml:"host"`
			Headless   bool   `yaml:"headless"`
		}{
			Path:       "/path/to/selenium",
			DriverPath: "/path/to/driver",
			Type:       "chrome",
			Port:       4444,
			Host:       "localhost",
			Headless:   true,
		},
		ImageStorageAPI: FileStorageAPI{
			Host:    "",
			Path:    "./images",
			Port:    0,
			Region:  "nowhere",
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    "local",
		},
		OS:         runtime.GOOS,
		DebugLevel: 0,
	}
}

// LoadConfig is responsible for loading the configuration file
// and return the Config struct
func LoadConfig(confName string) (Config, error) {

	// Get the configuration file
	config, err := getConfigFile(confName)
	if err != nil {
		return Config{}, err
	}

	return config, err
}

// IsEmpty checks if the given config is empty.
// It returns true if the config is empty, false otherwise.
func IsEmpty(config Config) bool {
	return config == Config{}
}
