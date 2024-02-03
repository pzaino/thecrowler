package netinfo

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"

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

// GetIPs returns the IP addresses of the provided URL
func (ni *NetInfo) GetIPs() error {
	// retrieve ni.URL IP addresses
	ips, err := net.LookupIP(ni.URL)
	if err != nil {
		return fmt.Errorf("error looking up IP addresses: %v", err)
	}
	ipsStr := ipsToString(ips)

	// Append only new IPs to the existing list
	ipList := ni.IPs.IP
	asnList := ni.IPs.ASN
	cidrList := ni.IPs.CIDR
	ntRangeList := ni.IPs.NetRange
	ntNameList := ni.IPs.NetName
	ntHandleList := ni.IPs.NetHandle
	ntParentList := ni.IPs.NetParent
	ntTypeList := ni.IPs.NetType
	countryList := ni.IPs.Country

	for _, newIP := range ipsStr {
		isNew := true
		// Check if the IP is already in the list
		for _, ip := range ipList {
			if ip == newIP {
				isNew = false
				break
			}
		}
		if isNew {
			ipList = append(ipList, newIP)
			// Get network information for the new IP
			entity, err := getIPInfo(ni, newIP)
			if err == nil {
				if entity.ASN == "" {
					entity.ASN = "N/A"
				}
				if entity.CIDR == "" {
					entity.CIDR = "N/A"
				}
				asnList = append(asnList, entity.ASN)
				cidrList = append(cidrList, entity.CIDR)
				ntRangeList = append(ntRangeList, entity.NetRange)
				ntNameList = append(ntNameList, entity.NetName)
				ntHandleList = append(ntHandleList, entity.NetHandle)
				ntParentList = append(ntParentList, entity.NetParent)
				ntTypeList = append(ntTypeList, entity.NetType)
				countryList = append(countryList, entity.Country)
			} else {
				asnList = append(asnList, "N/A")
				cidrList = append(cidrList, "N/A")
				ntRangeList = append(ntRangeList, "")
				ntNameList = append(ntNameList, "")
				ntHandleList = append(ntHandleList, "")
				ntParentList = append(ntParentList, "")
				ntTypeList = append(ntTypeList, "")
				countryList = append(countryList, "")
			}
		}
	}

	// Update all IP fields at once
	ni.IPs = IPData{
		IP:        ipList,
		ASN:       asnList,
		CIDR:      cidrList,
		NetRange:  ntRangeList,
		NetName:   ntNameList,
		NetHandle: ntHandleList,
		NetParent: ntParentList,
		NetType:   ntTypeList,
		Country:   countryList,
	}

	return nil
}

// ipsToString converts []net.IP to []string
func ipsToString(ips []net.IP) []string {
	var ipStrs []string
	for _, ip := range ips {
		ipStrs = append(ipStrs, ip.String())
	}
	return ipStrs
}

// GetHostsFromIPs returns the hostnames of the provided IP addresses
func (ni *NetInfo) GetHostsFromIPs() error {
	var hosts []string
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Create a channel to receive results
	results := make(chan []string, len(ni.IPs.IP))

	for _, ip := range ni.IPs.IP {
		wg.Add(1)

		// Perform reverse DNS lookup in a goroutine
		go func(ip string) {
			defer wg.Done()

			hostnames, err := net.LookupAddr(ip)
			if err == nil {
				results <- hostnames
			} else {
				log.Printf("error looking up hostnames for IP %s: %v", ip, err)
			}
		}(ip)
	}

	// Wait for all goroutines to finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect and deduplicate hostnames
	for result := range results {
		mu.Lock()
		for _, hostname := range result {
			hosts = appendIfNotExists(hosts, hostname)
		}
		mu.Unlock()
	}

	ni.Hosts = HostData{Host: hosts}

	return nil
}

// appendIfNotExists appends an item to a slice if it doesn't already exist in the slice.
func appendIfNotExists(slice []string, item string) []string {
	for _, existingItem := range slice {
		if existingItem == item {
			return slice
		}
	}
	return append(slice, item)
}

// GetHosts returns the hostnames of the provided URL
func (ni *NetInfo) GetHosts() error {
	host := urlToDomain(ni.URL)
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("error looking up hostnames: %v", err)
	}

	// Append only new IPs to the existing list
	ipList := ni.IPs.IP
	for _, newIP := range ips {
		isNew := true
		// Check if the IP is already in the list
		for _, ip := range ipList {
			if ip == newIP {
				isNew = false
			}
		}
		if isNew {
			ipList = append(ipList, newIP)
		}
	}
	ni.IPs = IPData{IP: ipList}

	// Append the new host to the existing list
	hostList := ni.Hosts.Host
	isNew := true
	for _, h := range hostList {
		if h == host {
			isNew = false
		}
	}
	if isNew {
		hostList = append(hostList, host)
	}
	ni.Hosts = HostData{Host: hostList}

	return nil
}

// GetIPsFromHosts returns the IP addresses of the provided hostnames
func (ni *NetInfo) GetIPsFromHosts() error {
	var ips []string
	for _, host := range ni.Hosts.Host {
		ipAddrs, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("error looking up IP addresses: %v", err)
		}
		ips = append(ips, ipsToString(ipAddrs)...)
	}

	ni.IPs = IPData{IP: ips}

	return nil
}

// GetNetInfo returns the IP addresses and hostnames of the provided URL
func (ni *NetInfo) GetNetInfo(url string) error {
	ni.URL = url
	host0 := urlToDomain(url)
	ni.Hosts.Host = append(ni.Hosts.Host, host0)

	err := ni.GetIPs()
	if err != nil {
		return err
	}

	if len(ni.IPs.IP) == 0 {
		err = ni.GetHosts()
		if err != nil {
			return err
		}
		err = ni.GetIPsFromHosts()
		if err != nil {
			return err
		}
	} else {
		err = ni.GetHostsFromIPs()
		if err != nil {
			return err
		}
	}

	err = ni.GetWHOISData()
	if err != nil {
		return err
	}

	return nil
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
		//log.Printf("WHOIS Output for %s:\n%s", domain, whoisOutput)
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

				//log.Printf("Field: %s, Value: %s", fieldName, value)

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

// urlToDomain extracts the domain from a URL
func urlToDomain(url string) string {
	domain := url
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "www.")
	domain = strings.TrimPrefix(domain, "/") // Remove any trailing slash
	if strings.Contains(domain, "/") {
		domain = domain[:strings.Index(domain, "/")]
	}
	return domain
}

// getASNCIDRInfo queries a public WHOIS service to retrieve ASN and CIDR information.
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
	//log.Printf("WHOIS Result for IP %s:\n%s", ip, result)

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
