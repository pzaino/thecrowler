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
	"regexp"
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
				br, _ := uaMap["br"].(string)
				pct, _ := uaMap["pct"].(float64)
				userAgents = append(userAgents, UserAgent{
					BR:  br,
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

// findEntry locates the group matching type/OS/BRG and picks one entry
// uniformly at random (crypto/rand), mirroring the long-standing selection
// distribution. It returns the raw template, the entry's browser-product
// metadata (BR field; may be empty in older data), and whether a pick was
// made. GetAgentByTypeAndOSAndBRG delegates to it, preserving behavior.
func (uaDB *UserAgentsDB) findEntry(uaType, os, brg string) (template, br string, ok bool) {
	if uaDB.IsEmpty() {
		return "", "", false
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
		return "", "", false
	}

	// Entries with empty templates are not candidates: accepting one
	// previously bypassed fallback with an empty UA (follow-up finding 3,
	// P2). Distribution over the valid entries is unchanged.
	var candidates []UserAgent
	for _, u := range group.UserAgents {
		if strings.TrimSpace(u.UA) != "" {
			candidates = append(candidates, u)
		}
	}
	if len(candidates) == 0 {
		return "", "", false
	}

	// Pick a random user agent string from the valid UA slice
	idxRaw, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return "", "", false
	}

	// convert idxRaw to int
	idx := int(idxRaw.Int64())

	return candidates[idx].UA, candidates[idx].BR, true
}

// GetAgentByTypeAndOSAndBRG returns a random user agent string from the database based on the type, OS, and BRG.
func (uaDB *UserAgentsDB) GetAgentByTypeAndOSAndBRG(uaType, os, brg string) string {
	template, _, _ := uaDB.findEntry(uaType, os, brg)
	return template
}

// fallbackGroupFor returns the best available group for family+device class
// when the exact OS group is missing: it prefers the requested OS (relevant
// on the product-degradation path, where another family may serve it),
// then the linux OS group (the global default platform), else the first
// match in database order. The boolean reports whether any group matched.
func (uaDB *UserAgentsDB) fallbackGroupFor(family, deviceClass, preferredOS string) (UserAgentGroup, bool) {
	var first, linuxGroup UserAgentGroup
	foundFirst, foundLinux := false, false
	for _, g := range uaDB.UserAgentsGroups {
		if g.Type != deviceClass || g.BRG != family || !groupHasValidEntry(g) {
			continue
		}
		if !foundFirst {
			first, foundFirst = g, true
		}
		if g.OS == preferredOS {
			return g, true
		}
		if g.OS == "linux" && !foundLinux {
			linuxGroup, foundLinux = g, true
		}
	}
	if foundLinux {
		return linuxGroup, true
	}
	return first, foundFirst
}

// groupHasValidEntry reports whether the group holds at least one
// non-empty UA template (see findEntry's empty-template rejection).
func groupHasValidEntry(g UserAgentGroup) bool {
	for _, u := range g.UserAgents {
		if strings.TrimSpace(u.UA) != "" {
			return true
		}
	}
	return false
}

// staticFallbackUA returns the static-map fallback for family+device class.
// Chromium shares chrome's (newer, compatible) entry; families without a
// static entry report false.
func staticFallbackUA(family string, mobile bool) (string, bool) {
	key := family
	if key == "chromium" {
		key = "chrome"
	}
	suffix := "-desktop01"
	if mobile {
		suffix = "-mobile01"
	}
	ua, ok := UsrAgentStrMap[key+suffix]
	if !ok || strings.TrimSpace(ua) == "" {
		return "", false
	}
	return ua, true
}

// osOfStaticUA derives the advertised-OS key from a fallback UA's OS token
// so a fallback never advertises a contradictory platform. Unknown tokens
// keep the previously resolved key.
func osOfStaticUA(userAgent, resolvedOS string) string {
	switch {
	case strings.Contains(userAgent, "Windows"):
		return "windows"
	case strings.Contains(userAgent, "Macintosh"):
		return "darwin"
	case strings.Contains(userAgent, "Android"):
		return "android"
	case strings.Contains(userAgent, "iPhone"), strings.Contains(userAgent, "iPad"):
		return "ios"
	case strings.Contains(userAgent, "X11"), strings.Contains(userAgent, "Linux"):
		return "linux"
	default:
		return resolvedOS
	}
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

// UASelection is the resolved browser identity for one selection event.
// Every consumer in the event (launch flags, JavaScript patches, CDP
// overrides) derives from these values together, so surfaces agree.
type UASelection struct {
	// UserAgent is the expanded, ready-to-advertise string (may be empty
	// only when no source at all produced one; see FallbackUsed).
	UserAgent string
	// ResolvedOS is the advertised-OS key. The fallback chain may adjust it
	// to the OS of the selected UA so metadata never contradicts the UA.
	ResolvedOS string
	// Family is the mapped database family (chromium shares chrome's).
	Family string
	// Product is the browser-product identity (edge, opera, chrome,
	// chromium, firefox, safari, herring, atcontent, or ""): the DB BR
	// field on a hit, else derived from UA tokens.
	Product string
	// FallbackUsed reports database-miss fallback (diagnostic only).
	FallbackUsed bool
	// DegradedProduct reports product degradation (e.g. edge requested but
	// no Edge UA available, chrome advertised instead) for diagnostics.
	DegradedProduct bool
}

// SelectUserAgent is the single shared mechanic for User-Agent selection at
// connection time (ConnectVDI) and request time (changeUserAgent). It
// performs family mapping (VDI-003), database lookup by device class and
// resolved advertised OS, template expansion with the caller-provided random
// source (VDI-004), product resolution (review finding 1), and
// identity-aware fallback when the database misses (review finding 3).
//
// mobile selects the fallback device class when the database misses; callers
// pass their historical rule unchanged (ConnectVDI: browseType == 1;
// changeUserAgent: Platform != "desktop"). Rotation policy (when selection
// runs: on_start/always/never) is untouched and stays with the callers.
//
// Fallback chain (first hit wins; FallbackUsed set for every step past the
// direct lookup):
//  1. exact group (device class, resolved OS, family);
//  2. same family+class group (requested OS, then linux, then first in DB
//     order), with ResolvedOS adjusted to the group's OS;
//  3. static map for the same family+class, with ResolvedOS derived from the
//     fallback UA's OS token;
//  4. product degradation to chrome (same class, steps 2-3), keeping OS
//     consistent and setting DegradedProduct.
func (uaDB *UserAgentsDB) SelectUserAgent(deviceClass, advertisedOS, browserType string, mobile bool, intN func(int) int) UASelection {
	family := UAFamilyForBrowser(browserType)
	resolvedOS := ResolveAdvertisedOS(advertisedOS, deviceClass, intN)

	if template, br, ok := uaDB.findEntry(deviceClass, resolvedOS, family); ok {
		expanded := ExpandUserAgentTemplate(template, intN)
		return UASelection{
			UserAgent:  expanded,
			ResolvedOS: resolvedOS,
			Family:     family,
			Product:    BrowserProduct(expanded, br),
		}
	}

	if sel, ok := uaDB.fallbackFromGroup(deviceClass, family, resolvedOS, intN); ok {
		return sel
	}
	if sel, ok := staticFallbackSelection(family, mobile, resolvedOS, intN); ok {
		return sel
	}
	if family != "chrome" {
		if sel, ok := uaDB.fallbackFromGroup(deviceClass, "chrome", resolvedOS, intN); ok {
			sel.DegradedProduct = true
			return sel
		}
		if sel, ok := staticFallbackSelection("chrome", mobile, resolvedOS, intN); ok {
			sel.DegradedProduct = true
			return sel
		}
	}
	return UASelection{
		Family:          family,
		ResolvedOS:      resolvedOS,
		FallbackUsed:    true,
		DegradedProduct: family != "chrome",
	}
}

// fallbackFromGroup builds a selection from the best available group for
// family+class (requested OS, then linux, then first in DB order). The
// advertised OS follows the group's OS so the UA and metadata always agree.
func (uaDB *UserAgentsDB) fallbackFromGroup(deviceClass, family, resolvedOS string, intN func(int) int) (UASelection, bool) {
	group, ok := uaDB.fallbackGroupFor(family, deviceClass, resolvedOS)
	if !ok {
		return UASelection{}, false
	}
	template, br, ok := uaDB.findEntry(deviceClass, group.OS, family)
	if !ok {
		return UASelection{}, false
	}
	expanded := ExpandUserAgentTemplate(template, intN)
	return UASelection{
		UserAgent:    expanded,
		ResolvedOS:   group.OS,
		Family:       family,
		Product:      BrowserProduct(expanded, br),
		FallbackUsed: true,
	}, true
}

// staticFallbackSelection builds a selection from the static map for
// family+class, deriving the advertised OS from the fallback UA's own OS
// token so a Linux static UA is never advertised as Windows (or vice versa).
func staticFallbackSelection(family string, mobile bool, resolvedOS string, intN func(int) int) (UASelection, bool) {
	template, ok := staticFallbackUA(family, mobile)
	if !ok {
		return UASelection{}, false
	}
	expanded := ExpandUserAgentTemplate(template, intN)
	return UASelection{
		UserAgent:    expanded,
		ResolvedOS:   osOfStaticUA(expanded, resolvedOS),
		Family:       UAFamilyForBrowser(family),
		Product:      BrowserProduct(expanded, UAFamilyForBrowser(family)),
		FallbackUsed: true,
	}, true
}

var (
	operaTokenPattern   = regexp.MustCompile(`OPR/(\d+)`)
	edgeTokenPattern    = regexp.MustCompile(`Edg/(\d+)`)
	chromeTokenPattern  = regexp.MustCompile(`Chrome/(\d+)`)
	firefoxTokenPattern = regexp.MustCompile(`Firefox/`)
)

// BrowserProduct resolves the browser-product identity for a selected UA.
// UA tokens win over database metadata (tokens are what the UA advertises):
// OPR/ means opera, modern Edg/ means edge (legacy Edge/ without Edg/ is
// EdgeHTML, reported as edge but carrying no Chromium brand surface).
// Otherwise a Chrome/ token with database metadata preserves that product
// (chrome, chromium, herring, atcontent); without metadata it is chrome.
// Firefox/ and (Chromeless) Safari/ map to firefox and safari; anything else
// keeps the database product or reports "".
func BrowserProduct(userAgent, dbBR string) string {
	switch {
	case operaTokenPattern.MatchString(userAgent):
		return "opera"
	case edgeTokenPattern.MatchString(userAgent):
		return "edge"
	case chromeTokenPattern.MatchString(userAgent):
		if dbBR != "" {
			return strings.ToLower(strings.TrimSpace(dbBR))
		}
		return "chrome"
	case firefoxTokenPattern.MatchString(userAgent):
		return "firefox"
	case strings.Contains(userAgent, "Safari/"):
		return "safari"
	default:
		return strings.ToLower(strings.TrimSpace(dbBR))
	}
}

// ProductMajorVersion extracts the product's own major version from UA
// tokens (Edg/ for edge, OPR/ for opera). It returns "" when the product
// carries no distinct version token.
func ProductMajorVersion(userAgent, product string) string {
	var pattern *regexp.Regexp
	switch product {
	case "edge":
		pattern = edgeTokenPattern
	case "opera":
		pattern = operaTokenPattern
	default:
		return ""
	}
	m := pattern.FindStringSubmatch(userAgent)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// UserAgentCompatibilityNote diagnoses a selection without rejecting it.
// It returns "" when nothing is worth reporting, otherwise a short
// human-readable note for debug logging. A natively different version or an
// intentional custom identity is not an error; only likely misconfigurations
// (empty result, fallback use, product degradation, token mismatch) are
// reported.
func UserAgentCompatibilityNote(sel UASelection) string {
	if sel.UserAgent == "" {
		return "user-agent selection produced an empty string"
	}
	if sel.DegradedProduct {
		return "no " + sel.Family + " identity available; advertising chrome instead (requested OS kept consistent)"
	}
	if sel.FallbackUsed {
		return "user-agent database lookup missed; fallback in use for family " + sel.Family
	}
	if (sel.Family == "chrome" || sel.Family == "chromium") && !strings.Contains(sel.UserAgent, "Chrome/") && !strings.Contains(sel.UserAgent, "Chromium") {
		return "chrome-family selection does not contain a Chrome/Chromium token"
	}
	if sel.Family == "firefox" && !strings.Contains(sel.UserAgent, "Firefox/") {
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
