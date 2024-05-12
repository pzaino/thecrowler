package common

import "strings"

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
