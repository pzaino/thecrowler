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

import "fmt"

// FingerprintType represents the type of fingerprint algorithm.
type FingerprintType int

const (
	// TypeJA3 represents the JA3 fingerprint type.
	TypeJA3 FingerprintType = iota
	// TypeJA3S represents the JA3S fingerprint type.
	TypeJA3S
	// TypeHASSH represents the HASSH fingerprint type.
	TypeHASSH
	// TypeHASSHServer represents the HASSHServer fingerprint type.
	TypeHASSHServer
	// TypeTLSH represents the TLSH fingerprint type.
	TypeTLSH
	// TypeSimHash represents the SimHash fingerprint type.
	TypeSimHash
	// TypeMinHash represents the MinHash fingerprint type.
	TypeMinHash
	// TypeBLAKE2 represents the BLAKE2 fingerprint type.
	TypeBLAKE2
	// TypeSHA256 represents the SHA256 fingerprint type.
	TypeSHA256
	// TypeCityHash represents the CityHash fingerprint type.
	TypeCityHash
	// TypeMurmurHash represents the MurmurHash fingerprint type.
	TypeMurmurHash
	// TypeCustomTLS represents the CustomTLS fingerprint type.
	TypeCustomTLS
	// TypeJARM represents the JARM fingerprint type.
	TypeJARM
)

// FingerprintFactory creates an instance of a Fingerprint implementation.
func FingerprintFactory(fType FingerprintType) (Fingerprint, error) {
	switch fType {
	case TypeJA3:
		return &JA3{}, nil
	case TypeJA3S:
		return &JA3S{}, nil
	case TypeHASSH:
		return &HASSH{}, nil
	case TypeHASSHServer:
		return &HASSHServer{}, nil
	case TypeTLSH:
		return &TLSH{}, nil
	case TypeSimHash:
		return &SimHash{}, nil
	case TypeMinHash:
		return &MinHash{}, nil
	case TypeBLAKE2:
		return &BLAKE2{}, nil
	case TypeSHA256:
		return &SHA256{}, nil
	case TypeCityHash:
		return &CityHash{}, nil
	case TypeMurmurHash:
		return &MurmurHash{}, nil
	case TypeCustomTLS:
		return &CustomTLS{}, nil
	case TypeJARM:
		return &JARM{}, nil
	default:
		return nil, fmt.Errorf("unknown fingerprint type")
	}
}
