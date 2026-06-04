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

package database

import (
	"fmt"
	"strings"
)

// CreateInformationSeed inserts a new information seed and returns its database ID.
func CreateInformationSeed(db *Handler, seed *InformationSeed) (uint64, error) {
	if db == nil || *db == nil {
		return 0, fmt.Errorf("database handler is nil")
	}
	if seed == nil {
		return 0, fmt.Errorf("information seed is nil")
	}

	seedText := strings.TrimSpace(seed.InformationSeed)
	if seedText == "" {
		return 0, fmt.Errorf("information seed text is required")
	}
	status := strings.TrimSpace(seed.Status)
	if status == "" {
		status = "new"
	}
	priority := strings.TrimSpace(seed.Priority)
	engine := strings.TrimSpace(seed.Engine)
	config, err := informationSeedConfigString(seed.Config)
	if err != nil {
		return 0, err
	}

	args := []interface{}{seed.CategoryID, seed.UsrID, seedText, status, priority, engine, seed.Disabled, config}

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		var id uint64
		err = (*db).QueryRow(`
			INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
			RETURNING information_seed_id`, args...).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to create PostgreSQL information seed: %w", err)
		}
		return id, nil
	case DBSQLiteStr:
		var id uint64
		err = (*db).QueryRow(`
			INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING information_seed_id`, args...).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to create SQLite information seed: %w", err)
		}
		return id, nil
	case DBMySQLStr:
		result, err := (*db).Exec(`
			INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, args...)
		if err != nil {
			return 0, fmt.Errorf("failed to create MySQL information seed: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("failed to retrieve MySQL information seed ID: %w", err)
		}
		if id < 0 {
			return 0, fmt.Errorf("failed to retrieve MySQL information seed ID: negative ID %d", id)
		}
		return uint64(id), nil
	default:
		return 0, fmt.Errorf("unsupported database type for information seed creation: %s", (*db).DBMS())
	}
}

// GetInformationSeedByID retrieves an information seed by its ID.
func GetInformationSeedByID(db *Handler, id uint64) (*InformationSeed, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if id == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for information seed retrieval: %s", (*db).DBMS())
	}
	placeholder := informationSeedPlaceholderForDBMS(dbms, 1)
	row := (*db).QueryRow(fmt.Sprintf(`
		SELECT %s
		FROM InformationSeed
		WHERE information_seed_id = %s`, informationSeedSelectColumns(), placeholder), id)
	seed, err := scanInformationSeedRow(row)
	if err != nil {
		return nil, fmt.Errorf("no information seed found with ID %d: %w", id, err)
	}
	return seed, nil
}

// SetInformationSeedDisabled updates whether an information seed is disabled.
func SetInformationSeedDisabled(db *Handler, id uint64, disabled bool) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if id == 0 {
		return fmt.Errorf("information seed ID must be provided")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed disabled update: %s", (*db).DBMS())
	}
	p1 := informationSeedPlaceholderForDBMS(dbms, 1)
	p2 := informationSeedPlaceholderForDBMS(dbms, 2)
	query := fmt.Sprintf(`
		UPDATE InformationSeed
		SET disabled = %s
		WHERE information_seed_id = %s`, p1, p2)
	result, err := (*db).Exec(query, disabled, id)
	if err != nil {
		return fmt.Errorf("failed to update information seed %d disabled flag: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return fmt.Errorf("no information seed found with ID %d", id)
	}
	return nil
}
