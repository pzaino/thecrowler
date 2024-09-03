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
	"encoding/binary"
	"fmt"
)

// Constants used in CityHash
const (
	k0 = uint64(0xc3a5c85c97cb3127)
	k1 = uint64(0xb492b66fbe98f273)
	k2 = uint64(0x9ae16a3b2f90404f)
	k3 = uint64(0xc949d7c7509e6557)
)

// CityHash implements the Fingerprint interface for CityHash fingerprints.
type CityHash struct{}

// Compute computes the CityHash64 of the given data
func (c CityHash) Compute(data string) string {
	return fmt.Sprintf("%x", CityHash64([]byte(data)))
}

// CityHash64 computes the CityHash64 of the given data
func CityHash64(data []byte) uint64 {
	if len(data) <= 16 {
		return hashLen0to16(data)
	} else if len(data) <= 32 {
		return hashLen17to32(data)
	} else if len(data) <= 64 {
		return hashLen33to64(data)
	}

	// For strings over 64 bytes we hash the end first, and then as we
	// loop we keep 56 bytes of state: v, w, x, y, and z.
	x := binary.LittleEndian.Uint64(data[len(data)-40 : len(data)-32])
	y := binary.LittleEndian.Uint64(data[len(data)-16:len(data)-8]) + k1
	z := binary.LittleEndian.Uint64(data[len(data)-56:len(data)-48]) + uint64(len(data))
	v := weakHashLen32WithSeeds(data[len(data)-64:len(data)-32], uint64(len(data)), y)
	w := weakHashLen32WithSeeds(data[len(data)-32:], z+k1, x)
	x = x*k1 + binary.LittleEndian.Uint64(data)

	offset := 0
	for len(data)-offset > 64 {
		x = rotateRight(x+y+v[0]+binary.LittleEndian.Uint64(data[offset+8:offset+16]), 37) * k1
		y = rotateRight(y+v[1]+binary.LittleEndian.Uint64(data[offset+48:offset+56]), 42) * k1
		x, y = x^w[1], y^v[0]
		z = rotateRight(z+w[0], 33) * k1
		v = weakHashLen32WithSeeds(data[offset:offset+32], v[1]*k1, x+w[0])
		w = weakHashLen32WithSeeds(data[offset+32:offset+64], z, y+binary.LittleEndian.Uint64(data[offset+48:offset+56]))
		offset += 64
	}
	return hashLen16(hashLen16(v[0], w[0])+shiftMix(y)*k0+z, hashLen16(v[1], w[1])+x)
}

func rotateRight(val uint64, shift uint) uint64 {
	return (val >> shift) | (val << (64 - shift))
}

func hashLen16(u, v uint64) uint64 {
	const (
		keyMul = uint64(0x9ddfea08eb382d69)
	)
	a := (u ^ v) * keyMul
	a ^= (a >> 47)
	b := (v ^ a) * keyMul
	b ^= (b >> 47)
	b *= keyMul
	return b
}

func shiftMix(val uint64) uint64 {
	return val ^ (val >> 47)
}

func weakHashLen32WithSeeds(data []byte, seedA, seedB uint64) [2]uint64 {
	a := binary.LittleEndian.Uint64(data[0:8])
	b := binary.LittleEndian.Uint64(data[8:16])
	c := binary.LittleEndian.Uint64(data[16:24])
	d := binary.LittleEndian.Uint64(data[24:32])

	a += seedA
	b = rotateRight(b+seedB+a, 21)
	c += a
	a += d
	d = rotateRight(d, 44)

	return [2]uint64{a + b + c, b + d}
}

func hashLen0to16(data []byte) uint64 {
	if len(data) > 8 {
		a := binary.LittleEndian.Uint64(data)
		b := binary.LittleEndian.Uint64(data[len(data)-8:])
		return hashLen16(a, rotateRight(b+uint64(len(data)), 53)^a) ^ b
	}
	if len(data) >= 4 {
		a := uint64(binary.LittleEndian.Uint32(data))
		return hashLen16(uint64(len(data))+(a<<3), uint64(binary.LittleEndian.Uint32(data[len(data)-4:])))
	}
	if len(data) > 0 {
		a := uint64(data[0])
		b := uint64(data[len(data)>>1])
		c := uint64(data[len(data)-1])
		y := a + (b << 8)
		z := uint64(len(data)) + (c << 2)
		return shiftMix(y*k2^z*k0) * k2
	}
	return k2
}

func hashLen17to32(data []byte) uint64 {
	a := binary.LittleEndian.Uint64(data) * k1
	b := binary.LittleEndian.Uint64(data[8:])
	c := binary.LittleEndian.Uint64(data[len(data)-8:]) * k2
	d := binary.LittleEndian.Uint64(data[len(data)-16:]) * k0
	return hashLen16(rotateRight(a-b, 43)+rotateRight(c, 30)+d, a+rotateRight(b^k3, 20)-c+uint64(len(data)))
}

func hashLen33to64(data []byte) uint64 {
	z := binary.LittleEndian.Uint64(data[24:])
	a := binary.LittleEndian.Uint64(data[0:8]) * k2
	b := binary.LittleEndian.Uint64(data[8:16])
	c := binary.LittleEndian.Uint64(data[len(data)-8:]) * k2
	d := binary.LittleEndian.Uint64(data[len(data)-16:]) * k2
	a = rotateRight(a+c, 43) + rotateRight(b, 30) + z
	b = shiftMix(b + a + d)
	return hashLen16(a, b)
}
