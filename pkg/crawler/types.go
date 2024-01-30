package crawler

import (
	cfg "github.com/pzaino/thecrowler/pkg/config"
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

var (
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
