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

// The config package contains the configuration file parsing logic.
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
	var config Config
	if (finalData != "") && (finalData != "\n") && (finalData != "\r\n") {
		err = yaml.Unmarshal([]byte(finalData), &config)
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
