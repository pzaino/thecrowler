package common

import (
	"reflect"
	"testing"
)

func TestPrepareSlice(t *testing.T) {
	slice := []string{"  Hello ", " World ", "  Gopher  "}
	expected := []string{"hello", "world", "gopher"}

	result := PrepareSlice(&slice, 3)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("PrepareSlice() = %v, want %v", result, expected)
	}
}

func TestSliceContains(t *testing.T) {
	slice := []string{"apple", "banana", "cherry"}
	item := "banana"
	expected := true
	result := SliceContains(slice, item)
	if result != expected {
		t.Errorf("SliceContains() = %v, want %v", result, expected)
	}
}
