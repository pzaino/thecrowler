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

const (
	JSONRulesDefaultPath  = "./rules/*.json"
	YAMLRulesDefaultPath1 = "./rules/*.yaml"
	YAMLRulesDefaultPath2 = "./rules/*.yml"
	DataDefaultPath       = "./data"
	SSDefaultTimeProfile  = 3
	SSDefaultTimeout      = 3600
	SSDefaultDelayTime    = 100
)

// RemoteFetcher is an interface for fetching remote files.
type RemoteFetcher interface {
	FetchRemoteFile(url string, timeout int, sslMode string) (string, error)
}

type CMNFetcher struct{}

func (f *CMNFetcher) FetchRemoteFile(url string, timeout int, sslMode string) (string, error) {
	return cmn.FetchRemoteFile(url, timeout, sslMode)
}

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
	config := *NewConfig()

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
func NewConfig() *Config {
	return &Config{
		Remote: Remote{
			Host:    "localhost",
			Path:    "/",
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
			Workers:               1,
			Interval:              "2",
			Timeout:               10,
			Maintenance:           60,
			SourceScreenshot:      false,
			FullSiteScreenshot:    false,
			MaxDepth:              0,
			Delay:                 "0",
			MaxSources:            4,
			BrowsingMode:          "recursive",
			CollectHTML:           true,
			CollectContent:        false,
			CollectKeywords:       true,
			CollectMetaTags:       true,
			CollectFiles:          false,
			CollectImages:         false,
			MaxRetries:            0,
			MaxRedirects:          3,
			ReportInterval:        1,
			ScreenshotMaxHeight:   0,
			ScreenshotSectionWait: 2,
			CheckForRobots:        false,
		},
		API: API{
			Host:              "localhost",
			Port:              8080,
			Timeout:           60,
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
				Path:        "",
				DriverPath:  "",
				Type:        "chrome",
				ServiceType: "standalone",
				Port:        4444,
				Host:        "localhost",
				Headless:    true,
				UseService:  false,
				SSLMode:     "disable",
				ProxyURL:    "",
			},
		},
		RulesetsSchemaPath: "./schemas/ruleset-schema.json",
		Rulesets: []Ruleset{
			{
				Type: "local",
				Path: []string{
					JSONRulesDefaultPath,
					YAMLRulesDefaultPath1,
					YAMLRulesDefaultPath2,
				},
			},
		},
		ImageStorageAPI: FileStorageAPI{
			Host:    "",
			Path:    DataDefaultPath,
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
			Path:    DataDefaultPath,
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
				Timeout:          SSDefaultTimeout,
				HostTimeout:      fmt.Sprint((SSDefaultTimeout - (SSDefaultTimeout / 4))),
				OSFingerprinting: false,
				ServiceDetection: true,
				NoDNSResolution:  true,
				MaxPortNumber:    9000,
				ScanDelay:        "", // fmt.Sprint(SSDefaultDelayTime),
				TimingTemplate:   fmt.Sprint(SSDefaultTimeProfile),
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

	if (config.Remote != (Remote{})) && (config.Remote.Type == "remote") {
		// This local configuration references a remote configuration
		// Load the remote configuration
		fetcher := &CMNFetcher{}
		config, err = LoadRemoteConfig(config, fetcher)
		if err != nil {
			return config, err
		}
	}

	// Check if the configuration file contains valid values
	if !config.IsEmpty() {
		err = config.Validate()
		if err != nil {
			return config, err
		}
	}

	// cast config.DebugLevel to common.DbgLevel
	var dbgLvl cmn.DbgLevel = cmn.DbgLevel(config.DebugLevel)

	// Set the debug level
	cmn.SetDebugLevel(dbgLvl)

	cmn.DebugMsg(cmn.DbgLvlDebug5, "Configuration file loaded: %#v", config)

	return config, err
}

// LoadRemoteConfig is responsible for retrieving the configuration file
// from a "remote" distribution server and return the Config struct
func LoadRemoteConfig(cfg Config, fetcher RemoteFetcher) (Config, error) {
	// Create a new configuration object
	config := *NewConfig()

	// Check if the remote configuration is empty
	if cfg.Remote == (Remote{}) {
		return Config{}, fmt.Errorf("remote configuration is empty")
	}

	// Check if the remote configuration contains valid values
	err := cfg.validateRemote()
	if err != nil {
		return Config{}, err
	}

	// Create a RESTClient object
	if strings.TrimSpace(config.Remote.Path) == "/" {
		config.Remote.Path = ""
	}
	url := fmt.Sprintf("http://%s/%s", config.Remote.Host, config.Remote.Path)
	rulesetBody, err := fetcher.FetchRemoteFile(url, config.Remote.Timeout, config.Remote.SSLMode)
	if err != nil {
		return config, fmt.Errorf("failed to fetch rules from %s: %v", url, err)
	}

	// Process ENV variables
	interpolatedData := cmn.InterpolateEnvVars(rulesetBody)

	// If the configuration file has been found and is not empty, unmarshal it
	interpolatedData = strings.TrimSpace(interpolatedData)
	if (interpolatedData != "") && (interpolatedData != "\n") && (interpolatedData != "\r\n") {
		err = yaml.Unmarshal([]byte(interpolatedData), &config)
		if err != nil {
			return config, err
		}
	}

	// Check if the configuration is correct:
	err = config.Validate()
	if err != nil {
		return config, err
	}

	return config, nil
}

// ParseConfig parses the configuration file and returns a Config struct.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	err := yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	err = cfg.Validate()
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks if the configuration file contains valid values.
// Where possible, if a provided value is wrong, it's replaced with a default value.
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

func (c *Config) validateRemote() error {
	c.validateRemoteHost()
	c.validateRemotePath()
	c.validateRemotePort()
	c.validateRemoteRegion()
	c.validateRemoteToken()
	c.validateRemoteSecret()
	c.validateRemoteTimeout()
	c.validateRemoteType()
	c.validateRemoteSSLMode()
	return nil
}

func (c *Config) validateRemoteHost() {
	if strings.TrimSpace(c.Remote.Host) == "" {
		c.Remote.Host = "localhost"
	} else {
		c.Remote.Host = strings.TrimSpace(c.Remote.Host)
	}
}

func (c *Config) validateRemotePath() {
	if strings.TrimSpace(c.Remote.Path) == "" {
		c.Remote.Path = "/"
	} else {
		c.Remote.Path = strings.TrimSpace(c.Remote.Path)
	}
}

func (c *Config) validateRemotePort() {
	if c.Remote.Port < 1 || c.Remote.Port > 65535 {
		c.Remote.Port = 8081
	}
}

func (c *Config) validateRemoteRegion() {
	if strings.TrimSpace(c.Remote.Region) == "" {
		c.Remote.Region = ""
	} else {
		c.Remote.Region = strings.TrimSpace(c.Remote.Region)
	}
}

func (c *Config) validateRemoteToken() {
	if strings.TrimSpace(c.Remote.Token) == "" {
		c.Remote.Token = ""
	} else {
		c.Remote.Token = strings.TrimSpace(c.Remote.Token)
	}
}

func (c *Config) validateRemoteSecret() {
	if strings.TrimSpace(c.Remote.Secret) == "" {
		c.Remote.Secret = ""
	} else {
		c.Remote.Secret = strings.TrimSpace(c.Remote.Secret)
	}
}

func (c *Config) validateRemoteTimeout() {
	if c.Remote.Timeout < 1 {
		c.Remote.Timeout = 15
	}
}

func (c *Config) validateRemoteType() {
	if strings.TrimSpace(c.Remote.Type) == "" {
		c.Remote.Type = "local"
	} else {
		c.Remote.Type = strings.TrimSpace(c.Remote.Type)
	}
}

func (c *Config) validateRemoteSSLMode() {
	if strings.TrimSpace(c.Remote.SSLMode) == "" {
		c.Remote.SSLMode = "disable"
	} else {
		c.Remote.SSLMode = strings.ToLower(strings.TrimSpace(c.Remote.SSLMode))
	}
}

func (c *Config) validateCrawler() {
	c.setDefaultWorkers()
	c.setDefaultInterval()
	c.setDefaultTimeout()
	c.setDefaultMaintenance()
	c.setDefaultMaxDepth()
	c.setDefaultDelay()
	c.setDefaultBrowsingMode()
	c.setDefaultScreenshotSectionWait()
	c.setDefaultMaxSources()
	c.setDefaultReportInterval()
	c.setDefaultScreenshotMaxHeight()
	c.setDefaultMaxRetries()
	c.setDefaultMaxRedirects()
}

func (c *Config) setDefaultWorkers() {
	if c.Crawler.Workers < 1 {
		c.Crawler.Workers = 1
	}
}

func (c *Config) setDefaultInterval() {
	if strings.TrimSpace(c.Crawler.Interval) == "" {
		c.Crawler.Interval = "2"
	} else {
		c.Crawler.Interval = strings.TrimSpace(c.Crawler.Interval)
	}
}

func (c *Config) setDefaultTimeout() {
	if c.Crawler.Timeout < 1 {
		c.Crawler.Timeout = 10
	}
}

func (c *Config) setDefaultMaintenance() {
	if c.Crawler.Maintenance < 1 {
		c.Crawler.Maintenance = 60
	}
}

func (c *Config) setDefaultMaxDepth() {
	if c.Crawler.MaxDepth < 0 {
		c.Crawler.MaxDepth = 0
	}
}

func (c *Config) setDefaultDelay() {
	if strings.TrimSpace(c.Crawler.Delay) == "" {
		c.Crawler.Delay = "random(1, 5)"
	} else {
		c.Crawler.Delay = strings.TrimSpace(c.Crawler.Delay)
	}
}

func (c *Config) setDefaultBrowsingMode() {
	if strings.TrimSpace(c.Crawler.BrowsingMode) == "" {
		c.Crawler.BrowsingMode = "recursive"
	} else {
		c.Crawler.BrowsingMode = strings.TrimSpace(c.Crawler.BrowsingMode)
	}
}

func (c *Config) setDefaultScreenshotSectionWait() {
	if c.Crawler.ScreenshotSectionWait < 0 {
		c.Crawler.ScreenshotSectionWait = 0
	}
}

func (c *Config) setDefaultMaxSources() {
	if c.Crawler.MaxSources < 1 {
		c.Crawler.MaxSources = 1
	}
}

func (c *Config) setDefaultReportInterval() {
	if c.Crawler.ReportInterval < 1 {
		c.Crawler.ReportInterval = 1
	}
}

func (c *Config) setDefaultScreenshotMaxHeight() {
	if c.Crawler.ScreenshotMaxHeight < 0 {
		c.Crawler.ScreenshotMaxHeight = 0
	}
}

func (c *Config) setDefaultMaxRetries() {
	if c.Crawler.MaxRetries < 0 {
		c.Crawler.MaxRetries = 0
	}
}

func (c *Config) setDefaultMaxRedirects() {
	if c.Crawler.MaxRedirects < 0 {
		c.Crawler.MaxRedirects = 0
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
		c.API.Timeout = 60
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
		c.validateSeleniumType(&c.Selenium[i])
		c.validateSeleniumServiceType(&c.Selenium[i])
		c.validateSeleniumPath(&c.Selenium[i])
		c.validateSeleniumDriverPath(&c.Selenium[i])
		c.validateSeleniumHost(&c.Selenium[i])
		c.validateSeleniumPort(&c.Selenium[i])
		c.validateSeleniumProxyURL(&c.Selenium[i])
	}
}

func (c *Config) validateSeleniumType(selenium *Selenium) {
	if strings.TrimSpace(selenium.Type) == "" {
		selenium.Type = "chrome"
	} else {
		selenium.Type = strings.TrimSpace(selenium.Type)
	}
}

func (c *Config) validateSeleniumServiceType(selenium *Selenium) {
	if strings.TrimSpace(selenium.ServiceType) == "" {
		selenium.ServiceType = "standalone"
	} else {
		selenium.ServiceType = strings.TrimSpace(selenium.ServiceType)
	}
}

func (c *Config) validateSeleniumPath(selenium *Selenium) {
	if strings.TrimSpace(selenium.Path) == "" {
		selenium.Path = ""
	} else {
		selenium.Path = strings.TrimSpace(selenium.Path)
	}
}

func (c *Config) validateSeleniumDriverPath(selenium *Selenium) {
	if strings.TrimSpace(selenium.DriverPath) == "" {
		selenium.DriverPath = ""
	} else {
		selenium.DriverPath = strings.TrimSpace(selenium.DriverPath)
	}
}

func (c *Config) validateSeleniumHost(selenium *Selenium) {
	if strings.TrimSpace(selenium.Host) == "" {
		selenium.Host = "localhost"
	} else {
		selenium.Host = strings.TrimSpace(selenium.Host)
	}
}

func (c *Config) validateSeleniumPort(selenium *Selenium) {
	if selenium.Port < 1 || selenium.Port > 65535 {
		selenium.Port = 4444
	}
}

func (c *Config) validateSeleniumProxyURL(selenium *Selenium) {
	if selenium.ProxyURL == "" {
		selenium.ProxyURL = ""
	} else {
		selenium.ProxyURL = strings.TrimSpace(selenium.ProxyURL)
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
			c.Rulesets[i].Path = []string{JSONRulesDefaultPath, YAMLRulesDefaultPath1, YAMLRulesDefaultPath2}
		}
		for j := range c.Rulesets[i].Path {
			if strings.TrimSpace(c.Rulesets[i].Path[j]) == "" {
				c.Rulesets[i].Path[j] = JSONRulesDefaultPath
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
		c.ImageStorageAPI.Path = DataDefaultPath
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
		c.FileStorageAPI.Path = DataDefaultPath
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
		c.validateTimeout()
		c.validateHostTimeout()
		c.validateScanDelay()
		c.validateMaxPortNumber()
		c.validateTimingTemplate()
	}
}

func (c *ServiceScoutConfig) validateTimeout() {
	if c.Timeout < 1 {
		c.Timeout = SSDefaultTimeout
	}
}

func (c *ServiceScoutConfig) validateHostTimeout() {
	if strings.TrimSpace(c.HostTimeout) == "" {
		c.HostTimeout = fmt.Sprint((c.Timeout - (c.Timeout / 4)))
	} else {
		c.HostTimeout = strings.TrimSpace(c.HostTimeout)
	}
}

func (c *ServiceScoutConfig) validateScanDelay() {
	if strings.TrimSpace(c.ScanDelay) == "" {
		c.ScanDelay = "" // fmt.Sprint(SSDefaultDelayTime)
	} else {
		c.ScanDelay = strings.TrimSpace(c.ScanDelay)
	}
}

func (c *ServiceScoutConfig) validateMaxPortNumber() {
	if c.MaxPortNumber < 1 {
		c.MaxPortNumber = 9000
	}
	if c.MaxPortNumber > 65535 {
		c.MaxPortNumber = 65535
	}
}

func (c *ServiceScoutConfig) validateTimingTemplate() {
	if strings.TrimSpace(c.TimingTemplate) == "" {
		c.TimingTemplate = fmt.Sprint(SSDefaultTimeProfile)
	} else {
		c.TimingTemplate = strings.TrimSpace(c.TimingTemplate)
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

func (cfg *Config) IsEmpty() bool {
	if cfg == nil {
		return true
	}

	if cfg.Remote != (Remote{}) {
		return false
	}

	if cfg.Database != (Database{}) {
		return false
	}

	if cfg.Crawler != (Crawler{}) {
		return false
	}

	if cfg.API != (API{}) {
		return false
	}

	if len(cfg.Selenium) != 0 {
		return false
	}

	if cfg.ImageStorageAPI != (FileStorageAPI{}) {
		return false
	}

	if cfg.FileStorageAPI != (FileStorageAPI{}) {
		return false
	}

	if cfg.HTTPHeaders != (HTTPConfig{}) {
		return false
	}

	if cfg.NetworkInfo.DNS != (DNSConfig{}) ||
		cfg.NetworkInfo.WHOIS != (WHOISConfig{}) ||
		cfg.NetworkInfo.NetLookup != (NetLookupConfig{}) ||
		!cfg.NetworkInfo.ServiceScout.IsEmpty() ||
		cfg.NetworkInfo.Geolocation != (GeoLookupConfig{}) {
		return false
	}

	if cfg.OS != "" {
		return false
	}

	if cfg.DebugLevel != 0 {
		return false
	}

	return true
}
