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
	"fmt"
	"hash/fnv"
	"math"
)

// MinHash implements the Fingerprint interface for MinHash fingerprints.
type MinHash struct {
	numHash int
	hashes  []uint64
}

// NewMinHash creates a new MinHash fingerprint with the given number of hashes.
func NewMinHash(numHash int) *MinHash {
	hashes := make([]uint64, numHash)
	for i := range hashes {
		hashes[i] = math.MaxUint64
	}
	return &MinHash{
		numHash: numHash,
		hashes:  hashes,
	}
}

// hashFunction computes the hash of the given data with the given seed.
func hashFunction(data []byte, seed uint64) uint64 {
	h := fnv.New64a()
	_, err := h.Write(data)
	if err != nil {
		_, err = h.Write([]byte{byte(seed)})
		if err != nil {
			return 0
		}
	}
	return h.Sum64()
}

// Push pushes the given data into the MinHash fingerprint.
func (mh *MinHash) Push(data []byte) {
	//nolint:gosec // Disabling G115: We are using the hash function to generate a fingerprint
	for i := uint64(0); i < uint64(mh.numHash); i++ {
		hashValue := hashFunction(data, i)
		if hashValue < mh.hashes[i] {
			mh.hashes[i] = hashValue
		}
	}
}

// Signature returns the MinHash fingerprint signature.
func (mh *MinHash) Signature() []uint64 {
	return mh.hashes
}

// Compute computes the MinHash fingerprint of a given data.
func (mh MinHash) Compute(data string) string {
	mh = *NewMinHash(200)
	mh.Push([]byte(data))
	return fmt.Sprintf("%x", mh.Signature())
}
