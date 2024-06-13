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
	"encoding/json"
	"fmt"
	"time"
)

// Helped functions to deal with different date formats in JSON

type FlexibleDate time.Time

var (
	dateFormats = []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z07:00",
	}
)

// UnmarshalJSON parses a date from a JSON string
func (fd *FlexibleDate) UnmarshalJSON(b []byte) error {
	str := string(b)
	// Trim the quotes
	str = str[1 : len(str)-1]
	var parsedTime time.Time
	var err error
	for _, format := range dateFormats {
		parsedTime, err = time.Parse(format, str)
		if err == nil {
			*fd = FlexibleDate(parsedTime)
			return nil
		}
	}
	return fmt.Errorf("could not parse date: %s", str)
}

// MarshalJSON returns a date as a JSON string
func (fd FlexibleDate) MarshalJSON() ([]byte, error) {
	formatted := fmt.Sprintf("\"%s\"", time.Time(fd).Format(dateFormats[0]))
	return []byte(formatted), nil
}

// String returns a date as a string
func (fd FlexibleDate) String() string {
	return time.Time(fd).Format(dateFormats[0])
}

// Time returns a date as a time.Time
func (fd FlexibleDate) Time() time.Time {
	return time.Time(fd)
}

// IsJSON checks if a string is a JSON
func IsJSON(s string) bool {
	var js map[string]interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// JSONStrToMap converts a JSON string to a map
func JSONStrToMap(s string) (map[string]interface{}, error) {
	var js map[string]interface{}
	err := json.Unmarshal([]byte(s), &js)
	return js, err
}
