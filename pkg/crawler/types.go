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
	cfg "github.com/pzaino/thecrowler/pkg/config"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

// SeleniumInstance holds a Selenium service and its configuration
type SeleniumInstance struct {
	Service *selenium.Service
	Config  cfg.Selenium
}

// MetaTag represents a single meta tag, including its name and content.
type MetaTag struct {
	Name    string
	Content string
}

// PageInfo represents the information of a web page.
type PageInfo struct {
	URL          string             `json:"URL"` // The URL of the web page.
	sourceID     int64              // The ID of the source.
	Title        string             `json:"title"`         // The title of the web page.
	Summary      string             `json:"summary"`       // A summary of the web page content.
	BodyText     string             `json:"body_text"`     // The main body text of the web page.
	HTML         string             `json:"html"`          // The HTML content of the web page.
	MetaTags     []MetaTag          `json:"meta_tags"`     // The meta tags of the web page.
	Keywords     map[string]string  `json:"keywords"`      // The keywords of the web page.
	DetectedType string             `json:"detected_type"` // The detected document type of the web page.
	DetectedLang string             `json:"detected_lang"` // The detected language of the web page.
	NetInfo      *neti.NetInfo      `json:"net_info"`      // The network information of the web page.
	HTTPInfo     *httpi.HTTPDetails `json:"http_info"`     // The HTTP header information of the web page.
	ScrapedData  []ScrapedItem      `json:"scraped_data"`  // The scraped data from the web page.
	Links        []string           `json:"links"`         // The links found in the web page.
	PerfInfo     PerformanceLog     `json:"performance"`   // The performance information of the web page.
}

// WebObjectDetails represents the details of a web object.
type WebObjectDetails struct {
	ScrapedData []ScrapedItem  `json:"scraped_data"` // The scraped data from the web page.
	Links       []string       `json:"links"`        // The links found in the web page.
	PerfInfo    PerformanceLog `json:"performance"`  // The performance information of the web page.
}

type ScrapedItem map[string]interface{}

type PerformanceLog struct {
	TCPConnection   float64               `json:"tcp_connection"`     // The time to establish a TCP connection.
	TimeToFirstByte float64               `json:"time_to_first_byte"` // The time to first byte.
	ContentLoad     float64               `json:"content_load"`       // The time to load the content.
	DNSLookup       float64               `json:"dns_lookup"`         // Number of DNS lookups.
	PageLoad        float64               `json:"page_load"`          // The time to load the page.
	LogEntries      []PerformanceLogEntry `json:"log_entries"`        // The log entries of the web page.
}

// PerformanceLog represents a structure for performance log entries
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

// ResponseExtraInfo represents additional information about a response in network logs.
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

// SecurityDetails holds detailed security information from the network logs.
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

// ResponseTiming holds timing information from the network logs.
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
	IndexID         int64  `json:"index_id"`
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
			"initialWindow": browserStartMaxDefault,   // Start with a maximized window
			"sandbox":       browserSandboxDefault,    // Bypass OS security model, necessary in some environments
			"infoBars":      browserInfoBarsDefault,   // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    browserExtensionsDefault, // Disables extensions to get a cleaner browsing experience
			"popups":        browserPopupsDefault,     // Disable pop-up blocking
			"gpu":           browserGpuDefault,        // Disable GPU hardware acceleration, if necessary
			"javascript":    browserJSDefault,         // Enable JavaScript, which is typically enabled in real user browsers
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
		".pdf":   "PDF",
		".html":  "HTML",
		".htm":   "HTML",
		".docx":  "DOCX",
		".xlsx":  "XLSX",
		".pptx":  "PPTX",
		".txt":   "TXT",
		".csv":   "CSV",
		".xml":   "XML",
		".json":  "JSON",
		".yaml":  "YAML",
		".yml":   "YAML",
		".tsv":   "TSV",
		".rtf":   "RTF",
		".doc":   "DOC",
		".xls":   "XLS",
		".ppt":   "PPT",
		".odt":   "ODT",
		".ods":   "ODS",
		".odp":   "ODP",
		".odg":   "ODG",
		".odf":   "ODF",
		".sxw":   "SXW",
		".sxc":   "SXC",
		".sxi":   "SXI",
		".sxd":   "SXD",
		".jar":   "JAR",
		".war":   "WAR",
		".ear":   "EAR",
		".zip":   "ZIP",
		".tar":   "TAR",
		".gz":    "GZ",
		".bz2":   "BZ2",
		".7z":    "7Z",
		".rar":   "RAR",
		".tgz":   "TGZ",
		".tbz2":  "TBZ2",
		".txz":   "TXZ",
		".lzma":  "LZMA",
		".tlz":   "TLZ",
		".apk":   "APK",
		".exe":   "EXE",
		".dll":   "DLL",
		".so":    "SO",
		".rpm":   "RPM",
		".deb":   "DEB",
		".iso":   "ISO",
		".img":   "IMG",
		".swf":   "SWF",
		".flv":   "FLV",
		".mpg":   "MPG",
		".mp2":   "MP2",
		".mp3":   "MP3",
		".mp4":   "MP4",
		".m4v":   "M4V",
		".mov":   "MOV",
		".3gp":   "3GP",
		".avi":   "AVI",
		".wmv":   "WMV",
		".ogg":   "OGG",
		".oga":   "OGA",
		".ogv":   "OGV",
		".ogx":   "OGX",
		".aac":   "AAC",
		".wav":   "WAV",
		".mpc":   "MPC",
		".mkv":   "MKV",
		".webm":  "WEBM",
		".woff":  "WOFF",
		".woff2": "WOFF2",
		".ttf":   "TTF",
		".eot":   "EOT",
		".flac":  "FLAC",
		".m4a":   "M4A",
		".mid":   "MID",
		".midi":  "MIDI",
		".mka":   "MKA",
		".opus":  "OPUS",
		".ra":    "RA",
		".svg":   "SVG",
		".svgz":  "SVGZ",
		".xcf":   "XCF",
		".xpi":   "XPI",
		".xhtml": "XHTML",
		".3g2":   "3G2",
		".3gp2":  "3GP2",
		".3gpp":  "3GPP",
		".3gpp2": "3GPP2",
		// add more FILE EXTENSION -> DOCUMENT TYPE mappings here
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

	// Lists of button texts in different languages for 'Accept' and 'Consent'
	acceptTexts = []string{
		"Accept", "Akzeptieren", "Aceptar", "Accettare", "Accetto", "Accepter", "Aceitar",
		"Godta", "Aanvaarden", "Zaakceptuj", "Elfogad", "Принять", "同意",
		"承認", "수락", // Add more translations as needed
	}
	consentTexts = []string{
		"Consent", "Zustimmen", "Consentir", "Consentire", "Consento", "Consentement", "Concordar",
		"Samtykke", "Toestemmen", "Zgoda", "Hozzájárulás", "Согласие", "同意する",
		"同意", "동의", // Add more translations as needed
	}
	rejectTexts = []string{
		"Reject", "Ablehnen", "Rechazar", "Rifiutare", "Rifiuto", "Refuser", "Rejeitar",
		"Avvise", "Weigeren", "Odrzuć", "Elutasít", "Отклонить", "拒绝",
		"拒否", "거부", // Add more translations as needed
	}

	// Global map for selector values to text arrays
	textMap = map[string][]string{
		"{{accept}}":  acceptTexts,
		"{{consent}}": consentTexts,
		"{{reject}}":  rejectTexts,
	}
)
