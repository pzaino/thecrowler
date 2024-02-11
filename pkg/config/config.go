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

	cmn "github.com/pzaino/thecrowler/pkg/common"

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
		Remote: Remote{
			Host:    "localhost",
			Path:    "./",
			Port:    0,
			Region:  "nowhere",
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    "local",
			SSLMode: "disable",
		},
		Database: Database{
			Type:      "postgres",
			Host:      "localhost",
			Port:      5432,
			User:      "postgres",
			Password:  "",
			DBName:    "SitesIndex",
			RetryTime: 5,
			PingTime:  5,
			SSLMode:   "disable",
		},
		Crawler: Crawler{
			Workers:            1,
			Interval:           "2",
			Timeout:            10,
			Maintenance:        60,
			SourceScreenshot:   false,
			FullSiteScreenshot: false,
			MaxDepth:           0,
			Delay:              "0",
			MaxSources:         4,
		},
		API: API{
			Host:          "localhost",
			Port:          8080,
			Timeout:       10,
			ContentSearch: false,
			ReturnContent: false,
			SSLMode:       "disable",
			CertFile:      "",
			KeyFile:       "",
		},
		Selenium: []Selenium{
			{
				Path:       "",
				DriverPath: "",
				Type:       "chrome",
				Port:       4444,
				Host:       "localhost",
				Headless:   true,
				UseService: false,
				SSLMode:    "disable",
			},
		},
		ImageStorageAPI: FileStorageAPI{
			Host:    "",
			Path:    "./data",
			Port:    0,
			Region:  "nowhere",
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    "local",
			SSLMode: "disable",
		},
		FileStorageAPI: FileStorageAPI{
			Host:    "",
			Path:    "./data",
			Port:    0,
			Region:  "nowhere",
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    "local",
			SSLMode: "disable",
		},
		NetworkInfo: NetworkInfo{
			DNS: DNSConfig{
				Enabled:   true,
				Timeout:   10,
				RateLimit: 1,
			},
			WHOIS: WHOISConfig{
				Enabled:   true,
				Timeout:   10,
				RateLimit: 1,
			},
			NetLookup: NetLookupConfig{
				Enabled:   true,
				Timeout:   10,
				RateLimit: 1,
			},
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

	// Check if the configuration file is empty
	if IsEmpty(config) {
		return Config{}, fmt.Errorf("configuration file is empty")
	}

	// Check if the configuration file contains valid values
	//err = config.Validate()
	//if err != nil {
	//	return Config{}, err
	//}

	// cast config.DebugLevel to common.DbgLevel
	var dbgLvl cmn.DbgLevel = cmn.DbgLevel(config.DebugLevel)

	// Set the debug level
	cmn.SetDebugLevel(dbgLvl)

	return config, err
}

// IsEmpty checks if the given config is empty.
// It returns true if the config is empty, false otherwise.
func IsEmpty(config Config) bool {
	// Check if Crawler slice is nil or has zero length
	if config.Crawler != (Crawler{}) {
		return false
	}

	// Add checks for other fields in Config struct
	// Example (assuming there are other fields like `SomeField`):
	if config.Database != (Database{}) {
		return false
	}

	if config.API != (API{}) {
		return false
	}

	if len(config.Selenium) != 0 {
		return false
	}

	if config.ImageStorageAPI != (FileStorageAPI{}) {
		return false
	}

	if config.FileStorageAPI != (FileStorageAPI{}) {
		return false
	}

	if config.NetworkInfo != (NetworkInfo{}) {
		return false
	}

	if config.OS != "" {
		return false
	}

	if config.DebugLevel != 0 {
		return false
	}

	// If all checks pass, the struct is considered empty
	return true
}
