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
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

// SeleniumInstance holds a Selenium service and its configuration
type SeleniumInstance struct {
	Service *selenium.Service
	Config  cfg.Selenium
}

// PageInfo represents the information of a web page.
type PageInfo struct {
	sourceID     int               // The ID of the source.
	Title        string            // The title of the web page.
	Summary      string            // A summary of the web page content.
	BodyText     string            // The main body text of the web page.
	MetaTags     map[string]string // The meta tags of the web page.
	DetectedType string            // The detected document type of the web page.
	DetectedLang string            // The detected language of the web page.
}

// ScraperRuleEngine extends RuleEngine from the ruleset package
type ScraperRuleEngine struct {
	*rs.RuleEngine // generic rule engine
}

var (
	browserSettingsMap = map[string]map[string]string{
		"chrome": {
			"browserName":   "chrome",
			"windowSize":    "--window-size=1920,1080", // Set the window size to 1920x1080
			"initialWindow": "--start-maximized",       // (--start-maximized) Start with a maximized window
			"sandbox":       "--no-sandbox",            // Bypass OS security model, necessary in some environments
			"infoBars":      "--disable-infobars",      // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    "--disable-extensions",    // Disables extensions to get a cleaner browsing experience
			"popups":        "",                        // Disable pop-up blocking (--disable-popup-blocking)
			"gpu":           "--disable-gpu",           // (--disable-gpu) Disable GPU hardware acceleration, if necessary
			"javascript":    "--enable-javascript",     // (--enable-javascript) Enable JavaScript, which is typically enabled in real user browsers
			"headless":      "",                        // Run in headless mode (--headless)
			"incognito":     "",                        // Run in incognito mode
			"disableDevShm": "--disable-dev-shm-usage", // Disable /dev/shm use
		},
		"firefox": {
			"browserName":   "firefox",
			"initialWindow": "--start-maximized",        // Start with a maximized window
			"sandbox":       "--no-sandbox",             // Bypass OS security model, necessary in some environments
			"infoBars":      "--disable-infobars",       // Disables the "Chrome is being controlled by automated test software" infobar
			"extensions":    "--disable-extensions",     // Disables extensions to get a cleaner browsing experience
			"popups":        "--disable-popup-blocking", // Disable pop-up blocking
			"gpu":           "--disable-gpu",            // Disable GPU hardware acceleration, if necessary
			"javascript":    "--enable-javascript",      // Enable JavaScript, which is typically enabled in real user browsers
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
)
