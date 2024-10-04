// Package common package is used to store common functions and variables
package common

import (
	"reflect"
	"testing"
	"time"
)

func TestFlexibleDateUnmarshalJSON(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected FlexibleDate
		wantErr  bool
	}{
		{
			name:     "Valid date format 1",
			input:    "\"2023-01-01\"",
			expected: FlexibleDate(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)),
			wantErr:  false,
		},
		{
			name:     "Valid date format 2",
			input:    "\"2023-01-01T12:34:56Z\"",
			expected: FlexibleDate(time.Date(2023, 1, 1, 12, 34, 56, 0, time.UTC)),
			wantErr:  false,
		},
		{
			name:     "Invalid date format",
			input:    "\"2023-01-01T12:34:56\"",
			expected: FlexibleDate(time.Time{}),
			wantErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var fd FlexibleDate
			err := fd.UnmarshalJSON([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if !reflect.DeepEqual(fd, tc.expected) {
				t.Errorf("UnmarshalJSON() got = %v, want %v", fd, tc.expected)
			}
		})
	}
}

func TestFlexibleDateMarshalJSON(t *testing.T) {
	testCases := []struct {
		name     string
		input    FlexibleDate
		expected string
		wantErr  bool
	}{
		{
			name:     "Valid date",
			input:    FlexibleDate(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)),
			expected: "\"2023-01-01\"",
			wantErr:  false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.input.MarshalJSON()
			if (err != nil) != tc.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if string(b) != tc.expected {
				t.Errorf("MarshalJSON() got = %s, want %s", string(b), tc.expected)
			}
		})
	}
}

func TestFlexibleDateString(t *testing.T) {
	testCases := []struct {
		name     string
		input    FlexibleDate
		expected string
	}{
		{
			name:     "Valid date",
			input:    FlexibleDate(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)),
			expected: "2023-01-01",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fd := tc.input
			result := fd.String()
			if result != tc.expected {
				t.Errorf("String() got = %s, want %s", result, tc.expected)
			}
		})
	}
}

func TestFlexibleDateTime(t *testing.T) {
	testCases := []struct {
		name     string
		input    FlexibleDate
		expected time.Time
	}{
		{
			name:     "Valid date",
			input:    FlexibleDate(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)),
			expected: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fd := tc.input
			result := fd.Time()
			if !result.Equal(tc.expected) {
				t.Errorf("Time() got = %v, want %v", result, tc.expected)
			}
		})
	}
}

func TestIsJSON(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Valid JSON",
			input:    `{"name": "John", "age": 30, "city": "New York"}`,
			expected: true,
		},
		{
			name:     "Invalid JSON",
			input:    `{"name": "John", "age": 30, "city": "New York"`,
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsJSON(tc.input)
			if result != tc.expected {
				t.Errorf("IsJSON() got = %v, want %v", result, tc.expected)
			}
		})
	}
}

func TestJSONStrToMap(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected map[string]interface{}
		wantErr  bool
	}{
		{
			name:  "Valid JSON",
			input: `{"name": "John", "age": 30, "city": "New York"}`,
			expected: map[string]interface{}{
				"name": "John",
				"age":  float64(30),
				"city": "New York",
			},
			wantErr: false,
		},
		{
			name:     "Invalid JSON",
			input:    `{"name": "John", "age": 30, "city": "New York"`,
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: nil,
			wantErr:  true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := JSONStrToMap(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("JSONStrToMap() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("JSONStrToMap() got = %v, want %v", result, tc.expected)
			}
		})
	}
}
