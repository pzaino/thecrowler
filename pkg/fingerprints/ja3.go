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
	//nolint:gosec // Disabling G501: Md5 is required for backward compatibility, we do not use it for security purposes
	"crypto/md5"
	"encoding/hex"
)

// JA3 implements the Fingerprint interface for JA3 fingerprints.
type JA3 struct{}

// Compute computes the JA3 fingerprint of a given data.
func (j JA3) Compute(data string) string {
	//nolint:gosec // Disabling G401: Md5 is required for backward compatibility, we do not use it for security purposes
	hash := md5.Sum([]byte(data))
	return hex.EncodeToString(hash[:])
}

// JA3S implements the Fingerprint interface for JA3S fingerprints.
type JA3S struct{}

// Compute computes the JA3S fingerprint of a given data.
func (j JA3S) Compute(data string) string {
	//nolint:gosec // Disabling G401: Md5 is required for backward compatibility, we do not use it for security purposes
	hash := md5.Sum([]byte(data))
	return hex.EncodeToString(hash[:])
}
