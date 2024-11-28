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

// Package database is responsible for handling the database setup, configuration and abstraction.
package database

import (
	"strconv"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// GenerateEventUID generates a unique identifier for the event.
func GenerateEventUID(e Event) string {
	// convert e.SourceID into a string
	sID := strconv.FormatUint(e.SourceID, 10)

	// Convert event's Details field from map[string]interface{} to a string
	details := cmn.ConvertMapToString(e.Details)

	// Concatenate all the event's fields and return the SHA256 hash
	eStr := sID + e.Type + e.Severity + e.Timestamp + details

	return cmn.GenerateSHA256(eStr)
}
