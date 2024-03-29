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

// Package httpinfo provides functionality to extract HTTP header information
package httpinfo

import (
	"crypto/x509"
	"net/http"
	"regexp"
	"time"
)

// Config is a struct to specify the configuration for header extraction
type Config struct {
	URL             string
	CustomHeader    map[string]string
	FollowRedirects bool
	Timeout         int
	SSLMode         string
	SSLDiscovery    bool
}

// HTTPDetails is a struct to store the collected HTTP header information
/*
type HTTPDetails struct {
	URL                           string            `json:"url"`
	CustomHeaders                 map[string]string `json:"custom_headers"`
	FollowRedirects               bool              `json:"follow_redirects"`
	ResponseHeaders               http.Header       `json:"response_headers"`
	ServerType                    string            `json:"server_type"`
	PoweredBy                     string            `json:"powered_by"`
	AspNetVersion                 string            `json:"asp_net_version"`
	FrameOptions                  string            `json:"frame_options"`
	XSSProtection                 string            `json:"xss_protection"`
	ContentType                   string            `json:"content_type"`
	ContentTypeOptions            string            `json:"content_type_options"`
	ContentSecurityPolicy         string            `json:"content_security_policy"`
	StrictTransportSecurity       string            `json:"strict_transport_security"`
	AccessControlAllowOrigin      string            `json:"access_control_allow_origin"`
	AccessControlAllowMethods     string            `json:"access_control_allow_methods"`
	AccessControlAllowHeaders     string            `json:"access_control_allow_headers"`
	AccessControlAllowCredentials string            `json:"access_control_allow_credentials"`
	AccessControlExposeHeaders    string            `json:"access_control_expose_headers"`
	SetCookie                     string            `json:"set_cookie"`
	WwwAuthenticate               string            `json:"www_authenticate"`
	ProxyAuthenticate             string            `json:"proxy_authenticate"`
	KeepAlive                     string            `json:"keep_alive"`
	Expires                       string            `json:"expires"`
	LastModified                  string            `json:"last_modified"`
	ETag                          string            `json:"etag"`
	ContentDisposition            string            `json:"content_disposition"`
	ContentLength                 string            `json:"content_length"`
	ContentEncoding               string            `json:"content_encoding"`
	TransferEncoding              string            `json:"transfer_encoding"`
	HSTS                          string            `json:"hsts"`
	ResponseBodyInfo              []string          `json:"response_body_info"`
}
*/

type HTTPDetails struct {
	URL             string            `json:"url"`
	CustomHeaders   map[string]string `json:"custom_headers"`
	FollowRedirects bool              `json:"follow_redirects"`
	ResponseHeaders http.Header       `json:"response_headers"`
	DetectedAssets  map[string]string `json:"detected_assets"`
}

// This struct is used to store the info we fetch about trustworthy authorities
// from https://www.ccadb.org/resources
type Authority struct {
	CAOwner                           string `json:"ca_owner"`
	SalesforceRecordID                string `json:"salesforce_record_id"`
	CertificateName                   string `json:"certificate_name"`
	ParentSalesforceRecordID          string `json:"parent_salesforce_record_id"`
	ParentCertificateName             string `json:"parent_certificate_name"`
	CertificateRecordType             string `json:"certificate_record_type"`
	RevocationStatus                  string `json:"revocation_status"`
	SHA256Fingerprint                 string `json:"sha256_fingerprint"`
	ParentSHA256Fingerprint           string `json:"parent_sha256_fingerprint"`
	AuditsSameAsParent                string `json:"audits_same_as_parent"`
	Auditor                           string `json:"auditor"`
	StandardAuditURL                  string `json:"standard_audit_url"`
	StandardAuditType                 string `json:"standard_audit_type"`
	StandardAuditStatementDate        string `json:"standard_audit_statement_date"`
	StandardAuditPeriodStartDate      string `json:"standard_audit_period_start_date"`
	StandardAuditPeriodEndDate        string `json:"standard_audit_period_end_date"`
	BRAuditURL                        string `json:"br_audit_url"`
	BRAuditType                       string `json:"br_audit_type"`
	BRAuditStatementDate              string `json:"br_audit_statement_date"`
	BRAuditPeriodStartDate            string `json:"br_audit_period_start_date"`
	BRAuditPeriodEndDate              string `json:"br_audit_period_end_date"`
	EVSSLAuditURL                     string `json:"evssl_audit_url"`
	EVSSLAuditType                    string `json:"evssl_audit_type"`
	EVSSLAuditStatementDate           string `json:"evssl_audit_statement_date"`
	EVSSLAuditPeriodStartDate         string `json:"evssl_audit_period_start_date"`
	EVSSLPeriodEndDate                string `json:"evssl_period_end_date"`
	EVCodeSigningAuditURL             string `json:"ev_code_signing_audit_url"`
	EVCodeSigningAuditType            string `json:"ev_code_signing_audit_type"`
	EVCodeSigningAuditStatementDate   string `json:"ev_code_signing_audit_statement_date"`
	EVCodeSigningAuditPeriodStartDate string `json:"ev_code_signing_audit_period_start_date"`
	EVCodeSigningAuditPeriodEndDate   string `json:"ev_code_signing_audit_period_end_date"`
	CPCPSSameAsParent                 string `json:"cpcps_same_as_parent"`
	CertificatePolicyURL              string `json:"certificate_policy_url"`
	CertificatePracticeStatementURL   string `json:"certificate_practice_statement_url"`
	CPCPSLastUpdatedDate              string `json:"cpcps_last_updated_date"`
	TestWebsiteURLValid               string `json:"test_website_url_valid"`
	TestWebsiteURLExpired             string `json:"test_website_url_expired"`
	TestWebsiteURLRevoked             string `json:"test_website_url_revoked"`
	TechnicallyConstrained            string `json:"technically_constrained"`
	MozillaStatus                     string `json:"mozilla_status"`
	MicrosoftStatus                   string `json:"microsoft_status"`
	SubordinateCAOwner                string `json:"subordinate_ca_owner"`
	FullCRLIssuedByThisCA             string `json:"full_crl_issued_by_this_ca"`
	JSONArrayOfPartitionedCRLs        string `json:"json_array_of_partitioned_crls"`
	ValidFromGMT                      string `json:"valid_from_gmt"`
	ValidToGMT                        string `json:"valid_to_gmt"`
	ChromeStatus                      string `json:"chrome_status"`
}

// SSLInfo contains information about the SSL certificate
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
	CertExpiration               time.Time           `json:"cert_expiration"`
}

// CMS Micro-Signature Patterns table: CMS name -> list of patterns
var CMSPatterns = map[string][]*regexp.Regexp{
	"Wordpress": {
		regexp.MustCompile(`/wp-json/`),
		regexp.MustCompile(`/api.w.org/`),
	},
	"Drupal": {
		regexp.MustCompile(`/some-drupal-signature/`),
		regexp.MustCompile(`/another-drupal-pattern/`),
	},
	// Add more CMS and patterns as needed
}
