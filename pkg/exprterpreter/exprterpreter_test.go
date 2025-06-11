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

// Package exprterpreter contains the expression interpreter logic.
package exprterpreter

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

var ParserTests = []struct {
	name          string
	command       string
	depth         int
	expectedToken int
	expectedArgs  []EncodedCmd
	expectedError string
}{
	{
		name:          "Valid command",
		command:       "random(param1, param2)",
		depth:         0,
		expectedToken: 1,
		expectedArgs: []EncodedCmd{
			{Token: -1, Args: nil, ArgValue: "param1"},
			{Token: -1, Args: nil, ArgValue: "param2"},
		},
		expectedError: "",
	},
	{
		name:          "Valid command 2",
		command:       "random(1, 10)",
		depth:         0,
		expectedToken: 1,
		expectedArgs: []EncodedCmd{
			{Token: -1, Args: nil, ArgValue: "1"},
			{Token: -1, Args: nil, ArgValue: "10"},
		},
		expectedError: "",
	},
	{
		name:          "Nested command",
		command:       "random(1, random(2, 3))",
		depth:         0,
		expectedToken: 1,
		expectedArgs: []EncodedCmd{
			{Token: -1, Args: nil, ArgValue: "1"},
			{Token: 1, Args: []EncodedCmd{
				{Token: -1, Args: nil, ArgValue: "2"},
				{Token: -1, Args: nil, ArgValue: "3"},
			}, ArgValue: "random(2, 3)"},
		},
		expectedError: "",
	},
	{
		name:          "Plain number",
		command:       "42",
		depth:         0,
		expectedToken: -1,
		expectedArgs:  nil,
		expectedError: "",
	},
	{
		name:          "Command with string parameter",
		command:       `random(1, "this is a test parameter")`,
		depth:         0,
		expectedToken: 1,
		expectedArgs: []EncodedCmd{
			{Token: -1, Args: nil, ArgValue: "1"},
			{Token: -1, Args: nil, ArgValue: `"this is a test parameter"`},
		},
		expectedError: "",
	},
}

var InterpreterTests = []struct {
	name           string
	encodedCmd     EncodedCmd
	expectedResult string
	expectedError  string
}{
	{
		name: "Non-command parameter",
		encodedCmd: EncodedCmd{
			Token:    -1,
			Args:     nil,
			ArgValue: "param1",
		},
		expectedResult: "param1",
		expectedError:  "",
	},
	{
		name: "Token representing the 'random' command",
		encodedCmd: EncodedCmd{
			Token: TokenRandom,
			Args: []EncodedCmd{
				{Token: -1, Args: nil, ArgValue: "1"},
				{Token: -1, Args: nil, ArgValue: "10"},
			},
			ArgValue: "random(1, 10)",
		},
		expectedResult: "",
		expectedError:  "",
	},
	{
		name: "Unknown command token",
		encodedCmd: EncodedCmd{
			Token:    999,
			Args:     nil,
			ArgValue: "invalid command",
		},
		expectedResult: "",
		expectedError:  "unknown command token: 999",
	},
}

func TestParseCmd(t *testing.T) {
	tests := ParserTests

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotErr := ParseCmd(tt.command, tt.depth)

			assertToken(t, gotCmd.Token, tt.expectedToken)
			assertArgs(t, gotCmd.Args, tt.expectedArgs)
			assertError(t, gotErr, tt.expectedError)
		})
	}
}

func assertToken(t *testing.T, gotToken, expectedToken int) {
	if gotToken != expectedToken {
		t.Errorf("InterpretCommand() token = %v, want %v", gotToken, expectedToken)
	}
}

func assertArgs(t *testing.T, gotArgs, expectedArgs []EncodedCmd) {
	if !reflect.DeepEqual(gotArgs, expectedArgs) {
		t.Errorf("InterpretCommand() args = %v, want %v", gotArgs, expectedArgs)
	}
}

func assertError(t *testing.T, gotErr error, expectedError string) {
	if gotErr != nil {
		if expectedError == "" || !strings.Contains(gotErr.Error(), expectedError) {
			t.Errorf("InterpretCommand() error = %v, want %v", gotErr, expectedError)
		}
	} else if expectedError != "" {
		t.Errorf("InterpretCommand() expected error but got nil")
	}
}

func TestInterpretCmd(t *testing.T) {
	tests := InterpreterTests

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResult, gotErr := InterpretCmd(tt.encodedCmd)

			if tt.name == "Token representing the 'random' command" {
				testRandomCommand(t, gotResult, gotErr)
			} else {
				testNonRandomCommand(t, gotResult, gotErr, tt.expectedResult, tt.expectedError)
			}
		})
	}
}

func TestHandleTimeCommand(t *testing.T) {
	tests := []struct {
		name          string
		args          []EncodedCmd
		expectErr     bool
		expectContent string // substring expected in result (format check)
	}{
		{
			name:      "No arguments",
			args:      []EncodedCmd{},
			expectErr: true,
		},
		{
			name:          "Unix time",
			args:          []EncodedCmd{{Token: -1, ArgValue: "unix"}},
			expectErr:     false,
			expectContent: "",
		},
		{
			name:          "UnixNano time",
			args:          []EncodedCmd{{Token: -1, ArgValue: "unixnano"}},
			expectErr:     false,
			expectContent: "",
		},
		{
			name:          "RFC3339 time",
			args:          []EncodedCmd{{Token: -1, ArgValue: "rfc3339"}},
			expectErr:     false,
			expectContent: "T", // RFC3339 always contains 'T'
		},
		{
			name:          "Now time",
			args:          []EncodedCmd{{Token: -1, ArgValue: "now"}},
			expectErr:     false,
			expectContent: "",
		},
		{
			name:      "Invalid format",
			args:      []EncodedCmd{{Token: -1, ArgValue: "invalid-format"}},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handleTimeCommand(tt.args)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got nil, result: %v", result)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if tt.expectContent != "" && !strings.Contains(result, tt.expectContent) {
					t.Errorf("Expected result to contain %q, got %q", tt.expectContent, result)
				}
				// Additional checks for unix/unixnano
				if len(tt.args) > 0 && tt.args[0].ArgValue == "unix" {
					if _, convErr := strconv.ParseInt(result, 10, 64); convErr != nil {
						t.Errorf("Expected unix timestamp as integer, got %q", result)
					}
				}
				if len(tt.args) > 0 && tt.args[0].ArgValue == "unixnano" {
					if _, convErr := strconv.ParseInt(result, 10, 64); convErr != nil {
						t.Errorf("Expected unixnano timestamp as integer, got %q", result)
					}
				}
			}
		})
	}
}

func TestIsNumber(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"123", true},
		{"-123", true},
		{"0", true},
		{"3.1415", true},
		{"-0.001", true},
		{"1e10", true},
		{"-2.5e-3", true},
		{"", false},
		{"abc", false},
		{"123abc", false},
		{"NaN", true}, // strconv.ParseFloat accepts "NaN"
		{"Inf", true}, // strconv.ParseFloat accepts "Inf"
		{"-Inf", true},
		{" ", false},
		{"1,000", false},
		{"0x10", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := IsNumber(tt.input)
			if result != tt.expected {
				t.Errorf("IsNumber(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		// If expectedErr is true, we expect the fallback value (1)
		expectedErr bool
		// If expectedRange is set, result must be in [min, max]
		expectedRange [2]float64
	}{
		{
			name:     "Simple integer string",
			input:    "42",
			expected: 42,
		},
		{
			name:     "Simple float string",
			input:    "3.14",
			expected: 3.14,
		},
		{
			name:     "Negative float",
			input:    "-2.5",
			expected: -2.5,
		},
		{
			name:     "Scientific notation",
			input:    "1e2",
			expected: 100,
		},
		{
			name:        "Invalid number string",
			input:       "notanumber",
			expected:    1,
			expectedErr: true,
		},
		{
			name:        "Empty string",
			input:       "",
			expected:    1,
			expectedErr: true,
		},
		{
			name:          "Random command",
			input:         "random(1, 3)",
			expectedRange: [2]float64{1, 3},
		},
		{
			name:          "Nested random command",
			input:         "random(1, random(2, 4))",
			expectedRange: [2]float64{1, 4},
		},
		{
			name:          "Time unix command",
			input:         "time(unix)",
			expectedRange: [2]float64{1e9, 1e11}, // unix timestamp, rough range
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetFloat(tt.input)
			if tt.expectedRange != [2]float64{0, 0} {
				if result < tt.expectedRange[0] || result > tt.expectedRange[1] {
					t.Errorf("GetFloat(%q) = %v, want in range [%v, %v]", tt.input, result, tt.expectedRange[0], tt.expectedRange[1])
				}
			} else if tt.expectedErr {
				if result != tt.expected {
					t.Errorf("GetFloat(%q) = %v, want fallback %v", tt.input, result, tt.expected)
				}
			} else {
				if (result-tt.expected) > 1e-9 || (tt.expected-result) > 1e-9 {
					t.Errorf("GetFloat(%q) = %v, want %v", tt.input, result, tt.expected)
				}
			}
		})
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    int
		expectedMin int // for random/time, min value
		expectedMax int // for random/time, max value
		expectRange bool
	}{
		{
			name:     "Simple integer string",
			input:    "42",
			expected: 42,
		},
		{
			name:     "Negative integer string",
			input:    "-7",
			expected: -7,
		},
		{
			name:     "Float string (should truncate)",
			input:    "3.99",
			expected: 3,
		},
		{
			name:     "Invalid number string",
			input:    "notanumber",
			expected: 1,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: 1,
		},
		{
			name:        "Random command",
			input:       "random(1, 5)",
			expectedMin: 1,
			expectedMax: 5,
			expectRange: true,
		},
		{
			name:        "Nested random command",
			input:       "random(2, random(3, 6))",
			expectedMin: 2,
			expectedMax: 6,
			expectRange: true,
		},
		{
			name:        "Time unix command",
			input:       "time(unix)",
			expectedMin: 1e9,
			expectedMax: 1e11,
			expectRange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetInt(tt.input)
			if tt.expectRange {
				if result < tt.expectedMin || result > tt.expectedMax {
					t.Errorf("GetInt(%q) = %v, want in range [%v, %v]", tt.input, result, tt.expectedMin, tt.expectedMax)
				}
			} else {
				if result != tt.expected {
					t.Errorf("GetInt(%q) = %v, want %v", tt.input, result, tt.expected)
				}
			}
		})
	}
}

func testRandomCommand(t *testing.T, gotResult string, gotErr error) {
	if gotErr != nil {
		t.Errorf("ProcessEncodedCmd() returned an unexpected error: %v", gotErr)
	} else {
		resultInt, err := strconv.Atoi(gotResult)
		if err != nil {
			t.Errorf("ProcessEncodedCmd() returned a non-integer result for 'random' command: %v", gotResult)
		} else if resultInt < 1 || resultInt > 10 {
			t.Errorf("ProcessEncodedCmd() result = %v, want it to be within [1, 10]", resultInt)
		}
	}
}

func testNonRandomCommand(t *testing.T, gotResult string, gotErr error, expectedResult string, expectedError string) {
	if gotResult != expectedResult {
		t.Errorf("ProcessEncodedCmd() result = %v, want %v", gotResult, expectedResult)
	}

	if gotErr != nil {
		if expectedError == "" || !strings.Contains(gotErr.Error(), expectedError) {
			t.Errorf("ProcessEncodedCmd() error = %v, want %v", gotErr, expectedError)
		}
	} else if expectedError != "" {
		t.Errorf("ProcessEncodedCmd() expected error but got nil")
	}
}
