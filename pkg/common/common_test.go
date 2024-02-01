package common

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

func TestInterpretCommand(t *testing.T) {
	tests := ParserTests

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotErr := InterpretCommand(tt.command, tt.depth)

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

func TestProcessEncodedCmd(t *testing.T) {
	tests := InterpreterTests

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotResult, gotErr := ProcessEncodedCmd(tt.encodedCmd)

			if tt.name == "Token representing the 'random' command" {
				testRandomCommand(t, gotResult, gotErr)
			} else {
				testNonRandomCommand(t, gotResult, gotErr, tt.expectedResult, tt.expectedError)
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
