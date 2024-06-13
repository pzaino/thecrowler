package detection

import (
	"crypto/x509"
	"net/http"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	ruleset "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

// DetectionContext is a struct to store the context of the detection process
type DetectionContext struct {
	TargetURL    string              `json:"target_url"` // (optional) the URL of the target website
	TargetIP     string              `json:"target_ip"`  // (optional) the IP address of the target website
	WD           *selenium.WebDriver // (optional) the Selenium WebDriver (required to run detection plugins)
	Header       *http.Header        // (optional) the HTTP header of the target website
	HSSLInfo     *SSLInfo            `json:"ssl_info"`      // (optional) the SSL information of the target website
	ResponseBody *string             `json:"response_body"` // (optional) the body of the HTTP response
	RE           *ruleset.RuleEngine // (required) the RuleEngine to use for the detection process
}

// DetectedEntity is a struct to store the detected entity (technology, asset, etc.)
type DetectedEntity struct {
	EntityType      string                 `json:"entity_type"`
	EntityName      string                 `json:"entity_name"`
	Confidence      float32                `json:"confidence"`
	MatchedPatterns []string               `json:"matched_patterns"`
	PluginResult    map[string]interface{} `json:"plugin_result"`
}

// SSLInfo contains information about the SSL certificate detected on a website
type SSLInfo struct {
	URL                          string              `json:"url"`
	CertChain                    []*x509.Certificate `json:"cert_chain"`
	IntermediateAuthorities      []string            `json:"intermediate_authorities"`
	IsCertChainOrderValid        bool                `json:"is_cert_chain_order_valid"`
	IsRootTrustworthy            bool                `json:"is_root_trustworthy"`
	IsCertValid                  bool                `json:"is_cert_valid"`
	IsCertExpired                bool                `json:"is_cert_expired"`
	IsCertRevoked                bool                `json:"is_cert_revoked"`
	IsCertSelfSigned             bool                `json:"is_cert_self_signed"`
	IsCertCA                     bool                `json:"is_cert_ca"`
	IsCertIntermediate           bool                `json:"is_cert_intermediate"`
	IsCertLeaf                   bool                `json:"is_cert_leaf"`
	IsCertTrusted                bool                `json:"is_cert_trusted"`
	IsCertTechnicallyConstrained bool                `json:"is_cert_technically_constrained"`
	IsCertEV                     bool                `json:"is_cert_ev"`
	IsCertEVCodeSigning          bool                `json:"is_cert_ev_code_signing"`
	IsCertEVSSL                  bool                `json:"is_cert_ev_ssl"`
	IsCertEVSGC                  bool                `json:"is_cert_ev_sgc"`
	IsCertEVSGCSSL               bool                `json:"is_cert_ev_sgc_ssl"`
	IsCertEVSGCCA                bool                `json:"is_cert_ev_sgc_ca"`
	IsCertEVSGCCASSL             bool                `json:"is_cert_ev_sgc_ca_ssl"`
	IsCertEVSGCCACodeSigning     bool                `json:"is_cert_ev_sgc_ca_code_signing"`
	IsCertEVSGCCACodeSigningSSL  bool                `json:"is_cert_ev_sgc_ca_code_signing_ssl"`
	IsCertEVSGCCodeSigning       bool                `json:"is_cert_ev_sgc_ca_code_signing_ev"`
	IsCertEVSGCCodeSigningSSL    bool                `json:"is_cert_ev_sgc_ca_code_signing_ev_ssl"`
	CertExpiration               cmn.FlexibleDate    `json:"cert_expiration"`
	Fingerprints                 map[string]string   `json:"fingerprints"`
}
