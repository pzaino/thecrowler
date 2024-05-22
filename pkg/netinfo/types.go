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
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// DNSRecord represents a DNS record.
type DNSRecord struct {
	Name     string `json:"name"`
	TTL      string `json:"ttl"`
	Class    string `json:"class"`
	Type     string `json:"type"`
	Response string `json:"response"`
	Value    string `json:"value"`
	Section  string `json:"section"`
	Special  string `json:"special"`
}

// DNSInfo represents DNS information for a domain.
type DNSInfo struct {
	Domain   string      `json:"domain"`
	Server   []string    `json:"server"`
	Records  []DNSRecord `json:"records"`
	Comments []string    `json:"comments"`
}

// WHOISData represents the structure of WHOIS data you want to extract and store.
type WHOISData struct {
	Entity                 string   `json:"entity"`
	EntityType             string   `json:"entity_type"`
	EntityStatus           string   `json:"entity_status"`
	RegistryDomainID       string   `json:"registry_domain_id"`
	RegistrarWhoisServer   string   `json:"registrar_whois_server"`
	RegistrarURL           string   `json:"registrar_url"`
	RegistryExpiryDate     string   `json:"registry_expiry_date"`
	RegistrarIANAID        string   `json:"registrar_iana_id"`
	Registrar              string   `json:"registrar"`
	Registrant             string   `json:"registrant"`
	RegistrantName         string   `json:"registrant_name"`
	RegistrantOrganization string   `json:"registrant_organization"`
	RegistrantStreet       string   `json:"registrant_street"`
	RegistrantCity         string   `json:"registrant_city"`
	RegistrantState        string   `json:"registrant_state"`
	RegistrantPostalCode   string   `json:"registrant_postal_code"`
	RegistrantCountry      string   `json:"registrant_country"`
	RegistrantPhone        string   `json:"registrant_phone"`
	RegistrantPhoneExt     string   `json:"registrant_phone_ext"`
	RegistrantFax          string   `json:"registrant_fax"`
	RegistrantFaxExt       string   `json:"registrant_fax_ext"`
	RegistrantEmail        string   `json:"registrant_email"`
	AdminContact           string   `json:"admin_contact"`
	TechContact            string   `json:"tech_contact"`
	TechEmail              string   `json:"tech_email"`
	NameServers            []string `json:"name_servers"`
	DNSSEC                 string   `json:"dnssec"`
	CreationDate           string   `json:"creation_date"`
	ExpirationDate         string   `json:"expiration_date"`
	UpdatedDate            string   `json:"updated_date"`
	NetRange               string   `json:"net_range"`
	OrgName                string   `json:"org_name"`
	OrgID                  string   `json:"org_id"`
	Address                string   `json:"address"`
	City                   string   `json:"city"`
	State                  string   `json:"state"`
	PostalCode             string   `json:"postal_code"`
	Country                string   `json:"country"`
	RegDate                string   `json:"reg_date"`
	Updated                string   `json:"updated"`
	Comment                string   `json:"comment"`
}

// DetectedLocation represents the detected geolocation for an IP address.
type DetectedLocation struct {
	CountryCode string
	CountryName string
	City        string
	Latitude    float64
	Longitude   float64
}

// IP2LocationResult represents the structure of the IP2Location result.
type IP2LocationResult struct {
	CountryCode string  `json:"country_code"`
	CountryName string  `json:"country_name"`
	RegionName  string  `json:"region_name"`
	CityName    string  `json:"city_name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

// IPInfo represents the structure of the IP information you want to extract and store.
type IPInfo struct {
	IP          string
	ASN         string
	CIDR        string
	NetRange    string
	NetName     string
	NetHandle   string
	NetParent   string
	NetType     string
	Country     string
	CountryCode string
	City        string
	Latitude    float64
	Longitude   float64
}

// IPData represents the structure of the IP data you want to extract and store.
type IPData struct {
	IP          []string  `json:"ip"`
	ASN         []string  `json:"asn"`
	CIDR        []string  `json:"cidr"`
	NetRange    []string  `json:"net_range"`
	NetName     []string  `json:"net_name"`
	NetHandle   []string  `json:"net_handle"`
	NetParent   []string  `json:"net_parent"`
	NetType     []string  `json:"net_type"`
	Country     []string  `json:"country"`
	CountryCode []string  `json:"country_code"`
	City        []string  `json:"city"`
	Latitude    []float64 `json:"latitude"`
	Longitude   []float64 `json:"longitude"`
}

// ipExtraData represents the structure of the extra IP data you want to extract and store.
type ipExtraData struct {
	ASN         string
	CIDR        string
	NetRange    string
	NetName     string
	NetHandle   string
	NetParent   string
	NetType     string
	Country     string
	CountryCode string
	City        string
	Latitude    float64
	Longitude   float64
}

// HostData represents the structure of the host data you want to extract and store.
type HostData struct {
	Host []string `json:"host"`
}

// NetInfo represents the structure of the network information you want to extract and store.
type NetInfo struct {
	URL          string           `json:"url,omitempty"`
	Hosts        HostData         `json:"hosts,omitempty"`
	IPs          IPData           `json:"ips,omitempty"`
	WHOIS        []WHOISData      `json:"whois,omitempty"`
	DNS          []DNSInfo        `json:"dns,omitempty"`
	ServiceScout ServiceScoutInfo `json:"service_scout,omitempty"`
	Config       *cfg.NetworkInfo `json:"Config,omitempty"`
}

// ServiceScoutInfo contains the information about the Nmap scan
type ServiceScoutInfo struct {
	Hosts []HostInfo `json:"hosts,omitempty"`
}

// HostInfo contains the information about a single host
type HostInfo struct {
	IP              []IPInfoDetails     `json:"ip"`
	Hostname        []HostNameDetails   `json:"hostname"`
	Ports           []PortInfo          `json:"ports,omitempty"`
	Services        []ServiceInfo       `json:"services,omitempty"`
	OS              []OSInfo            `json:"os,omitempty"`
	Vulnerabilities []VulnerabilityInfo `json:"vulnerabilities,omitempty"`
}

type IPInfoDetails struct {
	Address string `json:"address,omitempty"`
	Type    string `json:"address_type,omitempty"`
	Vendor  string `json:"vendor,omitempty"`
}

type HostNameDetails struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

type VulnerabilityInfo struct {
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Severity    string          `json:"severity,omitempty"`
	Reference   string          `json:"reference,omitempty"`
	Description string          `json:"description,omitempty"`
	State       string          `json:"state,omitempty"`
	Output      string          `json:"output,omitempty"`
	Elements    []ScriptElement `json:"elements,omitempty"`
	Tables      []ScriptTable   `json:"tables,omitempty"`
}

// PortInfo contains the information about a single port
type PortInfo struct {
	Port     int    `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	State    string `json:"state,omitempty"`
	Service  string `json:"service,omitempty"`
}

type ServiceInfo struct {
	Name          string       `json:"name,omitempty"`
	DeviceType    string       `json:"device_type,omitempty"`
	ExtraInfo     string       `json:"extra_info,omitempty"`
	HighVersion   string       `json:"high_version,omitempty"`
	LowVersion    string       `json:"low_version,omitempty"`
	Hostname      string       `json:"hostname,omitempty"`
	Method        string       `json:"method,omitempty"`
	OSType        string       `json:"os_type,omitempty"`
	Product       string       `json:"product,omitempty"`
	Proto         string       `json:"proto,omitempty"`
	RPCNum        string       `json:"rpc_num,omitempty"`
	ServiceFP     string       `json:"service_fp,omitempty"`
	Tunnel        string       `json:"tunnel,omitempty"`
	Version       string       `json:"version,omitempty"`
	Configuration string       `json:"configuration,omitempty"`
	CPEs          []string     `json:"cpes,omitempty"`
	Scripts       []ScriptInfo `json:"scripts,omitempty"`
}

type ScriptInfo struct {
	ID       string          `json:"id,omitempty"`
	Output   string          `json:"output,omitempty"`
	Elements []ScriptElement `json:"elements,omitempty"`
	Tables   []ScriptTable   `json:"tables,omitempty"`
}

type ScriptElement struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

type ScriptTable struct {
	Key      string          `json:"key,omitempty"`
	Elements []ScriptElement `json:"elements,omitempty"`
	Tables   []ScriptTable   `json:"tables,omitempty"`
}

// OSInfo contains the information about the detected OS
type OSInfo struct {
	Name     string    `json:"name,omitempty"`
	Accuracy int       `json:"accuracy,omitempty"`
	Classes  []OSCLass `json:"classes,omitempty"`
	Line     int       `json:"line,omitempty"`
}

type OSCLass struct {
	Type       string `json:"type,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	OSFamily   string `json:"os_family,omitempty"`
	OSGen      string `json:"os_gen,omitempty"`
	DeviceType string `json:"device_type,omitempty"`
	Accuracy   int    `json:"accuracy,omitempty"`
}

// Define a map to map record types to their corresponding values
var recordTypeMap = map[string]string{
	"A":          "A",
	"AAAA":       "AAAA",
	"MX":         "MX",
	"NS":         "NS",
	"CNAME":      "CNAME",
	"TXT":        "TXT",
	"SOA":        "SOA",
	"PTR":        "PTR",
	"SRV":        "SRV",
	"CAA":        "CAA",
	"TLSA":       "TLSA",
	"DS":         "DS",
	"DNSKEY":     "DNSKEY",
	"NSEC":       "NSEC",
	"NSEC3":      "NSEC3",
	"SPF":        "SPF",
	"DKIM":       "DKIM",
	"DMARC":      "DMARC",
	"OPENPGPKEY": "OPENPGPKEY",
	"URI":        "URI",
}
