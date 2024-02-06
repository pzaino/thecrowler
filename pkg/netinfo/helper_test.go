package netinfo

import (
	"reflect"
	"testing"
)

func TestURLToHost(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{
			url:      "http://www.example.com",
			expected: "www.example.com",
		},
		{
			url:      "https://www.example-x.com",
			expected: "www.example-x.com",
		},
		{
			url:      "https://www.example-y.com/path",
			expected: "www.example-y.com",
		},
		{
			url:      "https://www.example-z.com/",
			expected: "www.example-z.com",
		},
		{
			url:      "https://www.example-h.com/path/",
			expected: "www.example-h.com",
		},
		{
			url:      "https://www.example-n.com/path/file.html",
			expected: "www.example-n.com",
		},
	}

	for _, test := range tests {
		result := urlToHost(test.url)
		if result != test.expected {
			t.Errorf("urlToHost(%s) = %s; want %s", test.url, result, test.expected)
		}
	}
}

func TestURLToDomain(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{
			url:      "http://www.example1.com",
			expected: "example1.com",
		},
		{
			url:      "https://www.example2.com",
			expected: "example2.com",
		},
		{
			url:      "https://www.example3.com/path",
			expected: "example3.com",
		},
		{
			url:      "https://www.example4.com/",
			expected: "example4.com",
		},
		{
			url:      "https://www.example5.com/path/",
			expected: "example5.com",
		},
		{
			url:      "https://www.example6.com/path/file.html",
			expected: "example6.com",
		},
		{
			url:      "https://www.example7.co.uk",
			expected: "example7.co.uk",
		},
		{
			url:      "https://www.example8.co.uk/path",
			expected: "example8.co.uk",
		},
		{
			url:      "https://www.example9.co.uk/",
			expected: "example9.co.uk",
		},
		{
			url:      "https://www.example10.co.uk/path/",
			expected: "example10.co.uk",
		},
		{
			url:      "https://www.example11.co.uk/path/file.html",
			expected: "example11.co.uk",
		},
	}

	for _, test := range tests {
		result := urlToDomain(test.url)
		if result != test.expected {
			t.Errorf("urlToDomain(%s) = %s; want %s", test.url, result, test.expected)
		}
	}
}

func TestDefaultNA(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "",
			expected: "N/A",
		},
		{
			input:    "Hello",
			expected: "Hello",
		},
		{
			input:    "123",
			expected: "123",
		},
		{
			input:    " ",
			expected: " ",
		},
		{
			input:    "World",
			expected: "World",
		},
	}

	for _, test := range tests {
		result := defaultNA(test.input)
		if result != test.expected {
			t.Errorf("defaultNA(%s) = %s; want %s", test.input, result, test.expected)
		}
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{
			input:    "123",
			expected: true,
		},
		{
			input:    "abc",
			expected: false,
		},
		{
			input:    "1.23",
			expected: false,
		},
		{
			input:    "",
			expected: false,
		},
	}

	for _, test := range tests {
		result := isNumeric(test.input)
		if result != test.expected {
			t.Errorf("isNumeric(%s) = %t; want %t", test.input, result, test.expected)
		}
	}
}

func TestFieldsQuotes(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			input:    `field1 "field2 with 1" field3`,
			expected: []string{"field1", "field2 with 1", "field3"},
		},
		{
			input:    `"field1" field2 "field3"`,
			expected: []string{"field1", "field2", "field3"},
		},
		{
			input:    `field1 "field2"`,
			expected: []string{"field1", "field2"},
		},
		{
			input:    `"field1"`,
			expected: []string{"field1"},
		},
		{
			input:    `field1`,
			expected: []string{"field1"},
		},
		{
			input:    `""`,
			expected: []string{},
		},
		{
			input:    ` `,
			expected: []string{},
		},
		{
			input:    `field1 "field2" field3 "field4"`,
			expected: []string{"field1", "field2", "field3", "field4"},
		},
	}

	for _, test := range tests {
		result := fieldsQuotes(test.input)
		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("fieldsQuotes(%s) = %v; want %v", test.input, result, test.expected)
		}
	}
}
