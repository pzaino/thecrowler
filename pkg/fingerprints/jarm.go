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

// Package fingerprints implements the fingerprints library for the Crowler
package fingerprints

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// JARM implements the Fingerprint interface for JARM fingerprints.
type JARM struct{}

// Compute computes the JARM fingerprint of a given data.
func (j JARM) Compute(data string) string {
	// Assuming 'data' is a string containing multiple handshake details separated by commas.
	return jarmHash(data)
}

// jarmHash computes the JARM fingerprint of a given JARM string.
func jarmHash(jarmRaw string) string {
	if jarmRaw == "|||,|||,|||,|||,|||,|||,|||,|||,|||,|||" {
		return strings.Repeat("0", 62)
	}

	var fuzzyHash strings.Builder
	handshakes := strings.Split(jarmRaw, ",")
	var alpnsAndExt strings.Builder

	for _, handshake := range handshakes {
		components := strings.Split(handshake, "|")
		fuzzyHash.WriteString(cipherBytes(components[0]))
		fuzzyHash.WriteString(versionByte(components[1]))
		alpnsAndExt.WriteString(components[2])
		alpnsAndExt.WriteString(components[3])
	}

	sha256 := sha256Sum(alpnsAndExt.String())
	fuzzyHash.WriteString(sha256[:32])
	return fuzzyHash.String()
}

// cipherBytes returns the hex value of the cipher suite.
func cipherBytes(cipher string) string {
	if cipher == "" {
		return "00"
	}

	cipherList := [][]byte{
		{0x00, 0x04}, {0x00, 0x05}, {0x00, 0x07}, {0x00, 0x0a}, {0x00, 0x16},
		{0x00, 0x2f}, {0x00, 0x33}, {0x00, 0x35}, {0x00, 0x39}, {0x00, 0x3c},
		{0x00, 0x3d}, {0x00, 0x41}, {0x00, 0x45}, {0x00, 0x67}, {0x00, 0x6b},
		{0x00, 0x84}, {0x00, 0x88}, {0x00, 0x9a}, {0x00, 0x9c}, {0x00, 0x9d},
		{0x00, 0x9e}, {0x00, 0x9f}, {0x00, 0xba}, {0x00, 0xbe}, {0x00, 0xc0},
		{0x00, 0xc4}, {0xc0, 0x07}, {0xc0, 0x08}, {0xc0, 0x09}, {0xc0, 0x0a},
		{0xc0, 0x11}, {0xc0, 0x12}, {0xc0, 0x13}, {0xc0, 0x14}, {0xc0, 0x23},
		{0xc0, 0x24}, {0xc0, 0x27}, {0xc0, 0x28}, {0xc0, 0x2b}, {0xc0, 0x2c},
		{0xc0, 0x2f}, {0xc0, 0x30}, {0xc0, 0x60}, {0xc0, 0x61}, {0xc0, 0x72},
		{0xc0, 0x73}, {0xc0, 0x76}, {0xc0, 0x77}, {0xc0, 0x9c}, {0xc0, 0x9d},
		{0xc0, 0x9e}, {0xc0, 0x9f}, {0xc0, 0xa0}, {0xc0, 0xa1}, {0xc0, 0xa2},
		{0xc0, 0xa3}, {0xc0, 0xac}, {0xc0, 0xad}, {0xc0, 0xae}, {0xc0, 0xaf},
		{0xcc, 0x13}, {0xcc, 0x14}, {0xcc, 0xa8}, {0xcc, 0xa9}, {0x13, 0x01},
		{0x13, 0x02}, {0x13, 0x03}, {0x13, 0x04}, {0x13, 0x05},
	}

	count := 1
	for _, bytes := range cipherList {
		if cipher == hex.EncodeToString(bytes) {
			break
		}
		count++
	}

	hexValue := fmt.Sprintf("%02x", count)
	return hexValue
}

// versionByte returns the hex value of the TLS version.
func versionByte(version string) string {
	if version == "" {
		return "0"
	}

	options := "abcdef"
	count := int(version[3] - '0')
	return string(options[count])
}

// sha256Sum returns the SHA256 hash of the given data.
func sha256Sum(data string) string {
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
