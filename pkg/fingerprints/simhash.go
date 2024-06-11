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
	"encoding/binary"
	"fmt"
	"strings"
)

// SimHash implements the Fingerprint interface for SimHash fingerprints.
type SimHash struct{}

func (s SimHash) Compute(data string) string {
	bits := make([]int, 64)
	words := strings.Fields(data)

	for _, word := range words {
		hash := md5.Sum([]byte(word))
		for i := 0; i < 64; i++ {
			bit := (binary.BigEndian.Uint64(hash[:]) >> i) & 1
			if bit == 1 {
				bits[i]++
			} else {
				bits[i]--
			}
		}
	}

	var fingerprint uint64
	for i := 0; i < 64; i++ {
		if bits[i] > 0 {
			fingerprint |= 1 << i
		}
	}

	return fmt.Sprintf("%x", fingerprint)
}
