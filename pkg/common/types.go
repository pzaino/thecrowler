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

// DbgLevel is an enum to represent the debug level type
type DbgLevel int

const (
	// DbgLvlNone is the default debug level
	DbgLvlNone = iota
	// DbgLvlInfo is the info debug level
	DbgLvlInfo
	// DbgLvlDebug is the debug debug level
	DbgLvlDebug
	// DbgLvlError is the error debug level
	DbgLvlError
	// DbgLvlFatal is the fatal debug level (this will also exit the program!)
	DbgLvlFatal
)

var (
	// DebugLevel is the debug level for logging
	debugLevel DbgLevel
)

var (
	// UsrAgentStrMap is a list of valid user agent strings.
	UsrAgentStrMap = map[string]string{
		"chrome-desktop01": "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/104.0.0.0 Safari/537.36",
		"chrome-mobile01":  "Mozilla/5.0 (Linux; Android 10; SM-G960F) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/104.0.0.0 Mobile Safari/537.36",
	}
)

const (
	// DefaultFilePerms is the default file permissions
	DefaultFilePerms = 0644
	// DefaultDirPerms is the default directory permissions
	DefaultDirPerms = 0755
)
