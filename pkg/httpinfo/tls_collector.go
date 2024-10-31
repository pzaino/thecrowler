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

// Package httpinfo provides functionality to extract HTTP header and SSL/TLS information
package httpinfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"

	"crypto/tls"
	"io"
	"net"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

const (
	errInvalidExtension = "invalid extension"
)

type captureConn struct {
	net.Conn
	r io.Reader
	w io.Writer
}

// Read wraps Read and returns an error if it is not supported
func (c *captureConn) Read(b []byte) (int, error) {
	if c.r == nil {
		return 0, io.EOF
	}
	return c.r.Read(b)
}

// Write wraps Write and returns an error if it is not supported
func (c *captureConn) Write(b []byte) (int, error) {
	if c.w == nil {
		return len(b), fmt.Errorf("write not supported")
	}
	return c.w.Write(b)
}

// DataCollector stores the proxy configuration and provides methods to collect data from a host
type DataCollector struct {
	Proxy *cfg.SOCKSProxy
}

func (dc DataCollector) dial(host, port string) (net.Conn, error) {
	address := net.JoinHostPort(host, port)
	if dc.Proxy != nil && dc.Proxy.Address != "" {
		proxyURL, err := url.Parse(dc.Proxy.Address)
		if err != nil {
			return nil, err
		}

		if dc.Proxy.Username != "" {
			proxyURL.User = url.UserPassword(dc.Proxy.Username, dc.Proxy.Password)
		}

		dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, err
		}

		return dialer.Dial("tcp", address)
	}

	return net.DialTimeout("tcp", address, 10*time.Second)
}

// CollectAll collects all data from the given host and port
// aka calls CollectTLS, CollectSSH, CollectJARM and the Server and Client extensions parsers
// to extract all available data and stores it in a CollectedData struct
func (dc DataCollector) CollectAll(host string, port string, c *Config) (*CollectedData, error) {
	collectedData := &CollectedData{}

	// Set the proxy if it is defined
	var proxy *cfg.SOCKSProxy
	if c != nil {
		if len(c.Proxies) > 0 {
			if len(c.Proxies) > 1 {
				proxy = &c.Proxies[1]
			} else {
				proxy = &c.Proxies[0]
			}
		}
	}
	if proxy != nil {
		dc.Proxy = proxy
	}

	// Buffers to capture the TLS handshake
	var clientHelloBuf bytes.Buffer
	var serverHelloBuf bytes.Buffer

	// Dial the server
	rawConn, err := dc.dial(host, port)
	if err != nil {
		return nil, err
	}
	defer rawConn.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Capture server-side communication (ServerHello)
	serverHelloCapture := io.TeeReader(rawConn, &serverHelloBuf)

	// Wrap the connection to capture the ClientHello message
	//clientHelloCapture := io.TeeReader(rawConn, &clientHelloBuf)
	captureConn := &captureConn{Conn: rawConn, r: serverHelloCapture, w: rawConn}

	// Perform the TLS handshake
	conn := tls.Client(captureConn, &tls.Config{
		//nolint:gosec // Disabling G402: My code has to test insecure connections, so it's ok here to disable this
		InsecureSkipVerify: true,
		ServerName:         host,                       // Ensures SNI is used
		NextProtos:         []string{"h2", "http/1.1"}, // Enables ALPN negotiation for HTTP/2
	})
	err = conn.Handshake()
	if err != nil {
		return nil, err
	}

	// Collect TLS Handshake state
	collectedData.TLSHandshakeState = conn.ConnectionState()

	// Collect Peer Certificates
	collectedData.TLSCertificates = conn.ConnectionState().PeerCertificates

	// Store captured ClientHello message
	collectedData.RawClientHello = clientHelloBuf.Bytes()

	// Store captured ServerHello message
	collectedData.RawServerHello = serverHelloBuf.Bytes()

	// Debug: Print the captured ServerHello
	fmt.Printf("Captured ServerHello: %x\n", collectedData.RawServerHello)

	// Collect JARM fingerprint
	if c.SSLDiscovery.JARM {
		jarmCollector := JARMCollector{}
		if proxy != nil {
			jarmCollector.Proxy = proxy
		}
		jarmFingerprint, err := jarmCollector.Collect(host, port)
		if err != nil {
			return collectedData, err
		}
		collectedData.JARMFingerprint = jarmFingerprint
		cmn.DebugMsg(cmn.DbgLvlDebug5, "JARM collected Fingerprint: %s", jarmFingerprint)
	}

	// Collect SSH data
	if c.SSHDiscovery {
		err = dc.CollectSSH(collectedData, host, port)
		if err != nil {
			return collectedData, err
		}
	}

	return collectedData, nil
}

// ExtractServerHelloDetailsUsingState extracts TLS version, cipher suite, extensions, and ALPN from a TLS ConnectionState
func ExtractServerHelloDetailsUsingState(state tls.ConnectionState) (tlsVersion uint16, cipherSuite uint16, extensions []uint16, alpn string, err error) {
	tlsVersion = state.Version
	cipherSuite = state.CipherSuite

	// Assuming extensions and ALPN can be gathered from ConnectionState
	// Note: TLS 1.3 may not expose all extensions directly

	// Check negotiated ALPN protocol
	if len(state.NegotiatedProtocol) > 0 {
		alpn = state.NegotiatedProtocol
	}

	// Add logic to populate extensions if available through ConnectionState
	// This part may need custom handling if specific extensions need parsing
	return tlsVersion, cipherSuite, extensions, alpn, nil
}

// CollectSSH collects SSH data from the given host and port
func (dc DataCollector) CollectSSH(collectedData *CollectedData, host string, port string) error {
	// Buffers to capture the SSH handshake
	var clientHelloBuf, serverHelloBuf bytes.Buffer

	// Dial the SSH server
	conn, err := dc.dial(host, port)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Create SSH client config
	clientConfig := &ssh.ClientConfig{
		User: "user",
		//nolint:gosec // Disabling G402: My code has to test insecure connections, so it's ok here to disable this
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Wrap the connection to capture the ClientHello and ServerHello messages
	clientHelloCapture := io.TeeReader(conn, &clientHelloBuf)
	serverHelloCapture := io.MultiWriter(&serverHelloBuf, conn)
	captureConn := &captureConn{Conn: conn, r: clientHelloCapture, w: serverHelloCapture}

	// Perform the SSH handshake
	sshConn, newChannels, requests, err := ssh.NewClientConn(captureConn, host, clientConfig)
	if err != nil {
		return err
	}
	defer sshConn.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Store captured SSH ClientHello and ServerHello messages
	collectedData.SSHClientHello = clientHelloBuf.Bytes()
	collectedData.SSHServerHello = serverHelloBuf.Bytes()

	// Handle channels and requests (necessary for SSH connection)
	go ssh.DiscardRequests(requests)
	go handleSSHChannels(newChannels)

	return nil
}

func handleSSHChannels(channels <-chan ssh.NewChannel) {
	for newChannel := range channels {
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go ssh.DiscardRequests(requests)
		_ = channel.Close()
	}
}

/////////
// Parsers for TLS ClientHello and ServerHello messages:
////////

// ExtractExtensionsFromClientHello extracts extensions from a raw ClientHello message
func ExtractExtensionsFromClientHello(rawClientHello []byte) ([]uint16, error) {
	var extensions []uint16

	// Ensure the message is a valid ClientHello
	if len(rawClientHello) < 5 || rawClientHello[0] != 0x16 || rawClientHello[5] != 0x01 {
		return nil, errors.New("not a valid ClientHello message")
	}

	// Start parsing after TLS record layer (5 bytes) and handshake layer (4 bytes)
	pos := 5 + 4

	// Session ID Length
	sessionIDL := int(rawClientHello[pos])
	pos += 1 + sessionIDL

	// Cipher Suites Length
	cipherSuitesL := int(binary.BigEndian.Uint16(rawClientHello[pos:]))
	pos += 2 + cipherSuitesL

	// Compression Methods Length
	compressionMethodsL := int(rawClientHello[pos])
	pos += 1 + compressionMethodsL

	// Extensions Length
	if pos+2 > len(rawClientHello) {
		return nil, errors.New("invalid extensions length")
	}
	extensionsL := int(binary.BigEndian.Uint16(rawClientHello[pos:]))
	pos += 2

	endPos := pos + extensionsL
	if endPos > len(rawClientHello) {
		return nil, errors.New("invalid extensions data")
	}

	// Parse each extension
	for pos < endPos {
		if pos+4 > len(rawClientHello) {
			return nil, errors.New(errInvalidExtension)
		}
		extensionType := binary.BigEndian.Uint16(rawClientHello[pos:])
		extensions = append(extensions, extensionType)
		extensionL := int(binary.BigEndian.Uint16(rawClientHello[pos+2:]))
		pos += 4 + extensionL
	}

	return extensions, nil
}

// ExtractClientHelloDetails extracts TLS version, cipher suites, extensions, SNI, ALPN, supported groups, and signature algorithms from a raw ClientHello message
func ExtractClientHelloDetails(rawClientHello []byte) (tlsVersion uint16, cipherSuites, supportedGroups, signatureAlgorithms, extensions []uint16, sni string, alpn []string, err error) {
	if len(rawClientHello) < 5 || rawClientHello[0] != 0x16 || rawClientHello[5] != 0x01 {
		return 0, nil, nil, nil, nil, "", nil, errors.New("not a valid ClientHello message")
	}

	pos := 5 + 4 // Skip record header (5 bytes) and handshake header (4 bytes)

	// Extract TLS Version
	tlsVersion = binary.BigEndian.Uint16(rawClientHello[9:11])

	// Skip Session ID
	sessionIDL := int(rawClientHello[pos])
	pos += 1 + sessionIDL

	// Extract Cipher Suites
	cipherSuitesL := int(binary.BigEndian.Uint16(rawClientHello[pos:]))
	pos += 2
	cipherSuites = make([]uint16, 0, cipherSuitesL/2)
	for i := 0; i < cipherSuitesL; i += 2 {
		cipherSuite := binary.BigEndian.Uint16(rawClientHello[pos+i:])
		cipherSuites = append(cipherSuites, cipherSuite)
	}
	pos += cipherSuitesL

	// Skip Compression Methods
	compressionMethodsL := int(rawClientHello[pos])
	pos += 1 + compressionMethodsL

	// Extensions Length
	if pos+2 > len(rawClientHello) {
		return 0, nil, nil, nil, nil, "", nil, errors.New("invalid extensions length")
	}
	extensionsL := int(binary.BigEndian.Uint16(rawClientHello[pos:]))
	pos += 2

	endPos := pos + extensionsL
	if endPos > len(rawClientHello) {
		return 0, nil, nil, nil, nil, "", nil, errors.New("invalid extensions data")
	}

	// Parse each extension
	for pos < endPos {
		if pos+4 > len(rawClientHello) {
			return 0, nil, nil, nil, nil, "", nil, errors.New(errInvalidExtension)
		}
		extensionType := binary.BigEndian.Uint16(rawClientHello[pos:])
		extensions = append(extensions, extensionType)
		extensionL := int(binary.BigEndian.Uint16(rawClientHello[pos+2:]))
		extensionData := rawClientHello[pos+4 : pos+4+extensionL]

		switch extensionType {
		case 0x00: // SNI
			if len(extensionData) > 5 {
				serverNameLen := int(binary.BigEndian.Uint16(extensionData[3:]))
				sni = string(extensionData[5 : 5+serverNameLen])
			}
		case 0x10: // ALPN
			if len(extensionData) > 2 {
				protocolsLen := int(binary.BigEndian.Uint16(extensionData[:2]))
				protocolsData := extensionData[2 : 2+protocolsLen]
				for len(protocolsData) > 0 {
					protoLen := int(protocolsData[0])
					if protoLen+1 > len(protocolsData) {
						return 0, nil, nil, nil, nil, "", nil, errors.New("invalid ALPN protocol length")
					}
					alpn = append(alpn, string(protocolsData[1:1+protoLen]))
					protocolsData = protocolsData[1+protoLen:]
				}
			}
		case 0x0a: // Supported Groups (formerly Elliptic Curves)
			if len(extensionData) >= 2 {
				groupsLen := int(binary.BigEndian.Uint16(extensionData[:2]))
				for i := 2; i < 2+groupsLen; i += 2 {
					group := binary.BigEndian.Uint16(extensionData[i:])
					supportedGroups = append(supportedGroups, group)
				}
			}
		case 0x0d: // Signature Algorithms
			if len(extensionData) >= 2 {
				sigAlgosLen := int(binary.BigEndian.Uint16(extensionData[:2]))
				for i := 2; i < 2+sigAlgosLen; i += 2 {
					sigAlgo := binary.BigEndian.Uint16(extensionData[i:])
					signatureAlgorithms = append(signatureAlgorithms, sigAlgo)
				}
			}
		}

		pos += 4 + extensionL
	}

	return tlsVersion, cipherSuites, supportedGroups, signatureAlgorithms, extensions, sni, alpn, nil
}

// ExtractExtensionsFromServerHello extracts extensions from a raw ServerHello message
func ExtractExtensionsFromServerHello(rawServerHello []byte) ([]uint16, error) {
	var extensions []uint16

	// Ensure the message is a valid ServerHello
	if len(rawServerHello) < 5 || rawServerHello[0] != 0x16 || rawServerHello[5] != 0x02 {
		return nil, errors.New("not a valid ServerHello message")
	}

	// Start parsing after TLS record layer (5 bytes) and handshake layer (4 bytes)
	pos := 5 + 4

	// Skip to the start of extensions
	// Session ID Length
	sessionIDL := int(rawServerHello[pos])
	pos += 1 + sessionIDL

	// Cipher Suite
	pos += 2

	// Compression Method
	pos++

	// Extensions Length
	if pos+2 > len(rawServerHello) {
		return nil, errors.New("invalid extensions length")
	}
	extensionsL := int(binary.BigEndian.Uint16(rawServerHello[pos:]))
	pos += 2

	endPos := pos + extensionsL
	if endPos > len(rawServerHello) {
		return nil, errors.New("invalid extensions data")
	}

	// Parse each extension
	for pos < endPos {
		if pos+4 > len(rawServerHello) {
			return nil, errors.New(errInvalidExtension)
		}
		extensionType := binary.BigEndian.Uint16(rawServerHello[pos:])
		extensions = append(extensions, extensionType)
		extensionL := int(binary.BigEndian.Uint16(rawServerHello[pos+2:]))
		pos += 4 + extensionL
	}

	return extensions, nil
}

// ExtractServerHelloDetails extracts TLS version, cipher suite, extensions, and ALPN from a raw ServerHello message
func ExtractServerHelloDetails(rawServerHello []byte) (tlsVersion uint16, cipherSuite uint16, extensions []uint16, alpn string, err error) {
	if len(rawServerHello) < 5 {
		return 0, 0, nil, "", errors.New("not enough data for TLS record header")
	}

	// Check the TLS record layer header
	if rawServerHello[0] != 0x16 {
		return 0, 0, nil, "", errors.New("not a valid TLS Handshake message")
	}

	// Read the record length
	recordLength := int(binary.BigEndian.Uint16(rawServerHello[3:5]))
	if recordLength > len(rawServerHello)-5 {
		return 0, 0, nil, "", errors.New("record length exceeds data length")
	}

	pos := 5 // Start after the record header

	// Check the handshake message type
	if rawServerHello[pos] != 0x02 {
		return 0, 0, nil, "", errors.New("not a valid ServerHello message")
	}
	pos++

	// Read the handshake message length
	handshakeLength := int(binary.BigEndian.Uint32(append([]byte{0}, rawServerHello[pos:pos+3]...)))
	pos += 3

	if handshakeLength > recordLength {
		return 0, 0, nil, "", errors.New("handshake length exceeds record length")
	}

	// Extract the protocol version
	tlsVersion = binary.BigEndian.Uint16(rawServerHello[pos:])
	pos += 2

	// Skip random (32 bytes)
	pos += 32

	// Read Session ID length and skip the Session ID
	sessionIDL := int(rawServerHello[pos])
	pos += 1 + sessionIDL

	if pos+2 > len(rawServerHello) {
		return 0, 0, nil, "", errors.New("unexpected end of data after session ID")
	}

	// Extract the cipher suite
	cipherSuite = binary.BigEndian.Uint16(rawServerHello[pos:])
	pos += 2

	// Skip compression method
	pos++

	if pos+2 > len(rawServerHello) {
		return 0, 0, nil, "", errors.New("unexpected end of data after compression method")
	}

	// Extract extensions length
	extensionsL := int(binary.BigEndian.Uint16(rawServerHello[pos:]))
	pos += 2

	endPos := pos + extensionsL
	if endPos > len(rawServerHello) {
		return 0, 0, nil, "", errors.New("extensions length exceeds available data")
	}

	// Parse extensions
	for pos < endPos {
		if pos+4 > len(rawServerHello) {
			return 0, 0, nil, "", errors.New(errInvalidExtension)
		}

		extensionType := binary.BigEndian.Uint16(rawServerHello[pos:])
		extensions = append(extensions, extensionType)
		extensionL := int(binary.BigEndian.Uint16(rawServerHello[pos+2:]))

		if pos+4+extensionL > len(rawServerHello) {
			return 0, 0, nil, "", errors.New("extension data length exceeds available data")
		}

		extensionData := rawServerHello[pos+4 : pos+4+extensionL]
		if extensionType == 0x10 { // ALPN
			if len(extensionData) > 0 {
				alpnLen := int(extensionData[0])
				if alpnLen+1 > len(extensionData) {
					return 0, 0, nil, "", errors.New("invalid ALPN length")
				}
				alpn = string(extensionData[1 : 1+alpnLen])
			}
		}

		pos += 4 + extensionL
	}

	return tlsVersion, cipherSuite, extensions, alpn, nil
}
