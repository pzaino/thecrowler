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

// Micro-Interpreters for complex parameters

const maxInterpreterRecursionDepth = 100

// EncodedCmd is a struct containing the parsed command token and arguments.
type EncodedCmd struct {
	Token    int
	Args     []EncodedCmd
	ArgValue string // stores the argument value
}

const (
	// TokenRandom is the token for the random(x, y) command
	TokenRandom = 1 // Define a constant for each command's token
	// Add new tokens for additional commands here
)

// commandTokenMap maps command strings to their respective Token IDs.
var commandTokenMap = map[string]int{
	"random": TokenRandom,
	// Add new commands and their tokens here
}
