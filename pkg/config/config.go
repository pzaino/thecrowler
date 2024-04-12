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

type OsFileReader struct{}

func (OsFileReader) ReadFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)
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

// recursiveInclude processes the include statements in the YAML content.
func recursiveInclude(yamlContent string, baseDir string, reader FileReader) (string, error) {
	includePattern := regexp.MustCompile(`include:\s*["']?([^"'\s]+)["']?`)
	matches := includePattern.FindAllStringSubmatch(yamlContent, -1)

	for _, match := range matches {
		includePath := cmn.InterpolateEnvVars(match[1])
		includePath = filepath.Join(baseDir, includePath)

		includedContentBytes, err := reader.ReadFile(includePath)
		if err != nil {
			return "", err
		}

		includedContent := string(includedContentBytes)
		if strings.Contains(includedContent, "include:") {
			includedContent, err = recursiveInclude(includedContent, filepath.Dir(includePath), reader)
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
	interpolatedData := cmn.InterpolateEnvVars(string(data))

	finalData, err := recursiveInclude(interpolatedData, baseDir, OsFileReader{})
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
			Host:              "localhost",
			Port:              8080,
			Timeout:           30,
			ContentSearch:     false,
			ReturnContent:     false,
			SSLMode:           "disable",
			CertFile:          "",
			KeyFile:           "",
			RateLimit:         "10,10",
			EnableConsole:     false,
			ReadHeaderTimeout: 15,
			ReadTimeout:       15,
			WriteTimeout:      30,
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
				ProxyURL:   "",
			},
		},
		RulesetsSchemaPath: "./schemas/ruleset-schema.json",
		Rulesets: []Ruleset{
			{
				Type: "local",
				Path: []string{"./rules/*.json", "./rules/*.yaml"},
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
		HTTPHeaders: HTTPConfig{
			Enabled:      true,
			Timeout:      60,
			SSLDiscovery: true,
		},
		NetworkInfo: NetworkInfo{
			DNS: DNSConfig{
				Enabled:   true,
				Timeout:   10,
				RateLimit: "1",
			},
			WHOIS: WHOISConfig{
				Enabled:   true,
				Timeout:   10,
				RateLimit: "1",
			},
			NetLookup: NetLookupConfig{
				Enabled:   true,
				Timeout:   10,
				RateLimit: "1",
			},
			ServiceScout: ServiceScoutConfig{
				Enabled:          false,
				Timeout:          600,
				OSFingerprinting: false,
				ServiceDetection: true,
				NoDNSResolution:  true,
				ScanDelay:        "1",
			},
			Geolocation: GeoLookupConfig{
				Enabled: false,
				Type:    "maxmind",
				DBPath:  "./data/GeoLite2-City.mmdb",
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
	err = config.Validate()
	if err != nil {
		return Config{}, err
	}

	// cast config.DebugLevel to common.DbgLevel
	var dbgLvl cmn.DbgLevel = cmn.DbgLevel(config.DebugLevel)

	// Set the debug level
	cmn.SetDebugLevel(dbgLvl)

	cmn.DebugMsg(cmn.DbgLvlDebug5, "Configuration file loaded: %#v", config)

	return config, err
}

func (c *Config) Validate() error {
	// Check if the Crawling configuration file contains valid values
	c.validateCrawler()
	c.validateDatabase()
	c.validateAPI()
	c.validateSelenium()
	c.validateRulesets()
	c.validateImageStorageAPI()
	c.validateFileStorageAPI()
	c.validateHTTPHeaders()
	c.validateNetworkInfo()
	c.validateOS()
	c.validateDebugLevel()

	return nil
}

func (c *Config) validateCrawler() {
	if c.Crawler.Workers < 1 {
		c.Crawler.Workers = 1
	}
	if strings.TrimSpace(c.Crawler.Interval) == "" {
		c.Crawler.Interval = "2"
	} else {
		c.Crawler.Interval = strings.TrimSpace(c.Crawler.Interval)
	}
	if c.Crawler.Timeout < 1 {
		c.Crawler.Timeout = 10
	}
	if c.Crawler.Maintenance < 1 {
		c.Crawler.Maintenance = 60
	}
}

func (c *Config) validateDatabase() {
	// Check Database
	if strings.TrimSpace(c.Database.Type) == "" {
		c.Database.Type = "postgres"
	} else {
		c.Database.Type = strings.TrimSpace(c.Database.Type)
	}
	if strings.TrimSpace(c.Database.Host) == "" {
		c.Database.Host = "localhost"
	} else {
		c.Database.Host = strings.TrimSpace(c.Database.Host)
	}
	if c.Database.Port < 1 {
		c.Database.Port = 5432
	}
	if strings.TrimSpace(c.Database.User) == "" {
		c.Database.User = "crowler"
	} else {
		c.Database.User = strings.TrimSpace(c.Database.User)
	}
	if strings.TrimSpace(c.Database.DBName) == "" {
		c.Database.DBName = "SitesIndex"
	} else {
		c.Database.DBName = strings.TrimSpace(c.Database.DBName)
	}
	if c.Database.RetryTime < 1 {
		c.Database.RetryTime = 5
	}
	if c.Database.PingTime < 1 {
		c.Database.PingTime = 5
	}
}

func (c *Config) validateAPI() {
	// Check API
	if strings.TrimSpace(c.API.Host) == "" {
		c.API.Host = "0.0.0.0"
	} else {
		c.API.Host = strings.TrimSpace(c.API.Host)
	}
	if c.API.Port < 1 || c.API.Port > 65535 {
		c.API.Port = 8080
	}
	if c.API.Timeout < 1 {
		c.API.Timeout = 30
	}
	if strings.TrimSpace(c.API.RateLimit) == "" {
		c.API.RateLimit = "10,10"
	} else {
		c.API.RateLimit = strings.TrimSpace(c.API.RateLimit)
	}
	if c.API.ReadHeaderTimeout < 1 {
		c.API.ReadHeaderTimeout = 15
	}
	if c.API.ReadTimeout < 1 {
		c.API.ReadTimeout = 15
	}
	if c.API.WriteTimeout < 1 {
		c.API.WriteTimeout = 30
	}
}

func (c *Config) validateSelenium() {
	// Check Selenium
	for i := range c.Selenium {
		if strings.TrimSpace(c.Selenium[i].Type) == "" {
			c.Selenium[i].Type = "chrome"
		} else {
			c.Selenium[i].Type = strings.TrimSpace(c.Selenium[i].Type)
		}
		if strings.TrimSpace(c.Selenium[i].Path) == "" {
			c.Selenium[i].Path = ""
		} else {
			c.Selenium[i].Path = strings.TrimSpace(c.Selenium[i].Path)
		}
		if strings.TrimSpace(c.Selenium[i].DriverPath) == "" {
			c.Selenium[i].DriverPath = ""
		} else {
			c.Selenium[i].DriverPath = strings.TrimSpace(c.Selenium[i].DriverPath)
		}
		if strings.TrimSpace(c.Selenium[i].Host) == "" {
			c.Selenium[i].Host = "localhost"
		} else {
			c.Selenium[i].Host = strings.TrimSpace(c.Selenium[i].Host)
		}
		if c.Selenium[i].Port < 1 || c.Selenium[i].Port > 65535 {
			c.Selenium[i].Port = 4444
		}
		if c.Selenium[i].ProxyURL == "" {
			c.Selenium[i].ProxyURL = ""
		} else {
			c.Selenium[i].ProxyURL = strings.TrimSpace(c.Selenium[i].ProxyURL)
		}
	}
}

func (c *Config) validateRulesets() {
	// Check Rulesets
	if strings.TrimSpace(c.RulesetsSchemaPath) == "" {
		c.RulesetsSchemaPath = "./schemas/ruleset-schema.json"
	} else {
		c.RulesetsSchemaPath = strings.TrimSpace(c.RulesetsSchemaPath)
	}
	for i := range c.Rulesets {
		if strings.TrimSpace(c.Rulesets[i].Type) == "" {
			c.Rulesets[i].Type = "local"
		} else {
			c.Rulesets[i].Type = strings.TrimSpace(c.Rulesets[i].Type)
		}
		if len(c.Rulesets[i].Path) == 0 {
			c.Rulesets[i].Path = []string{"./rules/*.json", "./rules/*.yaml"}
		}
		for j := range c.Rulesets[i].Path {
			if strings.TrimSpace(c.Rulesets[i].Path[j]) == "" {
				c.Rulesets[i].Path[j] = "./rules/*.json"
			} else {
				c.Rulesets[i].Path[j] = strings.TrimSpace(c.Rulesets[i].Path[j])
			}
		}
	}
}

func (c *Config) validateImageStorageAPI() {
	// Check ImageStorageAPI
	if strings.TrimSpace(c.ImageStorageAPI.Type) == "" {
		c.ImageStorageAPI.Type = "local"
	} else {
		c.ImageStorageAPI.Type = strings.TrimSpace(c.ImageStorageAPI.Type)
	}
	if strings.TrimSpace(c.ImageStorageAPI.Host) == "" {
		c.ImageStorageAPI.Host = ""
	} else {
		c.ImageStorageAPI.Host = strings.TrimSpace(c.ImageStorageAPI.Host)
	}
	if strings.TrimSpace(c.ImageStorageAPI.Path) == "" {
		c.ImageStorageAPI.Path = "./data"
	} else {
		c.ImageStorageAPI.Path = strings.TrimSpace(c.ImageStorageAPI.Path)
	}
	if c.ImageStorageAPI.Port < 1 || c.ImageStorageAPI.Port > 65535 {
		c.ImageStorageAPI.Port = 0
	}
	if strings.TrimSpace(c.ImageStorageAPI.Region) == "" {
		c.ImageStorageAPI.Region = "nowhere"
	} else {
		c.ImageStorageAPI.Region = strings.TrimSpace(c.ImageStorageAPI.Region)
	}
	if strings.TrimSpace(c.ImageStorageAPI.Token) == "" {
		c.ImageStorageAPI.Token = ""
	} else {
		c.ImageStorageAPI.Token = strings.TrimSpace(c.ImageStorageAPI.Token)
	}
	if strings.TrimSpace(c.ImageStorageAPI.Secret) == "" {
		c.ImageStorageAPI.Secret = ""
	} else {
		c.ImageStorageAPI.Secret = strings.TrimSpace(c.ImageStorageAPI.Secret)
	}
	if c.ImageStorageAPI.Timeout < 1 {
		c.ImageStorageAPI.Timeout = 15
	}
}

func (c *Config) validateFileStorageAPI() {
	// Check FileStorageAPI
	if strings.TrimSpace(c.FileStorageAPI.Type) == "" {
		c.FileStorageAPI.Type = "local"
	} else {
		c.FileStorageAPI.Type = strings.TrimSpace(c.FileStorageAPI.Type)
	}
	if strings.TrimSpace(c.FileStorageAPI.Host) == "" {
		c.FileStorageAPI.Host = ""
	} else {
		c.FileStorageAPI.Host = strings.TrimSpace(c.FileStorageAPI.Host)
	}
	if strings.TrimSpace(c.FileStorageAPI.Path) == "" {
		c.FileStorageAPI.Path = "./data"
	} else {
		c.FileStorageAPI.Path = strings.TrimSpace(c.FileStorageAPI.Path)
	}
	if c.FileStorageAPI.Port < 1 || c.FileStorageAPI.Port > 65535 {
		c.FileStorageAPI.Port = 0
	}
	if strings.TrimSpace(c.FileStorageAPI.Region) == "" {
		c.FileStorageAPI.Region = "nowhere"
	} else {
		c.FileStorageAPI.Region = strings.TrimSpace(c.FileStorageAPI.Region)
	}
	if strings.TrimSpace(c.FileStorageAPI.Token) == "" {
		c.FileStorageAPI.Token = ""
	} else {
		c.FileStorageAPI.Token = strings.TrimSpace(c.FileStorageAPI.Token)
	}
	if strings.TrimSpace(c.FileStorageAPI.Secret) == "" {
		c.FileStorageAPI.Secret = ""
	} else {
		c.FileStorageAPI.Secret = strings.TrimSpace(c.FileStorageAPI.Secret)
	}
	if c.FileStorageAPI.Timeout < 1 {
		c.FileStorageAPI.Timeout = 15
	}
}

func (c *Config) validateHTTPHeaders() {
	// Check HTTPHeaders
	if c.HTTPHeaders.Timeout < 1 {
		c.HTTPHeaders.Timeout = 60
	}
}

func (c *Config) validateNetworkInfo() {
	// Check NetworkInfo
	c.NetworkInfo.DNS.validate()
	c.NetworkInfo.WHOIS.validate()
	c.NetworkInfo.NetLookup.validate()
	c.NetworkInfo.ServiceScout.validate()
	c.NetworkInfo.Geolocation.validate()
}

func (c *Config) validateOS() {
	// Check OS
	if strings.TrimSpace(c.OS) == "" {
		c.OS = runtime.GOOS
	} else {
		c.OS = strings.TrimSpace(c.OS)
	}
}

func (c *Config) validateDebugLevel() {
	// Check DebugLevel
	if c.DebugLevel < 0 {
		c.DebugLevel = 0
	}
}

func (c *Config) String() string {
	return fmt.Sprintf("Config{Remote: %v, Database: %v, Crawler: %v, API: %v, Selenium: %v, RulesetsSchemaPath: %v, Rulesets: %v, ImageStorageAPI: %v, FileStorageAPI: %v, HTTPHeaders: %v, NetworkInfo: %v, OS: %v, DebugLevel: %v}",
		c.Remote, c.Database, c.Crawler, c.API, c.Selenium, c.RulesetsSchemaPath, c.Rulesets, c.ImageStorageAPI, c.FileStorageAPI, c.HTTPHeaders, c.NetworkInfo, c.OS, c.DebugLevel)
}

func (c *DNSConfig) validate() {
	if c.Enabled {
		if c.Timeout < 1 {
			c.Timeout = 10
		}
		if strings.TrimSpace(c.RateLimit) == "" {
			c.RateLimit = "1"
		} else {
			c.RateLimit = strings.TrimSpace(c.RateLimit)
		}
	}
}

func (c *WHOISConfig) validate() {
	if c.Enabled {
		if c.Timeout < 1 {
			c.Timeout = 10
		}
		if strings.TrimSpace(c.RateLimit) == "" {
			c.RateLimit = "1"
		} else {
			c.RateLimit = strings.TrimSpace(c.RateLimit)
		}
	}
}

func (c *NetLookupConfig) validate() {
	if c.Enabled {
		if c.Timeout < 1 {
			c.Timeout = 10
		}
		if strings.TrimSpace(c.RateLimit) == "" {
			c.RateLimit = "1"
		} else {
			c.RateLimit = strings.TrimSpace(c.RateLimit)
		}
	}
}

func (c *ServiceScoutConfig) validate() {
	if c.Enabled {
		if c.Timeout < 1 {
			c.Timeout = 600
		}
		if strings.TrimSpace(c.ScanDelay) == "" {
			c.ScanDelay = "1"
		} else {
			c.ScanDelay = strings.TrimSpace(c.ScanDelay)
		}
	}
}

func (c *GeoLookupConfig) validate() {
	if c.Enabled {
		if strings.TrimSpace(c.Type) == "" {
			c.Type = "maxmind"
		} else {
			c.Type = strings.TrimSpace(c.Type)
		}
		if strings.TrimSpace(c.DBPath) == "" {
			c.DBPath = "./data/GeoLite2-City.mmdb"
		} else {
			c.DBPath = strings.TrimSpace(c.DBPath)
		}
	}
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

	if config.HTTPHeaders != (HTTPConfig{}) {
		return false
	}

	if config.NetworkInfo.DNS != (DNSConfig{}) ||
		config.NetworkInfo.WHOIS != (WHOISConfig{}) ||
		config.NetworkInfo.NetLookup != (NetLookupConfig{}) ||
		!config.NetworkInfo.ServiceScout.IsEmpty() ||
		config.NetworkInfo.Geolocation != (GeoLookupConfig{}) {
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

// IsEmpty checks if ServiceScoutConfig is empty.
func (ssc *ServiceScoutConfig) IsEmpty() bool {
	if ssc == nil {
		return true
	}

	if ssc.Enabled {
		return false
	}

	if ssc.Timeout != 0 {
		return false
	}

	if ssc.OSFingerprinting {
		return false
	}

	if ssc.ServiceDetection {
		return false
	}

	return true
}

// isEmpty checks if the given ExecutionPlanItem is empty.
func (ep *ExecutionPlanItem) IsEmpty() bool {
	if ep == nil {
		return true
	}

	if len(ep.Rulesets) == 0 && len(ep.RuleGroups) == 0 && len(ep.Rules) == 0 {
		return true
	}

	return false
}

// isEmpty checks if the given SourceConfig is empty.
func (sc *SourceConfig) IsEmpty() bool {
	if sc == nil {
		return true
	}

	if sc.ExecutionPlan != nil {
		return false
	}

	return true
}
