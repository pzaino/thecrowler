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

// This command is a simple automation server that listens for commands to move the mouse and click or perform keyboard actions.
package main

// Command represents a command to be executed by the rb
type Command struct {
	Action string `json:"action"`
	Value  string `json:"value"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
}
