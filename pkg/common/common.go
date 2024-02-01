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
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
)

// SetDebugLevel allows to set the current debug level
func SetDebugLevel(dbgLvl DbgLevel) {
	debugLevel = dbgLvl
}

// GetDebugLevel returns the value of the current debug level
func GetDebugLevel() DbgLevel {
	return debugLevel
}

// DebugMsg is a function that prints debug information
func DebugMsg(dbgLvl DbgLevel, msg string, args ...interface{}) {
	if dbgLvl != DbgLvlFatal {
		if GetDebugLevel() >= dbgLvl {
			log.Printf(msg, args...)
		}
	} else {
		log.Fatalf(msg, args...)
	}
}

/////////
// Micro-interpreter for complex parameters
////////

// ParseCmd interprets the string command and returns the EncodedCmd.
func ParseCmd(command string, depth int) (EncodedCmd, error) {
	if depth > maxInterpreterRecursionDepth {
		return EncodedCmd{}, fmt.Errorf("exceeded maximum recursion depth")
	}

	command = strings.TrimSpace(command)
	token, _, isACommand := getCommandToken(command)
	if isACommand && strings.Contains(command, "(") && strings.HasSuffix(command, ")") {
		paramString := command[strings.Index(command, "(")+1 : len(command)-1]
		params, err := parseParams(paramString)
		if err != nil {
			return EncodedCmd{}, err
		}

		var encodedArgs []EncodedCmd
		for _, param := range params {
			trimmedParam := strings.TrimSpace(param)
			if isCommand(trimmedParam) {
				nestedCmd, err := ParseCmd(trimmedParam, depth+1)
				if err != nil {
					return EncodedCmd{}, err
				}
				// Set ArgValue to the full command string for nested commands
				nestedCmd.ArgValue = trimmedParam
				encodedArgs = append(encodedArgs, nestedCmd)
			} else {
				encodedArgs = append(encodedArgs, EncodedCmd{
					Token:    -1, // For parameters
					Args:     nil,
					ArgValue: trimmedParam,
				})
			}
		}

		return EncodedCmd{
			Token:    token,
			Args:     encodedArgs,
			ArgValue: "", // Command itself doesn't directly have an ArgValue
		}, nil
	}

	// Handle plain text or numbers not forming a recognized command
	return EncodedCmd{
		Token:    -1,
		Args:     nil,
		ArgValue: command,
	}, nil
}

// isCommand checks if the given string is a valid command using getCommandToken.
func isCommand(s string) bool {
	_, _, exists := getCommandToken(s)
	return exists
}

// getCommandToken returns the token for a given command.
func getCommandToken(command string) (int, string, bool) {
	commandName := strings.SplitN(command, "(", 2)[0]
	token, exists := commandTokenMap[commandName]
	return token, commandName, exists
}

// parseParams parses the parameter string and returns a slice of parameters.
func parseParams(paramString string) ([]string, error) {
	var params []string
	var currentParam strings.Builder
	inQuotes := false
	parenthesisLevel := 0

	for _, char := range paramString {
		inQuotes = handleQuotes(char, inQuotes)
		parenthesisLevel = handleParentheses(char, inQuotes, parenthesisLevel)

		if char == ',' && !inQuotes && parenthesisLevel == 0 {
			params = append(params, strings.TrimSpace(currentParam.String()))
			currentParam.Reset()
		} else {
			currentParam.WriteRune(char)
		}
	}

	if inQuotes || parenthesisLevel != 0 {
		return nil, fmt.Errorf("unmatched quotes or parentheses in parameters")
	}

	params = append(params, strings.TrimSpace(currentParam.String()))
	return params, nil
}

func handleQuotes(char rune, inQuotes bool) bool {
	if char == '"' {
		return !inQuotes
	}
	return inQuotes
}

func handleParentheses(char rune, inQuotes bool, parenthesisLevel int) int {
	if char == '(' && !inQuotes {
		return parenthesisLevel + 1
	} else if char == ')' && !inQuotes {
		if parenthesisLevel > 0 {
			return parenthesisLevel - 1
		}
	}
	return parenthesisLevel
}

// InterpretCmd processes an EncodedCmd recursively and returns the calculated value as a string.
func InterpretCmd(encodedCmd EncodedCmd) (string, error) {
	switch encodedCmd.Token {
	case -1: // Non-command parameter
		return encodedCmd.ArgValue, nil
	case TokenRandom: // Token representing the 'random' command
		return handleRandomCommand(encodedCmd.Args)
	default:
		return "", fmt.Errorf("unknown command token: %d", encodedCmd.Token)
	}
}

// handleRandomCommand processes the 'random' command given its arguments.
func handleRandomCommand(args []EncodedCmd) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("random command expects 2 arguments, got %d", len(args))
	}

	// Process arguments recursively
	minArg, err := InterpretCmd(args[0])
	if err != nil {
		return "", err
	}
	maxArg, err := InterpretCmd(args[1])
	if err != nil {
		return "", err
	}

	// Convert arguments to integers
	min, err := strconv.Atoi(minArg)
	if err != nil {
		return "", fmt.Errorf("invalid min argument for random: %s", minArg)
	}
	max, err := strconv.Atoi(maxArg)
	if err != nil {
		return "", fmt.Errorf("invalid max argument for random: %s", maxArg)
	}

	// Ensure min is less than max
	if min >= max {
		return "", fmt.Errorf("min argument must be less than max argument for random")
	}

	// Generate and return random value using crypto/rand for better randomness
	// Compute the range (max - min + 1)
	rangeInt := big.NewInt(int64(max - min + 1))
	// Generate a random number in [0, rangeInt)
	n, err := rand.Int(rand.Reader, rangeInt)
	if err != nil {
		return "", err
	}

	// Shift the number to [min, max]
	result := int(n.Int64()) + min
	return strconv.Itoa(result), nil
}
