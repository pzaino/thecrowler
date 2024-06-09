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
)

// TLSH implements the Fingerprint interface for TLSH fingerprints.
type TLSH struct {
	buckets  [256]int
	total    int
	checksum [1]byte
}

// NewTLSH creates a new TLSH fingerprint.
func NewTLSH() *TLSH {
	return &TLSH{}
}

// Update updates the TLSH fingerprint with new data.
func (t *TLSH) Update(data []byte) {
	for _, b := range data {
		t.checksum[0] ^= b
		t.buckets[b]++
		t.total++
	}
}

// Finalize finalizes the TLSH fingerprint.
func (t *TLSH) Finalize() string {
	digest := sha256.New()
	for _, b := range t.buckets {
		digest.Write([]byte{byte(b)})
	}
	hash := digest.Sum(nil)
	return hex.EncodeToString(hash)
}

// Compute computes the TLSH fingerprint of a given data.
func (t TLSH) Compute(data string) string {
	tlsh := NewTLSH()
	tlsh.Update([]byte(data))
	return tlsh.Finalize()
}
