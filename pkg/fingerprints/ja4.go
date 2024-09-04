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
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"
)

// JA4 implements the Fingerprint interface for JA4 fingerprints.
type JA4 struct {
	Version             uint16
	Ciphers             []uint16
	Extensions          []uint16
	SupportedGroups     []uint16
	SignatureAlgorithms []uint16
	SNI                 string
	ALPN                []string
}

// Generic compute function to compute the JA4 fingerprint of a given TLS data string.
func compute(data string) string {
	// Split data string to retrieve all fields
	fields := strings.Split(data, ",")

	// Join fields relevant to JA4, e.g., Version, Ciphers, Extensions, etc.
	// Example: "Version,Ciphers,Extensions,Groups,Signatures,SNI,ALPN"
	fingerprint := strings.Join(fields, ",")

	// Compute the MD5 hash of the joined fields
	hash := md5.Sum([]byte(fingerprint))
	return hex.EncodeToString(hash[:])
}

// Compute computes the JA4 fingerprint of a given TLS data string.
// The data string should be constructed from the TLS handshake fields relevant to JA4.
func (j JA4) Compute(data string) string {
	if data == "" {
		// generate data using j's fields
		data = strings.Join([]string{
			strconv.Itoa(int(j.Version)),
			strconv.Itoa(len(j.Ciphers)),
			strconv.Itoa(len(j.Extensions)),
			strconv.Itoa(len(j.SupportedGroups)),
			strconv.Itoa(len(j.SignatureAlgorithms)),
			j.SNI,
			strconv.Itoa(len(j.ALPN)),
		}, ",")
	}
	return compute(data)
}

// JA4S implements the Fingerprint interface for JA4S fingerprints.
type JA4S struct {
	Version             uint16
	Ciphers             []uint16
	Extensions          []uint16
	SupportedGroups     []uint16
	SignatureAlgorithms []uint16
	SNI                 string
	ALPN                []string
}

// Compute computes the JA4S fingerprint of a given TLS data string.
// The data string should be constructed from the TLS server handshake fields.
func (j JA4S) Compute(data string) string {
	if data == "" {
		// generate data using j's fields
		data = strings.Join([]string{
			strconv.Itoa(int(j.Version)),
			strconv.Itoa(len(j.Ciphers)),
			strconv.Itoa(len(j.Extensions)),
			strconv.Itoa(len(j.SupportedGroups)),
			strconv.Itoa(len(j.SignatureAlgorithms)),
			j.SNI,
			strconv.Itoa(len(j.ALPN)),
		}, ",")
	}
	return compute(data)
}
