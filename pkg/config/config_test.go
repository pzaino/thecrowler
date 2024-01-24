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
	"testing"
)

// Test LoadConfig
func TestLoadConfig(t *testing.T) {
	// Set the environment variables
	os.Setenv("DB_USER", "testuser")
	os.Setenv("DB_PASSWORD", "testpassword")

	// Call the function
	config, err := LoadConfig("./test_config.yml")

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
