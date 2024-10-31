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

// Package crawler implements the crawling logic of the application.
// It's responsible for crawling a website and extracting information from it.
package crawler

import (
	"sync"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

// Pars type to pass parameters to the goroutine
type Pars struct {
	WG      *sync.WaitGroup
	DB      cdb.Handler
	Src     cdb.Source
	Sel     *chan SeleniumInstance
	SelIdx  int
	RE      *rules.RuleEngine
	Sources *[]cdb.Source
	Index   uint64
	Status  *Status
}

// Status holds the status of the crawler
type Status struct {
	PipelineID      uint64
	SourceID        uint64
	Source          string
	TotalPages      int
	TotalLinks      int
	TotalSkipped    int
	TotalDuplicates int
	TotalErrors     int
	TotalScraped    int
	TotalActions    int
	TotalFuzzing    int
	StartTime       time.Time
	EndTime         time.Time
	CurrentDepth    int
	LastWait        float64
	LastDelay       float64
	LastError       string
	// Flags values: 0 - Not started yet, 1 - Running, 2 - Completed, 3 - Error
	NetInfoRunning  int // Flag to check if network info is already gathered
	HTTPInfoRunning int // Flag to check if HTTP info is already gathered
	PipelineRunning int // Flag to check if site info is already gathered
	CrawlingRunning int // Flag to check if crawling is still running
}

// SeleniumInstance holds a Selenium service and its configuration
type SeleniumInstance struct {
	Service *selenium.Service
	Config  cfg.Selenium
	Mutex   *sync.Mutex
}

// MetaTag represents a single meta tag, including its name and content.
type MetaTag struct {
	Name    string
	Content string
}

// PageInfo represents the information of a web page.
type PageInfo struct {
	URL                     string                           `json:"URL"` // The URL of the web page.
	sourceID                uint64                           // The ID of the source.
	Title                   string                           `json:"title"`                      // The title of the web page.
	Summary                 string                           `json:"summary"`                    // A summary of the web page content.
	BodyText                string                           `json:"body_text"`                  // The main body text of the web page.
	HTML                    string                           `json:"html"`                       // The HTML content of the web page.
	MetaTags                []MetaTag                        `json:"meta_tags"`                  // The meta tags of the web page.
	Keywords                map[string]string                `json:"keywords"`                   // The keywords of the web page.
	DetectedType            string                           `json:"detected_type"`              // The detected document type of the web page.
	DetectedLang            string                           `json:"detected_lang"`              // The detected language of the web page.
	NetInfo                 *neti.NetInfo                    `json:"net_info"`                   // The network information of the web page.
	HTTPInfo                *httpi.HTTPDetails               `json:"http_info"`                  // The HTTP header information of the web page.
	ScrapedData             []ScrapedItem                    `json:"scraped_data"`               // The scraped data from the web page.
	Links                   []LinkItem                       `json:"links"`                      // The links found in the web page.
	PerfInfo                PerformanceLog                   `json:"performance"`                // The performance information of the web page.
	DetectedTech            map[string]detect.DetectedEntity `json:"detected_tech"`              // The detected technologies of the web page.
	ExtDetectionResults     []map[string]interface{}         `json:"external_detection_results"` // The results of the external detection tools.
	CollectedSessionCookies map[string]interface{}           `json:"collected_session_cookies"`  // The session cookies collected from the web page.
	Config                  *cfg.Config                      `json:"config"`                     // The configuration of the web page.
}

// CollectedScript represents a single collected script.
type CollectedScript struct {
	ID           uint64   `json:"id"`
	ScriptType   string   `json:"script_type"`
	Original     string   `json:"original"`
	Script       string   `json:"script"`
	Errors       []string `json:"errors"`
	IsObfuscated bool     `json:"is_obfuscated"`
}

// WebObjectDetails represents the details of a web object.
type WebObjectDetails struct {
	ScrapedData  ScrapedItem                      `json:"scraped_data"`  // The scraped data from the web page.
	Links        []string                         `json:"links"`         // The links found in the web page.
	PerfInfo     PerformanceLog                   `json:"performance"`   // The performance information of the web page.
	DetectedTech map[string]detect.DetectedEntity `json:"detected_tech"` // The detected technologies of the web page.
}

// ScrapedItem represents a single scraped item.
type ScrapedItem map[string]interface{}

// PerformanceLog represents the performance log of a web page.
type PerformanceLog struct {
	TCPConnection   float64               `json:"tcp_connection"`     // The time to establish a TCP connection.
	TimeToFirstByte float64               `json:"time_to_first_byte"` // The time to first byte.
	ContentLoad     float64               `json:"content_load"`       // The time to load the content.
	DNSLookup       float64               `json:"dns_lookup"`         // Number of DNS lookups.
	PageLoad        float64               `json:"page_load"`          // The time to load the page.
	LogEntries      []PerformanceLogEntry `json:"log_entries"`        // The log entries of the web page.
}

// PerformanceLogEntry represents a structure for performance log entries
type PerformanceLogEntry struct {
	Message LogMessage `json:"message"` // The log message.
	Webview string     `json:"webview"` // The webview.
}

// LogMessage represents a log message
type LogMessage struct {
	Method string    `json:"method"` // The method of the log message.
	Params LogParams `json:"params"` // The parameters of the log message.
}

// LogParams represents the parameters of a log message
type LogParams struct {
	ResponseInfo LogResponseInfo `json:"response"`       // The extra information of the response.
	TimeStamp    float64         `json:"timestamp"`      // The timestamp of the log message.
	Type         string          `json:"type,omitempty"` // The type of the log message.
}

// LogResponseInfo represents additional information about a response in network logs.
type LogResponseInfo struct {
	BlockedCookies         []BlockedCookie    `json:"blockedCookies,omitempty"`         // The blocked cookies.
	Headers                map[string]string  `json:"headers,omitempty"`                // The headers of the response.
	RequestID              string             `json:"requestId"`                        // The ID of the request.
	ResourceIPAddressSpace string             `json:"resourceIPAddressSpace,omitempty"` // The IP address space of the resource.
	StatusCode             int                `json:"statusCode"`                       // The status code of the response.
	StatusText             string             `json:"statusText"`                       // The status text of the response.
	MimeType               string             `json:"mimeType,omitempty"`               // The MIME type of the response.
	Protocol               string             `json:"protocol,omitempty"`               // The protocol of the response.
	RemoteIPAddress        string             `json:"remoteIPAddress,omitempty"`        // The remote IP address.
	RemotePort             int                `json:"remotePort,omitempty"`             // The remote port.
	ResponseTime           float64            `json:"responseTime,omitempty"`           // The response time.
	SecurityDetails        LogSecurityDetails `json:"securityDetails,omitempty"`        // Security details of the response.
	SecurityState          string             `json:"securityState,omitempty"`          // Security state of the response.
	Timing                 LogResponseTiming  `json:"timing,omitempty"`                 // Timing information.
	URL                    string             `json:"url"`                              // The URL of the response.
}

// LogSecurityDetails holds detailed security information from the network logs.
type LogSecurityDetails struct {
	CertificateID                     int      `json:"certificateId"`
	CertificateTransparencyCompliance string   `json:"certificateTransparencyCompliance"`
	Cipher                            string   `json:"cipher"`
	EncryptedClientHello              bool     `json:"encryptedClientHello"`
	Issuer                            string   `json:"issuer"`
	KeyExchange                       string   `json:"keyExchange"`
	KeyExchangeGroup                  string   `json:"keyExchangeGroup"`
	Protocol                          string   `json:"protocol"`
	SANList                           []string `json:"sanList"`
	ServerSignatureAlgorithm          int      `json:"serverSignatureAlgorithm"`
	SubjectName                       string   `json:"subjectName"`
	ValidFrom                         float64  `json:"validFrom"`
	ValidTo                           float64  `json:"validTo"`
}

// LogResponseTiming holds timing information from the network logs.
type LogResponseTiming struct {
	ConnectEnd               float64 `json:"connectEnd"`
	ConnectStart             float64 `json:"connectStart"`
	DNSEnd                   float64 `json:"dnsEnd"`
	DNSStart                 float64 `json:"dnsStart"`
	ReceiveHeadersEnd        float64 `json:"receiveHeadersEnd"`
	RequestTime              float64 `json:"requestTime"`
	SendEnd                  float64 `json:"sendEnd"`
	SendStart                float64 `json:"sendStart"`
	SSLStart                 float64 `json:"sslStart"`
	SSLEnd                   float64 `json:"sslEnd"`
	WorkerStart              float64 `json:"workerStart"`
	WorkerFetchStart         float64 `json:"workerFetchStart"`
	WorkerReady              float64 `json:"workerReady"`
	WorkerRespondWithSettled float64 `json:"workerRespondWithSettled"`
}

// BlockedCookie represents a blocked cookie object
type BlockedCookie struct {
	BlockedReasons []string `json:"blockedReasons"` // The reasons why the cookie was blocked.
	Cookie         Cookie   `json:"cookie"`         // The cookie that was blocked.
	CookieLine     string   `json:"cookieLine"`     // The cookie line.
}

// Cookie represents a cookie object
type Cookie struct {
	Domain       string  `json:"domain"`       // The domain of the cookie.
	Expires      float64 `json:"expires"`      // The expiration time of the cookie.
	HTTPOnly     bool    `json:"httpOnly"`     // Whether the cookie is HTTP only.
	Name         string  `json:"name"`         // The name of the cookie.
	Path         string  `json:"path"`         // The path of the cookie.
	Priority     string  `json:"priority"`     // The priority of the cookie.
	SameParty    bool    `json:"sameParty"`    // Whether the cookie is from the same party.
	SameSite     string  `json:"sameSite"`     // The same site attribute of the cookie.
	Secure       bool    `json:"secure"`       // Whether the cookie is secure.
	Session      bool    `json:"session"`      // Whether the cookie is a session cookie.
	Size         int     `json:"size"`         // The size of the cookie.
	SourcePort   int     `json:"sourcePort"`   // The source port of the cookie.
	SourceScheme string  `json:"sourceScheme"` // The source scheme of the cookie.
	Value        string  `json:"value"`        // The value of the cookie.
}

// PageDetails represents the details of a collected web page
type PageDetails struct {
	URL      string           `json:"URL"`         // The URL of the web page.
	Title    string           `json:"title"`       // The title of the web page.
	PerfInfo []PerformanceLog `json:"performance"` // The performance information of the web page.
	Links    []string         `json:"links"`       // The links found in the web page.
}

// Screenshot represents the metadata of a webpage screenshot
type Screenshot struct {
	IndexID         uint64 `json:"index_id"`
	ScreenshotLink  string `json:"screenshot_link"`
	Height          int    `json:"height"`
	Width           int    `json:"width"`
	ByteSize        int    `json:"byte_size"`
	ThumbnailHeight int    `json:"thumbnail_height"`
	ThumbnailWidth  int    `json:"thumbnail_width"`
	ThumbnailLink   string `json:"thumbnail_link"`
	Format          string `json:"format"`
}

// ScraperRuleEngine extends RuleEngine from the ruleset package
type ScraperRuleEngine struct {
	*rs.RuleEngine // generic rule engine
}

// LinkItem represents a link item collected on a web page
type LinkItem struct {
	PageURL   string `json:"url"`
	PageLevel int    `json:"level"`
	Link      string `json:"link"`
	ElementID string `json:"element_id"`
}

const (
	browserGpuDefault        = "--disable-gpu"
	browserJSDefault         = "--enable-javascript"
	browserStartMaxDefault   = "--start-maximized"
	browserWindowSizeDefault = "--window-size=1920,1080"
	browserExtensionsDefault = "--disable-extensions"
	browserSandboxDefault    = "--no-sandbox"
	browserInfoBarsDefault   = "--disable-infobars"
	browserPopupsDefault     = "--disable-popup-blocking"
	browserShmDefault        = "--disable-dev-shm-usage"
)

var (
	browserSettingsMap = map[string]map[string]string{
		"chrome": {
			"browserName":   "chrome",
			"windowSize":    browserWindowSizeDefault, // Set the window size to 1920x1080
			"initialWindow": browserStartMaxDefault,   // (--start-maximized) Start with a maximized window
			"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":        browserPopupsDefault,     // Disable pop-up blocking (--disable-popup-blocking)
			"gpu":           browserGpuDefault,        // (--disable-gpu) Disable GPU hardware acceleration, if necessary
			"javascript":    browserJSDefault,         // (--enable-javascript) Enable JavaScript, which is typically enabled in real user browsers
			"headless":      "",                       // Run in headless mode (--headless)
			"incognito":     "",                       // Run in incognito mode
			"disableDevShm": browserShmDefault,        // Disable /dev/shm use
		},
		"firefox": {
			"browserName":   "firefox",
			"initialWindow": browserStartMaxDefault, // Start with a maximized window
			//"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			//"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions": browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":     browserPopupsDefault,     // Disable pop-up blocking
			//"gpu":           browserGpuDefault,        // Disable GPU hardware acceleration, if necessary
			"javascript": browserJSDefault, // Enable JavaScript, which is typically enabled in real user browsers
		},
		"chromium": {
			"browserName":   "chromium",
			"windowSize":    browserWindowSizeDefault, // Set the window size to 1920x1080
			"initialWindow": browserStartMaxDefault,   // Start with a maximized window
			"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":        browserPopupsDefault,     // Disable pop-up blocking
			"gpu":           browserGpuDefault,        // Disable GPU hardware acceleration, if necessary
			"javascript":    browserJSDefault,         // Enable JavaScript, which is typically enabled in real user browsers
			"headless":      "",                       // Run in headless mode
			"incognito":     "",                       // Run in incognito mode
			"disableDevShm": browserShmDefault,        // Disable /dev/shm use
		},
	}

	// docTypeMap maps the file extension to the document type.
	docTypeMap = map[string]string{
		".pdf":   "application/pdf",
		".html":  "text/html",
		".htm":   "text/htm",
		".docx":  "application/docx",
		".xlsx":  "application/xlsx",
		".pptx":  "application/pptx",
		".txt":   "application/txt",
		".csv":   "application/csv",
		".xml":   "application/xml",
		".json":  "application/json",
		".yaml":  "application/yaml",
		".yml":   "application/yaml",
		".tsv":   "application/tsv",
		".rtf":   "application/rtf",
		".doc":   "application/doc",
		".xls":   "application/xls",
		".ppt":   "application/ppt",
		".odt":   "application/odt",
		".ods":   "application/ods",
		".odp":   "application/odp",
		".odg":   "application/odg",
		".odf":   "application/odf",
		".sxw":   "application/sxw",
		".sxc":   "application/sxc",
		".sxi":   "application/sxi",
		".sxd":   "application/sxd",
		".jar":   "application/jar",
		".war":   "application/war",
		".ear":   "application/ear",
		".zip":   "application/zip",
		".tar":   "application/tar",
		".gz":    "application/gz",
		".bz2":   "application/bz2",
		".7z":    "application/7z",
		".rar":   "application/rar",
		".tgz":   "application/tgz",
		".tbz2":  "application/tbz2",
		".txz":   "application/txz",
		".lzma":  "application/lzma",
		".tlz":   "application/tlz",
		".apk":   "application/apk",
		".exe":   "application/exe",
		".dll":   "application/dll",
		".so":    "application/so",
		".rpm":   "application/rpm",
		".deb":   "application/deb",
		".iso":   "application/iso",
		".img":   "application/img",
		".swf":   "application/swf",
		".flv":   "application/FLV",
		".mpg":   "application/MPG",
		".mp2":   "application/MP2",
		".mp3":   "application/MP3",
		".mp4":   "application/MP4",
		".m4v":   "application/M4V",
		".mov":   "application/MOV",
		".3gp":   "application/3GP",
		".avi":   "application/AVI",
		".wmv":   "application/WMV",
		".ogg":   "application/OGG",
		".oga":   "application/OGA",
		".ogv":   "application/OGV",
		".ogx":   "application/OGX",
		".aac":   "application/AAC",
		".wav":   "application/WAV",
		".mpc":   "application/MPC",
		".mkv":   "application/MKV",
		".webm":  "application/WEBM",
		".woff":  "application/WOFF",
		".woff2": "application/WOFF2",
		".ttf":   "application/TTF",
		".eot":   "application/EOT",
		".flac":  "application/FLAC",
		".m4a":   "application/M4A",
		".mid":   "application/MID",
		".midi":  "application/MIDI",
		".mka":   "application/MKA",
		".opus":  "application/OPUS",
		".ra":    "application/RA",
		".svg":   "application/SVG",
		".svgz":  "application/SVGZ",
		".xcf":   "application/XCF",
		".xpi":   "application/XPI",
		".xhtml": "text/XHTML",
		".3g2":   "application/3G2",
		".3gp2":  "application/3GP2",
		".3gpp":  "application/3GPP",
		".3gpp2": "application/3GPP2",
	}

	// langMap maps the whatlanggo language code to the ISO 639-1 language code.
	langMap = map[string]string{
		"unknown":  "unknown",
		"afr":      "af",
		"sqi":      "sq",
		"amh":      "am",
		"ara":      "ar",
		"hye":      "hy",
		"asm":      "as",
		"aze":      "az",
		"aze_cyrl": "az",
		"bel":      "be",
		"ben":      "bn",
		"bod":      "bo",
		"bos":      "bs",
		"bul":      "bg",
		"cat":      "ca",
		"ceb":      "ceb",
		"ces":      "cs",
		"cha":      "ch",
		"cmn":      "zh",
		"cnr":      "ru",
		"cos":      "co",
		"cre":      "cr",
		"cym":      "cy",
		"dan":      "da",
		"deu":      "de",
		"div":      "dv",
		"ell":      "el",
		"eng":      "en",
		"rus":      "ru",
		"spa":      "es",
		"por":      "pt",
		"ita":      "it",
		"fra":      "fr",
		"ukr":      "uk",
		"pol":      "pl",
		"slv":      "sl",
		"nld":      "nl",
		"fin":      "fi",
		"tur":      "tr",
		"heb":      "he",
		"hin":      "hi",
		"jpn":      "ja",
		"kor":      "ko",
		"zho":      "zh",
		"vie":      "vi",
		"ind":      "id",
		"msa":      "ms",
		"tha":      "th",
		"kat":      "ka",
		"kat_old":  "ka",
		"hrv":      "hr",
		"ron":      "ro",
		"srp":      "sr",
		"srp_latn": "sr",
		"slk":      "sk",
		"slk_frak": "sk",
		"slk_old":  "sk",
		"slk_1929": "sk",
		"slk_1996": "sk",
		"slk_2006": "sk",
		"slk_2010": "sk",
		"slk_2018": "sk",
	}
)
