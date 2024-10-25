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

// Package common package is used to store common functions and variables
package common

import (
	"strconv"
	"strings"
)

// PrepareSlice trims spaces from all elements of a slice.
// PrepareSlice prepares a slice of strings by trimming and lowercasing each element.
func PrepareSlice(slice *[]string, flags int) []string {
	prepared := make([]string, len(*slice)) // Pre-allocate slice to required size
	for i, s := range *slice {
		if flags&01 == 01 {
			s = strings.TrimSpace(s)
		}
		if flags&02 == 02 {
			s = strings.ToLower(s)
		}
		prepared[i] = s // Direct assignment to pre-allocated slice
	}
	return prepared
}

// SliceContains checks if a slice contains a specific item.
func SliceContains(slice []string, item string) bool {
	// After some benchmarking tests, this is the fastest way to check if a slice contains an item.
	// the performance resulted better than using "range" and pre-unrolled loops.
	for i := 0; i < len(slice); i++ {
		if slice[i] == item {
			return true
		}
	}
	return false
}

// IntSliceToString converts a slice of integers to a string.
func IntSliceToString(slice []int, joinStr string) string {
	// Convert the slice of integers to a slice of strings
	strSlice := make([]string, len(slice))
	for i, v := range slice {
		strSlice[i] = strconv.Itoa(v)
	}
	return strings.Join(strSlice, joinStr)
}

// Float64SliceToString converts a slice of float64 to a string.
func Float64SliceToString(slice []float64, joinStr string) string {
	// Convert the slice of float64 to a slice of strings
	strSlice := make([]string, len(slice))
	for i, v := range slice {
		strSlice[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strings.Join(strSlice, joinStr)
}

// Float32SliceToString converts a slice of float32 to a string.
func Float32SliceToString(slice []float32, joinStr string) string {
	// Convert the slice of float32 to a slice of strings
	strSlice := make([]string, len(slice))
	for i, v := range slice {
		strSlice[i] = strconv.FormatFloat(float64(v), 'f', -1, 32)
	}
	return strings.Join(strSlice, joinStr)
}

// BoolSliceToString converts a slice of bool to a string.
func BoolSliceToString(slice []bool, joinStr string) string {
	// Convert the slice of bool to a slice of strings
	strSlice := make([]string, len(slice))
	for i, v := range slice {
		strSlice[i] = strconv.FormatBool(v)
	}
	return strings.Join(strSlice, joinStr)
}
