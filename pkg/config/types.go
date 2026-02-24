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

// Package config contains the configuration file parsing logic.
package config

import (
	"strings"
	"time"
)

// FileStorageAPI is a generic File Storage API configuration
type FileStorageAPI struct {
	Host    string `json:"host" yaml:"host"`       // Hostname of the API server
	Path    string `json:"path" yaml:"path"`       // Path to the storage (e.g., "/tmp/images" or Bucket name)
	Port    int    `json:"port" yaml:"port"`       // Port number of the API server
	Region  string `json:"region" yaml:"region"`   // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token   string `json:"token" yaml:"token"`     // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret  string `json:"secret" yaml:"secret"`   // Secret for API authentication (e.g., AWS secret access key)
	Timeout int    `json:"timeout" yaml:"timeout"` // Timeout for API requests (in seconds)
	Type    string `json:"type" yaml:"type"`       // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode string `json:"sslmode" yaml:"sslmode"` // SSL mode for API connection (e.g., "disable")
}

// Database represents the database configuration
type Database struct {
	Type         string `json:"type" yaml:"type"`                     // Type of database (e.g., "postgres", "mysql", "sqlite")
	Host         string `json:"host" yaml:"host"`                     // Hostname of the database server
	Port         int    `json:"port" yaml:"port"`                     // Port number of the database server
	User         string `json:"user" yaml:"user"`                     // Username for database authentication
	Password     string `json:"password" yaml:"password"`             // Password for database authentication
	DBName       string `json:"dbname" yaml:"dbname"`                 // Name of the database
	RetryTime    int    `json:"retry_time" yaml:"retry_time"`         // Time to wait before retrying to connect to the database (in seconds)
	PingTime     int    `json:"ping_time" yaml:"ping_time"`           // Time to wait before retrying to ping the database (in seconds)
	SSLMode      string `json:"sslmode" yaml:"sslmode"`               // SSL mode for database connection (e.g., "disable")
	OptimizeFor  string `json:"optimize_for" yaml:"optimize_for"`     // Optimize for the database connection (e.g., "read", "write")
	MaxConns     int    `json:"max_conns" yaml:"max_conns"`           // Maximum number of connections to the database
	MaxIdleConns int    `json:"max_idle_conns" yaml:"max_idle_conns"` // Maximum number of idle connections to the database
}

// Crawler represents the crawler configuration
type Crawler struct {
	QueryTimer            int             `json:"query_timer" yaml:"query_timer"` // Time to wait before querying the next source (in seconds)
	Workers               int             `json:"workers" yaml:"workers"`         // Number of crawler workers
	Engine                []CustomEngine  `json:"engine" yaml:"engine"`           // Crawler engine to use (e.g., "chromium", "firefox", "selenium")
	VDIName               string          // Name of the VDI to use (this is useful when using custom configurations per each source)
	SetVDIGPUPatch        bool            `json:"set_vdi_gpu_patch" yaml:"set_vdi_gpu_patch"` // Whether to set the VDI GPU patch or not (this can be useful when using GPU-less VDIs)
	ResolveVDIDNS         bool            `json:"resolve_vdi_dns" yaml:"resolve_vdi_dns"`     // Whether to pre-resolve VDI DNS name or not
	Schedule              *EngineSchedule // Optional schedule configuration for this engine
	SourcePriority        string          // Source priority (e.g., "high", "medium", "low" , "medium,low", "high,medium,low")
	Platform              string          `json:"platform" yaml:"platform"`                                                 // Platform to use (e.g., "desktop", "mobile")
	BrowserPlatform       string          `json:"browser_platform" yaml:"browser_platform"`                                 // Browser platform to use (e.g., "desktop", "mobile")
	Interval              string          `json:"interval" yaml:"interval"`                                                 // Interval between crawler requests (in seconds)
	Timeout               int             `json:"timeout" yaml:"timeout"`                                                   // Timeout for crawler requests (in seconds)
	Maintenance           int             `json:"maintenance" yaml:"maintenance"`                                           // Interval between crawler maintenance tasks (in seconds)
	SourceScreenshot      bool            `json:"source_screenshot" yaml:"source_screenshot"`                               // Whether to take a screenshot of the source page or not
	FullSiteScreenshot    bool            `json:"full_site_screenshot" yaml:"full_site_screenshot"`                         // Whether to take a screenshot of the full site or not
	ScreenshotMaxHeight   int             `json:"screenshot_max_height" yaml:"screenshot_max_height"`                       // Maximum height of the screenshot
	ScreenshotSectionWait int             `json:"screenshot_section_wait" yaml:"screenshot_section_wait"`                   // Time to wait before taking a screenshot of a section in seconds
	MaxDepth              int             `json:"max_depth" yaml:"max_depth"`                                               // Maximum depth to crawl
	MaxLinks              int             `json:"max_links" yaml:"max_links"`                                               // Maximum number of links to crawl per Source
	MaxSources            int             `json:"max_sources" yaml:"max_sources"`                                           // Maximum number of sources to crawl
	InitialRampUp         int             `json:"initial_ramp_up" yaml:"initial_ramp_up"`                                   // Initial ramp-up time for the crawler (in seconds) (to help proxies to warm up) 0 = no ramp-up, -1 = automatic ramp-up
	Delay                 string          `json:"delay" yaml:"delay"`                                                       // Delay between requests (in seconds)
	BrowsingMode          string          `json:"browsing_mode" yaml:"browsing_mode"`                                       // Browsing type (e.g., "recursive", "human", "fuzzing")
	MaxRetries            int             `json:"max_retries" yaml:"max_retries"`                                           // Maximum number of retries
	MaxRedirects          int             `json:"max_redirects" yaml:"max_redirects"`                                       // Maximum number of redirects
	MaxRequests           int             `json:"max_requests" yaml:"max_requests"`                                         // Maximum number of requests
	ChangeUserAgent       string          `json:"change_useragent" yaml:"change_useragent"`                                 // Change user agent for each request (e.g., "never", "always", "on_start")
	ForceSFSSameOrigin    bool            `json:"force_sec_fetch_site_same_origin" yaml:"force_sec_fetch_site_same_origin"` // a technique to work around websites that requires sec-fetch-site same-origin for all pages but the home page
	ResetCookiesPolicy    string          `json:"reset_cookies_policy" yaml:"reset_cookies_policy"`                         // Cookies policy (e.g., "none", "on-request", "on-start", "when-done", "always")
	NoThirdPartyCookies   bool            `json:"no_third_party_cookies" yaml:"no_third_party_cookies"`                     // Whether to accept third-party cookies or not
	CrawlingInterval      string          `json:"crawling_interval" yaml:"crawling_interval"`                               // Time to wait before re-crawling a source
	CrawlingIfError       string          `json:"crawling_if_error" yaml:"crawling_if_error"`                               // Whether to re-crawl a source if an error occurs
	CrawlingIfOk          string          `json:"crawling_if_ok" yaml:"crawling_if_ok"`                                     // Whether to re-crawl a source if the crawling is successful
	ProcessingTimeout     string          `json:"processing_timeout" yaml:"processing_timeout"`                             // Timeout for processing the source
	RequestImages         bool            `json:"request_images" yaml:"request_images"`                                     // Whether to request the images or not
	RequestCSS            bool            `json:"request_css" yaml:"request_css"`                                           // Whether to request the CSS or not
	RequestScripts        bool            `json:"request_scripts" yaml:"request_scripts"`                                   // Whether to request the scripts or not
	RequestPlugins        bool            `json:"request_plugins" yaml:"request_plugins"`                                   // Whether to request the plugins or not
	RequestFrames         bool            `json:"request_frames" yaml:"request_frames"`                                     // Whether to request the frames or not
	PreventDuplicateURLs  bool            `json:"prevent_duplicate_urls" yaml:"prevent_duplicate_urls"`                     // Whether to prevent crawling of duplicate URLs or not
	RefreshContent        bool            `json:"refresh_content" yaml:"refresh_content"`                                   // Whether to refresh the content of the page or not
	CollectHTML           bool            `json:"collect_html" yaml:"collect_html"`                                         // Whether to collect the HTML content or not
	CollectImages         bool            `json:"collect_images" yaml:"collect_images"`                                     // Whether to collect the images or not
	CollectFiles          bool            `json:"collect_files" yaml:"collect_files"`                                       // Whether to collect the files or not
	CollectContent        bool            `json:"collect_content" yaml:"collect_content"`                                   // Whether to collect the content or not
	CollectKeywords       bool            `json:"collect_keywords" yaml:"collect_keywords"`                                 // Whether to collect the keywords or not
	CollectMetaTags       bool            `json:"collect_metatags" yaml:"collect_metatags"`                                 // Whether to collect the metatags or not
	CollectPerfMetrics    bool            `json:"collect_performance" yaml:"collect_performance"`                           // Whether to collect the performance metrics or not
	CollectPageEvents     bool            `json:"collect_events" yaml:"collect_events"`                                     // Whether to collect the page events or not
	CollectXHR            bool            `json:"collect_xhr" yaml:"collect_xhr"`                                           // Whether to collect the XHR requests or not
	FilterXHR             []string        `json:"filter_xhr" yaml:"filter_xhr"`                                             // Filter XHR mime_types
	CollectLinks          bool            `json:"collect_links" yaml:"collect_links"`                                       // Whether to collect the links or not
	ReportInterval        int             `json:"report_time" yaml:"report_time"`                                           // Time to wait before sending the report (in minutes)
	CheckForRobots        bool            `json:"check_for_robots" yaml:"check_for_robots"`                                 // Whether to check for robots.txt or not
	CreateEventWhenDone   bool            `json:"create_event_when_done" yaml:"create_event_when_done"`                     // Whether to create an event when the crawling is done or not
	Control               ControlConfig   `json:"control" yaml:"control"`                                                   // Control/COnsole internal API
}

// CustomEngine represents a custom engine configuration
type CustomEngine struct {
	Name           string          `json:"name" yaml:"name"`                             // Name of the custom engine (e.g., "chromium", "firefox", "selenium")
	QueryTimer     int             `json:"query_timer" yaml:"query_timer"`               // Time to wait before querying the next source (in seconds)
	SourcePriority []string        `json:"source_priority" yaml:"source_priority"`       // Source priority (e.g., "high", "medium", "low" , "medium,low", "high,medium,low")
	VDIName        []string        `json:"vdi_name" yaml:"vdi_name"`                     // Name of the VDI to use (this is useful when using custom configurations per each source)
	Schedule       *EngineSchedule `json:"schedule,omitempty" yaml:"schedule,omitempty"` // Optional schedule configuration
}

// EngineSchedule defines when a CROWler engine instance is allowed to operate
type EngineSchedule struct {
	ActiveDates *ActiveDateRange `json:"active_dates,omitempty" yaml:"active_dates,omitempty"` // Optional date range (ISO8601)
	Weekdays    []string         `json:"weekdays,omitempty" yaml:"weekdays,omitempty"`         // Optional list of active weekdays
	TimeRange   *TimeRange       `json:"time_range,omitempty" yaml:"time_range,omitempty"`     // Optional daily working hours
	Timezone    string           `json:"timezone,omitempty" yaml:"timezone,omitempty"`         // Optional IANA timezone (e.g. "Europe/London")
}

// ActiveDateRange represents a start and end datetime period
type ActiveDateRange struct {
	From string `json:"from" yaml:"from"` // Start date-time (RFC3339)
	To   string `json:"to" yaml:"to"`     // End date-time (RFC3339)
}

// TimeRange represents daily working hours (24-hour HH:MM)
type TimeRange struct {
	From string `json:"from" yaml:"from"` // Start time (HH:MM)
	To   string `json:"to" yaml:"to"`     // End time (HH:MM)
}

// IsActive determines whether the engine should be active at the given time.
// It respects the configured timezone, active date range, weekdays, and daily time range.
func (s *EngineSchedule) IsActive(now time.Time) bool {
	if s == nil {
		return true // No schedule → always active
	}

	// Load timezone (defaults to system)
	loc := time.Local
	if s.Timezone != "" {
		if tz, err := time.LoadLocation(s.Timezone); err == nil {
			loc = tz
		}
	}
	now = now.In(loc)

	// --- 1. Check active date range ---
	if s.ActiveDates != nil {
		start, err1 := time.ParseInLocation(time.RFC3339, s.ActiveDates.From, loc)
		end, err2 := time.ParseInLocation(time.RFC3339, s.ActiveDates.To, loc)
		if (err1 == nil) && (err2 == nil) {
			if now.Before(start) || now.After(end) {
				return false
			}
		}
	}

	// --- 2. Check weekday restriction ---
	if len(s.Weekdays) > 0 {
		currentDay := strings.ToLower(now.Weekday().String())
		if !containsIgnoreCase(s.Weekdays, currentDay) {
			return false
		}
	}

	// --- 3. Check daily time range ---
	if s.TimeRange != nil {
		layout := "15:04"
		start, err1 := time.ParseInLocation(layout, s.TimeRange.From, loc)
		end, err2 := time.ParseInLocation(layout, s.TimeRange.To, loc)
		if err1 == nil && err2 == nil {
			current, _ := time.ParseInLocation(layout, now.Format(layout), loc)
			if start.Before(end) || start.Equal(end) {
				// same-day window
				if current.Before(start) || current.After(end) {
					return false
				}
			} else {
				// overnight window
				if current.After(end) && current.Before(start) {
					return false
				}
			}
		}
	}

	return true
}

// containsIgnoreCase checks if a slice contains a string (case-insensitive)
func containsIgnoreCase(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}

// ControlConfig represents the internal control API configuration
type ControlConfig struct {
	Host              string `json:"host" yaml:"host"`                             // IP address for the health check server
	Port              int    `json:"port" yaml:"port"`                             // Port number for the health check server
	Timeout           int    `json:"timeout" yaml:"timeout"`                       // Timeout for API requests (in seconds)
	SSLMode           string `json:"sslmode" yaml:"sslmode"`                       // SSL mode for API connection (e.g., "disable")
	CertFile          string `json:"cert_file" yaml:"cert_file"`                   // Path to the SSL certificate file
	KeyFile           string `json:"key_file" yaml:"key_file"`                     // Path to the SSL key file
	RateLimit         string `json:"rate_limit" yaml:"rate_limit"`                 // Rate limit values are tuples (for ex. "1,3") where 1 means allows 1 request per second with a burst of 3 requests
	ReadHeaderTimeout int    `json:"readheader_timeout" yaml:"readheader_timeout"` // ReadHeaderTimeout is the amount of time allowed to read request headers.
	ReadTimeout       int    `json:"read_timeout" yaml:"read_timeout"`             // ReadTimeout is the maximum duration for reading the entire request
	WriteTimeout      int    `json:"write_timeout" yaml:"write_timeout"`           // WriteTimeout
}

// DNSConfig represents the DNS information gathering configuration
type DNSConfig struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`       // Whether to enable DNS information gathering or not
	Timeout   int    `json:"timeout" yaml:"timeout"`       // Timeout for DNS requests (in seconds)
	RateLimit string `json:"rate_limit" yaml:"rate_limit"` // Rate limit for DNS requests (in milliseconds)
}

// WHOISConfig represents the WHOIS information gathering configuration
type WHOISConfig struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Timeout   int    `json:"timeout" yaml:"timeout"`
	RateLimit string `json:"rate_limit" yaml:"rate_limit"`
}

// NetLookupConfig represents the network information gathering configuration
type NetLookupConfig struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Timeout   int    `json:"timeout" yaml:"timeout"`
	RateLimit string `json:"rate_limit" yaml:"rate_limit"`
}

// GeoLookupConfig represents the network information gathering configuration
type GeoLookupConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Type    string `json:"type" yaml:"type"`       // "maxmind" or "ip2location"
	DBPath  string `json:"db_path" yaml:"db_path"` // Used for MaxMind
	APIKey  string `json:"api_key" yaml:"api_key"` // Used for IP2Location
	Timeout int    `json:"timeout" yaml:"timeout"`
	SSLMode string `json:"sslmode" yaml:"sslmode"`
}

// HTTPConfig represents the HTTP information gathering configuration
type HTTPConfig struct {
	Enabled         bool           `json:"enabled" yaml:"enabled"`
	Timeout         int            `json:"timeout" yaml:"timeout"`
	FollowRedirects bool           `json:"follow_redirects" yaml:"follow_redirects"`
	SSLDiscovery    SSLScoutConfig `json:"ssl_discovery" yaml:"ssl_discovery"`
	Proxies         []SOCKSProxy   `json:"proxies" yaml:"proxies"`
}

// SSLScoutConfig represents the SSL information gathering configuration
type SSLScoutConfig struct {
	Enabled     bool `json:"enabled" yaml:"enabled"`
	JARM        bool `json:"jarm" yaml:"jarm"`
	JA3         bool `json:"ja3" yaml:"ja3"`
	JA3S        bool `json:"ja3s" yaml:"ja3s"`
	JA4         bool `json:"ja4" yaml:"ja4"`
	JA4S        bool `json:"ja4s" yaml:"ja4s"`
	HASSH       bool `json:"hassh" yaml:"hassh"`
	HASSHServer bool `json:"hassh_server" yaml:"hassh_server"`
	TLSH        bool `json:"tlsh" yaml:"tlsh"`
	SimHash     bool `json:"simhash" yaml:"simhash"`
	MinHash     bool `json:"minhash" yaml:"minhash"`
	BLAKE2      bool `json:"blake2" yaml:"blake2"`
	SHA256      bool `json:"sha256" yaml:"sha256"`
	CityHash    bool `json:"cityhash" yaml:"cityhash"`
	MurmurHash  bool `json:"murmurhash" yaml:"murmurhash"`
	CustomTLS   bool `json:"custom_tls" yaml:"custom_tls"`
}

// SOCKSProxy represents a SOCKS proxy configuration
type SOCKSProxy struct {
	Address  string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"`
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

// ServiceScoutConfig represents a structured configuration for an Nmap scan.
// This is a simplified example and does not cover all possible Nmap options.
type ServiceScoutConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"` // Whether to enable the Nmap scan or not
	Timeout int  `json:"timeout" yaml:"timeout"` // Timeout for the Nmap scan (in seconds)

	// Basic scan types
	IdleScan         SSIdleScan `yaml:"idle_scan"`             // --ip-options (Use idle scan)
	PingScan         bool       `yaml:"ping_scan"`             // -sn (No port scan)
	ConnectScan      bool       `yaml:"connect_scan"`          // -sT (TCP connect scan)
	SynScan          bool       `yaml:"syn_scan"`              // -sS (TCP SYN scan)
	UDPScan          bool       `yaml:"udp_scan"`              // -sU (UDP scan)
	NoDNSResolution  bool       `yaml:"no_dns_resolution"`     // -n (No DNS resolution)
	ServiceDetection bool       `yaml:"service_detection"`     // -sV (Service version detection)
	ServiceDB        string     `yaml:"service_db"`            // --service-db (Service detection database)
	OSFingerprinting bool       `yaml:"os_finger_print"`       // -O (Enable OS detection)
	AggressiveScan   bool       `yaml:"aggressive_scan"`       // -A (Aggressive scan options)
	ScriptScan       []string   `yaml:"script_scan,omitempty"` // --script (Script scan)

	// Host discovery
	Targets      []string `yaml:"targets,omitempty"`        // Targets can be IPs or hostnames
	ExcludeHosts []string `yaml:"excluded_hosts,omitempty"` // --exclude (Hosts to exclude)

	// Timing and performance
	TimingTemplate string `yaml:"timing_template"` // -T<0-5> (Timing template)
	HostTimeout    string `yaml:"host_timeout"`    // --host-timeout (Give up on target after this long)
	MinRate        string `yaml:"min_rate"`        // --min-rate (Send packets no slower than this)
	MaxRetries     int    `yaml:"max_retries"`     // --max-retries (Caps the number of port scan probe retransmissions)
	MaxPortNumber  int    `yaml:"max_port_number"` // allows to specify the maximum port number to scan (default is 9000)

	// Output (TBD)
	/*
	   OutputAll         bool     `yaml:"output_all"` // -oA (Output in the three major formats at once)
	   OutputNormal      bool     `yaml:"output_normal"` // -oN (Output in normal format)
	   OutputXML         bool     `yaml:"output_"` // -oX (Output in XML format)
	   OutputGrepable    bool     // -oG (Output in grepable format)
	   OutputDirectory   string   // Directory to store the output files
	*/

	// Advanced options
	SourcePort     int      `yaml:"source_port"`     // --source-port (Use given port number)
	Interface      string   `yaml:"interface"`       // -e (Use specified interface)
	SpoofIP        string   `yaml:"spoof_ip"`        // -S (Spoof source address)
	RandomizeHosts bool     `yaml:"randomize_hosts"` // --randomize-hosts (Randomize target scan order)
	DataLength     int      `yaml:"data_length"`     // --data-length (Append random data to sent packets)
	ScanDelay      string   `yaml:"delay"`           // --scan-delay (Adjust delay between probes)
	MTUDiscovery   bool     `yaml:"mtu_discovery"`   // --mtu (Discover MTU size)
	ScanFlags      string   `yaml:"scan_flags"`      // --scanflags (Customize TCP scan flags)
	IPFragment     bool     `yaml:"ip_fragment"`     // --ip-fragment (Fragment IP packets)
	MaxParallelism int      `yaml:"max_parallelism"` // --min-parallelism (Maximum number of parallelism)
	DNSServers     []string `yaml:"dns_servers"`     // --dns-servers (Specify custom DNS servers)
	Proxies        []string `yaml:"proxies"`         // Proxies for the database connection
}

// SSIdleScan represents the idle scan configuration
type SSIdleScan struct {
	ZombieHost string `yaml:"zombie_host"` // --zombie-host (Use a zombie host)
	ZombiePort int    `yaml:"zombie_port"` // --zombie-port (Use a zombie port)
}

// NetworkInfo represents the network information gathering configuration
type NetworkInfo struct {
	DNS          DNSConfig          `yaml:"dns"`
	WHOIS        WHOISConfig        `yaml:"whois"`
	NetLookup    NetLookupConfig    `yaml:"netlookup"`
	ServiceScout ServiceScoutConfig `yaml:"service_scout"`
	Geolocation  GeoLookupConfig    `yaml:"geolocation"`
	HostPlatform PlatformInfo       `yaml:"host_platform"`
}

// PlatformInfo represents the platform information
type PlatformInfo struct {
	OSName    string `yaml:"os_name"`
	OSVersion string `yaml:"os_version"`
	OSArch    string `yaml:"os_arch"`
}

// API represents the API configuration
type API struct {
	URL               string     `yaml:"url"`                                    // Base URL for the API (e.g., "http://localhost:8080/api")
	Host              string     `yaml:"host"`                                   // Hostname of the API server
	Port              int        `yaml:"port"`                                   // Port number of the API server
	Timeout           int        `yaml:"timeout"`                                // Timeout for API requests (in seconds)
	ContentSearch     bool       `yaml:"content_search"`                         // Whether to search in the content too or not
	EnableDefault     bool       `yaml:"enable_default"`                         // Whether to enable the default API endpoints or not
	EnableAPIDocs     bool       `json:"enable_api_docs" yaml:"enable_api_docs"` // Whether to enable API documentation or not
	ReturnContent     bool       `yaml:"return_content"`                         // Whether to return the content or not
	SSLMode           string     `yaml:"sslmode"`                                // SSL mode for API connection (e.g., "disable")
	CertFile          string     `yaml:"cert_file"`                              // Path to the SSL certificate file
	KeyFile           string     `yaml:"key_file"`                               // Path to the SSL key file
	RateLimit         string     `yaml:"rate_limit"`                             // Rate limit values are tuples (for ex. "1,3") where 1 means allows 1 request per second with a burst of 3 requests
	EnableConsole     bool       `yaml:"enable_console"`                         // Whether to enable the console or not
	ReadHeaderTimeout int        `yaml:"readheader_timeout"`                     // ReadHeaderTimeout is the amount of time allowed to read request headers.
	ReadTimeout       int        `yaml:"read_timeout"`                           // ReadTimeout is the maximum duration for reading the entire request
	WriteTimeout      int        `yaml:"write_timeout"`                          // WriteTimeout
	Return404         bool       `yaml:"return_404"`                             // Whether to return 404 for not found or not
	AllowedIPs        []string   `yaml:"allowed_ips"`                            // Allowed origins for CORS
	Plugins           APIPlugins `yaml:"plugins"`                                // API plugins configuration
}

// APIPlugins represents the API plugins configuration
type APIPlugins struct {
	Enabled bool     `yaml:"enabled"` // Whether to enable the plugins or not
	Timeout int      `yaml:"timeout"` // Timeout for plugin execution (in seconds)
	Allowed []string `yaml:"allowed"` // List of allowed plugins
}

// Selenium represents the CROWler VDI configuration
type Selenium struct {
	Name        string `yaml:"name"`         // Name of the Selenium instance
	Location    string `yaml:"location"`     // Location of the Selenium executable
	Path        string `yaml:"path"`         // Path to the Selenium executable
	DriverPath  string `yaml:"driver_path"`  // Path to the Selenium driver executable
	Type        string `yaml:"type"`         // Type of Selenium driver
	ServiceType string `yaml:"service_type"` // Type of Selenium service (standalone, hub)
	Port        int    `yaml:"port"`         // Port number for Selenium server
	Host        string `yaml:"host"`         // Hostname of the Selenium server
	Headless    bool   `yaml:"headless"`     // Whether to run Selenium in headless mode
	UseService  bool   `yaml:"use_service"`  // Whether to use Selenium service as well or not
	SSLMode     string `yaml:"sslmode"`      // SSL mode for Selenium connection (e.g., "disable")
	ProxyURL    string `yaml:"proxy_url"`    // Proxy URL for Selenium connection
	/*
		ProxyUser   string       `yaml:"proxy_user"`   // Proxy username for Selenium connection
		ProxyPass   string       `yaml:"proxy_pass"`   // Proxy password for Selenium connection
		ProxyPort   int          `yaml:"proxy_port"`   // Proxy port for Selenium connection
	*/
	DownloadDir string       `yaml:"download_dir"` // Download directory for Selenium
	Language    string       `yaml:"language"`     // Language for Selenium
	SysMng      SysMngConfig `yaml:"sys_manager"`  // System management configuration
}

// SysMngConfig represents the system management configuration
type SysMngConfig struct {
	Port              int    `yaml:"port"`               // Port number of the system management server
	Timeout           int    `yaml:"timeout"`            // Timeout for system management requests (in seconds)
	SSLMode           string `yaml:"sslmode"`            // SSL mode for system management connection (e.g., "disable")
	CertFile          string `yaml:"cert_file"`          // Path to the SSL certificate file
	KeyFile           string `yaml:"key_file"`           // Path to the SSL key file
	RateLimit         string `yaml:"rate_limit"`         // Rate limit values are tuples (for ex. "1,3") where 1 means allows 1 request per second with a burst of 3 requests
	ReadHeaderTimeout int    `yaml:"readheader_timeout"` // ReadHeaderTimeout is the amount of time allowed to read request headers.
	ReadTimeout       int    `yaml:"read_timeout"`       // ReadTimeout is the maximum duration for reading the entire request
	WriteTimeout      int    `yaml:"write_timeout"`      // WriteTimeout
}

// EventsConfig represents the events handler service configuration
type EventsConfig struct {
	URL                      string `json:"url" yaml:"url"`                                                 // Base URL for the events handler API (e.g., "http://localhost:8080/api/events")
	Host                     string `json:"host" yaml:"host"`                                               // Hostname of the events handler server
	Port                     int    `json:"port" yaml:"port"`                                               // Port number of the events handler server
	Timeout                  int    `json:"timeout" yaml:"timeout"`                                         // Timeout for events handler requests (in seconds)
	SSLMode                  string `json:"sslmode" yaml:"sslmode"`                                         // SSL mode for events handler connection (e.g., "disable")
	CertFile                 string `json:"cert_file" yaml:"cert_file"`                                     // Path to the SSL certificate file
	KeyFile                  string `json:"key_file" yaml:"key_file"`                                       // Path to the SSL key file
	RateLimit                string `json:"rate_limit" yaml:"rate_limit"`                                   // Rate limit values are tuples (for ex. "1,3") where 1 means allows 1 request per second with a burst of 3 requests
	ReadHeaderTimeout        int    `json:"readheader_timeout" yaml:"readheader_timeout"`                   // ReadHeaderTimeout is the amount of time allowed to read request headers.
	ReadTimeout              int    `json:"read_timeout" yaml:"read_timeout"`                               // ReadTimeout is the maximum duration for reading the entire request
	WriteTimeout             int    `json:"write_timeout" yaml:"write_timeout"`                             // WriteTimeout
	MasterEventsManager      string `json:"master_events_manager" yaml:"master_events_manager"`             // Master events manager's name (e.g., "crowler-events-0", "crowler-events-10")
	EnableAPIDocs            bool   `json:"enable_api_docs" yaml:"enable_api_docs"`                         // Whether to enable API documentation or not
	EventRemoval             string `json:"automatic_events_removal" yaml:"automatic_events_removal"`       // Automatic events removal from the database (always, fails, success, never or "")
	HeartbeatEnabled         bool   `json:"heartbeat_enabled" yaml:"heartbeat_enabled"`                     // Whether to enable heartbeat or not
	HeartbeatInterval        string `json:"heartbeat_interval" yaml:"heartbeat_interval"`                   // Heartbeat interval (in seconds)
	HeartbeatTimeout         string `json:"heartbeat_timeout" yaml:"heartbeat_timeout"`                     // Heartbeat timeout (in seconds)
	HeartbeatLog             bool   `json:"heartbeat_log" yaml:"heartbeat_log"`                             // Whether to log heartbeat or not
	SysDBMaintenance         string `json:"sys_db_maintenance_schedule" yaml:"sys_db_maintenance_schedule"` // System database maintenance interval (in hours)
	SysDBWebObjectsRetention string `json:"sys_db_webobjects_retention" yaml:"sys_db_webobjects_retention"` // System database web objects retention period (in hours/days/months/years)
}

// Rules represents the rules configuration sources for the crawler and the scrapper
type Rules struct {
	// Rules set location
	Path []string `yaml:"path"`
	// URL to fetch the rules from (in case they are distributed by a web server)
	URL []string `yaml:"url"`
	// Rules set update interval (in seconds)
	Interval int `yaml:"interval"`
}

// Remote represents a way to tell the CROWler to load a remote configuration
type Remote struct {
	Host    string `yaml:"host"`    // Hostname of the API server
	Path    string `yaml:"path"`    // Path to the storage (e.g., "/tmp/images" or Bucket name)
	Port    string `yaml:"port"`    // Port number of the API server (is typed string so users can use ENV)
	Region  string `yaml:"region"`  // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token   string `yaml:"token"`   // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret  string `yaml:"secret"`  // Secret for API authentication (e.g., AWS secret access key)
	Timeout int    `yaml:"timeout"` // Timeout for API requests (in seconds)
	Type    string `yaml:"type"`    // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode string `yaml:"sslmode"` // SSL mode for API connection (e.g., "disable")
}

// AgentsConfig represents the configuration section to tell the CROWler where to find the agents definitions
type AgentsConfig struct {
	GlobalParameters map[string]interface{} `yaml:"global_parameters" json:"global_parameters"` // Global parameters to be used by the agents
	Path             []string               `yaml:"path" json:"path"`                           // Path to the agents definition files
	Host             string                 `yaml:"host" json:"host"`                           // Hostname of the API server
	Port             string                 `yaml:"port" json:"port"`                           // Port number of the API server
	Region           string                 `yaml:"region" json:"region"`                       // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token            string                 `yaml:"token" json:"token"`                         // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret           string                 `yaml:"secret" json:"secret"`                       // Secret for API authentication (e.g., AWS secret access key)
	Timeout          int                    `yaml:"timeout" json:"timeout"`                     // Timeout for API requests (in seconds)
	AgentsTimeout    int                    `yaml:"agents_timeout" json:"agents_timeout"`       // Timeout for agent execution (in seconds)
	PluginsTimeout   int                    `yaml:"plugins_timeout" json:"plugins_timeout"`     // Timeout for plugin execution (in seconds)
	Type             string                 `yaml:"type" json:"type"`                           // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode          string                 `yaml:"sslmode" json:"sslmode"`                     // SSL mode for API connection (e.g., "disable")
	Refresh          int                    `yaml:"refresh" json:"refresh"`                     // Refresh interval for the ruleset (in seconds)
}

// RulesetConfig represents the top-level structure of the rules YAML file
type RulesetConfig struct {
	SchemaPath string   `yaml:"schema_path"` // Path to the JSON schema file
	Path       []string `yaml:"path"`        // Path to the ruleset files
	Host       string   `yaml:"host"`        // Hostname of the API server
	Port       string   `yaml:"port"`        // Port number of the API server
	Region     string   `yaml:"region"`      // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token      string   `yaml:"token"`       // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret     string   `yaml:"secret"`      // Secret for API authentication (e.g., AWS secret access key)
	Timeout    int      `yaml:"timeout"`     // Timeout for API requests (in seconds)
	Type       string   `yaml:"type"`        // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode    string   `yaml:"sslmode"`     // SSL mode for API connection (e.g., "disable")
	Refresh    int      `yaml:"refresh"`     // Refresh interval for the ruleset (in seconds)
}

// PluginConfig represents the top-level structure of the rules YAML file
type PluginConfig struct {
	GlobalParameters map[string]interface{} `yaml:"global_parameters"` // Global parameters to be used by the agents
	Path             []string               `yaml:"path"`              // Path to the ruleset files
	Host             string                 `yaml:"host"`              // Hostname of the API server
	Port             string                 `yaml:"port"`              // Port number of the API server
	Region           string                 `yaml:"region"`            // Region of the storage (e.g., "us-east-1" when using S3 like services)
	Token            string                 `yaml:"token"`             // Token for API authentication (e.g., API key or token, AWS access key ID)
	Secret           string                 `yaml:"secret"`            // Secret for API authentication (e.g., AWS secret access key)
	Timeout          int                    `yaml:"timeout"`           // Timeout for API requests (in seconds)
	Type             string                 `yaml:"type"`              // Type of storage (e.g., "local", "http", "volume", "queue", "s3")
	SSLMode          string                 `yaml:"sslmode"`           // SSL mode for API connection (e.g., "disable")
	Refresh          int                    `yaml:"refresh"`           // Refresh interval for the ruleset (in seconds)
}

// PrometheusConfig represents the Prometheus configuration
type PrometheusConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"` // Whether to enable Prometheus or not
	Host    string `json:"host" yaml:"host"`       // Hostname of the Prometheus server
	Port    int    `json:"port" yaml:"port"`       // Port number of the Prometheus server
}

// Config represents the structure of the configuration file
type Config struct {
	// Remote configuration
	Remote Remote `json:"remote" yaml:"remote"`

	// Database configuration
	Database Database `json:"database" yaml:"database"`

	// Crawler configuration
	Crawler Crawler `json:"crawler" yaml:"crawler"`

	// API configuration
	API API `json:"api" yaml:"api"`

	// VDIConfig configuration
	Selenium []Selenium `json:"selenium" yaml:"selenium"`

	// Prometheus configuration
	Prometheus PrometheusConfig `json:"prometheus" yaml:"prometheus"`

	// Events configuration
	Events EventsConfig `json:"events" yaml:"events"`

	// Image storage API configuration (to store images on a separate server)
	ImageStorageAPI FileStorageAPI `json:"image_storage" yaml:"image_storage"`

	// File storage API configuration (to store files on a separate server)
	FileStorageAPI FileStorageAPI `json:"file_storage" yaml:"file_storage"`

	// HTTP Headers Info configuration
	HTTPHeaders HTTPConfig `json:"http_headers" yaml:"http_headers"`

	// NetworkInfo configuration
	NetworkInfo NetworkInfo `json:"network_info" yaml:"network_info"`

	// Rules configuration
	RulesetsSchemaPath string          `json:"rulesets_schema_path" yaml:"rulesets_schema_path"` // Path to the JSON schema file for rulesets
	Rulesets           []RulesetConfig `json:"rulesets" yaml:"rulesets"`

	// Agents configuration
	Agents []AgentsConfig `json:"agents" yaml:"agents"`

	Plugins PluginsConfig `json:"plugins" yaml:"plugins"` // Plugins configuration

	ExternalDetection ExternalDetectionConfig `json:"external_detection" yaml:"external_detection"`

	OS         string // Operating system name
	DebugLevel int    `json:"debug_level" yaml:"debug_level"` // Debug level for logging
}

// PluginsConfig represents the configuration for plugins
type PluginsConfig struct {
	PluginsTimeout int            `json:"plugins_timeout" yaml:"plugins_timeout"` // Timeout for plugin execution (in seconds)
	Plugins        []PluginConfig `json:"locations" yaml:"locations"`
}

// ExternalDetectionConfig represents the configuration for external detection providers
type ExternalDetectionConfig struct {
	Timeout            int                     `json:"timeout" yaml:"timeout"`           // Timeout for external detection (in seconds)
	MaxRequests        int                     `json:"max_requests" yaml:"max_requests"` // Maximum number of requests
	MaxRetries         int                     `json:"max_retries" yaml:"max_retries"`   // Maximum number of retries
	Delay              string                  `json:"delay" yaml:"delay"`               // Delay between requests (in seconds)
	AbuseIPDB          ExtDetectProviderConfig `json:"abuse_ipdb" yaml:"abuse_ipdb"`
	AlienVault         ExtDetectProviderConfig `json:"alien_vault" yaml:"alien_vault"`
	Censys             ExtDetectProviderConfig `json:"censys" yaml:"censys"`
	CiscoUmbrella      ExtDetectProviderConfig `json:"cisco_umbrella" yaml:"cisco_umbrella"`
	Cuckoo             ExtDetectProviderConfig `json:"cuckoo" yaml:"cuckoo"`
	GreyNoise          ExtDetectProviderConfig `json:"grey_noise" yaml:"grey_noise"`
	GoogleSafeBrowsing ExtDetectProviderConfig `json:"google_safe_browsing" yaml:"google_safe_browsing"`
	HybridAnalysis     ExtDetectProviderConfig `json:"hybrid_analysis" yaml:"hybrid_analysis"`
	IPQualityScore     ExtDetectProviderConfig `json:"ip_quality_score" yaml:"ip_quality_score"`
	IPVoid             ExtDetectProviderConfig `json:"ipvoid" yaml:"ipvoid"`
	OpenPhish          ExtDetectProviderConfig `json:"open_phish" yaml:"open_phish"`
	PhishTank          ExtDetectProviderConfig `json:"phish_tank" yaml:"phish_tank"`
	Shodan             ExtDetectProviderConfig `json:"shodan" yaml:"shodan"`
	VirusTotal         ExtDetectProviderConfig `json:"virus_total" yaml:"virus_total"`
	URLHaus            ExtDetectProviderConfig `json:"url_haus" yaml:"url_haus"`
}

// ExtDetectProviderConfig represents the configuration for an external detection provider
type ExtDetectProviderConfig struct {
	Provider    string `json:"provider" yaml:"provider" validate:"required"`
	Host        string `json:"host" yaml:"host"`
	APIKeyLabel string `json:"api_key_label" yaml:"api_key_label"`
	APIKey      string `json:"api_key" yaml:"api_key"`
	APIID       string `json:"api_id" yaml:"api_id"`
	APISecret   string `json:"api_secret" yaml:"api_secret"`
	APIToken    string `json:"api_token" yaml:"api_token"`
}

/////////////////////////////////////////////////
//// ----------- Source Config ------------ ////

// SourceConfig represents the source configuration
type SourceConfig struct {
	Version        string                 `json:"version" yaml:"version" validate:"required,version_format"`               // Version of the source configuration
	FormatVersion  string                 `json:"format_version" yaml:"format_version" validate:"required,version_format"` // Regex for version format validation
	Author         string                 `json:"author,omitempty" yaml:"author,omitempty"`
	CreatedAt      time.Time              `json:"created_at,omitempty" yaml:"created_at,omitempty" validate:"datetime_format"` // Date-time formatted as string
	Description    string                 `json:"description,omitempty" yaml:"description,omitempty"`
	SourceName     string                 `json:"source_name" yaml:"source_name" validate:"required"`
	CrawlingConfig CrawlingConfig         `json:"crawling_config" yaml:"crawling_config" validate:"required"`
	ExecutionPlan  []ExecutionPlanItem    `json:"execution_plan,omitempty" yaml:"execution_plan,omitempty"`
	Custom         map[string]interface{} `json:"custom,omitempty" yaml:"custom,omitempty"` // Flexible custom configuration
	MetaData       map[string]interface{} `json:"meta_data,omitempty" yaml:"meta_data,omitempty"`
}

// CrawlingConfig represents the crawling configuration for a source
type CrawlingConfig struct {
	Site              string   `json:"site" yaml:"site" validate:"required,url"`
	URLReferrer       string   `json:"url_referrer,omitempty" yaml:"url_referrer,omitempty"`               // URL referrer for the source
	AlternativeLinks  []string `json:"alternative_links,omitempty" yaml:"alternative_links,omitempty"`     // URLs to use if no links are found
	RetriesOnRedirect int      `json:"retries_on_redirect,omitempty" yaml:"retries_on_redirect,omitempty"` // Number of retries on redirect
	UnwantedURLs      []string `json:"unwanted_urls,omitempty" yaml:"unwanted_urls,omitempty"`             // Unwanted URLs patterns that trigger a redirect detection
	SourceType        string   `json:"source_type" yaml:"source_type"`                                     // Type of the source (web, api, file) (validate:"required,oneof=website api file db")
}

// IsEmpty returns true if the CrawlingConfig is empty
func (c CrawlingConfig) IsEmpty() bool {
	return c.Site == "" && c.URLReferrer == "" && len(c.AlternativeLinks) == 0 && c.RetriesOnRedirect == 0 && len(c.UnwantedURLs) == 0 && c.SourceType == ""
}

// ExecutionPlanItem represents the execution plan item for a source
type ExecutionPlanItem struct {
	Label                string                 `json:"label" yaml:"label" validate:"required"`
	Conditions           Condition              `json:"conditions" yaml:"conditions" validate:"required"`
	Rulesets             []string               `json:"rulesets,omitempty" yaml:"rulesets,omitempty"`
	RuleGroups           []string               `json:"rule_groups,omitempty" yaml:"rule_groups,omitempty"`
	Rules                []string               `json:"rules,omitempty" yaml:"rules,omitempty"`
	AdditionalConditions map[string]interface{} `json:"additional_conditions,omitempty" yaml:"additional_conditions,omitempty"`
}

// Condition represents the conditions for a source
type Condition struct {
	URLPatterns []string `json:"url_patterns" yaml:"url_patterns" validate:"required"`
}

// FileReader is an interface to read files
type FileReader interface {
	ReadFile(filename string) ([]byte, error)
}

// IsEmpty returns true if the Crawler configuration is empty
func (c *Crawler) IsEmpty() bool {
	return c.MaxDepth == 0 && c.MaxLinks == 0 && c.MaxSources == 0 && c.Delay == "" && c.BrowsingMode == "" && c.MaxRetries == 0 && c.MaxRedirects == 0 && c.MaxRequests == 0 && c.ResetCookiesPolicy == "" && !c.NoThirdPartyCookies && c.CrawlingInterval == "" && c.CrawlingIfError == "" && c.CrawlingIfOk == "" && c.ProcessingTimeout == "" && !c.RequestImages && !c.RequestCSS && !c.RequestScripts && !c.RequestPlugins && !c.RequestFrames && !c.CollectHTML && !c.CollectImages && !c.CollectFiles && !c.CollectContent && !c.CollectKeywords && !c.CollectMetaTags && !c.CollectPerfMetrics && !c.CollectPageEvents && !c.CollectXHR && c.ReportInterval == 0 && !c.CheckForRobots && !c.CreateEventWhenDone && c.Control.IsEmpty()
}

// IsEmpty returns true if the ControlConfig is empty
func (c *ControlConfig) IsEmpty() bool {
	return c.Host == "" && c.Port == 0 && c.Timeout == 0 && c.SSLMode == "" && c.CertFile == "" && c.KeyFile == "" && c.RateLimit == "" && c.ReadHeaderTimeout == 0 && c.ReadTimeout == 0 && c.WriteTimeout == 0
}

// IsEmpty returns true if the SysMng is empty
func (s *SysMngConfig) IsEmpty() bool {
	return s.Port == 0 && s.Timeout == 0 && s.SSLMode == "" && s.CertFile == "" && s.KeyFile == "" && s.RateLimit == "" && s.ReadHeaderTimeout == 0 && s.ReadTimeout == 0 && s.WriteTimeout == 0
}

// IsEmpty returns true is the Selenium configuration is empty
func (s *Selenium) IsEmpty() bool {
	return s.Name == "" && s.Location == "" && s.Path == "" && s.DriverPath == "" && s.Type == "" && s.ServiceType == "" && s.Port == 0 && s.Host == "" && !s.Headless && !s.UseService && s.SSLMode == "" && s.ProxyURL == "" && s.DownloadDir == "" && s.Language == "" && s.SysMng.IsEmpty()
}
