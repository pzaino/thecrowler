package common

import "testing"

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Lowercase",
			input:    "http://example.com/",
			expected: "http://example.com",
		},
		{
			name:     "TrimSpaces",
			input:    "  http://example.com/  ",
			expected: "http://example.com",
		},
		{
			name:     "TrimTrailingSlash",
			input:    "http://example.com/",
			expected: "http://example.com",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := NormalizeURL(test.input)
			if res != test.expected {
				t.Errorf("expected %q, got %q", test.expected, res)
			}
		})
	}
}
