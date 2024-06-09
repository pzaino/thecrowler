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
	"encoding/hex"

	"golang.org/x/crypto/blake2b"
)

// BLAKE2 implements the Fingerprint interface for BLAKE2 fingerprints.
type BLAKE2 struct{}

// Compute computes the BLAKE2 fingerprint of a given data.
func (b BLAKE2) Compute(data string) string {
	hash := blake2b.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}
