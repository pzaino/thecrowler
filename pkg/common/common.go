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

// common package is used to store common functions and variables
package common

import (
	"log"
)

// This function allow to set the current debug level
func SetDebugLevel(dbgLvl DbgLevel) {
	debugLevel = dbgLvl
}

// This function return the value of the current debug level
func GetDebugLevel() DbgLevel {
	return debugLevel
}

// DebugInfo is a function that prints debug information
func DebugMsg(dbgLvl DbgLevel, msg string, args ...interface{}) {
	if dbgLvl != DbgLvlFatal {
		if GetDebugLevel() >= dbgLvl {
			log.Printf(msg, args...)
		}
	} else {
		log.Fatalf(msg, args...)
	}
}
