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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"gopkg.in/yaml.v2"
)

const (
	//nolint:errcheck // Don't lint for error not checked, this is a defer statement
	// PluginsDefaultPath Default plugins path
	PluginsDefaultPath = "./plugins/*.js"
	// JSONRulesDefaultPath Default rules path for json rules
	JSONRulesDefaultPath = "./rules/*.json"
	// YAMLRulesDefaultPath1 Default rules path for yaml rules
	YAMLRulesDefaultPath1 = "./rules/*.yaml"
	// YAMLRulesDefaultPath2 Default rules path for yaml rules
	YAMLRulesDefaultPath2 = "./rules/*.yml"
	// DataDefaultPath Default data path
	DataDefaultPath = "./data"
	// SSDefaultTimeProfile Default time profile for service scout
	SSDefaultTimeProfile = 3
	// SSDefaultTimeout Default timeout for service scout
	SSDefaultTimeout = 3600
	// SSDefaultDelayTime Default delay time for service scout
	SSDefaultDelayTime = 100

	stdRateLimit = "10,10"
)

// RemoteFetcher is an interface for fetching remote files.
type RemoteFetcher interface {
	FetchRemoteFile(url string, timeout int, sslMode string) (string, error)
}

// CMNFetcher is a fetcher that uses the common package to fetch remote files.
type CMNFetcher struct{}

// FetchRemoteFile fetches a remote file using the common package.
func (f *CMNFetcher) FetchRemoteFile(url string, timeout int, sslMode string) (string, error) {
	return cmn.FetchRemoteFile(url, timeout, sslMode)
}

// OsFileReader is a file reader that uses the os package to read files.
type OsFileReader struct{}

// ReadFile reads a file using the os package.
func (OsFileReader) ReadFile(filename string) ([]byte, error) {
	return os.ReadFile(filename) //nolint:gosec // This path is configured by the service owner
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
	data, err := os.ReadFile(confName) //nolint:gosec // This path is configured by the service owner
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
			Host:    cmn.LoalhostStr,
			Path:    "/",
			Port:    0,
			Region:  cmn.NowhereStr,
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    cmn.LocalStr,
			SSLMode: cmn.DisableStr,
		},
		Database: Database{
			Type:         "postgres",
			Host:         cmn.LoalhostStr,
			Port:         5432,
			User:         "postgres",
			Password:     "",
			DBName:       "SitesIndex",
			RetryTime:    5,
			PingTime:     5,
			SSLMode:      cmn.DisableStr,
			OptimizeFor:  "",
			MaxConns:     100,
			MaxIdleConns: 75,
		},
		Crawler: Crawler{
			Workers:               1,
			VDIName:               "",
			Platform:              "desktop",
			BrowserPlatform:       "linux",
			Interval:              "2",
			Timeout:               10,
			Maintenance:           60,
			SourceScreenshot:      false,
			FullSiteScreenshot:    false,
			MaxDepth:              0,
			MaxLinks:              0,
			CrawlingInterval:      "",
			CrawlingIfError:       "",
			CrawlingIfOk:          "",
			ProcessingTimeout:     "1 day",
			Delay:                 "0",
			MaxSources:            4,
			BrowsingMode:          "recursive",
			ResetCookiesPolicy:    "never",
			NoThirdPartyCookies:   false,
			RequestImages:         true,
			RequestCSS:            true,
			RequestScripts:        true,
			RequestPlugins:        true,
			RequestFrames:         true,
			CollectHTML:           true,
			CollectContent:        false,
			CollectKeywords:       true,
			CollectMetaTags:       true,
			CollectFiles:          false,
			CollectImages:         false,
			CollectPerfMetrics:    true,
			CollectPageEvents:     true,
			CollectXHR:            false,
			CollectLinks:          true,
			CreateEventWhenDone:   false,
			MaxRetries:            0,
			MaxRedirects:          3,
			ReportInterval:        1,
			ScreenshotMaxHeight:   0,
			ScreenshotSectionWait: 2,
			CheckForRobots:        false,
			Control: ControlConfig{
				Host:              cmn.LoalhostStr,
				Port:              8081,
				SSLMode:           cmn.DisableStr,
				Timeout:           15,
				RateLimit:         stdRateLimit,
				ReadHeaderTimeout: 15,
				ReadTimeout:       15,
				WriteTimeout:      30,
			},
		},
		API: API{
			Host:              cmn.LoalhostStr,
			Port:              8080,
			Timeout:           60,
			ContentSearch:     false,
			ReturnContent:     false,
			SSLMode:           cmn.DisableStr,
			CertFile:          "",
			KeyFile:           "",
			RateLimit:         stdRateLimit,
			EnableConsole:     false,
			ReadHeaderTimeout: 15,
			ReadTimeout:       15,
			WriteTimeout:      30,
		},
		Selenium: []Selenium{
			{
				Name:        "vdi-1",
				Path:        "",
				DriverPath:  "",
				Type:        "chrome",
				ServiceType: "standalone",
				Port:        4444,
				Host:        cmn.LoalhostStr,
				Headless:    true,
				UseService:  false,
				SSLMode:     cmn.DisableStr,
				ProxyURL:    "",
				SysMng: SysMngConfig{
					Port:              4443,
					SSLMode:           cmn.DisableStr,
					Timeout:           60,
					CertFile:          "",
					KeyFile:           "",
					RateLimit:         stdRateLimit,
					ReadHeaderTimeout: 15,
					ReadTimeout:       15,
					WriteTimeout:      30,
				},
			},
		},
		Prometheus: PrometheusConfig{
			Enabled: false,
			Port:    9091,
		},
		Events: EventsConfig{
			Host:              cmn.LoalhostStr,
			Port:              8082,
			SSLMode:           cmn.DisableStr,
			Timeout:           60,
			CertFile:          "",
			KeyFile:           "",
			RateLimit:         stdRateLimit,
			ReadHeaderTimeout: 15,
			ReadTimeout:       15,
			WriteTimeout:      30,
		},
		ImageStorageAPI: FileStorageAPI{
			Host:    "",
			Path:    DataDefaultPath,
			Port:    0,
			Region:  cmn.NowhereStr,
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    cmn.LocalStr,
			SSLMode: cmn.DisableStr,
		},
		FileStorageAPI: FileStorageAPI{
			Host:    "",
			Path:    DataDefaultPath,
			Port:    0,
			Region:  cmn.NowhereStr,
			Token:   "",
			Secret:  "",
			Timeout: 15,
			Type:    cmn.LocalStr,
			SSLMode: cmn.DisableStr,
		},
		HTTPHeaders: HTTPConfig{
			Enabled: true,
			Timeout: 60,
			SSLDiscovery: SSLScoutConfig{
				Enabled:     true,
				JARM:        false,
				JA3:         false,
				JA3S:        true,
				JA4:         false,
				JA4S:        true,
				HASSH:       false,
				HASSHServer: true,
				TLSH:        true,
				SimHash:     true,
				MinHash:     true,
				BLAKE2:      true,
				SHA256:      true,
				CityHash:    true,
				MurmurHash:  true,
				CustomTLS:   true,
			},
			Proxies: []SOCKSProxy{},
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
				ScanDelay:        "",
				TimingTemplate:   fmt.Sprint(SSDefaultTimeProfile),
				IPFragment:       true,
				UDPScan:          false,
				DNSServers:       []string{},
			},
			Geolocation: GeoLookupConfig{
				Enabled: false,
				Type:    "maxmind",
				DBPath:  "./data/GeoLite2-City.mmdb",
			},
			HostPlatform: PlatformInfo{
				OSName:    runtime.GOOS,
				OSArch:    runtime.GOARCH,
				OSVersion: "",
			},
		},
		RulesetsSchemaPath: "./schemas/ruleset-schema.json",
		Rulesets: []RulesetConfig{
			{
				Type: cmn.LocalStr,
				Path: []string{
					JSONRulesDefaultPath,
					YAMLRulesDefaultPath1,
					YAMLRulesDefaultPath2,
				},
			},
		},
		Plugins: PluginsConfig{
			PluginsTimeout: 15,
			Plugins: []PluginConfig{{
				Type: cmn.LocalStr,
				Path: []string{
					PluginsDefaultPath,
				}},
			},
		},
		Agents: []AgentsConfig{
			{
				Timeout:        10,
				AgentsTimeout:  90,
				PluginsTimeout: 60,
				Path: []string{
					"./agents/*.yaml",
				},
			},
		},
		ExternalDetection: ExternalDetectionConfig{},
		OS:                runtime.GOOS,
		DebugLevel:        0,
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
	c.validateVDI()
	c.validatePrometheus()
	c.validateImageStorageAPI()
	c.validateFileStorageAPI()
	c.validateHTTPHeaders()
	c.validateNetworkInfo()
	c.validateRulesets()
	c.validatePlugins()
	c.validateAgents()
	c.validateExternalDetection()
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
		c.Remote.Host = cmn.LoalhostStr
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
		c.Remote.Type = cmn.LocalStr
	} else {
		c.Remote.Type = strings.TrimSpace(c.Remote.Type)
	}
}

func (c *Config) validateRemoteSSLMode() {
	if strings.TrimSpace(c.Remote.SSLMode) == "" {
		c.Remote.SSLMode = cmn.DisableStr
	} else {
		c.Remote.SSLMode = strings.ToLower(strings.TrimSpace(c.Remote.SSLMode))
	}
}

func (c *Config) validateCrawler() {
	c.setDefaultWorkers()
	c.setDefaultVDIName()
	c.setDefaultPlatform()
	c.setDefaultInterval()
	c.setDefaultTimeout()
	c.setDefaultMaintenance()
	c.setDefaultCrawlingInterval()
	c.setDefaultCrawlingIfError()
	c.setDefaultCrawlingIfOk()
	c.setProcessingTimeout()
	c.setDefaultMaxDepth()
	c.setDefaultDelay()
	c.setDefaultBrowsingMode()
	c.setDefaultScreenshotSectionWait()
	c.setDefaultMaxSources()
	c.setDefaultReportInterval()
	c.setDefaultScreenshotMaxHeight()
	c.setDefaultMaxRetries()
	c.setDefaultMaxRedirects()
	c.setDefaultResetCookiesPolicy()
	c.setDefaultControl()
}

func (c *Config) setDefaultWorkers() {
	if c.Crawler.Workers < 1 {
		c.Crawler.Workers = 1
	}
}

func (c *Config) setDefaultVDIName() {
	if strings.TrimSpace(c.Crawler.VDIName) == "" {
		c.Crawler.VDIName = ""
	} else {
		c.Crawler.VDIName = strings.ToLower(strings.TrimSpace(c.Crawler.VDIName))
	}
}

func (c *Config) setDefaultPlatform() {
	if strings.TrimSpace(c.Crawler.Platform) == "" {
		c.Crawler.Platform = "desktop"
	} else {
		c.Crawler.Platform = strings.ToLower(strings.TrimSpace(c.Crawler.Platform))
	}
	// Check also BrowserPlatform
	if strings.TrimSpace(c.Crawler.BrowserPlatform) == "" {
		c.Crawler.BrowserPlatform = "linux"
	} else {
		c.Crawler.BrowserPlatform = strings.ToLower(strings.TrimSpace(c.Crawler.BrowserPlatform))
	}
}

func (c *Config) setDefaultInterval() {
	if strings.TrimSpace(c.Crawler.Interval) == "" {
		c.Crawler.Interval = "2"
	} else {
		c.Crawler.Interval = strings.TrimSpace(c.Crawler.Interval)
	}
}

func (c *Config) setDefaultCrawlingInterval() {
	if strings.TrimSpace(c.Crawler.CrawlingInterval) == "" {
		c.Crawler.CrawlingInterval = ""
	} else {
		c.Crawler.CrawlingInterval = strings.ToLower(strings.TrimSpace(c.Crawler.CrawlingInterval))
	}
}

func (c *Config) setDefaultCrawlingIfError() {
	if strings.TrimSpace(c.Crawler.CrawlingIfError) == "" {
		c.Crawler.CrawlingIfError = ""
	} else {
		c.Crawler.CrawlingIfError = strings.ToLower(strings.TrimSpace(c.Crawler.CrawlingIfError))
	}
}

func (c *Config) setDefaultCrawlingIfOk() {
	if strings.TrimSpace(c.Crawler.CrawlingIfOk) == "" {
		c.Crawler.CrawlingIfOk = ""
	} else {
		c.Crawler.CrawlingIfOk = strings.ToLower(strings.TrimSpace(c.Crawler.CrawlingIfOk))
	}
}

func (c *Config) setProcessingTimeout() {
	if strings.TrimSpace(c.Crawler.ProcessingTimeout) == "" {
		c.Crawler.ProcessingTimeout = "1 day"
	} else {
		c.Crawler.ProcessingTimeout = strings.TrimSpace(c.Crawler.ProcessingTimeout)
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

func (c *Config) setDefaultResetCookiesPolicy() {
	if strings.TrimSpace(c.Crawler.ResetCookiesPolicy) == "" {
		c.Crawler.ResetCookiesPolicy = "never"
	} else {
		c.Crawler.ResetCookiesPolicy = strings.ToLower(strings.TrimSpace(c.Crawler.ResetCookiesPolicy))
	}
}

func (c *Config) setDefaultControl() {
	if c.Crawler.Control.Port < 1 || c.Crawler.Control.Port > 65535 {
		c.Crawler.Control.Port = 8081
	}
	if strings.TrimSpace(c.Crawler.Control.Host) == "" {
		c.Crawler.Control.Host = cmn.LoalhostStr
	} else {
		c.Crawler.Control.Host = strings.TrimSpace(c.Crawler.Control.Host)
	}
	if strings.TrimSpace(c.Crawler.Control.RateLimit) == "" {
		c.Crawler.Control.RateLimit = stdRateLimit
	} else {
		c.Crawler.Control.RateLimit = strings.TrimSpace(c.Crawler.Control.RateLimit)
	}
	if c.Crawler.Control.Timeout < 1 {
		c.Crawler.Control.Timeout = 15
	}
	if c.Crawler.Control.ReadHeaderTimeout < 1 {
		c.Crawler.Control.ReadHeaderTimeout = 15
	}
	if c.Crawler.Control.ReadTimeout < 1 {
		c.Crawler.Control.ReadTimeout = 15
	}
	if c.Crawler.Control.WriteTimeout < 1 {
		c.Crawler.Control.WriteTimeout = 30
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
		c.Database.Host = cmn.LoalhostStr
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
	if strings.TrimSpace(c.Database.SSLMode) == "" {
		c.Database.SSLMode = cmn.DisableStr
	} else {
		c.Database.SSLMode = strings.ToLower(strings.TrimSpace(c.Database.SSLMode))
	}
	if strings.TrimSpace(c.Database.OptimizeFor) == "" {
		c.Database.OptimizeFor = ""
	} else {
		c.Database.OptimizeFor = strings.TrimSpace(c.Database.OptimizeFor)
	}
	if c.Database.MaxConns < 1 {
		c.Database.MaxConns = 100
	}
	if c.Database.MaxIdleConns < 1 {
		c.Database.MaxIdleConns = 75
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
		c.API.RateLimit = stdRateLimit
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

func (c *Config) validateVDI() {
	// Check Selenium
	for i := range c.Selenium {
		c.validateVDIName(&c.Selenium[i], i)
		c.validateVDIType(&c.Selenium[i])
		c.validateVDIServiceType(&c.Selenium[i])
		c.validateVDIPath(&c.Selenium[i])
		c.validateVDIDriverPath(&c.Selenium[i])
		c.validateVDIHost(&c.Selenium[i])
		c.validateVDIPort(&c.Selenium[i])
		c.validateVDIProxyURL(&c.Selenium[i])
	}
}

func (c *Config) validateVDIName(selenium *Selenium, index int) {
	if strings.TrimSpace(selenium.Name) == "" {
		selenium.Name = selenium.Host + ":" + fmt.Sprint(selenium.Port) + ":" + fmt.Sprint(index)
	} else {
		selenium.Name = strings.ToLower(strings.TrimSpace(selenium.Name))
	}
}

func (c *Config) validateVDIType(selenium *Selenium) {
	if strings.TrimSpace(selenium.Type) == "" {
		selenium.Type = "chrome"
	} else {
		selenium.Type = strings.TrimSpace(selenium.Type)
	}
}

func (c *Config) validateVDIServiceType(selenium *Selenium) {
	if strings.TrimSpace(selenium.ServiceType) == "" {
		selenium.ServiceType = "standalone"
	} else {
		selenium.ServiceType = strings.TrimSpace(selenium.ServiceType)
	}
}

func (c *Config) validateVDIPath(selenium *Selenium) {
	if strings.TrimSpace(selenium.Path) == "" {
		selenium.Path = ""
	} else {
		selenium.Path = strings.TrimSpace(selenium.Path)
	}
}

func (c *Config) validateVDIDriverPath(selenium *Selenium) {
	if strings.TrimSpace(selenium.DriverPath) == "" {
		selenium.DriverPath = ""
	} else {
		selenium.DriverPath = strings.TrimSpace(selenium.DriverPath)
	}
}

func (c *Config) validateVDIHost(selenium *Selenium) {
	if strings.TrimSpace(selenium.Host) == "" {
		selenium.Host = cmn.LoalhostStr
	} else {
		selenium.Host = strings.TrimSpace(selenium.Host)
	}
}

func (c *Config) validateVDIPort(selenium *Selenium) {
	if selenium.Port < 1 || selenium.Port > 65535 {
		selenium.Port = 4444
	}
}

func (c *Config) validateVDIProxyURL(selenium *Selenium) {
	if selenium.ProxyURL == "" {
		selenium.ProxyURL = ""
	} else {
		selenium.ProxyURL = strings.TrimSpace(selenium.ProxyURL)
	}
}

func (c *Config) validatePrometheus() {
	// Check Prometheus
	if c.Prometheus.Port < 1 || c.Prometheus.Port > 65535 {
		c.Prometheus.Port = 9090
	}
	if strings.TrimSpace(c.Prometheus.Host) == "" {
		c.Prometheus.Host = cmn.LoalhostStr
	} else {
		c.Prometheus.Host = strings.TrimSpace(c.Prometheus.Host)
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
			c.Rulesets[i].Type = cmn.LocalStr
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

func (c *Config) validatePlugins() {
	// Check Plugins
	if c.Plugins.PluginsTimeout < 1 {
		c.Plugins.PluginsTimeout = 15
	}
	for i := range c.Plugins.Plugins {
		if strings.TrimSpace(c.Plugins.Plugins[i].Type) == "" {
			c.Plugins.Plugins[i].Type = cmn.LocalStr
		} else {
			c.Plugins.Plugins[i].Type = strings.TrimSpace(c.Plugins.Plugins[i].Type)
		}
		if len(c.Plugins.Plugins[i].Path) == 0 && c.Plugins.Plugins[i].Type == cmn.LocalStr {
			c.Plugins.Plugins[i].Path = []string{PluginsDefaultPath}
		}
	}
}

func (c *Config) validateAgents() {
	// Check Agents
	for i := range c.Agents {
		if strings.TrimSpace(c.Agents[i].Type) == "" {
			c.Agents[i].Type = cmn.LocalStr
		} else {
			c.Agents[i].Type = strings.TrimSpace(c.Agents[i].Type)
		}
		if c.Agents[i].Timeout == 0 {
			c.Agents[i].Timeout = 10
		}
		if c.Agents[i].Timeout < 10 {
			c.Agents[i].Timeout = 10
		}
		if c.Agents[i].AgentsTimeout == 0 {
			c.Agents[i].AgentsTimeout = 90
		}
		if c.Agents[i].AgentsTimeout < 30 {
			c.Agents[i].AgentsTimeout = 30
		}
		if c.Agents[i].PluginsTimeout == 0 {
			c.Agents[i].PluginsTimeout = 60
		}
		if c.Agents[i].PluginsTimeout < 30 {
			c.Agents[i].PluginsTimeout = 30
		}
		if len(c.Agents[i].Path) == 0 && c.Agents[i].Type == cmn.LocalStr {
			c.Agents[i].Path = []string{"./agents/*.yaml"}
		}
		for j := range c.Agents[i].Path {
			if strings.TrimSpace(c.Agents[i].Path[j]) == "" {
				c.Agents[i].Path[j] = "./agents/*.yaml"
			} else {
				c.Agents[i].Path[j] = strings.TrimSpace(c.Agents[i].Path[j])
			}
		}
	}
}

func (c *Config) validateExternalDetection() {
	// Check ExternalDetection Timeout
	if c.ExternalDetection.Timeout < 1 {
		c.ExternalDetection.Timeout = 60
	}
	// Check ExternalDetection Delay
	if strings.TrimSpace(c.ExternalDetection.Delay) == "" {
		c.ExternalDetection.Delay = "1"
	} else {
		c.ExternalDetection.Delay = strings.TrimSpace(c.ExternalDetection.Delay)
	}
	// Check ExternalDetection MaxRetries
	if c.ExternalDetection.MaxRetries < 0 {
		c.ExternalDetection.MaxRetries = 0
	}
	// Check ExternalDetection MaxRequests
	if c.ExternalDetection.MaxRequests < 1 {
		c.ExternalDetection.MaxRequests = 1
	}
}

func (c *Config) validateImageStorageAPI() {
	// Check ImageStorageAPI
	if strings.TrimSpace(c.ImageStorageAPI.Type) == "" {
		c.ImageStorageAPI.Type = cmn.LocalStr
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
		c.ImageStorageAPI.Region = cmn.NowhereStr
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
		c.FileStorageAPI.Type = cmn.LocalStr
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
		c.FileStorageAPI.Region = cmn.NowhereStr
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
	if !config.Crawler.IsEmpty() {
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

	if !config.HTTPHeaders.IsEmpty() {
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
func (c *ServiceScoutConfig) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Enabled {
		return false
	}

	if c.Timeout != 0 {
		return false
	}

	if c.OSFingerprinting {
		return false
	}

	if c.ServiceDetection {
		return false
	}

	return true
}

// IsEmpty checks if the given ExecutionPlanItem is empty.
func (ep *ExecutionPlanItem) IsEmpty() bool {
	if ep == nil {
		return true
	}

	if len(ep.Rulesets) == 0 && len(ep.RuleGroups) == 0 && len(ep.Rules) == 0 {
		return true
	}

	return false
}

// IsEmpty checks if the given SourceConfig is empty.
func (sc *SourceConfig) IsEmpty() bool {
	if sc == nil {
		return true
	}

	if sc.ExecutionPlan != nil {
		return false
	}

	return true
}

// IsEmpty checks if the given Config is empty.
func (c *Config) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Remote != (Remote{}) {
		return false
	}

	if c.Database != (Database{}) {
		return false
	}

	if !c.Crawler.IsEmpty() {
		return false
	}

	if c.API != (API{}) {
		return false
	}

	if len(c.Selenium) != 0 {
		return false
	}

	if c.ImageStorageAPI != (FileStorageAPI{}) {
		return false
	}

	if c.FileStorageAPI != (FileStorageAPI{}) {
		return false
	}

	if !c.HTTPHeaders.IsEmpty() {
		return false
	}

	if c.NetworkInfo.DNS != (DNSConfig{}) ||
		c.NetworkInfo.WHOIS != (WHOISConfig{}) ||
		c.NetworkInfo.NetLookup != (NetLookupConfig{}) ||
		!c.NetworkInfo.ServiceScout.IsEmpty() ||
		c.NetworkInfo.Geolocation != (GeoLookupConfig{}) {
		return false
	}

	if c.OS != "" {
		return false
	}

	if c.DebugLevel != 0 {
		return false
	}

	return true
}

// IsEmpty checks if the given SSLScoutConfig is empty.
func (c *SSLScoutConfig) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Enabled {
		return false
	}

	return true
}

// IsEmpty checks if the given DNSConfig is empty.
func (c *DNSConfig) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Enabled {
		return false
	}

	if c.Timeout != 0 {
		return false
	}

	if c.RateLimit != "" {
		return false
	}

	return true
}

// IsEmpty checks if the given WHOISConfig is empty.
func (c *WHOISConfig) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Enabled {
		return false
	}

	if c.Timeout != 0 {
		return false
	}

	if c.RateLimit != "" {
		return false
	}

	return true
}

// IsEmpty checks if the given NetLookupConfig is empty.
func (c *NetLookupConfig) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Enabled {
		return false
	}

	if c.Timeout != 0 {
		return false
	}

	if c.RateLimit != "" {
		return false
	}

	return true
}

// IsEmpty checks if the given GeoLookupConfig is empty.
func (c *GeoLookupConfig) IsEmpty() bool {
	if c == nil {
		return true
	}

	if c.Enabled {
		return false
	}

	if c.Type != "" {
		return false
	}

	if c.DBPath != "" {
		return false
	}

	return true
}

// IsEmpty checks if the given HTTPConfig is empty.
func (hc *HTTPConfig) IsEmpty() bool {
	if hc == nil {
		return true
	}

	if hc.Enabled {
		return false
	}

	if hc.Timeout != 0 {
		return false
	}

	if hc.SSLDiscovery != (SSLScoutConfig{}) {
		return false
	}

	if len(hc.Proxies) != 0 {
		return false
	}

	return true
}

// CombineConfig combine the given config with a custom one provided as raw JSON message.
func CombineConfig(dstConfig Config, srcConfig json.RawMessage) (Config, error) {
	// Parse the provided JSON message
	var srcCfg SourceConfig
	err := json.Unmarshal(srcConfig, &srcCfg)
	if err != nil {
		return dstConfig, err
	}

	// Pretty print the parsed JSON message in srcCfg
	srcCfgJSON, err := json.MarshalIndent(srcCfg, "", "  ")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to pretty print the parsed JSON message: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Parsed JSON message: %s", srcCfgJSON)
	}

	// Combine the configurations
	dstConfig = CombineConfigs(dstConfig, srcCfg)

	return dstConfig, nil
}

// CombineConfigs combine the given configs.
func CombineConfigs(dstConfig Config, srcConfig SourceConfig) Config {
	// Combine the configurations
	if srcConfig.Custom["crawler"] != nil {
		combineCrawlerCfg(&dstConfig.Crawler, srcConfig.Custom["crawler"])
	}

	if srcConfig.Custom["selenium"] != nil {
		combineVDICfg(&dstConfig.Selenium, srcConfig.Custom["selenium"])
	}

	if srcConfig.Custom["image_storage"] != nil {
		// ImageStorage uses the same format as FileStorage
		combineFileStorageCfg(&dstConfig.ImageStorageAPI, srcConfig.Custom["image_storage"])
	}

	if srcConfig.Custom["file_storage"] != nil {
		combineFileStorageCfg(&dstConfig.FileStorageAPI, srcConfig.Custom["file_storage"])
	}

	if srcConfig.Custom["http_headers"] != nil {
		combineHTTPHeadersCfg(&dstConfig.HTTPHeaders, srcConfig.Custom["http_headers"])
	}

	if srcConfig.Custom["network_info"] != nil {
		NICfg := srcConfig.Custom["network_info"].(map[string]interface{})

		if NICfg["dns"] != nil {
			combineNIDNSCfg(&dstConfig.NetworkInfo.DNS, NICfg["dns"])
		}

		if NICfg["whois"] != nil {
			combineNIWHOISCfg(&dstConfig.NetworkInfo.WHOIS, NICfg["whois"])
		}

		if NICfg["netlookup"] != nil {
			combineNINetLookupCfg(&dstConfig.NetworkInfo.NetLookup, NICfg["netlookup"])
		}

		if NICfg["servicescout"] != nil {
			combineNIServiceScoutCfg(&dstConfig.NetworkInfo.ServiceScout, NICfg["servicescout"])
		}

		if NICfg["geolocation"] != nil {
			dstConfig.NetworkInfo.Geolocation = NICfg["geolocation"].(GeoLookupConfig)
		}
	}

	return dstConfig
}

func combineCrawlerCfg(dstCfg *Crawler, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})

	combineCrawlerBasicSettings(dstCfg, srcCfg)
	combineCrawlerRequestSettings(dstCfg, srcCfg)
	combineCrawlerCollectSettings(dstCfg, srcCfg)
}

func combineCrawlerBasicSettings(dstCfg *Crawler, srcCfg map[string]interface{}) {
	if srcCfg["workers"] != nil {
		if val, ok := srcCfg["workers"].(float64); ok {
			dstCfg.Workers = int(val)
		}
	}
	if srcCfg["interval"] != nil {
		if val, ok := srcCfg["interval"].(string); ok {
			dstCfg.Interval = val
		}
	}
	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok {
			dstCfg.Timeout = int(val)
		}
	}
	if srcCfg["max_depth"] != nil {
		if val, ok := srcCfg["max_depth"].(float64); ok {
			dstCfg.MaxDepth = int(val)
		}
	}
	if srcCfg["max_links"] != nil {
		if val, ok := srcCfg["max_links"].(float64); ok {
			dstCfg.MaxLinks = int(val)
		}
	}
	if srcCfg["delay"] != nil {
		if val, ok := srcCfg["delay"].(string); ok {
			dstCfg.Delay = val
		}
	}
	if srcCfg["browser_platform"] != nil {
		if val, ok := srcCfg["browser_platform"].(string); ok {
			dstCfg.BrowserPlatform = val
		}
	}
	if srcCfg["browsing_mode"] != nil {
		if val, ok := srcCfg["browsing_mode"].(string); ok {
			dstCfg.BrowsingMode = val
		}
	}
	if srcCfg["screenshot_section_wait"] != nil {
		if val, ok := srcCfg["screenshot_section_wait"].(float64); ok {
			dstCfg.ScreenshotSectionWait = int(val)
		}
	}
	if srcCfg["max_sources"] != nil {
		if val, ok := srcCfg["max_sources"].(float64); ok {
			dstCfg.MaxSources = int(val)
		}
	}
	if srcCfg["screenshot_max_height"] != nil {
		if val, ok := srcCfg["screenshot_max_height"].(float64); ok {
			dstCfg.ScreenshotMaxHeight = int(val)
		}
	}
	if srcCfg["max_retries"] != nil {
		if val, ok := srcCfg["max_retries"].(float64); ok {
			dstCfg.MaxRetries = int(val)
		}
	}
	if srcCfg["max_redirects"] != nil {
		if val, ok := srcCfg["max_redirects"].(float64); ok {
			dstCfg.MaxRedirects = int(val)
		}
	}
	if srcCfg["reset_cookies_policy"] != nil {
		if val, ok := srcCfg["reset_cookies_policy"].(string); ok {
			dstCfg.ResetCookiesPolicy = val
		}
	}
}

func combineCrawlerRequestSettings(dstCfg *Crawler, srcCfg map[string]interface{}) {
	if srcCfg["no_third_party_cookies"] != nil {
		if val, ok := srcCfg["no_third_party_cookies"].(bool); ok {
			dstCfg.NoThirdPartyCookies = val
		}
	}
	if srcCfg["request_images"] != nil {
		if val, ok := srcCfg["request_images"].(bool); ok {
			dstCfg.RequestImages = val
		}
	}
	if srcCfg["request_css"] != nil {
		if val, ok := srcCfg["request_css"].(bool); ok {
			dstCfg.RequestCSS = val
		}
	}
	if srcCfg["request_plugins"] != nil {
		if val, ok := srcCfg["request_plugins"].(bool); ok {
			dstCfg.RequestPlugins = val
		}
	}
	if srcCfg["create_event_when_done"] != nil {
		if val, ok := srcCfg["create_event_when_done"].(bool); ok {
			dstCfg.CreateEventWhenDone = val
		}
	}
}

func combineCrawlerCollectSettings(dstCfg *Crawler, srcCfg map[string]interface{}) {
	if srcCfg["collect_html"] != nil {
		if val, ok := srcCfg["collect_html"].(bool); ok {
			dstCfg.CollectHTML = val
		}
	}
	if srcCfg["collect_content"] != nil {
		if val, ok := srcCfg["collect_content"].(bool); ok {
			dstCfg.CollectContent = val
		}
	}
	if srcCfg["collect_performance"] != nil {
		if val, ok := srcCfg["collect_performance"].(bool); ok {
			dstCfg.CollectPerfMetrics = val
		}
	}
	if srcCfg["collect_events"] != nil {
		if val, ok := srcCfg["collect_events"].(bool); ok {
			dstCfg.CollectPageEvents = val
		}
	}
	if srcCfg["collect_xhr"] != nil {
		if val, ok := srcCfg["collect_xhr"].(bool); ok {
			dstCfg.CollectXHR = val
		}
	}
	if srcCfg["collect_keywords"] != nil {
		if val, ok := srcCfg["collect_keywords"].(bool); ok {
			dstCfg.CollectKeywords = val
		}
	}
	if srcCfg["collect_metatags"] != nil {
		if val, ok := srcCfg["collect_metatags"].(bool); ok {
			dstCfg.CollectMetaTags = val
		}
	}
}

// TODO: Selenium customization is not yet implemented
func combineVDICfg(dstCfg *[]Selenium, srcCfgIface interface{}) {
	srcCfgSlice := srcCfgIface.([]interface{})
	for i, v := range srcCfgSlice {
		srcCfg := v.(map[string]interface{})
		if srcCfg["type"] != nil {
			if val, ok := srcCfg["type"].(string); ok {
				(*dstCfg)[i].Type = val
			}
		}
		if srcCfg["service_type"] != nil {
			if val, ok := srcCfg["service_type"].(string); ok {
				(*dstCfg)[i].ServiceType = val
			}
		}
		if srcCfg["path"] != nil {
			if val, ok := srcCfg["path"].(string); ok {
				(*dstCfg)[i].Path = val
			}
		}
		if srcCfg["driver_path"] != nil {
			if val, ok := srcCfg["driver_path"].(string); ok {
				(*dstCfg)[i].DriverPath = val
			}
		}
		if srcCfg["host"] != nil {
			if val, ok := srcCfg["host"].(string); ok {
				(*dstCfg)[i].Host = val
			}
		}
		if srcCfg["port"] != nil {
			if val, ok := srcCfg["port"].(float64); ok { // Handle float64 to int conversion
				(*dstCfg)[i].Port = int(val)
			}
		}
		if srcCfg["proxy_url"] != nil {
			if val, ok := srcCfg["proxy_url"].(string); ok {
				(*dstCfg)[i].ProxyURL = val
			}
		}
	}
}

func combineFileStorageCfg(dstCfg *FileStorageAPI, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})
	if srcCfg["type"] != nil {
		if val, ok := srcCfg["type"].(string); ok {
			dstCfg.Type = val
		}
	}
	if srcCfg["host"] != nil {
		if val, ok := srcCfg["host"].(string); ok {
			dstCfg.Host = val
		}
	}
	if srcCfg["path"] != nil {
		if val, ok := srcCfg["path"].(string); ok {
			dstCfg.Path = val
		}
	}
	if srcCfg["port"] != nil {
		if val, ok := srcCfg["port"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Port = int(val)
		}
	}
	if srcCfg["region"] != nil {
		if val, ok := srcCfg["region"].(string); ok {
			dstCfg.Region = val
		}
	}
	if srcCfg["token"] != nil {
		if val, ok := srcCfg["token"].(string); ok {
			dstCfg.Token = val
		}
	}
	if srcCfg["secret"] != nil {
		if val, ok := srcCfg["secret"].(string); ok {
			dstCfg.Secret = val
		}
	}
	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Timeout = int(val)
		}
	}
}

func combineHTTPHeadersCfg(dstCfg *HTTPConfig, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})

	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Timeout = int(val)
		}
	}
	if srcCfg["ssl_discovery"] != nil {
		if val, ok := srcCfg["ssl_discovery"].(SSLScoutConfig); ok {
			dstCfg.SSLDiscovery = val
		}
	}
	if srcCfg["proxies"] != nil {
		if val, ok := srcCfg["proxies"].([]interface{}); ok {
			// Converting interface{} slice to []SOCKSProxy
			proxies := make([]SOCKSProxy, len(val))
			for i, p := range val {
				if proxy, ok := p.(SOCKSProxy); ok {
					proxies[i] = proxy
				}
			}
			dstCfg.Proxies = proxies
		}
	}
}

func combineNIDNSCfg(dstCfg *DNSConfig, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})

	if srcCfg["enabled"] != nil {
		if val, ok := srcCfg["enabled"].(bool); ok {
			dstCfg.Enabled = val
		}
	}
	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Timeout = int(val)
		}
	}
	if srcCfg["rate_limit"] != nil {
		if val, ok := srcCfg["rate_limit"].(string); ok {
			dstCfg.RateLimit = val
		}
	}
}

func combineNIWHOISCfg(dstCfg *WHOISConfig, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})

	if srcCfg["enabled"] != nil {
		if val, ok := srcCfg["enabled"].(bool); ok {
			dstCfg.Enabled = val
		}
	}
	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Timeout = int(val)
		}
	}
	if srcCfg["rate_limit"] != nil {
		if val, ok := srcCfg["rate_limit"].(string); ok {
			dstCfg.RateLimit = val
		}
	}
}

func combineNINetLookupCfg(dstCfg *NetLookupConfig, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})

	if srcCfg["enabled"] != nil {
		if val, ok := srcCfg["enabled"].(bool); ok {
			dstCfg.Enabled = val
		}
	}
	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Timeout = int(val)
		}
	}
	if srcCfg["rate_limit"] != nil {
		if val, ok := srcCfg["rate_limit"].(string); ok {
			dstCfg.RateLimit = val
		}
	}
}

func combineNIServiceScoutCfg(dstCfg *ServiceScoutConfig, srcCfgIface interface{}) {
	srcCfg := srcCfgIface.(map[string]interface{})

	combineNIServiceScoutCfgBasic(dstCfg, srcCfg)
	combineNIServiceScoutCfgAdvanced(dstCfg, srcCfg)
}

func combineNIServiceScoutCfgBasic(dstCfg *ServiceScoutConfig, srcCfg map[string]interface{}) {
	if srcCfg["aggressive_scan"] != nil {
		if val, ok := srcCfg["aggressive_scan"].(bool); ok {
			dstCfg.AggressiveScan = val
		}
	}
	if srcCfg["connect_scan"] != nil {
		if val, ok := srcCfg["connect_scan"].(bool); ok {
			dstCfg.ConnectScan = val
		}
	}
	if srcCfg["dns_servers"] != nil {
		if val, ok := srcCfg["dns_servers"].([]interface{}); ok {
			dnsServers := make([]string, len(val))
			for i, v := range val {
				if str, ok := v.(string); ok {
					dnsServers[i] = str
				}
			}
			dstCfg.DNSServers = dnsServers
		}
	}
	if srcCfg["data_length"] != nil {
		if val, ok := srcCfg["data_length"].(float64); ok { // Handle float64 to int conversion
			dstCfg.DataLength = int(val)
		}
	}
	if srcCfg["enabled"] != nil {
		if val, ok := srcCfg["enabled"].(bool); ok {
			dstCfg.Enabled = val
		}
	}
	if srcCfg["exclude_hosts"] != nil {
		if val, ok := srcCfg["exclude_hosts"].([]interface{}); ok {
			excludeHosts := make([]string, len(val))
			for i, v := range val {
				if str, ok := v.(string); ok {
					excludeHosts[i] = str
				}
			}
			dstCfg.ExcludeHosts = excludeHosts
		}
	}
	if srcCfg["host_timeout"] != nil {
		if val, ok := srcCfg["host_timeout"].(string); ok {
			dstCfg.HostTimeout = val
		}
	}
	if srcCfg["idle_scan"] != nil {
		if val, ok := srcCfg["idle_scan"].(SSIdleScan); ok {
			combineSSIdleScanCfg(&dstCfg.IdleScan, val)
		}
	}
	if srcCfg["ip_fragment"] != nil {
		if val, ok := srcCfg["ip_fragment"].(bool); ok {
			dstCfg.IPFragment = val
		}
	}
	if srcCfg["max_parallelism"] != nil {
		if val, ok := srcCfg["max_parallelism"].(float64); ok { // Handle float64 to int conversion
			dstCfg.MaxParallelism = int(val)
		}
	}
	if srcCfg["max_port_number"] != nil {
		if val, ok := srcCfg["max_port_number"].(float64); ok { // Handle float64 to int conversion
			dstCfg.MaxPortNumber = int(val)
		}
	}
	if srcCfg["max_retries"] != nil {
		if val, ok := srcCfg["max_retries"].(float64); ok { // Handle float64 to int conversion
			dstCfg.MaxRetries = int(val)
		}
	}
	if srcCfg["min_rate"] != nil {
		if val, ok := srcCfg["min_rate"].(string); ok {
			dstCfg.MinRate = val
		}
	}
	if srcCfg["no_dns_resolution"] != nil {
		if val, ok := srcCfg["no_dns_resolution"].(bool); ok {
			dstCfg.NoDNSResolution = val
		}
	}
	if srcCfg["os_fingerprinting"] != nil {
		if val, ok := srcCfg["os_fingerprinting"].(bool); ok {
			dstCfg.OSFingerprinting = val
		}
	}
	if srcCfg["ping_scan"] != nil {
		if val, ok := srcCfg["ping_scan"].(bool); ok {
			dstCfg.PingScan = val
		}
	}
}

func combineNIServiceScoutCfgAdvanced(dstCfg *ServiceScoutConfig, srcCfg map[string]interface{}) {
	if srcCfg["proxies"] != nil {
		if val, ok := srcCfg["proxies"].([]interface{}); ok {
			proxies := make([]string, len(val))
			for i, v := range val {
				if str, ok := v.(string); ok {
					proxies[i] = str
				}
			}
			dstCfg.Proxies = proxies
		}
	}
	if srcCfg["randomize_hosts"] != nil {
		if val, ok := srcCfg["randomize_hosts"].(bool); ok {
			dstCfg.RandomizeHosts = val
		}
	}
	if srcCfg["scan_delay"] != nil {
		if val, ok := srcCfg["scan_delay"].(string); ok {
			dstCfg.ScanDelay = val
		}
	}
	if srcCfg["scan_flags"] != nil {
		if val, ok := srcCfg["scan_flags"].(string); ok {
			dstCfg.ScanFlags = val
		}
	}
	if srcCfg["script_scan"] != nil {
		if val, ok := srcCfg["script_scan"].([]interface{}); ok {
			scriptScan := make([]string, len(val))
			for i, v := range val {
				if str, ok := v.(string); ok {
					scriptScan[i] = str
				}
			}
			dstCfg.ScriptScan = scriptScan
		}
	}
	if srcCfg["service_db"] != nil {
		if val, ok := srcCfg["service_db"].(string); ok {
			dstCfg.ServiceDB = val
		}
	}
	if srcCfg["service_detection"] != nil {
		if val, ok := srcCfg["service_detection"].(bool); ok {
			dstCfg.ServiceDetection = val
		}
	}
	if srcCfg["source_port"] != nil {
		if val, ok := srcCfg["source_port"].(float64); ok { // Handle float64 to int conversion
			dstCfg.SourcePort = int(val)
		}
	}
	if srcCfg["spoof_ip"] != nil {
		if val, ok := srcCfg["spoof_ip"].(string); ok {
			dstCfg.SpoofIP = val
		}
	}
	if srcCfg["syn_scan"] != nil {
		if val, ok := srcCfg["syn_scan"].(bool); ok {
			dstCfg.SynScan = val
		}
	}
	if srcCfg["targets"] != nil {
		if val, ok := srcCfg["targets"].([]interface{}); ok {
			targets := make([]string, len(val))
			for i, v := range val {
				if str, ok := v.(string); ok {
					targets[i] = str
				}
			}
			dstCfg.Targets = targets
		}
	}
	if srcCfg["timeout"] != nil {
		if val, ok := srcCfg["timeout"].(float64); ok { // Handle float64 to int conversion
			dstCfg.Timeout = int(val)
		}
	}
	if srcCfg["timing_template"] != nil {
		if val, ok := srcCfg["timing_template"].(string); ok {
			dstCfg.TimingTemplate = val
		}
	}
	if srcCfg["udp_scan"] != nil {
		if val, ok := srcCfg["udp_scan"].(bool); ok {
			dstCfg.UDPScan = val
		}
	}
}

func combineSSIdleScanCfg(dstCfg *SSIdleScan, srcCfg SSIdleScan) {
	if srcCfg.ZombieHost != "" {
		dstCfg.ZombieHost = srcCfg.ZombieHost
		dstCfg.ZombiePort = srcCfg.ZombiePort
	}
}

// DeepCopyConfig performs a deep copy of the Config struct
func DeepCopyConfig(src *Config) *Config {
	if src == nil {
		return nil
	}

	// Create a new Config instance
	copyConfig := *src // Shallow copy the main struct first

	// Deep copy Remote (struct can be copied directly as it's not containing references or slices)
	copyConfig.Remote = src.Remote

	// Deep copy Database (struct can be copied directly)
	copyConfig.Database = src.Database

	// Deep copy Crawler (struct can be copied directly)
	copyConfig.Crawler = src.Crawler

	// Deep copy API (struct can be copied directly)
	copyConfig.API = src.API

	// Deep copy the Selenium slice
	copyConfig.Selenium = make([]Selenium, len(src.Selenium))
	copy(copyConfig.Selenium, src.Selenium)

	// Deep copy ImageStorageAPI (struct can be copied directly)
	copyConfig.ImageStorageAPI = src.ImageStorageAPI

	// Deep copy FileStorageAPI (struct can be copied directly)
	copyConfig.FileStorageAPI = src.FileStorageAPI

	// Deep copy HTTPHeaders (deep copy SSLScoutConfig and Proxies slice inside)
	copyConfig.HTTPHeaders = src.HTTPHeaders
	copyConfig.HTTPHeaders.Proxies = make([]SOCKSProxy, len(src.HTTPHeaders.Proxies))
	copy(copyConfig.HTTPHeaders.Proxies, src.HTTPHeaders.Proxies)

	// Deep copy SSLScoutConfig in HTTPHeaders (not needed for basic types, but ensuring clarity)
	copyConfig.HTTPHeaders.SSLDiscovery = src.HTTPHeaders.SSLDiscovery

	// Deep copy NetworkInfo (each field is either a basic type or a struct that can be shallow copied)
	copyConfig.NetworkInfo = src.NetworkInfo

	// Deep copy Rulesets slice
	copyConfig.Rulesets = make([]RulesetConfig, len(src.Rulesets))
	copy(copyConfig.Rulesets, src.Rulesets)

	// Deep copy PluginsConfig (deep copy Plugins slice)
	copyConfig.Plugins.Plugins = make([]PluginConfig, len(src.Plugins.Plugins))
	copy(copyConfig.Plugins.Plugins, src.Plugins.Plugins)

	// Deep copy ExternalDetectionConfig
	copyConfig.ExternalDetection = src.ExternalDetection

	// Return the deep copied config
	return &copyConfig
}

// MergeConfig merges the values from type Config into the SourceConfig when fields are missing
func MergeConfig(sourceConfig *SourceConfig, globalConfig Config) {
	// Get the value of the source config (destination of the merge)
	srcVal := reflect.ValueOf(sourceConfig).Elem()
	// Get the value of the global config (source of the merge)
	globVal := reflect.ValueOf(globalConfig)

	// Iterate over the fields of SourceConfig
	for i := 0; i < srcVal.NumField(); i++ {
		srcField := srcVal.Field(i)
		globField := globVal.Field(i)

		// If the field in SourceConfig is empty, copy it from globalConfig
		if isZeroValue(srcField) {
			srcField.Set(globField)
		}
	}
}

// isZeroValue checks if the given field is empty or uninitialized (zero value)
func isZeroValue(v reflect.Value) bool {
	return reflect.DeepEqual(v.Interface(), reflect.Zero(v.Type()).Interface())
}
