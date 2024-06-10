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

type captureConn struct {
	net.Conn
	r io.Reader
	w io.Writer
}

func (c *captureConn) Read(b []byte) (int, error) {
	if c.r == nil {
		return 0, io.EOF
	}
	return c.r.Read(b)
}

func (c *captureConn) Write(b []byte) (int, error) {
	if c.w == nil {
		return len(b), fmt.Errorf("write not supported")
	}
	return c.w.Write(b)
}

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

	// Buffer to capture the TLS handshake
	var clientHelloBuf bytes.Buffer //, serverHelloBuf bytes.Buffer

	// Dial the server
	rawConn, err := dc.dial(host, port)
	if err != nil {
		return nil, err
	}
	defer rawConn.Close()

	// Wrap the connection to capture the ClientHello message
	clientHelloCapture := io.TeeReader(rawConn, &clientHelloBuf)
	captureConn := &captureConn{Conn: rawConn, r: clientHelloCapture, w: rawConn}

	// Perform the TLS handshake
	conn := tls.Client(captureConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	err = conn.Handshake()
	if err != nil {
		return nil, err
	}

	// Collect TLS Handshake state
	collectedData.TLSHandshakeState = conn.ConnectionState()

	// Collect Peer Certificates
	collectedData.TLSCertificates = conn.ConnectionState().PeerCertificates

	// Store captured ClientHello and ServerHello messages
	collectedData.RawClientHello = clientHelloBuf.Bytes()

	// Capture the ServerHello message directly from the connection
	err = conn.Handshake()
	if err != nil {
		return nil, err
	}
	collectedData.RawServerHello = captureServerHello(conn)

	// Collect JARM fingerprint
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

	// Collect SSH data
	if c.SSHDiscovery {
		err = dc.CollectSSH(collectedData, host, port)
		if err != nil {
			return collectedData, err
		}
	}

	return collectedData, nil
}

func captureServerHello(conn *tls.Conn) []byte {
	var serverHelloBuf bytes.Buffer
	serverHelloCapture := io.TeeReader(conn, &serverHelloBuf)
	_, err := io.Copy(io.Discard, serverHelloCapture)
	if err != nil && err != io.EOF {
		return nil
	}
	return serverHelloBuf.Bytes()
}

func (dc DataCollector) CollectSSH(collectedData *CollectedData, host string, port string) error {
	// Buffers to capture the SSH handshake
	var clientHelloBuf, serverHelloBuf bytes.Buffer

	// Dial the SSH server
	conn, err := dc.dial(host, port)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Create SSH client config
	clientConfig := &ssh.ClientConfig{
		User:            "user",
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
	defer sshConn.Close()

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
		channel.Close()
	}
}
