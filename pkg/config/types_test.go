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
	"testing"
)

// Test LoadConfig
func TestLoadConfig(t *testing.T) {
	// Call the function
	config, err := LoadConfig("./test_config.yml")

	// Check for errors
	if err != nil {
		t.Errorf("LoadConfig returned an error: %v", err)
	}

	// Check if the returned structure matches the expected output
	if ConfigIsEmpty(config) {
		t.Errorf("No config was loaded")
	}

	// Additional checks can be added to validate the contents of 'config'
}

// Test LoadConfigInvalidFile
func TestLoadConfigInvalidFile(t *testing.T) {
	_, err := LoadConfig("./invalid_test_config.yaml")
	if err == nil {
		t.Errorf("Expected error, got none")
	}
}
