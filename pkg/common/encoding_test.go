package common

import (
	"testing"
)

func TestBase64Encode(t *testing.T) {
	data := "Hello, World!"
	expected := "SGVsbG8sIFdvcmxkIQ=="
	result := Base64Encode(data)
	if result != expected {
		t.Errorf("Base64Encode(%s) = %s, expected %s", data, result, expected)
	}
}

func TestCalculateEntropy(t *testing.T) {
	data := "Hello, World!"
	expected := 3.1808329877552226
	result := CalculateEntropy(data)
	expectedCheck := float32(expected)
	resultCheck := float32(result)
	if resultCheck != expectedCheck {
		t.Errorf("CalculateEntropy(%s) = %f, expected %f", data, resultCheck, expectedCheck)
	}
}
