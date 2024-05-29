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
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"golang.org/x/crypto/ocsp"
)

// Globals:

// Initialize a slice to store Authority data
var authorities []Authority

// Initialize a variable to store the debug level
const (
	YYYYMMDD        = "2006.01.02"
	debug_level int = 0
)

// ExtractSSLInfo extracts SSL information from the provided URL
func (ssl *SSLInfo) ExtractInfo(url string) error {
	if ssl == nil {
		return fmt.Errorf("SSLInfo is nil")
	}
	return ssl.GetSSLInfo(url, "")
}

// String returns a string representation of the SSLInfo
func (ssl *SSLInfo) String() string {
	// transform the SSLInfo into a string
	SSLInfoString := fmt.Sprintf("URL: %s\n", ssl.URL) // TODO: Add more fields
	return SSLInfoString
}

// NewSSLInfo creates a new SSLInfo instance
func NewSSLInfo() *SSLInfo {
	return &SSLInfo{}
}

// SSLInfoExtractor is an interface for extracting SSL information
type SSLInfoExtractor interface {
	ExtractInfo(url string) error
}

// SSLInfoStringer is an interface for string-ifying SSL information
type SSLInfoStringer interface {
	String() string
}

// SSLInfoFactory is an interface for creating SSLInfo instances
type SSLInfoFactory interface {
	NewSSLInfo() *SSLInfo
}

// SSLInfoFactoryImpl is an implementation of the SSLInfoFactory interface
type SSLInfoFactoryImpl struct{}

// NewSSLInfo creates a new SSLInfo instance
func (f *SSLInfoFactoryImpl) NewSSLInfo() *SSLInfo {
	return &SSLInfo{}
}

// NewSSLInfoFactory creates a new SSLInfoFactory instance
func NewSSLInfoFactory() *SSLInfoFactoryImpl {
	return &SSLInfoFactoryImpl{}
}

/*
func getTimeInCertReportFormat() string {
	now := time.Now().UTC()
	return now.Format(YYYYMMDD)
}
*/

func (ssl *SSLInfo) GetSSLInfo(url string, port string) error {
	// Get the certificate from the server
	var err error
	url = strings.TrimSpace(url)
	ssl.URL = url
	isAFile := false
	if strings.HasPrefix(url, "ftps://") {
		url = url[len("ftps://"):]
	} else if strings.HasPrefix(url, "https://") {
		url = url[len("https://"):]
	} else {
		isAFile = true
	}

	var certChain []*x509.Certificate
	if !isAFile {
		// convert port to uint16
		uport, errx := strconv.ParseUint(port, 10, 16)
		if errx != nil {
			return fmt.Errorf("port number incorrect")
		}
		certChain, err = getCertChain(url, uint16(uport))
		if err != nil {
			return err
		}
	} else {
		if url[len(url)-4:] != ".crt" {
			return fmt.Errorf("file name must end with .crt")
		}

		certChain, err = verifyCertFile(url)
		if err != nil {
			return err
		}
	}

	// Extract the certificate information
	ssl.CertChain = certChain

	return nil
}

func (ssl *SSLInfo) ValidateCertificate() error {
	var err error

	// Validate Cert Chain order:
	ssl.IsCertChainOrderValid, err = validateCertificateChainOrder(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if Root Authority is trustworthy:
	ssl.IsRootTrustworthy, err = checkTrustworthyRoot(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is valid:
	ssl.IsCertValid, err = checkCertificateValidity(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is revoked:
	ssl.IsCertRevoked, err = checkCertificateRevocation(ssl.CertChain[0], ssl.CertChain[1])
	if err != nil {
		return err
	}

	// Check if the certificate is technically constrained:
	ssl.IsCertTechnicallyConstrained, err = checkCertificateTechnicalConstraints(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV:
	ssl.IsCertEV, err = checkCertificateEV(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV Code Signing:
	ssl.IsCertEVCodeSigning, err = checkCertificateEVCodeSigning(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SSL:
	ssl.IsCertEVSSL, err = checkCertificateEVSSL(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC:
	ssl.IsCertEVSGC, err = checkCertificateEVSGC(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC SSL:
	ssl.IsCertEVSGCSSL, err = checkCertificateEVSGCSSL(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC CA:
	ssl.IsCertEVSGCCA, err = checkCertificateEVSGCCA(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC CA SSL:
	ssl.IsCertEVSGCCASSL, err = checkCertificateEVSGCCASSL(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC CA Code Signing:
	ssl.IsCertEVSGCCACodeSigning, err = checkCertificateEVSGCCACodeSigning(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC CA Code Signing SSL:
	ssl.IsCertEVSGCCACodeSigningSSL, err = checkCertificateEVSGCCACodeSigningSSL(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC Code Signing:
	ssl.IsCertEVSGCCodeSigning, err = checkCertificateEVSGCCodeSigning(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if the certificate is EV SGC Code Signing SSL:
	ssl.IsCertEVSGCCodeSigningSSL, err = checkCertificateEVSGCCodeSigningSSL(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check Certificate Expiration:
	ssl.CertExpiration, err = checkCertificateExpiration(ssl.CertChain)
	if err != nil {
		return err
	}

	// Check if certificate is expired:
	ssl.IsCertExpired, err = checkCertificateExpired(ssl.CertChain)
	if err != nil {
		return err
	}

	// List intermediate Authorities:
	ssl.IntermediateAuthorities, err = listIntermediateAuthorities(ssl.CertChain)
	if err != nil {
		return err
	}

	return nil
}

func checkCertificateValidity(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is valid:
	var isCertValid bool
	// retrieve the current time
	currentTime := time.Now()
	// retrieve certificate expiration time
	certExpiration := certChain[0].NotAfter
	// compare the current time with the certificate expiration time
	isCertValid = currentTime.Before(certExpiration)
	return isCertValid, nil
}

func checkCertificateEV(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV:
	isCertEV := certChain[0].IsCA
	return isCertEV, nil
}

func checkCertificateEVSGC(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC:
	isCertEVSGC := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageAny
	return isCertEVSGC, nil
}

func checkCertificateEVSGCSSL(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC SSL:
	isCertEVSGCSSL := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth
	return isCertEVSGCSSL, nil
}

func checkCertificateEVSGCCA(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC CA:
	isCertEVSGCCA := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageAny
	return isCertEVSGCCA, nil
}

func checkCertificateEVSGCCASSL(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC CA SSL:
	isCertEVSGCCASSL := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth && certChain[0].ExtKeyUsage[1] == x509.ExtKeyUsageAny
	return isCertEVSGCCASSL, nil
}

func checkCertificateEVSGCCACodeSigning(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC CA Code Signing:
	isCertEVSGCCACodeSigning := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageCodeSigning && certChain[0].ExtKeyUsage[1] == x509.ExtKeyUsageAny
	return isCertEVSGCCACodeSigning, nil
}

func checkCertificateEVSGCCACodeSigningSSL(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC CA Code Signing SSL:
	isCertEVSGCCACodeSigningSSL := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth && certChain[0].ExtKeyUsage[1] == x509.ExtKeyUsageCodeSigning && certChain[0].ExtKeyUsage[2] == x509.ExtKeyUsageAny
	return isCertEVSGCCACodeSigningSSL, nil
}

func checkCertificateEVSGCCodeSigning(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC Code Signing:
	isCertEVSGCCodeSigning := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageCodeSigning
	return isCertEVSGCCodeSigning, nil
}

func checkCertificateEVSGCCodeSigningSSL(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SGC Code Signing SSL:
	isCertEVSGCCodeSigningSSL := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth && certChain[0].ExtKeyUsage[1] == x509.ExtKeyUsageCodeSigning
	return isCertEVSGCCodeSigningSSL, nil
}

func checkCertificateExpiration(certChain []*x509.Certificate) (cmn.FlexibleDate, error) {
	// Check when the certificate expires:
	certExpiration := certChain[0].NotAfter
	certExpirationDate := cmn.FlexibleDate(certExpiration)
	return certExpirationDate, nil
}

func checkCertificateExpired(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is expired:
	var isCertExpired bool
	// retrieve the current time
	currentTime := time.Now()
	// retrieve certificate expiration time
	certExpiration := certChain[0].NotAfter
	// compare the current time with the certificate expiration time
	isCertExpired = currentTime.After(certExpiration)
	return isCertExpired, nil
}

/*
func checkCertificateIssuer(certChain []*x509.Certificate) (string, error) {
	// Check the certificate issuer:
	certIssuer := certChain[0].Issuer.CommonName
	return certIssuer, nil
}

func checkCertificateSubject(certChain []*x509.Certificate) (string, error) {
	// Check the certificate subject:
	certSubject := certChain[0].Subject.CommonName
	return certSubject, nil
}

func checkCertificateSubjectAltName(certChain []*x509.Certificate) ([]string, error) {
	// Check the certificate subject alternative name:
	certSubjectAltName := certChain[0].DNSNames
	return certSubjectAltName, nil
}

func checkCertificateSubjectAltNameIP(certChain []*x509.Certificate) ([]net.IP, error) {
	// Check the certificate subject alternative name IP:
	certSubjectAltNameIP := certChain[0].IPAddresses
	return certSubjectAltNameIP, nil
}

func checkCertificateSubjectAltNameEmail(certChain []*x509.Certificate) ([]string, error) {
	// Check the certificate subject alternative name email:
	certSubjectAltNameEmail := certChain[0].EmailAddresses
	return certSubjectAltNameEmail, nil
}

func checkCertificateRevocationList(certChain []*x509.Certificate) ([]string, error) {
	// Check the certificate revocation list:
	certRevocationList := certChain[0].CRLDistributionPoints
	return certRevocationList, nil
}
*/

// Check if the certificate has been revoked, using OCSP
func checkCertificateRevocation(cert *x509.Certificate, issuerCert *x509.Certificate) (bool, error) {
	if len(cert.OCSPServer) == 0 {
		return false, nil // No OCSP servers listed, can't check revocation via OCSP
	}

	ocspURL := cert.OCSPServer[0] // Get the first OCSP server in the list
	req, err := ocsp.CreateRequest(cert, issuerCert, nil)
	if err != nil {
		return false, err // Error creating OCSP request
	}

	// Send the OCSP request and get the response
	httpResponse, err := http.Post(ocspURL, "application/ocsp-request", bytes.NewReader(req))
	if err != nil {
		return false, err // Error sending the OCSP request
	}
	defer httpResponse.Body.Close()

	responseBytes, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return false, err // Error reading the OCSP response
	}

	ocspResponse, err := ocsp.ParseResponse(responseBytes, issuerCert)
	if err != nil {
		return false, err // Error parsing the OCSP response
	}

	return ocspResponse.Status == ocsp.Revoked, nil
}

func checkCertificateTechnicalConstraints(certChain []*x509.Certificate) (bool, error) {
	// Check the certificate technical constraints:
	certTechnicalConstraints := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageAny
	return certTechnicalConstraints, nil
}

func checkCertificateEVCodeSigning(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV Code Signing:
	isCertEVCodeSigning := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageCodeSigning
	return isCertEVCodeSigning, nil
}

func checkCertificateEVSSL(certChain []*x509.Certificate) (bool, error) {
	// Check if the certificate is EV SSL:
	isCertEVSSL := certChain[0].IsCA && certChain[0].ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth
	return isCertEVSSL, nil
}

// This function processes the CSV file with the list of Authorities
// if the file doesn't exists then it will pull it down from https://www.ccadb.org/resources
// if the file exists it will process it and store elements in the Authority
// data structure.
func ProcessAuthFile() {
	// Check if the CSV file exists, and if not, download it
	filename := "AllCertificatesRecordReport.csv"
	if !fileExists(filename) {
		fmt.Println("Downloading CSV file...")
		err := downloadFile("https://ccadb.my.salesforce-sites.com/ccadb/AllCertificateRecordsCSVFormat", filename)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "downloading the CSV file: %v", err)
			return
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "CCADB All Certificate Records CSV download complete!")
		}
	}
	// Open the CSV file
	file, err := os.Open(filename)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "opening the file: %v", err)
		return
	}
	defer file.Close()

	// Create a CSV reader
	reader := csv.NewReader(file)

	// Read the header row to get the column names
	header, err := reader.Read()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "reading header row: %v", err)
		return
	}

	// Map column names to indices for efficient access
	columnIndices := make(map[string]int)
	for idx, columnName := range header {
		columnIndices[columnName] = idx
	}

	// Read the remaining rows and populate the authorities slice
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break // End of file
		}
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "reading record: %v", err)
			return
		}

		authority := Authority{
			CAOwner:                           record[columnIndices["Ca Owner"]],
			SalesforceRecordID:                record[columnIndices["Salesforce Record ID"]],
			CertificateName:                   record[columnIndices["Certificate Name"]],
			ParentSalesforceRecordID:          record[columnIndices["Parent Salesforce Record ID"]],
			ParentCertificateName:             record[columnIndices["Parent Certificate Name"]],
			CertificateRecordType:             record[columnIndices["Certificate Record Type"]],
			RevocationStatus:                  record[columnIndices["Revocation Status"]],
			SHA256Fingerprint:                 record[columnIndices["SHA-256 Fingerprint"]],
			ParentSHA256Fingerprint:           record[columnIndices["Parent SHA-256 Fingerprint"]],
			AuditsSameAsParent:                record[columnIndices["Audits Same as Parent?"]],
			Auditor:                           record[columnIndices["Auditor"]],
			StandardAuditURL:                  record[columnIndices["Standard Audit URL"]],
			StandardAuditType:                 record[columnIndices["Standard Audit Type"]],
			StandardAuditStatementDate:        record[columnIndices["Standard Audit Statement Date"]],
			StandardAuditPeriodStartDate:      record[columnIndices["Standard Audit Period Start Date"]],
			StandardAuditPeriodEndDate:        record[columnIndices["STandard Audit Period End Date"]],
			BRAuditURL:                        record[columnIndices["BR Audit URL"]],
			BRAuditType:                       record[columnIndices["BR Audit Type"]],
			BRAuditStatementDate:              record[columnIndices["BR Audit Statement Date"]],
			BRAuditPeriodStartDate:            record[columnIndices["BR Audit Period Start Date"]],
			BRAuditPeriodEndDate:              record[columnIndices["BR Audit Period End Date"]],
			EVSSLAuditURL:                     record[columnIndices["EV SSL Audit URL"]],
			EVSSLAuditType:                    record[columnIndices["EV SSL Audit Type"]],
			EVSSLAuditStatementDate:           record[columnIndices["EV SSL Audit Statement Date"]],
			EVSSLAuditPeriodStartDate:         record[columnIndices["EV SSL Audit Period Start Date"]],
			EVSSLPeriodEndDate:                record[columnIndices["EV SSL Period End Date"]],
			EVCodeSigningAuditURL:             record[columnIndices["EV Code Signing Audit URL"]],
			EVCodeSigningAuditType:            record[columnIndices["EV Code Signing Audit Type"]],
			EVCodeSigningAuditStatementDate:   record[columnIndices["EV Code Signing Audit Statement Date"]],
			EVCodeSigningAuditPeriodStartDate: record[columnIndices["EV Code Signing Audit Period Start Date"]],
			EVCodeSigningAuditPeriodEndDate:   record[columnIndices["EV Code Signing Audit Period End Date"]],
			CPCPSSameAsParent:                 record[columnIndices["CP/CPS Same as Parent?"]],
			CertificatePolicyURL:              record[columnIndices["Certificate Policy (CP) URL"]],
			CertificatePracticeStatementURL:   record[columnIndices["Certificate Practice Statement URL"]],
			CPCPSLastUpdatedDate:              record[columnIndices["CP/CPS Last Updated Date"]],
			TestWebsiteURLValid:               record[columnIndices["Test Website URL - Valid"]],
			TestWebsiteURLExpired:             record[columnIndices["Test Website URL - Expired"]],
			TestWebsiteURLRevoked:             record[columnIndices["Test Website URL - Revoked"]],
			TechnicallyConstrained:            record[columnIndices["Technically Constrained"]],
			MozillaStatus:                     record[columnIndices["Mozilla Status"]],
			MicrosoftStatus:                   record[columnIndices["Microsoft Status"]],
			SubordinateCAOwner:                record[columnIndices["Subordinate CA Owner"]],
			FullCRLIssuedByThisCA:             record[columnIndices["Full CRL Issued By This CA"]],
			JSONArrayOfPartitionedCRLs:        record[columnIndices["JSON Array of Partitioned CRLs"]],
			ValidFromGMT:                      record[columnIndices["Valid From (GMT)"]],
			ValidToGMT:                        record[columnIndices["Valid to (GMT)"]],
			ChromeStatus:                      record[columnIndices["Chrome Status"]],
		}

		authorities = append(authorities, authority)
	}

	// Print the authorities
	if debug_level > 0 {
		for _, authority := range authorities {
			fmt.Println("Authority:", authority.CAOwner)
			fmt.Println("Aliases:", authority.SalesforceRecordID)
			// Print other fields as needed
			fmt.Println()
		}
	}
}

// Download a file from the given URL and save it to the given filename
func downloadFile(url, filename string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch the file: %s", resp.Status)
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

// Check if the file exists
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

// Get the certificate chain from the given hostname and port
func getCertChain(hostname string, port uint16) ([]*x509.Certificate, error) {
	conn, err := tls.Dial("tcp", hostname+":"+strconv.FormatUint(uint64(port), 10), nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.Handshake(); err != nil {
		return nil, err
	}

	return conn.ConnectionState().PeerCertificates, nil
}

/*
func checkCertExpirationDate(cert *x509.Certificate) (bool, error) {
	// extract certificate expiry date:
	expiryDate := cert.NotAfter
	// compare certificate expiry date with today's date:
	if expiryDate.Before(time.Now()) {
		return false, nil
	}
	return true, nil
}
*/

// Verify the certificate chain
func verifyCertFile(certFilePath string) ([]*x509.Certificate, error) {
	certBytes, err := os.ReadFile(certFilePath)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to parse certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	certChain := []*x509.Certificate{cert}

	// Check for intermediate certificates
	for i := 1; i < 10; i++ { // You can adjust the loop limit if needed
		intermediateCertPath := certFilePath[:len(certFilePath)-len(".crt")] + fmt.Sprintf("-%d.crt", i)
		intermediateCertBytes, err := os.ReadFile(intermediateCertPath)
		if err != nil {
			break // Intermediate certificate not found, break the loop
		}

		block, _ := pem.Decode(intermediateCertBytes)
		if block == nil {
			return nil, fmt.Errorf("failed to parse intermediate certificate PEM")
		}

		intermediateCert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}

		certChain = append(certChain, intermediateCert)
	}

	return certChain, nil
}

func isIssuerOf(issuer *x509.Certificate, subject *x509.Certificate) bool {
	return issuer.Subject.CommonName == subject.Issuer.CommonName
}

func validateCertificateChainOrder(chain []*x509.Certificate) (bool, error) {
	if len(chain) < 2 {
		return false, fmt.Errorf("certificate chain is too short")
	}

	// Check if the end-entity certificate (server certificate) is the first in the chain
	firstOrg := strings.Join(chain[1].Subject.Organization, " ")
	secondOrg := strings.Join(chain[0].Issuer.Organization, " ")
	if debug_level > 1 {
		fmt.Println(firstOrg + " == " + secondOrg)
	}
	if !isIssuerOf(chain[1], chain[0]) && (firstOrg != secondOrg) {
		if debug_level > 1 {
			fmt.Println("Chain 0:")
			fmt.Println(chain[0].Subject)
			fmt.Println(chain[0].Issuer)
			fmt.Println("Chain 1:")
			fmt.Println(chain[1].Subject)
			fmt.Println(chain[1].Issuer)
			fmt.Println("End-entity certificate is not first in the chain")
		}
	}

	// Check if each subsequent certificate directly certifies the previous one
	for i := 1; i < len(chain)-1; i++ {
		if !isDirectlyCertifiedBy(chain[i+1], chain[i]) {
			// Certificate chain order is incorrect
			return false, nil
		}
	}

	// Certificate chain order is correct
	return true, nil
}

func isDirectlyCertifiedBy(issuer *x509.Certificate, subject *x509.Certificate) bool {
	// Compare subject and issuer fields
	// Allow flexibility in matching by considering common attributes
	return issuer.Subject.CommonName == subject.Issuer.CommonName
}

/* TODO: Complete this function, it needs a better approach to fetch CT Logs
 *		 It requires a user managed repo of CT Logs.
 */
func checkTrustworthyRoot(certChain []*x509.Certificate) (bool, error) {
	if len(certChain) == 0 {
		return false, fmt.Errorf("- Certificate chain is empty")
	}

	if authorities == nil {
		ProcessAuthFile()
	}

	// Check if the root certificate is trustworthy
	rootCert := certChain[len(certChain)-1]
	//fmt.Println("Root Authority found:", rootCert)
	//fmt.Println("- Root Authority: ", rootCert.Subject.CommonName)
	//fmt.Println("- Root Issuer: ", rootCert.Issuer.CommonName)

	RootAuthVerified := isRootAuthorityVerified(rootCert)
	issuerVerified := isIssuerVerified(rootCert)

	return RootAuthVerified && issuerVerified, nil
}

func isRootAuthorityVerified(rootCert *x509.Certificate) bool {
	for _, knownRoot := range authorities {
		if rootCert.Subject.CommonName == knownRoot.CertificateName {
			return true
		}
	}
	return false
}

func isIssuerVerified(rootCert *x509.Certificate) bool {
	if rootCert.Subject.CommonName != rootCert.Issuer.CommonName {
		for _, knownRoot := range authorities {
			if rootCert.Issuer.CommonName == knownRoot.CertificateName {
				return true
			}
		}
	}
	return false
}

func listIntermediateAuthorities(certChain []*x509.Certificate) ([]string, error) {
	if len(certChain) <= 2 {
		return []string{}, nil
	}

	var intermediateAuthorities []string
	for i, cert := range certChain[1 : len(certChain)-1] {
		auth := fmt.Sprintf("  %d. Subject: %s\n", i+1, cert.Subject.CommonName)
		intermediateAuthorities = append(intermediateAuthorities, auth)
	}

	return intermediateAuthorities, nil
}
