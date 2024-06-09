// Package httpinfo provides functionality to extract HTTP header information
package httpinfo

// "math/rand"
import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"time"
)

type JARMCollector struct{}

func (jc JARMCollector) Collect(host string, port string) (string, error) {
	jarmDetails := [10][]string{
		{host, port, "TLS_1.2", "ALL", "FORWARD", "NO_GREASE", "APLN", "1.2_SUPPORT", "REVERSE"},
		{host, port, "TLS_1.2", "ALL", "REVERSE", "NO_GREASE", "APLN", "1.2_SUPPORT", "FORWARD"},
		{host, port, "TLS_1.2", "ALL", "TOP_HALF", "NO_GREASE", "APLN", "NO_SUPPORT", "FORWARD"},
		{host, port, "TLS_1.2", "ALL", "BOTTOM_HALF", "NO_GREASE", "RARE_APLN", "NO_SUPPORT", "FORWARD"},
		{host, port, "TLS_1.2", "ALL", "MIDDLE_OUT", "GREASE", "RARE_APLN", "NO_SUPPORT", "REVERSE"},
		{host, port, "TLS_1.1", "ALL", "FORWARD", "NO_GREASE", "APLN", "NO_SUPPORT", "FORWARD"},
		{host, port, "TLS_1.3", "ALL", "FORWARD", "NO_GREASE", "APLN", "1.3_SUPPORT", "REVERSE"},
		{host, port, "TLS_1.3", "ALL", "REVERSE", "NO_GREASE", "APLN", "1.3_SUPPORT", "FORWARD"},
		{host, port, "TLS_1.3", "NO1.3", "FORWARD", "NO_GREASE", "APLN", "1.3_SUPPORT", "FORWARD"},
		{host, port, "TLS_1.3", "ALL", "MIDDLE_OUT", "GREASE", "APLN", "1.3_SUPPORT", "REVERSE"},
	}

	var jarmBuilder strings.Builder
	for _, detail := range jarmDetails {
		packet := buildPacket(detail)
		serverHello, err := sendPacket(packet, host, port)
		if err != nil {
			return "", err
		}
		ans := readPacket(serverHello, detail)
		jarmBuilder.WriteString(ans + ",")
	}
	jarm := strings.TrimRight(jarmBuilder.String(), ",")
	return jarm, nil
}

func buildPacket(jarmDetails []string) []byte {
	payload := []byte{0x16}
	var clientHello []byte

	switch jarmDetails[2] {
	case "TLS_1.3":
		payload = append(payload, []byte{0x03, 0x01}...)
		clientHello = append(clientHello, []byte{0x03, 0x03}...)
	case "SSLv3":
		payload = append(payload, []byte{0x03, 0x00}...)
		clientHello = append(clientHello, []byte{0x03, 0x00}...)
	case "TLS_1":
		payload = append(payload, []byte{0x03, 0x01}...)
		clientHello = append(clientHello, []byte{0x03, 0x01}...)
	case "TLS_1.1":
		payload = append(payload, []byte{0x03, 0x02}...)
		clientHello = append(clientHello, []byte{0x03, 0x02}...)
	case "TLS_1.2":
		payload = append(payload, []byte{0x03, 0x03}...)
		clientHello = append(clientHello, []byte{0x03, 0x03}...)
	}

	clientHello = append(clientHello, randomBytes(32)...)
	sessionID := randomBytes(32)
	clientHello = append(clientHello, byte(len(sessionID)))
	clientHello = append(clientHello, sessionID...)
	cipherChoice := getCiphers(jarmDetails)
	clientHello = append(clientHello, byte(len(cipherChoice)>>8), byte(len(cipherChoice)))
	clientHello = append(clientHello, cipherChoice...)
	clientHello = append(clientHello, 0x01, 0x00)
	extensions := getExtensions(jarmDetails)
	clientHello = append(clientHello, byte(len(extensions)>>8), byte(len(extensions)))
	clientHello = append(clientHello, extensions...)

	innerLength := append([]byte{0x00}, toBytes(len(clientHello))...)
	handshakeProtocol := append([]byte{0x01}, innerLength...)
	handshakeProtocol = append(handshakeProtocol, clientHello...)
	outerLength := toBytes(len(handshakeProtocol))
	payload = append(payload, outerLength...)
	payload = append(payload, handshakeProtocol...)
	return payload
}

func getCiphers(jarmDetails []string) []byte {
	selectedCiphers := []byte{}
	var cipherList [][]byte

	if jarmDetails[3] == "ALL" {
		cipherList = [][]byte{{0x00, 0x16}, {0x00, 0x33}, {0x00, 0x67}, {0xc0, 0x9e}, {0xc0, 0xa2},
			{0x00, 0x9e}, {0x00, 0x39}, {0x00, 0x6b}, {0xc0, 0x9f}, {0xc0, 0xa3}, {0x00, 0x9f}, {0x00, 0x45},
			{0x00, 0xbe}, {0x00, 0x88}, {0x00, 0xc4}, {0x00, 0x9a}, {0xc0, 0x08}, {0xc0, 0x09}, {0xc0, 0x23},
			{0xc0, 0xac}, {0xc0, 0xae}, {0xc0, 0x2b}, {0xc0, 0x0a}, {0xc0, 0x24}, {0xc0, 0xad}, {0xc0, 0xaf},
			{0xc0, 0x2c}, {0xc0, 0x72}, {0xc0, 0x73}, {0xcc, 0xa9}, {0x13, 0x02}, {0x13, 0x01}, {0xcc, 0x14},
			{0xc0, 0x07}, {0xc0, 0x12}, {0xc0, 0x13}, {0xc0, 0x27}, {0xc0, 0x2f}, {0xc0, 0x14}, {0xc0, 0x28},
			{0xc0, 0x30}, {0xc0, 0x60}, {0xc0, 0x61}, {0xc0, 0x76}, {0xc0, 0x77}, {0xcc, 0xa8}, {0x13, 0x05},
			{0x13, 0x04}, {0x13, 0x03}, {0xcc, 0x13}, {0xc0, 0x11}, {0x00, 0x0a}, {0x00, 0x2f}, {0x00, 0x3c},
			{0xc0, 0x9c}, {0xc0, 0xa0}, {0x00, 0x9c}, {0x00, 0x35}, {0x00, 0x3d}, {0xc0, 0x9d}, {0xc0, 0xa1},
			{0x00, 0x9d}, {0x00, 0x41}, {0x00, 0xba}, {0x00, 0x84}, {0x00, 0xc0}, {0x00, 0x07}, {0x00, 0x04},
			{0x00, 0x05}}
	} else if jarmDetails[3] == "NO1.3" {
		cipherList = [][]byte{{0x00, 0x16}, {0x00, 0x33}, {0x00, 0x67}, {0xc0, 0x9e}, {0xc0, 0xa2},
			{0x00, 0x9e}, {0x00, 0x39}, {0x00, 0x6b}, {0xc0, 0x9f}, {0xc0, 0xa3}, {0x00, 0x9f}, {0x00, 0x45},
			{0x00, 0xbe}, {0x00, 0x88}, {0x00, 0xc4}, {0x00, 0x9a}, {0xc0, 0x08}, {0xc0, 0x09}, {0xc0, 0x23},
			{0xc0, 0xac}, {0xc0, 0xae}, {0xc0, 0x2b}, {0xc0, 0x0a}, {0xc0, 0x24}, {0xc0, 0xad}, {0xc0, 0xaf},
			{0xc0, 0x2c}, {0xc0, 0x72}, {0xc0, 0x73}, {0xcc, 0xa9}, {0xcc, 0x14}, {0xc0, 0x07}, {0xc0, 0x12},
			{0xc0, 0x13}, {0xc0, 0x27}, {0xc0, 0x2f}, {0xc0, 0x14}, {0xc0, 0x28}, {0xc0, 0x30}, {0xc0, 0x60},
			{0xc0, 0x61}, {0xc0, 0x76}, {0xc0, 0x77}, {0xcc, 0xa8}, {0xcc, 0x13}, {0xc0, 0x11}, {0x00, 0x0a},
			{0x00, 0x2f}, {0x00, 0x3c}, {0xc0, 0x9c}, {0xc0, 0xa0}, {0x00, 0x9c}, {0x00, 0x35}, {0x00, 0x3d},
			{0xc0, 0x9d}, {0xc0, 0xa1}, {0x00, 0x9d}, {0x00, 0x41}, {0x00, 0xba}, {0x00, 0x84}, {0x00, 0xc0},
			{0x00, 0x07}, {0x00, 0x04}, {0x00, 0x05}}
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

func cipherMung(ciphers [][]byte, request string) [][]byte {
	var output [][]byte
	cipherLen := len(ciphers)

	switch request {
	case "REVERSE":
		for i := len(ciphers) - 1; i >= 0; i-- {
			output = append(output, ciphers[i])
		}
	case "BOTTOM_HALF":
		output = ciphers[cipherLen/2:]
	case "TOP_HALF":
		output = ciphers[:cipherLen/2]
	case "MIDDLE_OUT":
		middle := cipherLen / 2
		if cipherLen%2 == 1 {
			output = append(output, ciphers[middle])
		}
		for i := 1; i <= middle; i++ {
			if middle+i < cipherLen {
				output = append(output, ciphers[middle+i])
			}
			if middle-i >= 0 {
				output = append(output, ciphers[middle-i])
			}
		}
	}

	return output
}

func getExtensions(jarmDetails []string) []byte {
	var extensionBytes []byte
	var allExtensions []byte
	grease := false

	if jarmDetails[5] == "GREASE" {
		allExtensions = append(allExtensions, chooseGrease()...)
		allExtensions = append(allExtensions, 0x00, 0x00)
		grease = true
	}

	allExtensions = append(allExtensions, extensionServerName(jarmDetails[0])...)
	allExtensions = append(allExtensions, 0x00, 0x17, 0x00, 0x00)                                                             // Extended Master Secret
	allExtensions = append(allExtensions, 0x00, 0x01, 0x00, 0x01, 0x01)                                                       // Max Fragment Length
	allExtensions = append(allExtensions, 0xff, 0x01, 0x00, 0x01, 0x00)                                                       // Renegotiation Info
	allExtensions = append(allExtensions, 0x00, 0x0a, 0x00, 0x0a, 0x00, 0x08, 0x00, 0x1d, 0x00, 0x17, 0x00, 0x18, 0x00, 0x19) // Supported Groups
	allExtensions = append(allExtensions, 0x00, 0x0b, 0x00, 0x02, 0x01, 0x00)                                                 // EC Point Formats
	allExtensions = append(allExtensions, 0x00, 0x23, 0x00, 0x00)                                                             // Session Ticket
	allExtensions = append(allExtensions, appLayerProtoNegotiation(jarmDetails)...)
	allExtensions = append(allExtensions, 0x00, 0x0d, 0x00, 0x14, 0x00, 0x12, 0x04, 0x03, 0x08, 0x04, 0x04, 0x01, 0x05, 0x03, 0x08, 0x05, 0x05, 0x01, 0x08, 0x06, 0x06, 0x01, 0x02, 0x01) // Signature Algorithms
	allExtensions = append(allExtensions, keyShare(grease)...)
	allExtensions = append(allExtensions, 0x00, 0x2d, 0x00, 0x02, 0x01, 0x01) // PSK Key Exchange Modes

	if jarmDetails[2] == "TLS_1.3" || jarmDetails[7] == "1.2_SUPPORT" {
		allExtensions = append(allExtensions, supportedVersions(jarmDetails, grease)...)
	}

	extensionLength := toBytes(len(allExtensions))
	extensionBytes = append(extensionBytes, extensionLength...)
	extensionBytes = append(extensionBytes, allExtensions...)
	return extensionBytes
}

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

func supportedVersions(jarmDetails []string, grease bool) []byte {
	var ext []byte
	ext = append(ext, 0x00, 0x2b)
	var versions [][]byte

	if jarmDetails[7] == "1.2_SUPPORT" {
		versions = [][]byte{[]byte{0x03, 0x01}, []byte{0x03, 0x02}, []byte{0x03, 0x03}}
	} else {
		versions = [][]byte{[]byte{0x03, 0x01}, []byte{0x03, 0x02}, []byte{0x03, 0x03}, []byte{0x03, 0x04}}
	}

	if jarmDetails[8] != "FORWARD" {
		versions = cipherMung(versions, jarmDetails[8])
	}

	if grease {
		versions = append([][]byte{chooseGrease()}, versions...)
	}

	var allVersions []byte
	for _, version := range versions {
		allVersions = append(allVersions, version...)
	}

	secondLength := len(allVersions)
	firstLength := secondLength + 1
	ext = append(ext, byte(firstLength>>8), byte(firstLength))
	ext = append(ext, byte(secondLength))
	ext = append(ext, allVersions...)
	return ext
}

func sendPacket(packet []byte, host string, port string) ([]byte, error) {
	address := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", address, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	err = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err != nil {
		return nil, err
	}
	_, err = conn.Write(packet)
	if err != nil {
		return nil, err
	}

	buff := make([]byte, 1484)
	n, err := conn.Read(buff)
	if err != nil {
		return nil, err
	}

	return buff[:n], nil
}

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

func chooseGrease() []byte {
	greaseList := [][]byte{
		{0x0a, 0x0a}, {0x1a, 0x1a}, {0x2a, 0x2a}, {0x3a, 0x3a}, {0x4a, 0x4a},
		{0x5a, 0x5a}, {0x6a, 0x6a}, {0x7a, 0x7a}, {0x8a, 0x8a}, {0x9a, 0x9a},
		{0xaa, 0xaa}, {0xba, 0xba}, {0xca, 0xca}, {0xda, 0xda}, {0xea, 0xea},
		{0xfa, 0xfa},
	}
	//rand.Seed(time.Now().UnixNano())
	//New(NewSource(time.Now().UnixNano()))
	// Use crypto/rand equivalent of math/rand rand.Intn
	// rand.Intn(len(greaseList))
	x := io.Reader(rand.Reader)
	// transform lent(greaseList) to a big.Int
	y := big.NewInt(int64(len(greaseList)))
	n, err := rand.Int(x, y)
	if err != nil {
		fmt.Println("error:", err)
		return nil
	}
	// transform n to int
	idx := int(n.Int64())
	return greaseList[idx]
}
