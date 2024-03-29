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
	"testing"
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
func TestIsEmpty(t *testing.T) {
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
		t.Errorf("Expected false, got true")
	}

	// Create an empty config
	emptyConfig := Config{}

	// Call the IsEmpty function with the empty config
	isEmpty = IsEmpty(emptyConfig)

	// Check if the IsEmpty function returns true for an empty config
	if !isEmpty {
		t.Errorf("Expected true, got false")
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
