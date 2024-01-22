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

package main

// Config represents the structure of the configuration file
type Config struct {
	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
	} `yaml:"database"`
	Crawler struct {
		Workers     int `yaml:"workers"`
		Interval    int `yaml:"interval"`
		Timeout     int `yaml:"timeout"`
		Maintenance int `yaml:"maintenance"`
	} `yaml:"crawler"`
	API struct {
		Host    string `yaml:"host"`
		Port    int    `yaml:"port"`
		Timeout int    `yaml:"timeout"`
	} `yaml:"api"`
	Selenium struct {
		Path       string `yaml:"path"`
		DriverPath string `yaml:"driver_path"`
		Type       string `yaml:"type"`
		Port       int    `yaml:"port"`
		Host       string `yaml:"host"`
		Headless   bool   `yaml:"headless"`
	} `yaml:"selenium"`
	OS         string `yaml:"os"`
	DebugLevel int    `yaml:"debug_level"`
}

var (
	config Config // Global variable to store the configuration
)

type SearchResult struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Summary string `json:"summary"`
		Snippet string `json:"snippet"`
	} `json:"items"`
}
