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

// Package netinfo provides functionality to extract network information
package netinfo

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"github.com/likexian/whois"
)

// Define a map for WHOIS field regular expressions
var whoisFieldRegex = map[string]*regexp.Regexp{
	"registry domain id":        regexp.MustCompile(`(?i)(Registry\s*Domain\s*ID):\s*(.+)`),
	"registrar whois server":    regexp.MustCompile(`(?i)(Registrar\s*WHOIS\s*Server):\s*(.+)`),
	"registrar url":             regexp.MustCompile(`(?i)(Registrar\s*URL):\s*(.+)`),
	"registry expiry date":      regexp.MustCompile(`(?i)(Registry\s*Expiry\s*Date):\s*(.+)`),
	"registrar":                 regexp.MustCompile(`(?i)(Registrar):\s*(.+)`),
	"registrar iana id":         regexp.MustCompile(`(?i)(Registrar\s*IANA\s*ID):\s*(.+)`),
	"registrant":                regexp.MustCompile(`(?i)(Registrant):\s*(.+)`),
	"registrant name":           regexp.MustCompile(`(?i)(Registrant\s*Name):\s*(.+)`),
	"registrant organization":   regexp.MustCompile(`(?i)(Registrant\s*Organization):\s*(.+)`),
	"registrant street":         regexp.MustCompile(`(?i)(Registrant\s*Street):\s*(.+)`),
	"registrant city":           regexp.MustCompile(`(?i)(Registrant\s*City):\s*(.+)`),
	"registrant state/province": regexp.MustCompile(`(?i)(Registrant\s*State/Province):\s*(.+)`),
	"registrant postal code":    regexp.MustCompile(`(?i)(Registrant\s*Postal\s*Code):\s*(.+)`),
	"registrant country":        regexp.MustCompile(`(?i)(Registrant\s*Country):\s*(.+)`),
	"registrant phone":          regexp.MustCompile(`(?i)(Registrant\s*Phone):\s*(.+)`),
	"registrant phone ext":      regexp.MustCompile(`(?i)(Registrant\s*Phone\s*Ext):\s*(.+)`),
	"registrant fax":            regexp.MustCompile(`(?i)(Registrant\s*Fax):\s*(.+)`),
	"registrant fax ext":        regexp.MustCompile(`(?i)(Registrant\s*Fax\s*Ext):\s*(.+)`),
	"registrant email":          regexp.MustCompile(`(?i)(Registrant\s*Email):\s*(.+)`),
	"admin contact":             regexp.MustCompile(`(?i)(admin\s*contact):\s*(.+)`),
	"admin-c":                   regexp.MustCompile(`(?i)(admin-c):\s*(.+)`),
	"admin email":               regexp.MustCompile(`(?i)(admin\s*email):\s*(.+)`),
	"tech contact":              regexp.MustCompile(`(?i)(tech\s*contact):\s*(.+)`),
	"tech-c":                    regexp.MustCompile(`(?i)(tech-c):\s*(.+)`),
	"tech email":                regexp.MustCompile(`(?i)(tech\s*email):\s*(.+)`),
	"dnssec":                    regexp.MustCompile(`(?i)(DNSSEC):\s*(.+)`),
	"creation date":             regexp.MustCompile(`(?mi)(Creation\s*Date):\s*(.+)`),
	"created on":                regexp.MustCompile(`(?i)(Created\s*On):\s*(.+)`),
	"created date":              regexp.MustCompile(`(?i)(Created\s*Date):\s*(.+)`),
	"created-date":              regexp.MustCompile(`(?i)(created-date):\s*(.+)`),
	"created":                   regexp.MustCompile(`(?i)(created):\s*(.+)`),
	"expiration date":           regexp.MustCompile(`(?i)(Expiration\s*Date):\s*(.+)`),
	"updated date":              regexp.MustCompile(`(?i)(Updated\s*Date):\s*(.+)`),
	"org name":                  regexp.MustCompile(`(?i)(Org\s*Name):\s*(.+)`),
	"org id":                    regexp.MustCompile(`(?i)(org\s*id):\s*(.+)`),
	"address":                   regexp.MustCompile(`(?i)(address):\s*(.+)`),
	"city":                      regexp.MustCompile(`(?i)(city):\s*(.+)`),
	"state prov":                regexp.MustCompile(`(?i)(state\s*prov):\s*(.+)`),
	"state":                     regexp.MustCompile(`(?i)(state):\s*(.+)`),
	"postal code":               regexp.MustCompile(`(?i)(postal\s*code):\s*(.+)`),
	"country":                   regexp.MustCompile(`(?i)(country):\s*(.+)`),
	"reg date":                  regexp.MustCompile(`(?i)(reg\s*date):\s*(.+)`),
	"updated":                   regexp.MustCompile(`(?i)(updated):\s*(.+)`),
	"comment":                   regexp.MustCompile(`(?i)(Comment):\s*(.+)`),
}

// GetWHOISData returns the WHOIS data of the provided NetInfo URL
func (ni *NetInfo) GetWHOISData() error {

	// Extract the domain from the URL
	domain := urlToDomain(ni.URL)

	// Use the "whois" Go library to query WHOIS data
	result, err := whois.Whois(domain)
	if err != nil {
		return err
	}

	// Process the WHOIS output and extract relevant information
	whoisData, err := parseWHOISOutput(string(result), domain)
	if err != nil {
		return err
	}

	// Append WHOIS data to the NetInfo
	ni.WHOIS = append(ni.WHOIS, whoisData)

	return nil
}

func extractFieldFromLine(line string, regex *regexp.Regexp) (string, error) {
	matches := regex.FindStringSubmatch(line)
	if len(matches) > 2 {
		return strings.TrimSpace(matches[2]), nil
	}
	return "", fmt.Errorf("no match for regex: %v, Line: %s", regex, line)
}

/*
func parseDateField(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	// Parse date using a specific layout, adjust as needed
	layout := "2006-01-02T15:04:05Z"
	parsedTime, err := time.Parse(layout, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsedTime, nil
}
*/

func parseWHOISOutput(whoisOutput, domain string) (WHOISData, error) {
	data := WHOISData{
		Entity: domain,
	}

	// check if domain contains an IPv4 or IPv6 address
	ip := net.ParseIP(domain)
	if ip != nil {
		// Check if IP is IPv6 or IPv4
		if ip.To4() != nil {
			data.EntityType = "IPv4"
		} else {
			data.EntityType = "IPv6"
		}
		if ip.IsPrivate() {
			data.EntityStatus = "PRIVATE"
		} else {
			data.EntityStatus = "PUBLIC"
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "WHOIS Output for IP %s:\n%s", domain, whoisOutput)
	} else {
		data.EntityType = "DOMAIN"
		// Check if the domain is public or private
		if strings.Contains(whoisOutput, "This query returned 0 objects") {
			data.EntityStatus = "PRIVATE"
		} else {
			data.EntityStatus = "PUBLIC"
		}
	}

	lines := strings.Split(whoisOutput, "\n")

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		line = strings.TrimSpace(line)
		// Skip comments
		if strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// Use a regular expression to capture the field name
		fieldNameMatches := regexp.MustCompile(`^(.*?):\s*`).FindStringSubmatch(line)
		if len(fieldNameMatches) < 2 {
			continue
		}
		fieldName := strings.ToLower(strings.TrimSpace(fieldNameMatches[1]))
		//fmt.Printf("Field Name: %s\n", fieldName)

		// Check if the captured field name matches a key in whoisFieldRegex
		if regex, ok := whoisFieldRegex[fieldName]; ok {
			value, err := extractFieldFromLine(line, regex)
			if err == nil {
				// Trim spaces from the extracted value
				value = strings.TrimSpace(value)

				switch fieldName {
				case "creation date", "created on", "created", "created date", "created-date":
					data.CreationDate = value
				case "expiration date":
					data.ExpirationDate = value
				case "name server":
					data.NameServers = append(data.NameServers, value)
				default:
					// Handle other fields as needed
					switch fieldName {
					case "registry domain id":
						data.RegistryDomainID = value
					case "registrar whois server":
						data.RegistrarWhoisServer = value
					case "registrar url":
						data.RegistrarURL = value
					case "registry expiry date":
						data.RegistryExpiryDate = value
					case "registrar iana id":
						data.RegistrarIANAID = value
					case "registrar":
						data.Registrar = value
					case "registrant":
						data.Registrant = value
					case "registrant name":
						data.RegistrantName = value
					case "registrant organization":
						data.RegistrantOrganization = value
					case "registrant street":
						data.RegistrantStreet = value
					case "registrant city":
						data.RegistrantCity = value
					case "registrant state/province":
						data.RegistrantState = value
					case "registrant postal code":
						data.RegistrantPostalCode = value
					case "registrant country":
						data.RegistrantCountry = value
					case "registrant phone":
						data.RegistrantPhone = value
					case "registrant phone ext":
						data.RegistrantPhoneExt = value
					case "registrant fax":
						data.RegistrantFax = value
					case "registrant fax ext":
						data.RegistrantFaxExt = value
					case "registrant email":
						data.RegistrantEmail = value
					case "admin contact", "admin-c":
						data.AdminContact = value
					case "tech contact", "tech-c":
						data.TechContact = value
					case "tech email":
						data.TechEmail = value
					case "dnssec":
						data.DNSSEC = value
					case "org name":
						data.OrgName = value
					case "org id":
						data.OrgID = value
					case "address":
						data.Address = value
					case "city":
						data.City = value
					case "state":
						data.State = value
					case "postal code":
						data.PostalCode = value
					case "country":
						data.Country = value
					case "reg date":
						data.RegDate = value
					case "updated":
						data.Updated = value
					case "comment":
						data.Comment = data.Comment + "\\n" + value
					}
				}
			}

		}
	}

	nameServerRegex := regexp.MustCompile(`Name Server:\s*([^\n]+)`)
	extractedNameServers := nameServerRegex.FindAllStringSubmatch(whoisOutput, -1)

	var nameServers []string
	for _, match := range extractedNameServers {
		if len(match) > 1 {
			nameServers = append(nameServers, strings.TrimSpace(match[1]))
		}
	}

	data.NameServers = nameServers

	return data, nil
}

// getIPInfo queries a public WHOIS service to retrieve ASN and CIDR information.
func getIPInfo(ni *NetInfo, ip string) (ipExtraData, error) {
	result, err := whois.Whois(ip)
	if err != nil {
		return ipExtraData{}, err
	}

	whoisData, err := parseWHOISOutput(string(result), ip)
	if err == nil {
		//jsonData, _ := json.MarshalIndent(whoisData, "", "  ")
		//fmt.Println(string(jsonData))
		ni.WHOIS = append(ni.WHOIS, whoisData)
	}

	// Print the entire WHOIS result for debugging
	cmn.DebugMsg(cmn.DbgLvlDebug2, "WHOIS Result for IP %s:\n%s", ip, result)

	// Define regular expressions to match ASN and CIDR information
	asnRegex := regexp.MustCompile(`(?i)(Origin\s*AS):\s*(?:AS)?(\d+)`)
	cidrRegex := regexp.MustCompile(`(?i)(CIDR|inet6num):\s*(.+)`)
	netRangeRegex := regexp.MustCompile(`(?i)(NetRange):\s*(.+)`)
	netNameRegex := regexp.MustCompile(`(?i)(NetName):\s*(.+)`)
	netHandleRegex := regexp.MustCompile(`(?i)(NetHandle):\s*(.+)`)
	netParentRegex := regexp.MustCompile(`(?i)(NetParent):\s*(.+)`)
	netTypeRegex := regexp.MustCompile(`(?i)(NetType):\s*(.+)`)
	countryRegex := regexp.MustCompile(`(?i)(Country):\s*(.+)`)

	// Search for ASN and CIDR matches in the WHOIS result
	asnMatches := asnRegex.FindStringSubmatch(result)
	cidrMatches := cidrRegex.FindStringSubmatch(result)
	netRangeMatches := netRangeRegex.FindStringSubmatch(result)
	netNameMatches := netNameRegex.FindStringSubmatch(result)
	netHandleMatches := netHandleRegex.FindStringSubmatch(result)
	netParentMatches := netParentRegex.FindStringSubmatch(result)
	netTypeMatches := netTypeRegex.FindStringSubmatch(result)
	countryMatches := countryRegex.FindStringSubmatch(result)

	// Extract ASN and CIDR data from the matches, if found
	entity := ipExtraData{}

	if len(asnMatches) > 1 {
		entity.ASN = asnMatches[2]
	}

	if len(cidrMatches) > 1 {
		entity.CIDR = cidrMatches[2]
	}

	if len(netRangeMatches) > 1 {
		entity.NetRange = netRangeMatches[2]
	}

	if len(netNameMatches) > 1 {
		entity.NetName = netNameMatches[2]
	}

	if len(netHandleMatches) > 1 {
		entity.NetHandle = netHandleMatches[2]
	}

	if len(netParentMatches) > 1 {
		entity.NetParent = netParentMatches[2]
	}

	if len(netTypeMatches) > 1 {
		entity.NetType = netTypeMatches[2]
	}

	if len(countryMatches) > 1 {
		entity.Country = countryMatches[2]
	}

	return entity, nil
}
