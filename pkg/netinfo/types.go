package netinfo

// DNSRecord represents a DNS record.
type DNSRecord struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	TTL      string `json:"ttl"`
	Class    string `json:"class"`
	Response string `json:"response"`
	Section  string `json:"section"`
	Value    string `json:"value"`
	Special  string `json:"special"`
}

// DNSInfo represents DNS information for a domain.
type DNSInfo struct {
	Domain  string      `json:"domain"`
	Server  []string    `json:"server"`
	Records []DNSRecord `json:"records"`
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

// IPData represents the structure of the IP data you want to extract and store.
type IPData struct {
	IP        []string `json:"ip"`
	ASN       []string `json:"asn"`
	CIDR      []string `json:"cidr"`
	NetRange  []string `json:"net_range"`
	NetName   []string `json:"net_name"`
	NetHandle []string `json:"net_handle"`
	NetParent []string `json:"net_parent"`
	NetType   []string `json:"net_type"`
	Country   []string `json:"country"`
}

// ipExtraData represents the structure of the extra IP data you want to extract and store.
type ipExtraData struct {
	ASN       string
	CIDR      string
	NetRange  string
	NetName   string
	NetHandle string
	NetParent string
	NetType   string
	Country   string
}

// HostData represents the structure of the host data you want to extract and store.
type HostData struct {
	Host []string `json:"host"`
}

// NetInfo represents the structure of the network information you want to extract and store.
type NetInfo struct {
	URL   string      `json:"url"`
	Hosts HostData    `json:"hosts"`
	IPs   IPData      `json:"ips"`
	WHOIS []WHOISData `json:"whois"`
	DNS   []DNSInfo   `json:"dns"`
}

// Define a map to map record types to their corresponding values
var recordTypeMap = map[string]string{
	"A":      "A",
	"AAAA":   "AAAA",
	"MX":     "MX",
	"NS":     "NS",
	"CNAME":  "CNAME",
	"TXT":    "TXT",
	"SOA":    "SOA",
	"PTR":    "PTR",
	"SRV":    "SRV",
	"CAA":    "CAA",
	"TLSA":   "TLSA",
	"DS":     "DS",
	"DNSKEY": "DNSKEY",
	"NSEC":   "NSEC",
	"NSEC3":  "NSEC3",
}
