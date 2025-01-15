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

import "fmt"

// GetSourceByID retrieves a source from the database by its ID.
func GetSourceByID(db *Handler, sourceID uint64) (*Source, error) {
	source := &Source{}

	// Query the database
	err := (*db).QueryRow(`SELECT source_id, url, name, category_id, usr_id, restricted, flags, details FROM Sources WHERE source_id = $1`, sourceID).Scan(&source.ID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
	if err != nil {
		return nil, fmt.Errorf("no source found with ID %d", sourceID)
	}

	return source, nil
}
