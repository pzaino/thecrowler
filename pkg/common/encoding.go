package common

import (
	"encoding/base64"
	"math"
)

// Base64Encode encodes a string to base64, this may be required by some
// configurations.
func Base64Encode(data string) string {
	return base64.StdEncoding.EncodeToString([]byte(data))
}

// Base64Decode decodes a base64 string to a normal string.
func Base64Decode(data string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	return string(decoded), err
}

// CalculateEntropy of a string
func CalculateEntropy(data string) float64 {
	frequency := make(map[rune]int)
	for _, char := range data {
		frequency[char]++
	}

	var entropy float64
	length := float64(len(data))
	for _, count := range frequency {
		probability := float64(count) / length
		entropy -= probability * math.Log2(probability)
	}

	return entropy
}
