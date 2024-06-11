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
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"golang.org/x/net/proxy"
)

const (
	sslV3         = "SSLv3"
	tlsV10        = "TLS_1"
	tlsV11        = "TLS_1.1"
	tlsV12        = "TLS_1.2"
	tlsV12Support = "1.2_SUPPORT"
	tlsV13        = "TLS_1.3"
	tlsV13Support = "1.3_SUPPORT"
)

type JARMCollector struct {
	Proxy *cfg.SOCKSProxy
}

type ProxyConfig struct {
	Address  string
	Username string
	Password string
}

// formatForPython encodes a byte slice into a Python byte string format.
func formatForPython(data []byte) string {
	var buffer bytes.Buffer
	buffer.WriteString("b'")
	for _, b := range data {
		if b >= 0x20 && b <= 0x7e {
			buffer.WriteByte(b)
		} else {
			buffer.WriteString(fmt.Sprintf("\\x%02x", b))
		}
	}
	buffer.WriteString("'")
	return buffer.String()
}

// Print out detailed parts of the ClientHello message
func PrintClientHelloDetails(packet []byte) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("Recovered in printClientHelloDetails:", r)
		}
	}()

	fmt.Println("------------------------------------------------------------")
	fmt.Printf("ClientHello Packet: %x\n", packet)

	if len(packet) < 9 {
		fmt.Println("Packet too short")
		return
	}

	contentType := packet[0]
	version := packet[1:3]
	length := packet[3:5]
	handshakeType := packet[5]
	handshakeLength := packet[6:9]
	clientVersion := packet[9:11]
	random := packet[11:43]
	sessionIDLength := packet[43]

	if len(packet) < 44+int(sessionIDLength) {
		fmt.Println("Packet too short for session ID")
		return
	}
	sessionID := packet[44 : 44+sessionIDLength]

	if len(packet) < 46+int(sessionIDLength) {
		fmt.Println("Packet too short for cipher suites length")
		return
	}
	cipherSuitesLength := packet[44+sessionIDLength : 46+sessionIDLength]
	cipherSuitesLen := int(cipherSuitesLength[0])<<8 + int(cipherSuitesLength[1])

	if len(packet) < 46+int(sessionIDLength)+cipherSuitesLen {
		fmt.Println("Packet too short for cipher suites")
		return
	}
	cipherSuites := packet[46+int(sessionIDLength) : 46+int(sessionIDLength)+cipherSuitesLen]

	if len(packet) < 46+int(sessionIDLength)+cipherSuitesLen+1 {
		fmt.Println("Packet too short for compression methods length")
		return
	}
	compressionMethodsLength := packet[46+int(sessionIDLength)+cipherSuitesLen]
	compressionMethodsLen := int(compressionMethodsLength)

	if len(packet) < 47+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen {
		fmt.Println("Packet too short for compression methods")
		return
	}
	compressionMethods := packet[47+int(sessionIDLength)+cipherSuitesLen : 47+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen]

	if len(packet) < 49+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen {
		fmt.Println("Packet too short for extensions length")
		return
	}
	extensionsLength := packet[47+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen : 49+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen]
	extensionsLen := int(extensionsLength[0])<<8 + int(extensionsLength[1])

	if len(packet) < 49+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen+extensionsLen {
		fmt.Println("Packet too short for extensions")
		return
	}
	extensions := packet[49+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen : 49+int(sessionIDLength)+cipherSuitesLen+compressionMethodsLen+extensionsLen]

	fmt.Printf("Content Type: %x\n", contentType)
	fmt.Printf("Version: %x\n", version)
	fmt.Printf("Length: %x\n", length)
	fmt.Printf("Handshake Type: %x\n", handshakeType)
	fmt.Printf("Handshake Length: %x\n", handshakeLength)
	fmt.Printf("Client Version: %x\n", clientVersion)
	fmt.Printf("Client Version PyString: %s\n", formatForPython(clientVersion))
	fmt.Printf("Random: %x\n", random)
	fmt.Printf("Session ID Length: %x\n", sessionIDLength)
	fmt.Printf("Session ID: %x\n", sessionID)
	fmt.Printf("Cipher Suites Length: %x\n", cipherSuitesLength)
	fmt.Printf("Cipher Suites: %x\n", cipherSuites)
	fmt.Printf("Cipher Suites PyString: %s\n", formatForPython(cipherSuites))
	fmt.Printf("Compression Methods Length: %x\n", compressionMethodsLength)
	fmt.Printf("Compression Methods: %x\n", compressionMethods)
	fmt.Printf("Extensions Length: %x\n", extensionsLength)
	fmt.Printf("Extensions: %x\n", extensions)
	fmt.Printf("Extensions PyString: %s\n", formatForPython(extensions))
	fmt.Println("------------------------------------------------------------")
}

// Collect collects JARM fingerprint for a given host and port
func (jc JARMCollector) Collect(host string, port string) (string, error) {
	jarmDetails := [10][]string{
		{host, port, tlsV12, "ALL", "FORWARD", "NO_GREASE", "APLN", tlsV12Support, "REVERSE"},
		{host, port, tlsV12, "ALL", "REVERSE", "NO_GREASE", "APLN", tlsV12Support, "FORWARD"},
		{host, port, tlsV12, "ALL", "TOP_HALF", "NO_GREASE", "APLN", "NO_SUPPORT", "FORWARD"},
		{host, port, tlsV12, "ALL", "BOTTOM_HALF", "NO_GREASE", "RARE_APLN", "NO_SUPPORT", "FORWARD"},
		{host, port, tlsV12, "ALL", "MIDDLE_OUT", "GREASE", "RARE_APLN", "NO_SUPPORT", "REVERSE"},
		{host, port, tlsV11, "ALL", "FORWARD", "NO_GREASE", "APLN", "NO_SUPPORT", "FORWARD"},
		{host, port, tlsV13, "ALL", "FORWARD", "NO_GREASE", "APLN", tlsV13Support, "REVERSE"},
		{host, port, tlsV13, "ALL", "REVERSE", "NO_GREASE", "APLN", tlsV13Support, "FORWARD"},
		{host, port, tlsV13, "NO1.3", "FORWARD", "NO_GREASE", "APLN", tlsV13Support, "FORWARD"},
		{host, port, tlsV13, "ALL", "MIDDLE_OUT", "GREASE", "APLN", tlsV13Support, "REVERSE"},
	}

	var jarmBuilder strings.Builder
	for _, detail := range jarmDetails {
		packet := buildPacket(detail)

		// debug:
		cmn.DebugMsg(cmn.DbgLvlDebug3, "JARM built packet: %s\n", formatForPython(packet))
		//PrintClientHelloDetails(packet)

		serverHello, err := jc.sendPacket(packet, host, port)
		if err != nil {
			return "", err
		}
		ans := readPacket(serverHello, detail)
		// debug:
		cmn.DebugMsg(cmn.DbgLvlDebug3, "JARM collected response: %s\n", formatForPython(serverHello))
		jarmBuilder.WriteString(ans + ",")
	}
	jarm := strings.TrimRight(jarmBuilder.String(), ",")
	return jarm, nil
}

// buildPacket constructs a ClientHello packet based on the provided JARM details
func buildPacket(jarmDetails []string) []byte {
	payload := []byte{0x16}
	var clientHello []byte

	// Version Check
	switch jarmDetails[2] {
	case tlsV13:
		payload = append(payload, []byte{0x03, 0x01}...)
		clientHello = append(clientHello, []byte{0x03, 0x03}...)
	case sslV3:
		payload = append(payload, []byte{0x03, 0x00}...)
		clientHello = append(clientHello, []byte{0x03, 0x00}...)
	case tlsV10:
		payload = append(payload, []byte{0x03, 0x01}...)
		clientHello = append(clientHello, []byte{0x03, 0x01}...)
	case tlsV11:
		payload = append(payload, []byte{0x03, 0x02}...)
		clientHello = append(clientHello, []byte{0x03, 0x02}...)
	case tlsV12:
		payload = append(payload, []byte{0x03, 0x03}...)
		clientHello = append(clientHello, []byte{0x03, 0x03}...)
	}

	// Random values in client hello
	rndBytes := randomBytes(32)
	clientHello = append(clientHello, rndBytes...)
	// debug:
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Random Bytes: %s\n\n", formatForPython(rndBytes))
	sessionID := randomBytes(32)
	clientHello = append(clientHello, byte(len(sessionID)))
	clientHello = append(clientHello, sessionID...)
	// debug:
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Session ID: %s\n\n", formatForPython(sessionID))

	// Get ciphers
	cipherChoice := getCiphers(jarmDetails)
	clientSuitesLength := toBytes(len(cipherChoice))
	clientHello = append(clientHello, clientSuitesLength...)
	clientHello = append(clientHello, cipherChoice...)
	// debug:
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Cipher Suites: %s\n\n", formatForPython(cipherChoice))

	// Cipher methods
	clientHello = append(clientHello, 0x01)
	// Compression methods
	clientHello = append(clientHello, 0x00)

	// Add extensions to client hello
	extensions := getExtensions(jarmDetails)
	clientHello = append(clientHello, toBytes(len(extensions))...)
	clientHello = append(clientHello, extensions...)
	// debug:
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Extensions: %s\n\n", formatForPython(extensions))

	// Finish packet assembly
	innerLength := append([]byte{0x00}, toBytes(len(clientHello))...)
	handshakeProtocol := append([]byte{0x01}, innerLength...)
	handshakeProtocol = append(handshakeProtocol, clientHello...)
	outerLength := toBytes(len(handshakeProtocol))
	payload = append(payload, outerLength...)
	payload = append(payload, handshakeProtocol...)

	// debug:
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Constructed ClientHello: %s\n", formatForPython(clientHello))
	return payload
}

// getCiphers returns the selected ciphers based on the JARM details
func getCiphers(jarmDetails []string) []byte {
	var selectedCiphers []byte
	var cipherList [][]byte

	if jarmDetails[3] == "ALL" {
		cipherList = [][]byte{
			{0x00, 0x16}, {0x00, 0x33}, {0x00, 0x67}, {0xc0, 0x9e}, {0xc0, 0xa2},
			{0x00, 0x9e}, {0x00, 0x39}, {0x00, 0x6b}, {0xc0, 0x9f}, {0xc0, 0xa3},
			{0x00, 0x9f}, {0x00, 0x45}, {0x00, 0xbe}, {0x00, 0x88}, {0x00, 0xc4},
			{0x00, 0x9a}, {0xc0, 0x08}, {0xc0, 0x09}, {0xc0, 0x23}, {0xc0, 0xac},
			{0xc0, 0xae}, {0xc0, 0x2b}, {0xc0, 0x0a}, {0xc0, 0x24}, {0xc0, 0xad},
			{0xc0, 0xaf}, {0xc0, 0x2c}, {0xc0, 0x72}, {0xc0, 0x73}, {0xcc, 0xa9},
			{0x13, 0x02}, {0x13, 0x01}, {0xcc, 0x14}, {0xc0, 0x07}, {0xc0, 0x12},
			{0xc0, 0x13}, {0xc0, 0x27}, {0xc0, 0x2f}, {0xc0, 0x14}, {0xc0, 0x28},
			{0xc0, 0x30}, {0xc0, 0x60}, {0xc0, 0x61}, {0xc0, 0x76}, {0xc0, 0x77},
			{0xcc, 0xa8}, {0x13, 0x05}, {0x13, 0x04}, {0x13, 0x03}, {0xcc, 0x13},
			{0xc0, 0x11}, {0x00, 0x0a}, {0x00, 0x2f}, {0x00, 0x3c}, {0xc0, 0x9c},
			{0xc0, 0xa0}, {0x00, 0x9c}, {0x00, 0x35}, {0x00, 0x3d}, {0xc0, 0x9d},
			{0xc0, 0xa1}, {0x00, 0x9d}, {0x00, 0x41}, {0x00, 0xba}, {0x00, 0x84},
			{0x00, 0xc0}, {0x00, 0x07}, {0x00, 0x04}, {0x00, 0x05},
		}
	} else if jarmDetails[3] == "NO1.3" {
		cipherList = [][]byte{
			{0x00, 0x16}, {0x00, 0x33}, {0x00, 0x67}, {0xc0, 0x9e}, {0xc0, 0xa2},
			{0x00, 0x9e}, {0x00, 0x39}, {0x00, 0x6b}, {0xc0, 0x9f}, {0xc0, 0xa3},
			{0x00, 0x9f}, {0x00, 0x45}, {0x00, 0xbe}, {0x00, 0x88}, {0x00, 0xc4},
			{0x00, 0x9a}, {0xc0, 0x08}, {0xc0, 0x09}, {0xc0, 0x23}, {0xc0, 0xac},
			{0xc0, 0xae}, {0xc0, 0x2b}, {0xc0, 0x0a}, {0xc0, 0x24}, {0xc0, 0xad},
			{0xc0, 0xaf}, {0xc0, 0x2c}, {0xc0, 0x72}, {0xc0, 0x73}, {0xcc, 0xa9},
			{0xcc, 0x14}, {0xc0, 0x07}, {0xc0, 0x12}, {0xc0, 0x13}, {0xc0, 0x27},
			{0xc0, 0x2f}, {0xc0, 0x14}, {0xc0, 0x28}, {0xc0, 0x30}, {0xc0, 0x60},
			{0xc0, 0x61}, {0xc0, 0x76}, {0xc0, 0x77}, {0xcc, 0xa8}, {0xcc, 0x13},
			{0xc0, 0x11}, {0x00, 0x0a}, {0x00, 0x2f}, {0x00, 0x3c}, {0xc0, 0x9c},
			{0xc0, 0xa0}, {0x00, 0x9c}, {0x00, 0x35}, {0x00, 0x3d}, {0xc0, 0x9d},
			{0xc0, 0xa1}, {0x00, 0x9d}, {0x00, 0x41}, {0x00, 0xba}, {0x00, 0x84},
			{0x00, 0xc0}, {0x00, 0x07}, {0x00, 0x04}, {0x00, 0x05},
		}
	}

	if jarmDetails[4] != "FORWARD" {
		cipherList = cipherMung(cipherList, jarmDetails[4])
	}

	if jarmDetails[5] == "GREASE" {
		cipherList = append([][]byte{chooseGrease()}, cipherList...)
	}

	for _, cipher := range cipherList {
		selectedCiphers = append(selectedCiphers, cipher...)
	}

	return selectedCiphers
}

// cipherMung returns a modified list of ciphers based on the request
func cipherMung(ciphers [][]byte, request string) [][]byte {
	var output [][]byte
	cipherLen := len(ciphers)

	switch request {
	case "REVERSE":
		// Ciphers backward
		for i := cipherLen - 1; i >= 0; i-- {
			output = append(output, ciphers[i])
		}
	case "BOTTOM_HALF":
		// Bottom half of ciphers
		if cipherLen%2 == 1 {
			output = ciphers[int(cipherLen/2)+1:]
		} else {
			output = ciphers[int(cipherLen/2):]
		}
	case "TOP_HALF":
		// Top half of ciphers in reverse order
		if cipherLen%2 == 1 {
			output = append(output, ciphers[int(cipherLen/2)])
		}
		output = append(output, cipherMung(cipherMung(ciphers, "REVERSE"), "BOTTOM_HALF")...)
	case "MIDDLE_OUT":
		// Middle-out cipher order
		middle := int(cipherLen / 2)
		if cipherLen%2 == 1 {
			output = append(output, ciphers[middle])
			for i := 1; i <= middle; i++ {
				output = append(output, ciphers[middle+i])
				output = append(output, ciphers[middle-i])
			}
		} else {
			for i := 1; i <= middle; i++ {
				output = append(output, ciphers[middle-1+i])
				output = append(output, ciphers[middle-i])
			}
		}
	}

	return output
}

// getExtensions returns the selected extensions based on the JARM details
func getExtensions(jarmDetails []string) []byte {
	var extensionBytes []byte
	var allExtensions []byte
	grease := false

	// GREASE
	if jarmDetails[5] == "GREASE" {
		allExtensions = append(allExtensions, chooseGrease()...)
		allExtensions = append(allExtensions, 0x00, 0x00)
		grease = true
	}

	// Server name
	allExtensions = append(allExtensions, extensionServerName(jarmDetails[0])...)

	// Other extensions
	extendedMasterSecret := []byte{0x00, 0x17, 0x00, 0x00}
	allExtensions = append(allExtensions, extendedMasterSecret...)

	maxFragmentLength := []byte{0x00, 0x01, 0x00, 0x01, 0x01}
	allExtensions = append(allExtensions, maxFragmentLength...)

	renegotiationInfo := []byte{0xff, 0x01, 0x00, 0x01, 0x00}
	allExtensions = append(allExtensions, renegotiationInfo...)

	supportedGroups := []byte{0x00, 0x0a, 0x00, 0x0a, 0x00, 0x08, 0x00, 0x1d, 0x00, 0x17, 0x00, 0x18, 0x00, 0x19}
	allExtensions = append(allExtensions, supportedGroups...)

	ecPointFormats := []byte{0x00, 0x0b, 0x00, 0x02, 0x01, 0x00}
	allExtensions = append(allExtensions, ecPointFormats...)

	sessionTicket := []byte{0x00, 0x23, 0x00, 0x00}
	allExtensions = append(allExtensions, sessionTicket...)

	// Application Layer Protocol Negotiation extension
	allExtensions = append(allExtensions, appLayerProtoNegotiation(jarmDetails)...)

	signatureAlgorithms := []byte{0x00, 0x0d, 0x00, 0x14, 0x00, 0x12, 0x04, 0x03, 0x08, 0x04, 0x04, 0x01, 0x05, 0x03, 0x08, 0x05, 0x05, 0x01, 0x08, 0x06, 0x06, 0x01, 0x02, 0x01}
	allExtensions = append(allExtensions, signatureAlgorithms...)

	// Key share extension
	allExtensions = append(allExtensions, keyShare(grease)...)

	pskKeyExchangeModes := []byte{0x00, 0x2d, 0x00, 0x02, 0x01, 0x01}
	allExtensions = append(allExtensions, pskKeyExchangeModes...)

	// Supported versions extension
	if jarmDetails[2] == tlsV13 || jarmDetails[7] == tlsV12Support {
		allExtensions = append(allExtensions, supportedVersions(jarmDetails, grease)...)
	}

	// Finish assembling extensions
	extensionLength := len(allExtensions)
	extensionBytes = append(extensionBytes, byte(extensionLength>>8), byte(extensionLength&0xff))
	extensionBytes = append(extensionBytes, allExtensions...)

	return extensionBytes
}

// extensionServerName returns the Server Name Indication extension
func extensionServerName(host string) []byte {
	var extSNI []byte
	extSNI = append(extSNI, 0x00, 0x00)
	extSNILength := len(host) + 5
	extSNI = append(extSNI, byte(extSNILength>>8), byte(extSNILength))
	extSNILength2 := len(host) + 3
	extSNI = append(extSNI, byte(extSNILength2>>8), byte(extSNILength2))
	extSNI = append(extSNI, 0x00)
	extSNILength3 := len(host)
	extSNI = append(extSNI, byte(extSNILength3>>8), byte(extSNILength3))
	extSNI = append(extSNI, host...)
	return extSNI
}

// appLayerProtoNegotiation returns the Application Layer Protocol Negotiation extension
func appLayerProtoNegotiation(jarmDetails []string) []byte {
	var ext []byte
	ext = append(ext, 0x00, 0x10)
	var alpns [][]byte

	if jarmDetails[6] == "RARE_APLN" {
		alpns = [][]byte{
			{0x08, 0x68, 0x74, 0x74, 0x70, 0x2f, 0x30, 0x2e, 0x39},
			{0x08, 0x68, 0x74, 0x74, 0x70, 0x2f, 0x31, 0x2e, 0x30},
			{0x06, 0x73, 0x70, 0x64, 0x79, 0x2f, 0x31},
			{0x06, 0x73, 0x70, 0x64, 0x79, 0x2f, 0x32},
			{0x06, 0x73, 0x70, 0x64, 0x79, 0x2f, 0x33},
			{0x03, 0x68, 0x32, 0x63},
			{0x02, 0x68, 0x71},
		}
	} else {
		alpns = [][]byte{
			{0x08, 0x68, 0x74, 0x74, 0x70, 0x2f, 0x30, 0x2e, 0x39},
			{0x08, 0x68, 0x74, 0x74, 0x70, 0x2f, 0x31, 0x2e, 0x30},
			{0x08, 0x68, 0x74, 0x74, 0x70, 0x2f, 0x31, 0x2e, 0x31},
			{0x06, 0x73, 0x70, 0x64, 0x79, 0x2f, 0x31},
			{0x06, 0x73, 0x70, 0x64, 0x79, 0x2f, 0x32},
			{0x06, 0x73, 0x70, 0x64, 0x79, 0x2f, 0x33},
			{0x02, 0x68, 0x32},
			{0x03, 0x68, 0x32, 0x63},
			{0x02, 0x68, 0x71},
		}
	}

	if jarmDetails[8] != "FORWARD" {
		alpns = cipherMung(alpns, jarmDetails[8])
	}

	var allAlpns []byte
	for _, alpn := range alpns {
		allAlpns = append(allAlpns, alpn...)
	}

	secondLength := len(allAlpns)
	firstLength := secondLength + 2
	ext = append(ext, byte(firstLength>>8), byte(firstLength))
	ext = append(ext, byte(secondLength>>8), byte(secondLength))
	ext = append(ext, allAlpns...)
	return ext
}

// keyShare returns the Key Share extension
func keyShare(grease bool) []byte {
	var ext []byte
	ext = append(ext, 0x00, 0x33)
	var shareExt []byte

	if grease {
		shareExt = append(shareExt, chooseGrease()...)
		shareExt = append(shareExt, 0x00, 0x01, 0x00)
	}

	shareExt = append(shareExt, 0x00, 0x1d, 0x00, 0x20)
	shareExt = append(shareExt, randomBytes(32)...)

	secondLength := len(shareExt)
	firstLength := secondLength + 2
	ext = append(ext, byte(firstLength>>8), byte(firstLength))
	ext = append(ext, byte(secondLength>>8), byte(secondLength))
	ext = append(ext, shareExt...)

	return ext
}

// supportedVersions returns the Supported Versions extension
func supportedVersions(jarmDetails []string, grease bool) []byte {
	var ext []byte
	ext = append(ext, 0x00, 0x2b)

	var versions [][]byte
	if jarmDetails[7] == tlsV12Support {
		versions = [][]byte{
			{0x03, 0x01},
			{0x03, 0x02},
			{0x03, 0x03},
		}
	} else {
		versions = [][]byte{
			{0x03, 0x01},
			{0x03, 0x02},
			{0x03, 0x03},
			{0x03, 0x04},
		}
	}

	if jarmDetails[8] != "FORWARD" {
		versions = cipherMung(versions, jarmDetails[8])
	}

	var allVersions []byte
	if grease {
		allVersions = append(allVersions, chooseGrease()...)
	}
	for _, version := range versions {
		allVersions = append(allVersions, version...)
	}

	secondLength := len(allVersions)
	firstLength := secondLength + 1
	ext = append(ext, byte(firstLength>>8), byte(firstLength))
	ext = append(ext, byte(secondLength))
	ext = append(ext, allVersions...)

	// Debug print to match Python format
	cmn.DebugMsg(cmn.DbgLvlDebug3, "supported_versions:  %s\n", formatForPython(ext))
	return ext
}

// sendPacket sends the constructed packet to the target host and port
func (jc JARMCollector) sendPacket(packet []byte, host string, port string) ([]byte, error) {
	address := net.JoinHostPort(host, port)

	var conn net.Conn
	var err error
	if jc.Proxy != nil {
		proxyURL, err := url.Parse(jc.Proxy.Address)
		if err != nil {
			return nil, fmt.Errorf("proxy parse error: %v", err)
		}

		dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("proxy error: %v", err)
		}
		conn, err = dialer.Dial("tcp", address)
		if err != nil {
			return nil, fmt.Errorf("proxy dial error: %v", err)
		}
	} else {
		// Connect directly if no proxy is provided
		conn, err = net.DialTimeout("tcp", address, 20*time.Second)
		if err != nil {
			return nil, fmt.Errorf("direct dial error: %v", err)
		}
	}
	defer conn.Close()

	// Set timeout
	err = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("set deadline error: %v", err)
	}

	// Send packet
	_, err = conn.Write(packet)
	if err != nil {
		return nil, fmt.Errorf("write packet error: %v", err)
	}

	// Receive server hello
	buff := make([]byte, 1484)
	n, err := conn.Read(buff)
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("connection closed by peer")
		}
		return nil, fmt.Errorf("read packet error: %v", err)
	}

	return buff[:n], nil
}

// readPacket reads the response packet and extracts the JARM fingerprint
func readPacket(data []byte, _ []string) string {
	// _ should be jarmDetails, but it is not used at the moment
	if data == nil {
		return "|||"
	}
	var jarm strings.Builder

	if data[0] == 21 {
		return "|||"
	}

	if data[0] == 22 && data[5] == 2 {
		serverHelloLength := int(binary.BigEndian.Uint16(data[3:5]))
		counter := int(data[43])
		selectedCipher := data[counter+44 : counter+46]
		version := data[9:11]

		jarm.WriteString(hex.EncodeToString(selectedCipher))
		jarm.WriteString("|")
		jarm.WriteString(hex.EncodeToString(version))
		jarm.WriteString("|")
		extensions := extractExtensionInfo(data, counter, serverHelloLength)
		jarm.WriteString(extensions)
		return jarm.String()
	}

	return "|||"
}

// extractExtensionInfo extracts the extension information from the ServerHello message
func extractExtensionInfo(data []byte, counter int, serverHelloLength int) string {
	if data[counter+47] == 11 || bytes.Equal(data[counter+50:counter+53], []byte{0x0e, 0xac, 0x0b}) || counter+42 >= serverHelloLength {
		return "|"
	}

	count := 49 + counter
	length := int(binary.BigEndian.Uint16(data[counter+47 : counter+49]))
	maximum := length + count - 1
	var types [][]byte
	var values [][]byte

	for count < maximum {
		types = append(types, data[count:count+2])
		extLength := int(binary.BigEndian.Uint16(data[count+2 : count+4]))
		if extLength == 0 {
			count += 4
			values = append(values, []byte{})
		} else {
			values = append(values, data[count+4:count+4+extLength])
			count += extLength + 4
		}
	}

	var result strings.Builder
	alpn := findExtension([]byte{0x00, 0x10}, types, values)
	result.WriteString(alpn)
	result.WriteString("|")

	for i, t := range types {
		result.WriteString(hex.EncodeToString(t))
		if i < len(types)-1 {
			result.WriteString("-")
		}
	}

	return result.String()
}

// findExtension finds the extension type in the list of types and returns the corresponding value
func findExtension(extType []byte, types [][]byte, values [][]byte) string {
	for i, t := range types {
		if bytes.Equal(t, extType) {
			if bytes.Equal(extType, []byte{0x00, 0x10}) {
				return string(values[i][3:])
			}
			return hex.EncodeToString(values[i])
		}
	}
	return ""
}

func toBytes(i int) []byte {
	return []byte{byte(i >> 8), byte(i)}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		fmt.Println("error:", err)
		return nil
	}
	return b
}

// chooseGrease returns a GREASE value
func chooseGrease() []byte {
	greaseList := [][]byte{
		{0x0a, 0x0a}, {0x1a, 0x1a}, {0x2a, 0x2a}, {0x3a, 0x3a}, {0x4a, 0x4a},
		{0x5a, 0x5a}, {0x6a, 0x6a}, {0x7a, 0x7a}, {0x8a, 0x8a}, {0x9a, 0x9a},
		{0xaa, 0xaa}, {0xba, 0xba}, {0xca, 0xca}, {0xda, 0xda}, {0xea, 0xea},
		{0xfa, 0xfa},
	}

	// Use crypto/rand equivalent of math/rand rand.Intn
	x := io.Reader(rand.Reader)
	y := big.NewInt(int64(len(greaseList)))
	n, err := rand.Int(x, y)
	if err != nil {
		fmt.Println("error:", err)
		return greaseList[0] // return a default value in case of error
	}

	idx := int(n.Int64())
	return greaseList[idx]
}
