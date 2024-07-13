package common

import (
	"testing"
)

const (
	helloWorld = "Hello, World!"
)

func TestBase64Encode(t *testing.T) {
	data := helloWorld
	expected := "SGVsbG8sIFdvcmxkIQ=="
	result := Base64Encode(data)
	if result != expected {
		t.Errorf("Base64Encode(%s) = %s, expected %s", data, result, expected)
	}
}

func TestCalculateEntropy(t *testing.T) {
	data := helloWorld
	expected := 3.1808329877552226
	result := CalculateEntropy(data)
	expectedCheck := float32(expected)
	resultCheck := float32(result)
	if resultCheck != expectedCheck {
		t.Errorf("CalculateEntropy(%s) = %f, expected %f", data, resultCheck, expectedCheck)
	}
}

func TestBase64Decode(t *testing.T) {
	data := "SGVsbG8sIFdvcmxkIQ=="
	expected := helloWorld
	result, err := Base64Decode(data)
	if err != nil {
		t.Errorf("Base64Decode(%s) returned an error: %v", data, err)
	}
	if result != expected {
		t.Errorf("Base64Decode(%s) = %s, expected %s", data, result, expected)
	}
}
