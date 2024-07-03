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
	"bytes"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"
)

const (
	testURL            = "https://example.com"
	testRegion         = "us-west-1"
	errExpected        = "expected %v, got %v"
	errExpectedFalse   = "Expected false, got true"
	errExpectedTrue    = "Expected true, got false"
	errExpectedTimeout = "Expected Timeout to be %d, got %v"
)

type MockFileReader struct {
	// Mock data or behavior can be customized per test
	Data map[string][]byte
}

func (m MockFileReader) ReadFile(filename string) ([]byte, error) {
	data, exists := m.Data[filename]
	if !exists {
		return nil, fmt.Errorf("file not found")
	}
	return data, nil
}

// Test LoadConfig
func TestLoadConfig(t *testing.T) {
	// Set the environment variables
	os.Setenv("DB_USER", "testuser")
	os.Setenv("DB_PASSWORD", "testpassword")

	// Call the function
	config, err := LoadConfig("./test-config.yml")

	// Check for errors
	if err != nil {
		t.Errorf("LoadConfig returned an error: %v", err)
	}

	// Check if the returned structure matches the expected output
	if IsEmpty(config) {
		t.Errorf("No config was loaded")
	}

	// Check if the database credentials are correct
	// aka verify that the environment variables are read correctly
	if config.Database.User != "testuser" {
		t.Errorf("Expected testuser, got %v", config.Database.User)
	}

	// Check if the database credentials are correct
	// aka verify that the environment variables are read correctly
	if config.Database.Password != "testpassword" {
		t.Errorf("Expected testpassword, got %v", config.Database.Password)
	}
}

// Test LoadConfigInvalidFile
func TestLoadConfigInvalidFile(t *testing.T) {
	_, err := LoadConfig("./invalid_test_config.yaml")
	if err == nil {
		t.Errorf("Expected error, got none")
	}
}

// Test IsEmpty
func TestConfigIsEmpty(t *testing.T) {
	// Create a non-empty config
	nonEmptyConfig := Config{
		Crawler: Crawler{
			// Set some fields in Crawler struct
		},
		Database: Database{
			// Set some fields in Database struct
		},
		API: API{
			// Set some fields in API struct
		},
		Selenium: []Selenium{
			{
				// Set some fields in Selenium struct
				Type: "chrome",
				Path: "",
				Port: 4444,
			},
		},
		ImageStorageAPI: FileStorageAPI{
			// Set some fields in FileStorageAPI struct
		},
		FileStorageAPI: FileStorageAPI{
			// Set some fields in FileStorageAPI struct
		},
		HTTPHeaders: HTTPConfig{
			// Set some fields in HTTPConfig struct
		},
		NetworkInfo: NetworkInfo{
			DNS:       DNSConfig{},
			WHOIS:     WHOISConfig{},
			NetLookup: NetLookupConfig{},
			// Set some fields in other NetworkInfo structs
		},
		OS:         "linux",
		DebugLevel: 1,
	}

	// Call the IsEmpty function with the non-empty config
	isEmpty := IsEmpty(nonEmptyConfig)

	// Check if the IsEmpty function returns false for a non-empty config
	if isEmpty {
		t.Errorf(errExpectedFalse)
	}

	// Create an empty config
	emptyConfig := Config{}

	// Call the IsEmpty function with the empty config
	isEmpty = IsEmpty(emptyConfig)

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf(errExpectedTrue)
	}
}

// Test recursiveInclude
func TestRecursiveInclude(t *testing.T) {
	// Example setup for a test
	baseDir := "/path/to/base"
	yamlContent := `include: "config.yaml"`
	mockData := map[string][]byte{
		"/path/to/base/config.yaml": []byte("content: value"),
	}
	mockReader := MockFileReader{Data: mockData}

	// Call the function with the mock
	result, err := recursiveInclude(yamlContent, baseDir, mockReader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert the result
	expected := `content: value`
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// Test ReadFile
func TestReadFile(t *testing.T) {
	// Create a temporary file
	tempFile, err := os.CreateTemp("", "test-file")
	if err != nil {
		t.Fatalf("Failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	// Write some data to the temporary file
	data := []byte("test data")
	if _, err := tempFile.Write(data); err != nil {
		t.Fatalf("Failed to write data to temporary file: %v", err)
	}

	// Close the temporary file
	if err := tempFile.Close(); err != nil {
		t.Fatalf("Failed to close temporary file: %v", err)
	}

	// Create an instance of OsFileReader
	reader := OsFileReader{}

	// Call the ReadFile function with the temporary file
	result, err := reader.ReadFile(tempFile.Name())
	if err != nil {
		t.Fatalf("ReadFile returned an error: %v", err)
	}

	// Check if the returned data matches the expected data
	if !bytes.Equal(result, data) {
		t.Errorf("Expected %v, got %v", data, result)
	}
}

// Test validateRemote
func TestValidateRemote(t *testing.T) {
	config := &Config{
		// Set the necessary fields for testing
	}

	err := config.validateRemote()

	// Check for errors
	if err != nil {
		t.Errorf("validateRemote returned an error: %v", err)
	}

	// Add additional assertions if needed
}

// Test validateRemoteHost
func TestValidateRemoteHost(t *testing.T) {
	// Create a config instance with empty Remote.Host
	config := &Config{
		Remote: Remote{
			Host: "",
		},
	}

	// Call the validateRemoteHost function
	config.validateRemoteHost()

	// Check if the Remote.Host is set to "localhost"
	if config.Remote.Host != "localhost" {
		t.Errorf("Expected Remote.Host to be 'localhost', got %v", config.Remote.Host)
	}

	// Create a config instance with non-empty Remote.Host
	config = &Config{
		Remote: Remote{
			Host: testURL,
		},
	}

	// Call the validateRemoteHost function
	config.validateRemoteHost()

	// Check if the Remote.Host is trimmed and unchanged
	if config.Remote.Host != testURL {
		t.Errorf("Expected Remote.Host to be 'example.com', got %v", config.Remote.Host)
	}
}

// Test validateRemotePath
func TestValidateRemotePath(t *testing.T) {
	// Create a config instance with empty Remote.Path
	config := &Config{
		Remote: Remote{
			Path: "",
		},
	}

	// Call the validateRemotePath function
	config.validateRemotePath()

	// Check if the Remote.Path is set to "/"
	if config.Remote.Path != "/" {
		t.Errorf("Expected Remote.Path to be '/', got %v", config.Remote.Path)
	}

	// Create a config instance with non-empty Remote.Path
	config = &Config{
		Remote: Remote{
			Path: "/api",
		},
	}

	// Call the validateRemotePath function
	config.validateRemotePath()

	// Check if the Remote.Path is trimmed and unchanged
	if config.Remote.Path != "/api" {
		t.Errorf("Expected Remote.Path to be '/api', got %v", config.Remote.Path)
	}
}

// Test validateRemotePort
func TestValidateRemotePort(t *testing.T) {
	// Create a config instance with a valid port
	config := &Config{
		Remote: Remote{
			Port: 8080,
		},
	}

	// Call the validateRemotePort function
	config.validateRemotePort()

	// Check if the Remote.Port remains unchanged
	if config.Remote.Port != 8080 {
		t.Errorf("Expected Remote.Port to be 8080, got %v", config.Remote.Port)
	}

	// Create a config instance with an invalid port
	config = &Config{
		Remote: Remote{
			Port: 99999,
		},
	}

	// Call the validateRemotePort function
	config.validateRemotePort()

	// Check if the Remote.Port is set to the default port (8081)
	if config.Remote.Port != 8081 {
		t.Errorf("Expected Remote.Port to be 8081, got %v", config.Remote.Port)
	}

	// Create a config instance with a negative port
	config = &Config{
		Remote: Remote{
			Port: -123,
		},
	}

	// Call the validateRemotePort function
	config.validateRemotePort()

	// Check if the Remote.Port is set to the default port (8081)
	if config.Remote.Port != 8081 {
		t.Errorf("Expected Remote.Port to be 8081, got %v", config.Remote.Port)
	}
}

// Test validateRemoteRegion
func TestValidateRemoteRegion(t *testing.T) {
	// Create a config instance with empty Remote.Region
	config := &Config{
		Remote: Remote{
			Region: "",
		},
	}

	// Call the validateRemoteRegion function
	config.validateRemoteRegion()

	// Check if the Remote.Region is set to an empty string
	if config.Remote.Region != "" {
		t.Errorf("Expected Remote.Region to be an empty string, got %v", config.Remote.Region)
	}

	// Create a config instance with non-empty Remote.Region
	config = &Config{
		Remote: Remote{
			Region: testRegion,
		},
	}

	// Call the validateRemoteRegion function
	config.validateRemoteRegion()

	// Check if the Remote.Region is trimmed and unchanged
	if config.Remote.Region != testRegion {
		t.Errorf("Expected Remote.Region to be 'us-west-1', got %v", config.Remote.Region)
	}
}

// Test validateRemoteToken
func TestValidateRemoteToken(t *testing.T) {
	// Create a config instance with an empty Remote.Token
	config := &Config{
		Remote: Remote{
			Token: "",
		},
	}

	// Call the validateRemoteToken function
	config.validateRemoteToken()

	// Check if the Remote.Token remains unchanged
	if config.Remote.Token != "" {
		t.Errorf("Expected Remote.Token to be an empty string, got %v", config.Remote.Token)
	}

	// Create a config instance with a non-empty Remote.Token
	config = &Config{
		Remote: Remote{
			Token: "  mytoken  ",
		},
	}

	// Call the validateRemoteToken function
	config.validateRemoteToken()

	// Check if the Remote.Token is trimmed and unchanged
	if config.Remote.Token != "mytoken" {
		t.Errorf("Expected Remote.Token to be 'mytoken', got %v", config.Remote.Token)
	}
}

// Test validateRemoteSecret
func TestValidateRemoteSecret(t *testing.T) {
	// Create a config instance with an empty Remote.Secret
	config := &Config{
		Remote: Remote{
			Secret: "",
		},
	}

	// Call the validateRemoteSecret function
	config.validateRemoteSecret()

	// Check if the Remote.Secret remains unchanged
	if config.Remote.Secret != "" {
		t.Errorf("Expected Remote.Secret to be an empty string, got %v", config.Remote.Secret)
	}

	// Create a config instance with a non-empty Remote.Secret
	config = &Config{
		Remote: Remote{
			Secret: "  mysecret  ",
		},
	}

	// Call the validateRemoteSecret function
	config.validateRemoteSecret()

	// Check if the Remote.Secret is trimmed and unchanged
	if config.Remote.Secret != "mysecret" {
		t.Errorf("Expected Remote.Secret to be 'mysecret', got %v", config.Remote.Secret)
	}
}

// Test validateRemoteTimeout
func TestValidateRemoteTimeout(t *testing.T) {
	// Create a config instance with a valid timeout
	config := &Config{
		Remote: Remote{
			Timeout: 10,
		},
	}

	// Call the validateRemoteTimeout function
	config.validateRemoteTimeout()

	// Check if the Remote.Timeout remains unchanged
	if config.Remote.Timeout != 10 {
		t.Errorf("Expected Remote.Timeout to be 10, got %v", config.Remote.Timeout)
	}

	// Create a config instance with an invalid timeout
	config = &Config{
		Remote: Remote{
			Timeout: -5,
		},
	}

	// Call the validateRemoteTimeout function
	config.validateRemoteTimeout()

	// Check if the Remote.Timeout is set to the default timeout (15)
	if config.Remote.Timeout != 15 {
		t.Errorf("Expected Remote.Timeout to be 15, got %v", config.Remote.Timeout)
	}
}

// Test validateRemoteType
func TestValidateRemoteType(t *testing.T) {
	// Create a config instance with an empty Remote.Type
	config := &Config{
		Remote: Remote{
			Type: "",
		},
	}

	// Call the validateRemoteType function
	config.validateRemoteType()

	// Check if the Remote.Type is set to "local"
	if config.Remote.Type != "local" {
		t.Errorf("Expected Remote.Type to be 'local', got %v", config.Remote.Type)
	}

	// Create a config instance with a non-empty Remote.Type
	config = &Config{
		Remote: Remote{
			Type: "  remote  ",
		},
	}

	// Call the validateRemoteType function
	config.validateRemoteType()

	// Check if the Remote.Type is trimmed and unchanged
	if config.Remote.Type != "remote" {
		t.Errorf("Expected Remote.Type to be 'remote', got %v", config.Remote.Type)
	}
}

// Test validateRemoteSSLMode
func TestValidateRemoteSSLMode(t *testing.T) {
	// Create a config instance with an empty Remote.SSLMode
	config := &Config{
		Remote: Remote{
			SSLMode: "",
		},
	}

	// Call the validateRemoteSSLMode function
	config.validateRemoteSSLMode()

	// Check if the Remote.SSLMode is set to "disable"
	if config.Remote.SSLMode != "disable" {
		t.Errorf("Expected Remote.SSLMode to be 'disable', got %v", config.Remote.SSLMode)
	}

	// Create a config instance with a non-empty Remote.SSLMode
	config = &Config{
		Remote: Remote{
			SSLMode: "  require  ",
		},
	}

	// Call the validateRemoteSSLMode function
	config.validateRemoteSSLMode()

	// Check if the Remote.SSLMode is trimmed and set to lowercase
	if config.Remote.SSLMode != "require" {
		t.Errorf("Expected Remote.SSLMode to be 'require', got %v", config.Remote.SSLMode)
	}
}

// Test validateCrawler
func TestValidateCrawler(t *testing.T) {
	// Create a config instance with default values
	config := &Config{
		Crawler: Crawler{
			Workers:     0,
			Interval:    "",
			Timeout:     0,
			Maintenance: 0,
		},
	}

	// Call the validateCrawler function
	config.validateCrawler()

	// Check if the Workers field is set to 1
	if config.Crawler.Workers != 1 {
		t.Errorf("Expected Workers to be %d, got %v", 1, config.Crawler.Workers)
	}

	// Check if the Interval field is set to "2"
	if config.Crawler.Interval != "2" {
		t.Errorf("Expected Interval to be '%d', got %v", 2, config.Crawler.Interval)
	}

	// Check if the Timeout field is set to 10
	if config.Crawler.Timeout != 10 {
		t.Errorf(errExpectedTimeout, 10, config.Crawler.Timeout)
	}

	// Check if the Maintenance field is set to 60
	if config.Crawler.Maintenance != 60 {
		t.Errorf("Expected Maintenance to be 60, got %v", config.Crawler.Maintenance)
	}
}

// Test validateDatabase
func TestValidateDatabase(t *testing.T) {
	// Create a config instance with empty values
	config := &Config{
		Database: Database{},
	}

	// Call the validateDatabase function
	config.validateDatabase()

	// Check if the Database.Type is set to "postgres"
	if config.Database.Type != "postgres" {
		t.Errorf("Expected Database.Type to be 'postgres', got %v", config.Database.Type)
	}

	// Check if the Database.Host is set to "localhost"
	if config.Database.Host != "localhost" {
		t.Errorf("Expected Database.Host to be 'localhost', got %v", config.Database.Host)
	}

	// Check if the Database.Port is set to 5432
	if config.Database.Port != 5432 {
		t.Errorf("Expected Database.Port to be 5432, got %v", config.Database.Port)
	}

	// Check if the Database.User is set to "crowler"
	if config.Database.User != "crowler" {
		t.Errorf("Expected Database.User to be 'crowler', got %v", config.Database.User)
	}

	// Check if the Database.DBName is set to "SitesIndex"
	if config.Database.DBName != "SitesIndex" {
		t.Errorf("Expected Database.DBName to be 'SitesIndex', got %v", config.Database.DBName)
	}

	// Check if the Database.RetryTime is set to 5
	if config.Database.RetryTime != 5 {
		t.Errorf("Expected Database.RetryTime to be 5, got %v", config.Database.RetryTime)
	}

	// Check if the Database.PingTime is set to 5
	if config.Database.PingTime != 5 {
		t.Errorf("Expected Database.PingTime to be 5, got %v", config.Database.PingTime)
	}
}

// Test validateAPI
func TestValidateAPI(t *testing.T) {
	// Create a config instance with empty API fields
	config := &Config{
		API: API{},
	}

	// Call the validateAPI function
	config.validateAPI()

	// Check if the API.Host is set to "0.0.0.0"
	if config.API.Host != "0.0.0.0" {
		t.Errorf("Expected API.Host to be '0.0.0.0', got %v", config.API.Host)
	}

	// Check if the API.Port is set to the default port (8080)
	if config.API.Port != 8080 {
		t.Errorf("Expected API.Port to be 8080, got %v", config.API.Port)
	}

	// Check if the API.Timeout is set to the default timeout (30)
	if config.API.Timeout != 60 {
		t.Errorf("Expected API.Timeout to be 30, got %v", config.API.Timeout)
	}

	// Check if the API.RateLimit is set to the default rate limit ("10,10")
	if config.API.RateLimit != "10,10" {
		t.Errorf("Expected API.RateLimit to be '10,10', got %v", config.API.RateLimit)
	}

	// Check if the API.ReadHeaderTimeout is set to the default timeout (15)
	if config.API.ReadHeaderTimeout != 15 {
		t.Errorf("Expected API.ReadHeaderTimeout to be 15, got %v", config.API.ReadHeaderTimeout)
	}

	// Check if the API.ReadTimeout is set to the default timeout (15)
	if config.API.ReadTimeout != 15 {
		t.Errorf("Expected API.ReadTimeout to be 15, got %v", config.API.ReadTimeout)
	}

	// Check if the API.WriteTimeout is set to the default timeout (30)
	if config.API.WriteTimeout != 30 {
		t.Errorf("Expected API.WriteTimeout to be 30, got %v", config.API.WriteTimeout)
	}

	// Create a config instance with non-empty API fields
	config = &Config{
		API: API{
			Host:              testURL,
			Port:              8888,
			Timeout:           60,
			RateLimit:         "20,20",
			ReadHeaderTimeout: 30,
			ReadTimeout:       30,
			WriteTimeout:      60,
		},
	}

	// Call the validateAPI function
	config.validateAPI()

	// Check if the API.Host remains unchanged
	if config.API.Host != testURL {
		t.Errorf("Expected API.Host to be 'example.com', got %v", config.API.Host)
	}

	// Check if the API.Port remains unchanged
	if config.API.Port != 8888 {
		t.Errorf("Expected API.Port to be 8888, got %v", config.API.Port)
	}

	// Check if the API.Timeout remains unchanged
	if config.API.Timeout != 60 {
		t.Errorf("Expected API.Timeout to be 60, got %v", config.API.Timeout)
	}

	// Check if the API.RateLimit remains unchanged
	if config.API.RateLimit != "20,20" {
		t.Errorf("Expected API.RateLimit to be '20,20', got %v", config.API.RateLimit)
	}

	// Check if the API.ReadHeaderTimeout remains unchanged
	if config.API.ReadHeaderTimeout != 30 {
		t.Errorf("Expected API.ReadHeaderTimeout to be 30, got %v", config.API.ReadHeaderTimeout)
	}

	// Check if the API.ReadTimeout remains unchanged
	if config.API.ReadTimeout != 30 {
		t.Errorf("Expected API.ReadTimeout to be 30, got %v", config.API.ReadTimeout)
	}

	// Check if the API.WriteTimeout remains unchanged
	if config.API.WriteTimeout != 60 {
		t.Errorf("Expected API.WriteTimeout to be 60, got %v", config.API.WriteTimeout)
	}
}

// Test validateSelenium
func TestValidateSelenium(t *testing.T) {
	// Create a config instance with some Selenium configurations
	config := &Config{
		Selenium: []Selenium{
			{
				Type:        "chrome",
				ServiceType: "chromedriver",
				Path:        "/path/to/chrome",
				DriverPath:  "/path/to/chromedriver",
				Host:        "localhost",
				Port:        4444,
				ProxyURL:    "http://proxy.example.com",
			},
			{
				Type:        "firefox",
				ServiceType: "geckodriver",
				Path:        "/path/to/firefox",
				DriverPath:  "/path/to/geckodriver",
				Host:        "localhost",
				Port:        4444,
				ProxyURL:    "http://proxy.example.com",
			},
		},
	}

	// Call the validateSelenium function
	config.validateSelenium()

	// Add assertions to check if the validations are successful
	for _, selenium := range config.Selenium {
		// Add assertions for each validation function
		// Example assertion for validateSeleniumType
		if selenium.Type != "chrome" && selenium.Type != "firefox" {
			t.Errorf("Invalid Selenium type: %s", selenium.Type)
		}

		// Add assertions for other validation functions
		// validateSeleniumServiceType, validateSeleniumPath, etc.
	}
}

// Test validateSeleniumType
func TestValidateSeleniumType(t *testing.T) {
	// Create a config instance
	config := &Config{}

	// Create a Selenium instance with an empty Type
	selenium := &Selenium{
		Type: "",
	}

	// Call the validateSeleniumType function
	config.validateSeleniumType(selenium)

	// Check if the Type is set to "chrome"
	if selenium.Type != "chrome" {
		t.Errorf("Expected Type to be 'chrome', got %v", selenium.Type)
	}

	// Create a Selenium instance with a non-empty Type
	selenium = &Selenium{
		Type: "firefox",
	}

	// Call the validateSeleniumType function
	config.validateSeleniumType(selenium)

	// Check if the Type is trimmed and unchanged
	if selenium.Type != "firefox" {
		t.Errorf("Expected Type to be 'firefox', got %v", selenium.Type)
	}
}

// Test validateSeleniumServiceType
func TestValidateSeleniumServiceType(t *testing.T) {
	// Create a config instance with a Selenium struct
	config := &Config{
		Selenium: []Selenium{
			{
				ServiceType: "",
			},
			{
				ServiceType: "  grid  ",
			},
		},
	}

	// Call the validateSeleniumServiceType function for each Selenium struct
	for i := range config.Selenium {
		config.validateSeleniumServiceType(&config.Selenium[i])
	}

	// Check if the ServiceType is set to "standalone" for the first Selenium struct
	if config.Selenium[0].ServiceType != "standalone" {
		t.Errorf("Expected ServiceType to be 'standalone', got %v", config.Selenium[0].ServiceType)
	}

	// Check if the ServiceType is trimmed and unchanged for the second Selenium struct
	if config.Selenium[1].ServiceType != "grid" {
		t.Errorf("Expected ServiceType to be 'grid', got %v", config.Selenium[1].ServiceType)
	}
}

// Test validateSeleniumHost
func TestValidateSeleniumHost(t *testing.T) {
	// Create a config instance with an empty Selenium.Host
	config := &Config{
		Selenium: []Selenium{
			{
				Host: "",
			},
		},
	}

	// Call the validateSeleniumHost function
	config.validateSeleniumHost(&config.Selenium[0])

	// Check if the Selenium.Host is set to "localhost"
	if config.Selenium[0].Host != "localhost" {
		t.Errorf("Expected Selenium.Host to be 'localhost', got %v", config.Selenium[0].Host)
	}

	// Create a config instance with non-empty Selenium.Host
	config = &Config{
		Selenium: []Selenium{
			{
				Host: testURL,
			},
		},
	}

	// Call the validateSeleniumHost function
	config.validateSeleniumHost(&config.Selenium[0])

	// Check if the Selenium.Host is trimmed and unchanged
	if config.Selenium[0].Host != testURL {
		t.Errorf("Expected Selenium.Host to be 'example.com', got %v", config.Selenium[0].Host)
	}
}

// Test validateSeleniumPort
func TestValidateSeleniumPort(t *testing.T) {
	// Create a config instance with a Selenium struct
	config := &Config{
		Selenium: []Selenium{
			{
				Port: 0,
			},
			{
				Port: 65536,
			},
			{
				Port: 8080,
			},
		},
	}

	// Call the validateSeleniumPort function for each Selenium struct
	for i := range config.Selenium {
		config.validateSeleniumPort(&config.Selenium[i])
	}

	// Check if the Selenium ports are validated correctly
	expectedPorts := []int{4444, 4444, 8080}
	for i, selenium := range config.Selenium {
		if selenium.Port != expectedPorts[i] {
			t.Errorf("Expected Selenium port to be %d, got %d", expectedPorts[i], selenium.Port)
		}
	}
}

// Test validateRulesets
func TestValidateRulesets(t *testing.T) {
	// Create a config instance
	config := &Config{
		RulesetsSchemaPath: "",
		Rulesets: []RulesetConfig{
			{
				Type: "",
				Path: []string{},
			},
			{
				Type: "remote",
				Path: []string{"", "path1", "path2"},
			},
		},
	}

	// Call the validateRulesets function
	config.validateRulesets()

	// Check if the RulesetsSchemaPath is set to the default value
	if config.RulesetsSchemaPath != "./schemas/ruleset-schema.json" {
		t.Errorf("Expected RulesetsSchemaPath to be './schemas/ruleset-schema.json', got %v", config.RulesetsSchemaPath)
	}

	// Check if the first Ruleset Type is set to the default value
	if config.Rulesets[0].Type != "local" {
		t.Errorf("Expected first Ruleset Type to be 'local', got %v", config.Rulesets[0].Type)
	}

	// Check if the first Ruleset Path is set to the default values
	expectedPath := []string{JSONRulesDefaultPath, YAMLRulesDefaultPath1, YAMLRulesDefaultPath2}
	if !reflect.DeepEqual(config.Rulesets[0].Path, expectedPath) {
		t.Errorf("Expected first Ruleset Path to be %v, got %v", expectedPath, config.Rulesets[0].Path)
	}

	// Check if the second Ruleset Type remains unchanged
	if config.Rulesets[1].Type != "remote" {
		t.Errorf("Expected second Ruleset Type to be 'remote', got %v", config.Rulesets[1].Type)
	}

	// Check if the second Ruleset Path is trimmed and unchanged
	expectedPath = []string{JSONRulesDefaultPath, "path1", "path2"}
	if !reflect.DeepEqual(config.Rulesets[1].Path, expectedPath) {
		t.Errorf("Expected second Ruleset Path to be %v, got %v", expectedPath, config.Rulesets[1].Path)
	}
}

// Test validateImageStorageAPI
func TestValidateImageStorageAPI(t *testing.T) {
	// Create a config instance with empty ImageStorageAPI fields
	config := &Config{
		ImageStorageAPI: FileStorageAPI{},
	}

	// Call the validateImageStorageAPI function
	config.validateImageStorageAPI()

	// Check if the ImageStorageAPI fields are set to their default values
	if config.ImageStorageAPI.Type != "local" {
		t.Errorf("Expected ImageStorageAPI.Type to be 'local', got %v", config.ImageStorageAPI.Type)
	}
	if config.ImageStorageAPI.Host != "" {
		t.Errorf("Expected ImageStorageAPI.Host to be an empty string, got %v", config.ImageStorageAPI.Host)
	}
	if config.ImageStorageAPI.Path != DataDefaultPath {
		t.Errorf("Expected ImageStorageAPI.Path to be '%v', got %v", DataDefaultPath, config.ImageStorageAPI.Path)
	}
	if config.ImageStorageAPI.Port != 0 {
		t.Errorf("Expected ImageStorageAPI.Port to be 0, got %v", config.ImageStorageAPI.Port)
	}
	if config.ImageStorageAPI.Region != "nowhere" {
		t.Errorf("Expected ImageStorageAPI.Region to be 'nowhere', got %v", config.ImageStorageAPI.Region)
	}
	if config.ImageStorageAPI.Token != "" {
		t.Errorf("Expected ImageStorageAPI.Token to be an empty string, got %v", config.ImageStorageAPI.Token)
	}
	if config.ImageStorageAPI.Secret != "" {
		t.Errorf("Expected ImageStorageAPI.Secret to be an empty string, got %v", config.ImageStorageAPI.Secret)
	}
	if config.ImageStorageAPI.Timeout != 15 {
		t.Errorf("Expected ImageStorageAPI.Timeout to be 15, got %v", config.ImageStorageAPI.Timeout)
	}

	// Add additional assertions if needed
}

// Test validateFileStorageAPI
func TestValidateFileStorageAPI(t *testing.T) {
	// Create a config instance with empty FileStorageAPI fields
	config := &Config{
		FileStorageAPI: FileStorageAPI{},
	}

	// Call the validateFileStorageAPI function
	config.validateFileStorageAPI()

	// Check if the FileStorageAPI fields are set to their default values
	if config.FileStorageAPI.Type != "local" {
		t.Errorf("Expected FileStorageAPI.Type to be 'local', got %v", config.FileStorageAPI.Type)
	}
	if config.FileStorageAPI.Host != "" {
		t.Errorf("Expected FileStorageAPI.Host to be an empty string, got %v", config.FileStorageAPI.Host)
	}
	if config.FileStorageAPI.Path != DataDefaultPath {
		t.Errorf("Expected FileStorageAPI.Path to be '%v', got %v", DataDefaultPath, config.FileStorageAPI.Path)
	}
	if config.FileStorageAPI.Port != 0 {
		t.Errorf("Expected FileStorageAPI.Port to be 0, got %v", config.FileStorageAPI.Port)
	}
	if config.FileStorageAPI.Region != "nowhere" {
		t.Errorf("Expected FileStorageAPI.Region to be 'nowhere', got %v", config.FileStorageAPI.Region)
	}
	if config.FileStorageAPI.Token != "" {
		t.Errorf("Expected FileStorageAPI.Token to be an empty string, got %v", config.FileStorageAPI.Token)
	}
	if config.FileStorageAPI.Secret != "" {
		t.Errorf("Expected FileStorageAPI.Secret to be an empty string, got %v", config.FileStorageAPI.Secret)
	}
	if config.FileStorageAPI.Timeout != 15 {
		t.Errorf("Expected FileStorageAPI.Timeout to be 15, got %v", config.FileStorageAPI.Timeout)
	}

	// Create a config instance with non-empty FileStorageAPI fields
	config = &Config{
		FileStorageAPI: FileStorageAPI{
			Type:    "s3",
			Host:    testURL,
			Path:    "/data",
			Port:    9000,
			Region:  "us-west-2",
			Token:   "mytoken",
			Secret:  "mysecret",
			Timeout: 30,
		},
	}

	// Call the validateFileStorageAPI function
	config.validateFileStorageAPI()

	// Check if the FileStorageAPI fields remain unchanged
	if config.FileStorageAPI.Type != "s3" {
		t.Errorf("Expected FileStorageAPI.Type to be 's3', got %v", config.FileStorageAPI.Type)
	}
	if config.FileStorageAPI.Host != testURL {
		t.Errorf("Expected FileStorageAPI.Host to be 'example.com', got %v", config.FileStorageAPI.Host)
	}
	if config.FileStorageAPI.Path != "/data" {
		t.Errorf("Expected FileStorageAPI.Path to be '/data', got %v", config.FileStorageAPI.Path)
	}
	if config.FileStorageAPI.Port != 9000 {
		t.Errorf("Expected FileStorageAPI.Port to be 9000, got %v", config.FileStorageAPI.Port)
	}
	if config.FileStorageAPI.Region != "us-west-2" {
		t.Errorf("Expected FileStorageAPI.Region to be 'us-west-2', got %v", config.FileStorageAPI.Region)
	}
	if config.FileStorageAPI.Token != "mytoken" {
		t.Errorf("Expected FileStorageAPI.Token to be 'mytoken', got %v", config.FileStorageAPI.Token)
	}
	if config.FileStorageAPI.Secret != "mysecret" {
		t.Errorf("Expected FileStorageAPI.Secret to be 'mysecret', got %v", config.FileStorageAPI.Secret)
	}
	if config.FileStorageAPI.Timeout != 30 {
		t.Errorf("Expected FileStorageAPI.Timeout to be 30, got %v", config.FileStorageAPI.Timeout)
	}
}

// Test validateHTTPHeaders
func TestValidateHTTPHeaders(t *testing.T) {
	// Create a config instance with a valid timeout
	config := &Config{
		HTTPHeaders: HTTPConfig{
			Timeout: 30,
		},
	}

	// Call the validateHTTPHeaders function
	config.validateHTTPHeaders()

	// Check if the HTTPHeaders.Timeout remains unchanged
	if config.HTTPHeaders.Timeout != 30 {
		t.Errorf("Expected HTTPHeaders.Timeout to be 30, got %v", config.HTTPHeaders.Timeout)
	}

	// Create a config instance with an invalid timeout
	config = &Config{
		HTTPHeaders: HTTPConfig{
			Timeout: -10,
		},
	}

	// Call the validateHTTPHeaders function
	config.validateHTTPHeaders()

	// Check if the HTTPHeaders.Timeout is set to the default timeout (60)
	if config.HTTPHeaders.Timeout != 60 {
		t.Errorf("Expected HTTPHeaders.Timeout to be 60, got %v", config.HTTPHeaders.Timeout)
	}
}

// Test validateOS
func TestValidateOS(t *testing.T) {
	// Create a config instance with an empty OS
	config := &Config{
		OS: "",
	}

	// Call the validateOS function
	config.validateOS()

	// Check if the OS is set to the default value (runtime.GOOS)
	if config.OS != runtime.GOOS {
		t.Errorf("Expected OS to be %v, got %v", runtime.GOOS, config.OS)
	}

	// Create a config instance with a non-empty OS
	config = &Config{
		OS: "   linux   ",
	}

	// Call the validateOS function
	config.validateOS()

	// Check if the OS is trimmed and unchanged
	if config.OS != "linux" {
		t.Errorf("Expected OS to be 'linux', got %v", config.OS)
	}
}

// Test validateDebugLevel
func TestValidateDebugLevel(t *testing.T) {
	// Create a config instance with a negative DebugLevel
	config := &Config{
		DebugLevel: -1,
	}

	// Call the validateDebugLevel function
	config.validateDebugLevel()

	// Check if the DebugLevel is set to 0
	if config.DebugLevel != 0 {
		t.Errorf("Expected DebugLevel to be 0, got %v", config.DebugLevel)
	}

	// Create a config instance with a positive DebugLevel
	config = &Config{
		DebugLevel: 2,
	}

	// Call the validateDebugLevel function
	config.validateDebugLevel()

	// Check if the DebugLevel remains unchanged
	if config.DebugLevel != 2 {
		t.Errorf("Expected DebugLevel to be 2, got %v", config.DebugLevel)
	}

	// Create a config instance with a zero DebugLevel
	config = &Config{
		DebugLevel: 0,
	}

	// Call the validateDebugLevel function
	config.validateDebugLevel()

	// Check if the DebugLevel remains unchanged
	if config.DebugLevel != 0 {
		t.Errorf("Expected DebugLevel to be 0, got %v", config.DebugLevel)
	}
}

// Test String
func TestConfigString(t *testing.T) {
	// Create a config instance with sample values
	config := &Config{
		Remote: Remote{
			Host:   testURL,
			Path:   "/api",
			Port:   8080,
			Region: testRegion,
			Token:  "mytoken",
		},
		Database: Database{
			User:     "testuser",
			Password: "testpassword",
		},
		Crawler: Crawler{
			// Set some fields in Crawler struct
		},
		API: API{
			// Set some fields in API struct
		},
		Selenium: []Selenium{
			{
				// Set some fields in Selenium struct
				Type: "chrome",
				Path: "",
				Port: 4444,
			},
		},
		RulesetsSchemaPath: "path/to/schema",
		Rulesets:           []RulesetConfig{},
		ImageStorageAPI:    FileStorageAPI{
			// Set some fields in FileStorageAPI struct
		},
		FileStorageAPI: FileStorageAPI{
			// Set some fields in FileStorageAPI struct
		},
		HTTPHeaders: HTTPConfig{
			// Set some fields in HTTPConfig struct
		},
		NetworkInfo: NetworkInfo{
			DNS:       DNSConfig{},
			WHOIS:     WHOISConfig{},
			NetLookup: NetLookupConfig{},
			// Set some fields in other NetworkInfo structs
		},
		OS:         "linux",
		DebugLevel: 1,
	}

	// Define the expected string representation of the config
	expected := "Config{Remote: {https://example.com /api 8080 us-west-1 mytoken  0  }, Database: {  0 testuser testpassword  0 0  }, Crawler: {0  0 0 false false 0 0 0 0   0 0 false false false false false false 0 false}, API: { 0 0 false false     false 0 0 0 false}, Selenium: [{    chrome  4444  false false    }], RulesetsSchemaPath: path/to/schema, Rulesets: [], ImageStorageAPI: {  0    0  }, FileStorageAPI: {  0    0  }, HTTPHeaders: {false 0 false {false false false false false false false false false false false false false false} []}, NetworkInfo: {{false 0 } {false 0 } {false 0 } {false 0 { 0} false false false false false false  false false [] [] []    0 0 0   false 0  false  false 0 [] []} {false    0 } {  }}, OS: linux, DebugLevel: 1}"

	// Call the String method on the config
	result := config.String()

	// Check if the result matches the expected string representation
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

// Test validate
func TestDNSConfigValidate(t *testing.T) {
	// Create a DNSConfig instance with Enabled set to true
	config := &DNSConfig{
		Enabled:   true,
		Timeout:   0,
		RateLimit: "",
	}

	// Call the validate function
	config.validate()

	// Check if the Timeout is set to the default value (10)
	if config.Timeout != 10 {
		t.Errorf(errExpectedTimeout, 10, config.Timeout)
	}

	// Check if the RateLimit is set to the default value ("1")
	if config.RateLimit != "1" {
		t.Errorf("Expected RateLimit to be \"%d\", got %v", 1, config.RateLimit)
	}

	// Create a DNSConfig instance with Enabled set to false
	config = &DNSConfig{
		Enabled:   false,
		Timeout:   5,
		RateLimit: "2",
	}

	// Call the validate function
	config.validate()

	// Check if the Timeout remains unchanged
	if config.Timeout != 5 {
		t.Errorf(errExpectedTimeout, 5, config.Timeout)
	}

	// Check if the RateLimit remains unchanged
	if config.RateLimit != "2" {
		t.Errorf("Expected RateLimit to be \"%d\", got %v", 2, config.RateLimit)
	}
}

// Test validate
func TestValidateWHOISConfig(t *testing.T) {
	// Create a WHOISConfig instance with enabled set to true
	config := &WHOISConfig{
		Enabled:   true,
		Timeout:   0,
		RateLimit: "",
	}

	// Call the validate function
	config.validate()

	// Check if the Timeout is set to the default value (10)
	if config.Timeout != 10 {
		t.Errorf("Expected Timeout to be %d, got %v", 10, config.Timeout)
	}

	// Check if the RateLimit is set to the default value ("1")
	if config.RateLimit != "1" {
		t.Errorf("Expected RateLimit to be '1', got %v", config.RateLimit)
	}

	// Create a WHOISConfig instance with enabled set to false
	config = &WHOISConfig{
		Enabled:   false,
		Timeout:   5,
		RateLimit: "2",
	}

	// Call the validate function
	config.validate()

	// Check if the Timeout remains unchanged
	if config.Timeout != 5 {
		t.Errorf("Expected Timeout to be 5, got %v", config.Timeout)
	}

	// Check if the RateLimit remains unchanged
	if config.RateLimit != "2" {
		t.Errorf("Expected RateLimit to be '2', got %v", config.RateLimit)
	}
}

// Test validate
func TestNetLookupConfigValidate(t *testing.T) {
	// Create a NetLookupConfig instance with Enabled set to true
	config := &NetLookupConfig{
		Enabled:   true,
		Timeout:   0,
		RateLimit: "",
	}

	// Call the validate method
	config.validate()

	// Check if the Timeout is set to the default value (10)
	if config.Timeout != 10 {
		t.Errorf("Expected Timeout to be 10, got %v", config.Timeout)
	}

	// Check if the RateLimit is set to the default value ("1")
	if config.RateLimit != "1" {
		t.Errorf("Expected RateLimit to be \"1\", got %v", config.RateLimit)
	}

	// Create a NetLookupConfig instance with Enabled set to false
	config = &NetLookupConfig{
		Enabled:   false,
		Timeout:   5,
		RateLimit: "2",
	}

	// Call the validate method
	config.validate()

	// Check if the Timeout remains unchanged
	if config.Timeout != 5 {
		t.Errorf("Expected Timeout to be 5, got %v", config.Timeout)
	}

	// Check if the RateLimit remains unchanged
	if config.RateLimit != "2" {
		t.Errorf("Expected RateLimit to be \"2\", got %v", config.RateLimit)
	}
}

// Test validate
func TestServiceScoutConfigValidate(t *testing.T) {
	// Create a ServiceScoutConfig instance with enabled set to true
	config := &ServiceScoutConfig{
		Enabled: true,
	}

	// Call the validate function
	config.validate()

	// Add assertions to check if the validation logic is working correctly
	// For example, you can check if the timeout validation is performed correctly
	// by asserting the expected behavior based on the configuration values.

	// Example assertion:
	if config.Timeout <= 0 {
		t.Errorf("Expected Timeout to be greater than 0, got %v", config.Timeout)
	}
}

// Test validateTimeout
func TestValidateTimeout(t *testing.T) {
	// Create a ServiceScoutConfig instance with a timeout less than 1
	config := &ServiceScoutConfig{
		Timeout: 0,
	}

	// Call the validateTimeout function
	config.validateTimeout()

	// Check if the Timeout is set to the default timeout (SSDefaultTimeout)
	if config.Timeout != SSDefaultTimeout {
		t.Errorf("Expected Timeout to be %v, got %v", SSDefaultTimeout, config.Timeout)
	}

	// Create a ServiceScoutConfig instance with a timeout greater than 1
	config = &ServiceScoutConfig{
		Timeout: 10,
	}

	// Call the validateTimeout function
	config.validateTimeout()

	// Check if the Timeout remains unchanged
	if config.Timeout != 10 {
		t.Errorf("Expected Timeout to be 10, got %v", config.Timeout)
	}
}

// Test validateHostTimeout
func TestValidateHostTimeout(t *testing.T) {
	// Create a ServiceScoutConfig instance with a non-empty HostTimeout
	config := &ServiceScoutConfig{
		HostTimeout: "  10s  ",
		Timeout:     30,
	}

	// Call the validateHostTimeout function
	config.validateHostTimeout()

	// Check if the HostTimeout is trimmed and unchanged
	if config.HostTimeout != "10s" {
		t.Errorf("Expected HostTimeout to be '10s', got %v", config.HostTimeout)
	}

	// Create a ServiceScoutConfig instance with an empty HostTimeout
	config = &ServiceScoutConfig{
		HostTimeout: "",
		Timeout:     30,
	}

	// Call the validateHostTimeout function
	config.validateHostTimeout()

	// Calculate the expected HostTimeout value
	expectedTimeout := fmt.Sprint((config.Timeout - (config.Timeout / 4)))

	// Check if the HostTimeout is set to the expected value
	if config.HostTimeout != expectedTimeout {
		t.Errorf("Expected HostTimeout to be %v, got %v", expectedTimeout, config.HostTimeout)
	}
}

// Test validateScanDelay
func TestValidateScanDelay(t *testing.T) {
	// Create a ServiceScoutConfig instance with an empty ScanDelay
	config := &ServiceScoutConfig{
		ScanDelay: "",
	}

	// Call the validateScanDelay function
	config.validateScanDelay()

	// Check if the ScanDelay is set to an empty string
	if config.ScanDelay != "" {
		t.Errorf("Expected ScanDelay to be an empty string, got %v", config.ScanDelay)
	}

	// Create a ServiceScoutConfig instance with a non-empty ScanDelay
	config = &ServiceScoutConfig{
		ScanDelay: "  10s  ",
	}

	// Call the validateScanDelay function
	config.validateScanDelay()

	// Check if the ScanDelay is trimmed and unchanged
	if config.ScanDelay != "10s" {
		t.Errorf("Expected ScanDelay to be '10s', got %v", config.ScanDelay)
	}
}

// Test validateMaxPortNumber
func TestValidateMaxPortNumber(t *testing.T) {
	// Create a config instance with MaxPortNumber less than 1
	config := &ServiceScoutConfig{
		MaxPortNumber: 0,
	}

	// Call the validateMaxPortNumber function
	config.validateMaxPortNumber()

	// Check if the MaxPortNumber is set to the default value (9000)
	if config.MaxPortNumber != 9000 {
		t.Errorf("Expected MaxPortNumber to be 9000, got %v", config.MaxPortNumber)
	}

	// Create a config instance with MaxPortNumber greater than 65535
	config = &ServiceScoutConfig{
		MaxPortNumber: 70000,
	}

	// Call the validateMaxPortNumber function
	config.validateMaxPortNumber()

	// Check if the MaxPortNumber is set to the maximum value (65535)
	if config.MaxPortNumber != 65535 {
		t.Errorf("Expected MaxPortNumber to be 65535, got %v", config.MaxPortNumber)
	}

	// Create a config instance with MaxPortNumber within the valid range
	config = &ServiceScoutConfig{
		MaxPortNumber: 5000,
	}

	// Call the validateMaxPortNumber function
	config.validateMaxPortNumber()

	// Check if the MaxPortNumber remains unchanged
	if config.MaxPortNumber != 5000 {
		t.Errorf("Expected MaxPortNumber to be 5000, got %v", config.MaxPortNumber)
	}
}

// Test validateTimingTemplate
func TestValidateTimingTemplate(t *testing.T) {
	// Create a ServiceScoutConfig instance with an empty TimingTemplate
	config := &ServiceScoutConfig{
		TimingTemplate: "",
	}

	// Call the validateTimingTemplate function
	config.validateTimingTemplate()

	// Check if the TimingTemplate is set to the default value
	expected := fmt.Sprint(SSDefaultTimeProfile)
	if config.TimingTemplate != expected {
		t.Errorf("Expected TimingTemplate to be %q, got %q", expected, config.TimingTemplate)
	}

	// Create a ServiceScoutConfig instance with a non-empty TimingTemplate
	config = &ServiceScoutConfig{
		TimingTemplate: "  template  ",
	}

	// Call the validateTimingTemplate function
	config.validateTimingTemplate()

	// Check if the TimingTemplate is trimmed and unchanged
	expected = "template"
	if config.TimingTemplate != expected {
		t.Errorf("Expected TimingTemplate to be %q, got %q", expected, config.TimingTemplate)
	}
}

// Test validate
func TestValidateGeoLookupConfig(t *testing.T) {
	// Create a GeoLookupConfig instance with Enabled set to true
	config := &GeoLookupConfig{
		Enabled: true,
		Type:    "  ",
		DBPath:  "  ",
	}

	// Call the validate function
	config.validate()

	// Check if the Type is set to the default value "maxmind"
	if config.Type != "maxmind" {
		t.Errorf("Expected Type to be 'maxmind', got %v", config.Type)
	}

	// Check if the DBPath is set to the default value "./data/GeoLite2-City.mmdb"
	if config.DBPath != "./data/GeoLite2-City.mmdb" {
		t.Errorf("Expected DBPath to be './data/GeoLite2-City.mmdb', got %v", config.DBPath)
	}

	// Create a GeoLookupConfig instance with Enabled set to false
	config = &GeoLookupConfig{
		Enabled: false,
		Type:    "geolite",
		DBPath:  "/path/to/database",
	}

	// Call the validate function
	config.validate()

	// Check if the Type remains unchanged
	if config.Type != "geolite" {
		t.Errorf("Expected Type to be 'geolite', got %v", config.Type)
	}

	// Check if the DBPath remains unchanged
	if config.DBPath != "/path/to/database" {
		t.Errorf("Expected DBPath to be '/path/to/database', got %v", config.DBPath)
	}
}

// Test IsEmpty
func TestServiceScoutConfigIsEmpty(t *testing.T) {
	// Create a non-empty ServiceScoutConfig
	nonEmptyConfig := &ServiceScoutConfig{
		Enabled:          true,
		Timeout:          10,
		OSFingerprinting: true,
		ServiceDetection: true,
	}

	// Call the IsEmpty function with the non-empty config
	isEmpty := nonEmptyConfig.IsEmpty()

	// Check if the IsEmpty function returns false for a non-empty config
	if isEmpty {
		t.Errorf(errExpectedFalse)
	}

	// Create an empty ServiceScoutConfig
	emptyConfig := &ServiceScoutConfig{}

	// Call the IsEmpty function with the empty config
	isEmpty = emptyConfig.IsEmpty()

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf(errExpectedTrue)
	}
}

// Test IsEmpty
func TestExecutionPlanItemIsEmpty(t *testing.T) {
	// Create an empty ExecutionPlanItem
	emptyItem := &ExecutionPlanItem{}

	// Call the IsEmpty function on the empty item
	isEmpty := emptyItem.IsEmpty()

	// Check if the IsEmpty function returns true for an empty item
	if !isEmpty {
		t.Errorf(errExpectedTrue)
	}

	// Create a non-empty ExecutionPlanItem
	nonEmptyItem := &ExecutionPlanItem{
		Rulesets:   []string{"ruleset1", "ruleset2"},
		RuleGroups: []string{"group1", "group2"},
		Rules:      []string{"rule1", "rule2"},
	}

	// Call the IsEmpty function on the non-empty item
	isEmpty = nonEmptyItem.IsEmpty()

	// Check if the IsEmpty function returns false for a non-empty item
	if isEmpty {
		t.Errorf(errExpectedFalse)
	}
}

// Test IsEmpty
func TestSourceConfigIsEmpty(t *testing.T) {
	// Create a nil SourceConfig
	var sc *SourceConfig

	// Call the IsEmpty function with the nil SourceConfig
	isEmpty := sc.IsEmpty()

	// Check if the IsEmpty function returns true for a nil SourceConfig
	if !isEmpty {
		t.Errorf(errExpectedTrue)
	}

	// Create a non-nil SourceConfig with a nil ExecutionPlan
	sc = &SourceConfig{
		ExecutionPlan: nil,
	}

	// Call the IsEmpty function with the non-nil SourceConfig
	isEmpty = sc.IsEmpty()

	// Check if the IsEmpty function returns true for a non-nil SourceConfig with a nil ExecutionPlan
	if !isEmpty {
		t.Errorf(errExpectedTrue)
	}

	// Create a non-nil SourceConfig with a non-nil ExecutionPlan
	sc = &SourceConfig{
		ExecutionPlan: []ExecutionPlanItem{},
	}

	// Call the IsEmpty function with the non-nil SourceConfig
	isEmpty = sc.IsEmpty()

	// Check if the IsEmpty function returns false for a non-nil SourceConfig with a non-nil ExecutionPlan
	if isEmpty {
		t.Errorf(errExpectedFalse)
	}
}

type MockFetcher struct {
	Body string
	Err  error
}

func (m *MockFetcher) FetchRemoteFile(url string, timeout int, sslMode string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return m.Body, nil
}

func TestLoadRemoteConfig(t *testing.T) {
	cases := []struct {
		name      string
		cfg       Config
		mockBody  string
		mockErr   error
		expectErr bool
		validate  func(t *testing.T, cfg Config, err error)
	}{
		{
			name: "empty remote config",
			cfg: Config{
				Remote: Remote{}, // Explicitly empty to trigger the expected error
			},
			mockBody:  "",
			mockErr:   nil,
			expectErr: true,
			validate: func(t *testing.T, cfg Config, err error) {
				if err == nil {
					t.Errorf("expected an error, got none")
				}
			},
		},
		{
			name: "valid remote config fetch",
			cfg: Config{
				Remote: Remote{
					Host:    testURL,
					Path:    "config.yaml",
					Timeout: 10,
					SSLMode: "disable",
				},
			},
			mockBody:  "remote_content: valid",
			mockErr:   nil,
			expectErr: false,
			validate: func(t *testing.T, cfg Config, err error) {
				if err != nil {
					t.Errorf("did not expect an error, got %v", err)
				}
			},
		},
		{
			name: "network error",
			cfg: Config{
				Remote: Remote{
					Host:    testURL,
					Path:    "config.yaml",
					Timeout: 10,
					SSLMode: "disable",
				},
			},
			mockBody:  "",
			mockErr:   fmt.Errorf("network failure"),
			expectErr: true,
			validate: func(t *testing.T, cfg Config, err error) {
				if err == nil {
					t.Errorf("expected an error, got none")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &MockFetcher{Body: tc.mockBody, Err: tc.mockErr}
			resultCfg, err := LoadRemoteConfig(tc.cfg, fetcher)

			if (err != nil) != tc.expectErr {
				t.Errorf("expected error: %v, got: %v", tc.expectErr, err != nil)
			}

			tc.validate(t, resultCfg, err)
		})
	}
}

// Test IsEmpty
func TestDNSConfigIsEmpty(t *testing.T) {
	// Create a non-empty config
	nonEmptyConfig := DNSConfig{
		Enabled: true,
	}

	// Call the IsEmpty function with the non-empty config
	isEmpty := nonEmptyConfig.IsEmpty()

	// Check if the IsEmpty function returns false for a non-empty config
	if isEmpty {
		t.Errorf("Expected IsEmpty to return false for a non-empty DNSConfig")
	}

	// Create an empty config
	emptyConfig := DNSConfig{}

	// Call the IsEmpty function with the empty config
	isEmpty = emptyConfig.IsEmpty()

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf("Expected IsEmpty to return true for an empty DNSConfig")
	}
}

func TestWHOISConfigIsEmpty(t *testing.T) {
	// Create a non-empty config
	nonEmptyConfig := WHOISConfig{
		Enabled: true,
	}

	// Call the IsEmpty function with the non-empty config
	isEmpty := nonEmptyConfig.IsEmpty()

	// Check if the IsEmpty function returns false for a non-empty config
	if isEmpty {
		t.Errorf("Expected IsEmpty to return false for a non-empty WHOISConfig")
	}

	// Create an empty config
	emptyConfig := WHOISConfig{}

	// Call the IsEmpty function with the empty config
	isEmpty = emptyConfig.IsEmpty()

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf("Expected IsEmpty to return true for an empty WHOISConfig")
	}
}
func TestNetLookupConfigIsEmpty(t *testing.T) {
	// Create a non-empty config
	nonEmptyConfig := NetLookupConfig{
		Enabled: true,
	}

	// Call the IsEmpty function with the non-empty config
	isEmpty := nonEmptyConfig.IsEmpty()

	// Check if the IsEmpty function returns false for a non-empty config
	if isEmpty {
		t.Errorf("Expected IsEmpty to return false for a non-empty NetLookupConfig")
	}

	// Create an empty config
	emptyConfig := NetLookupConfig{}

	// Call the IsEmpty function with the empty config
	isEmpty = emptyConfig.IsEmpty()

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf("Expected IsEmpty to return true for an empty NetLookupConfig")
	}
}

func TestGeoLookupConfigIsEmpty(t *testing.T) {
	// Create a non-empty config
	nonEmptyConfig := GeoLookupConfig{
		Enabled: true,
	}

	// Call the IsEmpty function with the non-empty config
	isEmpty := nonEmptyConfig.IsEmpty()

	// Check if the IsEmpty function returns false for a non-empty config
	if isEmpty {
		t.Errorf("Expected IsEmpty to return false for a non-empty GeoLookupConfig")
	}

	// Create an empty config
	emptyConfig := GeoLookupConfig{}

	// Call the IsEmpty function with the empty config
	isEmpty = emptyConfig.IsEmpty()

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf("Expected IsEmpty to return true for an empty GeoLookupConfig")
	}
}
