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
	"strconv"
	"strings"

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

	// Check if brg is set to "random"
	brg_selected := ""
	if brg == "random" || strings.TrimSpace(brg) == "" {
		// Generate a random index between 0 and the number of user agent groups
		idxRaw, err := rand.Int(rand.Reader, big.NewInt(int64(len(uaDB.UserAgentsGroups))))
		if err != nil {
			brg_selected = "linux"
		} else {
			// convert idxRaw to int
			idx := int(idxRaw.Int64())
			brg_selected = uaDB.UserAgentsGroups[idx].BRG
		}
	} else {
		brg_selected = strings.ToLower(strings.TrimSpace(brg))
	}

	// Find the user agent group with the matching type, OS, and BRG
	var group UserAgentGroup
	for _, g := range uaDB.UserAgentsGroups {
		if g.Type == uaType && g.OS == os && g.BRG == brg_selected {
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

// Supported placeholder names and production ranges for User-Agent templates
// in support/user_agents.json. Ranges are part of the existing behavior
// contract: {random_int1} expands to [0, 8000), {random_int2} to [0, 999).
// They must not be replaced with constants.
const (
	// UARandomInt1Placeholder is replaced with an integer in [0, 8000).
	UARandomInt1Placeholder = "{random_int1}"
	// UARandomInt1Range is the exclusive upper bound for {random_int1}.
	UARandomInt1Range = 8000
	// UARandomInt2Placeholder is replaced with an integer in [0, 999).
	UARandomInt2Placeholder = "{random_int2}"
	// UARandomInt2Range is the exclusive upper bound for {random_int2}.
	UARandomInt2Range = 999
)

// UAFamilyForBrowser maps a Selenium browser executable type to the
// User-Agent database family used for template selection.
//
// Preservation note (VDI-003): the database carries a "chrome" group but no
// "chromium" group, so passing "chromium" through verbatim always missed and
// fell back to a static older UA. Chromium shares Chrome's UA family, hence
// the adaptation. All other values pass through lower-cased and trimmed, so
// existing lookup and fallback behavior for chrome/firefox (and unknown
// types) is unchanged.
func UAFamilyForBrowser(browserType string) string {
	family := strings.ToLower(strings.TrimSpace(browserType))
	if family == "chromium" {
		return "chrome"
	}
	return family
}

// ExpandUserAgentTemplate replaces the {random_int1} and {random_int2}
// placeholders in tmpl, preserving the existing production ranges. intN
// supplies integers in [0, n): production callers pass rand.IntN
// (math/rand/v2, non-deterministic), tests inject a deterministic stub.
// Templates without placeholders pass through untouched, including custom
// user-supplied values, which are never rejected here.
func ExpandUserAgentTemplate(tmpl string, intN func(int) int) string {
	if strings.Contains(tmpl, UARandomInt1Placeholder) {
		tmpl = strings.ReplaceAll(tmpl, UARandomInt1Placeholder, strconv.Itoa(intN(UARandomInt1Range)))
	}
	if strings.Contains(tmpl, UARandomInt2Placeholder) {
		tmpl = strings.ReplaceAll(tmpl, UARandomInt2Placeholder, strconv.Itoa(intN(UARandomInt2Range)))
	}
	return tmpl
}

// ResolveAdvertisedOS maps the configured crawler.browser_platform value to
// a concrete database OS key. "random" is resolved via intN within the pool
// matching the device class (desktop: windows/linux/darwin,
// mobile: android/ios); "" defaults to "linux", matching Validate()
// normalization. Explicit values pass through trimmed (case preserved: the
// database keys are lowercase and callers compare verbatim, as before).
func ResolveAdvertisedOS(browserPlatform, deviceClass string, intN func(int) int) string {
	trimmed := strings.TrimSpace(browserPlatform)
	if !strings.EqualFold(trimmed, "random") {
		if trimmed == "" {
			return "linux"
		}
		return trimmed
	}
	var pool []string
	if strings.EqualFold(strings.TrimSpace(deviceClass), "mobile") {
		pool = []string{"android", "ios"}
	} else {
		pool = []string{"windows", "linux", "darwin"}
	}
	return pool[intN(len(pool))%len(pool)]
}

// SelectUserAgent is the single shared mechanic for User-Agent selection at
// connection time (ConnectVDI) and request time (changeUserAgent). It
// performs family mapping (VDI-003), database lookup by device class and
// resolved advertised OS, template expansion with the caller-provided random
// source (VDI-004), and profile-aware fallback to the static map for the
// same family and device class when the database misses.
//
// mobile selects the fallback device class when the database misses; callers
// pass their historical rule unchanged (ConnectVDI: browseType == 1;
// changeUserAgent: Platform != "desktop"). Rotation policy (when selection
// runs: on_start/always/never) is untouched and stays with the callers.
//
// It returns the selected UA, the resolved advertised-OS key (so JavaScript
// and CDP surfaces in the same selection event use identical labels), and
// whether the static fallback was used (diagnostic only: callers log it,
// never rewrite the selection).
func (uaDB *UserAgentsDB) SelectUserAgent(deviceClass, advertisedOS, browserType string, mobile bool, intN func(int) int) (userAgent, resolvedOS string, fallbackUsed bool) {
	family := UAFamilyForBrowser(browserType)
	resolvedOS = ResolveAdvertisedOS(advertisedOS, deviceClass, intN)

	selected := uaDB.GetAgentByTypeAndOSAndBRG(deviceClass, resolvedOS, family)
	if selected == "" {
		fallbackUsed = true
		suffix := "-desktop01"
		if mobile {
			suffix = "-mobile01"
		}
		selected = UsrAgentStrMap[family+suffix]
	}
	return ExpandUserAgentTemplate(selected, intN), resolvedOS, fallbackUsed
}

// UserAgentCompatibilityNote diagnoses a selected UA against the requested
// browser family without rejecting it. It returns "" when nothing is worth
// reporting, otherwise a short human-readable note for debug logging. A
// natively different version or an intentional custom identity is not an
// error; only likely misconfigurations are reported.
func UserAgentCompatibilityNote(selectedUA, browserFamily string, fallbackUsed bool) string {
	if selectedUA == "" {
		return "user-agent selection produced an empty string"
	}
	if fallbackUsed {
		return "user-agent database lookup missed; static fallback in use for family " + UAFamilyForBrowser(browserFamily)
	}
	family := UAFamilyForBrowser(browserFamily)
	if (family == "chrome" || family == "chromium") && !strings.Contains(selectedUA, "Chrome/") && !strings.Contains(selectedUA, "Chromium") {
		return "chrome-family selection does not contain a Chrome/Chromium token"
	}
	if family == "firefox" && !strings.Contains(selectedUA, "Firefox/") {
		return "firefox-family selection does not contain a Firefox token"
	}
	return ""
}

// JSStringLiteral encodes s as a double-quoted JavaScript string literal
// using JSON string syntax (valid for embedding into scripts). It replaces
// the previous raw %s/'%s' interpolation, which broke on UA or language
// values containing quotes, backslashes, or non-ASCII text.
func JSStringLiteral(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(encoded)
}
