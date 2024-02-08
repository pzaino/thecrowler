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
	"log"
	"os"
	"strconv"
)

// InitLogger initializes the logger
func InitLogger(appName string) {
	log.SetOutput(os.Stdout)

	// Retrieve process PID
	pid := os.Getpid()

	// Retrieve process PPID
	ppid := os.Getppid()

	// Retrieve default network address
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	// create process instance name: <hostname>:<pid>:<ppid>
	processName := hostname + ":" + strconv.Itoa(pid) + ":" + strconv.Itoa(ppid)

	// Setting the log prefix
	loggerPrefix = appName + " [" + processName + "]: "

	// Setting the log flags (date, time, microseconds, short file name)
	log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds)
}

// UpdateLoggerConfig Updates the logger configuration
func UpdateLoggerConfig() {
	if debugLevel > 0 {
		log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.LstdFlags | log.Ldate | log.Ltime | log.Lmicroseconds)
	}
}

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
	// For always-log messages (Info, Warning, Error, Fatal)
	if dbgLvl <= DbgLvlInfo {
		log.Printf(loggerPrefix+msg, args...)
		if dbgLvl == DbgLvlFatal {
			os.Exit(1)
		}
		return
	}
	if debugLevel >= dbgLvl {
		// For Debug messages, log only if the set debug level is equal or higher
		log.Printf(loggerPrefix+msg, args...)
	}
}
