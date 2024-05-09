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

// DbgLogType is an enum to represent the debug log type
type DbgLogType int

const (
	// DbgLogTypeStdout is the standard output
	DbgLogTypeStdout = 0
	// DbgLogTypeFile is the file output
	DbgLogTypeFile = 1
	// DbgLogTypeSyslog is the syslog output
	DbgLogTypeSyslog = 2
)

// LoggerCfg is the logger configuration
type LoggerCfg struct {
	// Type is the type of logger to use (stdout, file, syslog)
	Type DbgLogType
	// File is the file to write the logs to
	File string
	// Host is the syslog server to send the logs to
	Host string
	// Port is the syslog server port
	Port int
	// Tag is the syslog tag
	Tag string
	// Facility is the syslog facility
	Facility string
	// Priority is the syslog priority
	Priority string
}

// DbgLevel is an enum to represent the debug level type
type DbgLevel int

const (
	// DbgLvlDebug5 is the debug level
	DbgLvlDebug5 = 5
	// DbgLvlDebug4 is the debug level
	DbgLvlDebug4 = 4
	// DbgLvlDebug3 is the debug level
	DbgLvlDebug3 = 3
	// DbgLvlDebug2 is the debug level
	DbgLvlDebug2 = 2
	// DbgLvlDebug1 is the debug level
	DbgLvlDebug1 = 1
	// DbgLvlDebug is the debug level
	DbgLvlDebug = 1
	// DbgLvlInfo is the info debug level
	DbgLvlInfo = 0
	// DbgLvlWarn is the warning debug level
	DbgLvlWarn = -1
	// DbgLvlError is the error debug level
	DbgLvlError = -2
	// DbgLvlFatal is the fatal debug level (this will also exit the program!)
	DbgLvlFatal = -3
)

var (
	// DebugLevel is the debug level for logging
	debugLevel   DbgLevel
	loggerPrefix string
)

var (
	// UsrAgentStrMap is a list of valid user agent strings.
	UsrAgentStrMap = map[string]string{
		"chrome-desktop01":   "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.112 Safari/537.36",
		"chrome-mobile01":    "Mozilla/5.0 (Linux; Android 10; SM-G960F) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.112 Mobile Safari/537.36",
		"firefox-desktop01":  "Mozilla/5.0 (X11; Linux x86_64; rv:85.0) Gecko/20100101 Firefox/85.0",
		"firefox-mobile01":   "Mozilla/5.0 (Android 10; Mobile; rv:85.0) Gecko/20100101 Firefox/85.0",
		"chromium-desktop01": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.112 Safari/537.36",
		"chromium-mobile01":  "Mozilla/5.0 (Linux; Android 10; SM-G960F) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.6261.112 Mobile Safari/537.36",
	}
)

const (
	// DefaultFilePerms is the default file permissions
	DefaultFilePerms = 0644
	// DefaultDirPerms is the default directory permissions
	DefaultDirPerms = 0755
)
