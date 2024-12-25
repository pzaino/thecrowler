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

// Package common provides common utilities and functions used across the application.
package common

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"crypto/rand"
)

// UserAgent represents a user agent with its string and percentage.
type UserAgent struct {
	// BR is the actual browser name
	BR string `json:"br" yaml:"br"`
	// UA is the user agent string
	UA string `json:"ua" yaml:"ua"`
	// PCT is the percentage of the user agent
	PCT float64 `json:"pct" yaml:"pct"`
}

// UserAgentGroup represents a group of user agents with metadata.
type UserAgentGroup struct {
	// OS is the operating system
	OS string `json:"os" yaml:"os"`
	// BRG is the browser group (e.g. "chrome" identify all chrome related browsers)
	BRG string `json:"brg" yaml:"brg"`
	// Type identify the type of user agent (desktop or mobile)
	Type string // Type is added manually ("desktop" or "mobile")
	// UserAgents is a list of user agents
	UserAgents []UserAgent `json:"ua" yaml:"ua"`
}

// UserAgentsDB represents the entire user agents database.
type UserAgentsDB struct {
	// UserAgentsGroups is a list of user agent groups
	UserAgentsGroups []UserAgentGroup
}

// UADB is the global user agents database.
var UADB UserAgentsDB

// InitUserAgentsDB initializes the user agents database.
func (uaDB *UserAgentsDB) InitUserAgentsDB() error {
	// Load the raw JSON database from file.
	uaDBRaw, err := loadUserAgentsDB()
	if err != nil {
		return err
	}

	// Process the JSON data and populate the database.
	for uaType, groups := range uaDBRaw {
		// uaType is "desktop" or "mobile"
		groupList, ok := groups.([]interface{})
		if !ok {
			return fmt.Errorf("unexpected format for groups in %s", uaType)
		}

		for _, group := range groupList {
			groupMap, ok := group.(map[string]interface{})
			if !ok {
				return fmt.Errorf("unexpected format for group data")
			}

			// Extract fields
			os, _ := groupMap["os"].(string)
			brg, _ := groupMap["brg"].(string)
			uaList, ok := groupMap["ua"].([]interface{})
			if !ok {
				continue // Skip empty or malformed user agent lists
			}

			var userAgents []UserAgent
			for _, ua := range uaList {
				uaMap, ok := ua.(map[string]interface{})
				if !ok {
					continue
				}

				uaStr, _ := uaMap["ua"].(string)
				pct, _ := uaMap["pct"].(float64)
				userAgents = append(userAgents, UserAgent{
					UA:  uaStr,
					PCT: pct,
				})
			}

			// Create and append the UserAgentGroup
			uaGroup := UserAgentGroup{
				OS:         os,
				BRG:        brg,
				Type:       uaType,
				UserAgents: userAgents,
			}
			uaDB.UserAgentsGroups = append(uaDB.UserAgentsGroups, uaGroup)
		}
	}

	return nil
}

// IsEmpty checks if the user agents database is empty.
func (uaDB *UserAgentsDB) IsEmpty() bool {
	return len(uaDB.UserAgentsGroups) == 0
}

// GetAnyUserAgent returns a random user agent string from the database.
func (uaDB *UserAgentsDB) GetAnyUserAgent() string {
	if uaDB.IsEmpty() {
		return ""
	}

	// Generate a random index between 0 and the number of user agent groups
	idxRaw, err := rand.Int(rand.Reader, big.NewInt(int64(len(uaDB.UserAgentsGroups))))
	if err != nil {
		return ""
	}

	// convert idxRaw to int
	idx := int(idxRaw.Int64())

	// Pick a random user agent group
	group := uaDB.UserAgentsGroups[idx]

	// Pick a random user agent string
	if len(group.UserAgents) == 0 {
		return ""
	}

	// Pick a random user agent string from the group UA slice
	idxRaw, err = rand.Int(rand.Reader, big.NewInt(int64(len(group.UserAgents))))
	if err != nil {
		return ""
	}

	// convert idxRaw to int
	idx = int(idxRaw.Int64())

	return group.UserAgents[idx].UA
}

// GetAgentByTypeAndOS returns a random user agent string from the database based on the type and OS.
func (uaDB *UserAgentsDB) GetAgentByTypeAndOS(uaType, os string) string {
	if uaDB.IsEmpty() {
		return ""
	}

	// Find the user agent group with the matching type and OS
	var group UserAgentGroup
	for _, g := range uaDB.UserAgentsGroups {
		if g.Type == uaType && g.OS == os {
			group = g
			break
		}
	}

	// Pick a random user agent string
	if len(group.UserAgents) == 0 {
		return ""
	}

	// Pick a random user agent string from the group UA slice
	idxRaw, err := rand.Int(rand.Reader, big.NewInt(int64(len(group.UserAgents))))
	if err != nil {
		return ""
	}

	// convert idxRaw to int
	idx := int(idxRaw.Int64())

	return group.UserAgents[idx].UA
}

// GetAgentByTypeAndOSAndBRG returns a random user agent string from the database based on the type, OS, and BRG.
func (uaDB *UserAgentsDB) GetAgentByTypeAndOSAndBRG(uaType, os, brg string) string {
	if uaDB.IsEmpty() {
		return ""
	}

	// Find the user agent group with the matching type, OS, and BRG
	var group UserAgentGroup
	for _, g := range uaDB.UserAgentsGroups {
		if g.Type == uaType && g.OS == os && g.BRG == brg {
			group = g
			break
		}
	}

	// Pick a random user agent string
	if len(group.UserAgents) == 0 {
		return ""
	}

	// Pick a random user agent string from the group UA slice
	idxRaw, err := rand.Int(rand.Reader, big.NewInt(int64(len(group.UserAgents))))
	if err != nil {
		return ""
	}

	// convert idxRaw to int
	idx := int(idxRaw.Int64())

	return group.UserAgents[idx].UA
}

// GetAgentByTypeAndOSAndBRGAndPCT returns a random user agent string from the database based on the type, OS, BRG, and PCT.
func (uaDB *UserAgentsDB) GetAgentByTypeAndOSAndBRGAndPCT(uaType, os, brg string, pct float64) string {
	if uaDB.IsEmpty() {
		return ""
	}

	// Find the user agent group with the matching type, OS, and BRG
	var group UserAgentGroup
	for _, g := range uaDB.UserAgentsGroups {
		if g.Type == uaType && g.OS == os && g.BRG == brg {
			group = g
			break
		}
	}

	// Pick a random user agent string
	if len(group.UserAgents) == 0 {
		return ""
	}

	// Pick a random user agent string from the group UA slice
	var uaList []UserAgent
	for _, ua := range group.UserAgents {
		if ua.PCT >= pct {
			uaList = append(uaList, ua)
		}
	}

	// Pick a random user agent string from the filtered UA list
	if len(uaList) == 0 {
		return ""
	}

	idxRaw, err := rand.Int(rand.Reader, big.NewInt(int64(len(uaList))))
	if err != nil {
		return ""
	}

	// convert idxRaw to int
	idx := int(idxRaw.Int64())

	return uaList[idx].UA
}

// loadUserAgentsDB loads the user agents database from a JSON file.
func loadUserAgentsDB() (map[string]interface{}, error) {
	// File path to the JSON database.
	file := "./support/user_agents.json"

	// Read the JSON file.
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// Unmarshal the JSON data.
	var uaDB map[string]interface{}
	err = json.Unmarshal(data, &uaDB)
	if err != nil {
		return nil, err
	}

	return uaDB, nil
}
