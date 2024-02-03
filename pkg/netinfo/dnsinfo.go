package netinfo

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// NewDNSInfo initializes a new DNSInfo struct.
func NewDNSInfo(domain string) DNSInfo {
	return DNSInfo{
		Domain: domain,
	}
}

// GetDNSInfo collects DNS information for the given domain using dig.
func (ni *NetInfo) GetDNSInfo() error {
	domain := ni.URL
	domain = URLToHost(domain)

	// Get DNS information
	output, err := GetDigInfo(domain)
	if err != nil {
		return err
	}
	fmt.Printf("Output: %s\n", output)

	dnsList := ni.DNS
	for domain != "" {
		dnsInfo := NewDNSInfo(domain)
		dnsInfo.parseDNSRecords(output)

		// Check for unresolved answers:
		domain = ""
		for _, record := range dnsInfo.Records {
			if record.Section == "ANSWER" && record.Type == "CNAME" {
				if net.ParseIP(record.Response) == nil {
					domain = record.Response
					output, err = GetDigInfo(domain)
					if err != nil {
						return err
					}
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

// GetDigInfo collects DNS information for the given domain using dig.
func GetDigInfo(domain string) (string, error) {
	cmd := exec.Command("dig", domain, "ANY")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// URLToHost extracts a host (or an IP) from a URL.
func URLToHost(url string) string {
	host := url
	if strings.HasPrefix(url, "http://") {
		host = strings.TrimPrefix(url, "http://")
	}
	if strings.HasPrefix(url, "https://") {
		host = strings.TrimPrefix(url, "https://")
	}
	if strings.Contains(host, "/") {
		host = host[:strings.Index(host, "/")]
	}
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimSpace(host)
	return host
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

func processSection(record string, dnsInfo *DNSInfo) string {
	record = strings.ToUpper(record)
	record = strings.TrimLeft(record, ";")
	record = strings.TrimSpace(record)

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

func processFields(record string, section string, dnsInfo *DNSInfo) {
	fields := strings.Fields(record)
	var dnsRecord DNSRecord

	for i := 0; i < len(fields); i++ {
		if i > 0 {
			dnsRecord.Value = dnsRecord.Value + " " + fields[i]
		} else {
			dnsRecord.Value = fields[i]
		}
		switch {
		case fields[i] == "RRSIG":
			dnsRecord.Special = "RRSIG"
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
				dnsRecord.Response = fields[i]
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

func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
