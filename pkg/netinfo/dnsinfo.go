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
	"os/exec"
	"strings"
	"time"
)

// NewDNSInfo initializes a new DNSInfo struct.
func NewDNSInfo(domain string) DNSInfo {
	return DNSInfo{
		Domain: domain,
	}
}

// GetDNSInfo collects DNS information for the given domain using dig.
func (ni *NetInfo) GetDNSInfo() error {
	host := urlToHost(ni.URL)
	domain := urlToDomain(ni.URL)

	// Get DNS information
	output, err := getDigInfo(host, "specific")
	if err != nil {
		return err
	}

	err = parseDNSInfo(ni, domain, host, output)

	return err
}

// parseDNSInfo parses the dig output and populates the DNSInfo fields.
func parseDNSInfo(ni *NetInfo, domain, host, output string) error {
	var err error
	// Parse DNS information
	dnsList := ni.DNS
	stage := 0
	for host != "" || domain != "" {
		var dnsInfo DNSInfo
		if stage == 0 {
			dnsInfo = NewDNSInfo(domain)
			domain = ""
			stage = 1
		} else {
			dnsInfo = NewDNSInfo(host)
			host = ""
			stage = 2
		}
		dnsInfo.parseDNSRecords(output)

		// Check for unresolved answers:
		for _, record := range dnsInfo.Records {
			if record.Section == "ANSWER" && record.Type == "CNAME" {
				if stage > 1 {
					host = record.Response
					time.Sleep(time.Duration(ni.Config.DNS.RateLimit) * time.Second)
					output, err = getDigInfo(host, "")
					if err != nil {
						return err
					}
				} else {
					domain = record.Response
					time.Sleep(time.Duration(ni.Config.DNS.RateLimit) * time.Second)
					output, err = getDigInfo(domain, "")
					if err != nil {
						return err
					}
					stage = 0
				}
				break
			}
		}

		if dnsInfo.Records != nil {
			dnsList = append(dnsList, dnsInfo)
		}
	}
	ni.DNS = dnsList
	return nil
}

// getDigInfo collects DNS information for the given domain using dig.
func getDigInfo(domain string, requestType string) (string, error) {
	// prepare requestType
	requestType = strings.ToLower(strings.TrimSpace(requestType))

	// Run the command
	var cmd *exec.Cmd
	if requestType == "" {
		cmd = exec.Command("dig", domain, "TXT", "ANY")
	} else {
		cmd = exec.Command("dig", domain)
	}
	// Retrieve the output
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// parseDNSRecords parses dig output and populates DNSInfo fields.
func (dnsInfo *DNSInfo) parseDNSRecords(output string) {
	records := strings.Split(strings.TrimSpace(output), "\n")
	section := ""
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}

		// Process sections:
		if strings.HasPrefix(record, ";") {
			section = processSection(record, dnsInfo)
			continue
		}

		processFields(record, section, dnsInfo)
	}
}

// processSection processes the section of the dig output and returns the section name.
func processSection(record string, dnsInfo *DNSInfo) string {
	record = strings.ToUpper(record)
	record = strings.TrimLeft(record, ";")
	record = strings.TrimSpace(record)
	dnsInfo.Comments = append(dnsInfo.Comments, record)

	if strings.Contains(record, "ANSWER SECTION") {
		return "ANSWER"
	} else if strings.Contains(record, "AUTHORITY SECTION") {
		return "AUTHORITY"
	} else if strings.Contains(record, "ADDITIONAL SECTION") {
		return "ADDITIONAL"
	}

	if strings.HasPrefix(record, "SERVER:") {
		server := strings.TrimSpace(strings.TrimPrefix(record, "SERVER:"))
		server = strings.TrimSpace(server)
		server = strings.TrimSuffix(server, "(")
		server = strings.TrimSpace(server)
		// remove the substring from #53 to the end of the string
		server = server[:strings.Index(server, "#53")]
		server = strings.TrimSpace(server)
		dnsInfo.Server = append(dnsInfo.Server, server)
	}

	return ""
}

// processFields processes the fields of the dig output and populates the DNSRecord fields.
func processFields(record string, section string, dnsInfo *DNSInfo) {
	fields := fieldsQuotes(record)
	var dnsRecord DNSRecord

	// Process fields:
	for i := 0; i < len(fields); i++ {
		if i > 0 {
			dnsRecord.Value = dnsRecord.Value + " " + fields[i]
		} else {
			dnsRecord.Value = fields[i]
		}
		switch {
		case fields[i] == "TXT":
			dnsRecord.Special = "TXT"
			dnsRecord.Type = "TXT"
			continue
		case fields[i] == "RRSIG":
			dnsRecord.Special = "RRSIG"
			dnsRecord.Type = "RRSIG"
			continue
		default:
			rType := parseDNSRecordType(fields[i], "")
			if rType != "" {
				dnsRecord.Type = rType
				continue
			}
			if i == 0 {
				dnsRecord.Name = fields[i]
				continue
			}
			if i == 1 && isNumeric(fields[i]) {
				dnsRecord.TTL = fields[i]
				continue
			}
			if i == 2 && fields[i] == "IN" {
				dnsRecord.Class = fields[i]
				continue
			}
			if i == len(fields)-1 {
				r := fields[i]
				if strings.HasPrefix(r, "\"") && strings.HasSuffix(r, "\"") {
					r = strings.TrimPrefix(r, "\"")
					r = strings.TrimSuffix(r, "\"")
				}
				dnsRecord.Response = r
				continue
			}
		}
	}
	if dnsRecord.Type != "" {
		dnsRecord.Section = section
		dnsInfo.Records = append(dnsInfo.Records, dnsRecord)
	}
}

// This function returns a DNS record type if the recordType is found in the map, otherwise it returns t.
func parseDNSRecordType(recordType string, t string) string {
	// Look up the record type in the map and return the corresponding value
	if value, ok := recordTypeMap[recordType]; ok {
		return value
	}
	// If the record type is not found, return t
	return t
}
