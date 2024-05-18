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
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"sort"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
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
type HTTPDetails struct {
	URL              string                    `json:"url"`
	CustomHeaders    map[string]string         `json:"custom_headers"`
	FollowRedirects  bool                      `json:"follow_redirects"`
	ResponseHeaders  http.Header               `json:"response_headers"`
	SSLInfo          SSLDetails                `json:"ssl_info"`
	DetectedEntities map[string]DetectedEntity `json:"detected_assets"`
}

// DetectedEntity is a struct to store the detected entity (technology, asset, etc.)
type DetectedEntity struct {
	EntityType      string   `json:"entity_type"`
	EntityName      string   `json:"entity_name"`
	Confidence      float32  `json:"confidence"`
	MatchedPatterns []string `json:"matched_patterns"`
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
	CertExpiration               cmn.FlexibleDate    `json:"cert_expiration"`
}

// SSLDetails is identical to SSLInfo, however it is designed to be easy to unmarshal/marshal
// from/to JSON, so it's used to store data on the DB and return data from requests.
type SSLDetails struct {
	URL                          string      `json:"url"`
	Issuers                      []string    `json:"issuers"`                    // List of issuers
	OwnerOrganizations           []string    `json:"owner_organizations"`        // Organizations
	OwnerOrganizationalUnits     []string    `json:"owner_organizational_units"` // Organizational Units
	OwnerCountries               []string    `json:"owner_countries"`            // Countries
	OwnerStates                  []string    `json:"owner_states"`               // States
	OwnerLocalities              []string    `json:"owner_localities"`           // Localities
	OwnerCommonNames             []string    `json:"owner_common_names"`         // Common Names
	FQDNs                        []string    `json:"fqdns"`                      // List of FQDNs the certificate is valid for
	PublicKeys                   []string    `json:"public_keys"`                // Public key info, possibly base64-encoded
	SignatureAlgorithms          []string    `json:"signature_algorithms"`       // Signature algorithms used
	CertChains                   []CertChain `json:"cert_chain"`                 // Base64-encoded certificates
	IsCertChainOrderValid        bool        `json:"is_cert_chain_order_valid"`
	IsRootTrustworthy            bool        `json:"is_root_trustworthy"`
	IsCertValid                  bool        `json:"is_cert_valid"`
	IsCertExpired                bool        `json:"is_cert_expired"`
	IsCertRevoked                bool        `json:"is_cert_revoked"`
	IsCertSelfSigned             bool        `json:"is_cert_self_signed"`
	IsCertCA                     bool        `json:"is_cert_ca"`
	IsCertIntermediate           bool        `json:"is_cert_intermediate"`
	IsCertLeaf                   bool        `json:"is_cert_leaf"`
	IsCertTrusted                bool        `json:"is_cert_trusted"`
	IsCertTechnicallyConstrained bool        `json:"is_cert_technically_constrained"`
	IsCertEV                     bool        `json:"is_cert_ev"`
	IsCertEVSSL                  bool        `json:"is_cert_ev_ssl"`
	CertExpiration               string      `json:"cert_expiration"` // Use string to simplify
}

// CertChain is a struct to store the base64-encoded certificate chain
type CertChain struct {
	Certificates []string `json:"certificates"`
}

// ConvertSSLInfoToDetails converts SSLInfo to SSLDetails
func ConvertSSLInfoToDetails(info SSLInfo) (SSLDetails, error) {
	certChainBase64 := make([]CertChain, len(info.CertChain))
	issuers := make([]string, len(info.CertChain))
	publicKeys := make([]string, len(info.CertChain))
	signatureAlgorithms := make([]string, len(info.CertChain))
	ownerOrganizations := make([]string, len(info.CertChain))
	ownerOrganizationalUnits := make([]string, len(info.CertChain))
	ownerCountries := make([]string, len(info.CertChain))
	ownerStates := make([]string, len(info.CertChain))
	ownerLocalities := make([]string, len(info.CertChain))
	ownerCommonNames := make([]string, len(info.CertChain))

	fqdnSet := make(map[string]struct{}) // Let's use a map to avoid duplicates
	for i, cert := range info.CertChain {
		// Base64 encode the certificate
		block := &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		}
		certData := pem.EncodeToMemory(block)
		certChainBase64[i].Certificates = make([]string, 1)
		certChainBase64[i].Certificates[0] = base64.StdEncoding.EncodeToString(certData)

		// Get issuer details
		issuers[i] = cert.Issuer.CommonName

		// Get owner details
		ownerOrganizations[i] = strings.Join(cert.Subject.Organization, ", ")
		ownerOrganizationalUnits[i] = strings.Join(cert.Subject.OrganizationalUnit, ", ")
		ownerCountries[i] = strings.Join(cert.Subject.Country, ", ")
		ownerStates[i] = strings.Join(cert.Subject.Province, ", ")
		ownerLocalities[i] = strings.Join(cert.Subject.Locality, ", ")
		ownerCommonNames[i] = cert.Subject.CommonName

		// Get FQDNs
		for _, name := range cert.DNSNames {
			fqdnSet[name] = struct{}{}
		}
		for _, ip := range cert.IPAddresses {
			fqdnSet[ip.String()] = struct{}{}
		}

		// Get public key info (encoded or detailed as needed)
		publicKeyBytes, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
		if err == nil {
			publicKeys[i] = base64.StdEncoding.EncodeToString(publicKeyBytes)
		}

		// Get signature algorithm
		signatureAlgorithms[i] = cert.SignatureAlgorithm.String()
	}

	// Convert the FQDN map to a slice
	fqdns := make([]string, 0, len(fqdnSet))
	for name := range fqdnSet {
		fqdns = append(fqdns, name)
	}
	sort.Strings(fqdns)

	return SSLDetails{
		URL:                          info.URL,
		CertChains:                   certChainBase64,
		Issuers:                      issuers,
		OwnerOrganizations:           ownerOrganizations,
		OwnerOrganizationalUnits:     ownerOrganizationalUnits,
		OwnerCountries:               ownerCountries,
		OwnerStates:                  ownerStates,
		OwnerLocalities:              ownerLocalities,
		OwnerCommonNames:             ownerCommonNames,
		FQDNs:                        fqdns,
		PublicKeys:                   publicKeys,
		SignatureAlgorithms:          signatureAlgorithms,
		IsCertChainOrderValid:        info.IsCertChainOrderValid,
		IsRootTrustworthy:            info.IsRootTrustworthy,
		IsCertValid:                  info.IsCertValid,
		IsCertExpired:                info.IsCertExpired,
		IsCertRevoked:                info.IsCertRevoked,
		IsCertSelfSigned:             info.IsCertSelfSigned,
		IsCertCA:                     info.IsCertCA,
		IsCertIntermediate:           info.IsCertIntermediate,
		IsCertLeaf:                   info.IsCertLeaf,
		IsCertTrusted:                info.IsCertTrusted,
		IsCertTechnicallyConstrained: info.IsCertTechnicallyConstrained,
		IsCertEV:                     info.IsCertEV,
		IsCertEVSSL:                  info.IsCertEVSSL,
		CertExpiration:               info.CertExpiration.String(),
	}, nil
}

// DecodeCert decodes a base64-encoded certificate stored in SSLDetails
func DecodeCert(certBase64 string) (*x509.Certificate, error) {
	certPEM, err := base64.StdEncoding.DecodeString(certBase64)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("failed to decode PEM block containing the certificate")
	}

	return x509.ParseCertificate(block.Bytes)
}
