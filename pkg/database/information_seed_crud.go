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
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
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
	priority, engine := strings.TrimSpace(seed.Priority), strings.TrimSpace(seed.Engine)
	config, err := informationSeedConfigString(seed.Config)
	if err != nil {
		return 0, err
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return 0, fmt.Errorf("unsupported database type for information seed creation: %s", (*db).DBMS())
	}
	tx, err := (*db).Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	args := []interface{}{seed.CategoryID, seed.UsrID, seedText, status, priority, engine, seed.Disabled, config}
	var id uint64
	switch dbms {
	case DBPostgresStr:
		err = tx.QueryRow(`INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb) RETURNING information_seed_id`, args...).Scan(&id)
	case DBSQLiteStr:
		err = tx.QueryRow(`INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING information_seed_id`, args...).Scan(&id)
	case DBMySQLStr:
		var result sql.Result
		result, err = tx.Exec(`INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES (?,?,?,?,?,?,?,?)`, args...)
		if err == nil {
			var inserted int64
			inserted, err = result.LastInsertId()
			if inserted < 0 {
				err = fmt.Errorf("negative information seed ID %d", inserted)
			}
			id = uint64(inserted)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("failed to create information seed: %w", err)
	}
	persisted := *seed
	persisted.ID = id
	persisted.InformationSeed = seedText
	persisted.Status = status
	persisted.Priority = priority
	persisted.Engine = engine
	persisted.LastUpdatedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	if err = emitInformationSeedLifecycleObservationsTx(tx, dbms, persisted, "created", "", status); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return id, nil
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
	tx, err := (*db).Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	p := newInformationSeedPlaceholders(dbms)
	var previous bool
	if err = tx.QueryRow(`SELECT disabled FROM InformationSeed WHERE information_seed_id = `+p.Next(), id).Scan(&previous); err != nil {
		return fmt.Errorf("no information seed found with ID %d: %w", id, err)
	}
	p = newInformationSeedPlaceholders(dbms)
	if _, err = tx.Exec(`UPDATE InformationSeed SET disabled = `+p.Next()+` WHERE information_seed_id = `+p.Next(), disabled, id); err != nil {
		return fmt.Errorf("failed to update information seed %d disabled flag: %w", id, err)
	}
	seed, err := getInformationSeedByIDTx(tx, dbms, id)
	if err != nil {
		return err
	}
	if previous != disabled {
		if err = emitInformationSeedLifecycleObservationsTx(tx, dbms, *seed, "disabled_changed", fmt.Sprint(previous), fmt.Sprint(disabled)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func getInformationSeedByIDTx(tx *sql.Tx, dbms string, id uint64) (*InformationSeed, error) {
	p := newInformationSeedPlaceholders(dbms)
	row := tx.QueryRow(`SELECT `+informationSeedSelectColumns()+` FROM InformationSeed WHERE information_seed_id = `+p.Next(), id)
	seed, err := scanInformationSeedRow(row)
	if err != nil {
		return nil, fmt.Errorf("lookup persisted information seed %d: %w", id, err)
	}
	return seed, nil
}

func emitInformationSeedLifecycleObservationsTx(tx *sql.Tx, dbms string, seed InformationSeed, event, previous, current string) error {
	config := map[string]interface{}{}
	if seed.Config != nil {
		_ = json.Unmarshal(*seed.Config, &config)
	}
	observedAt := time.Now().UTC()
	if seed.LastUpdatedAt.Valid {
		observedAt = seed.LastUpdatedAt.Time.UTC()
	}
	fields := map[string]interface{}{"count": 1, "status": seed.Status, "priority": seed.Priority, "disabled": seed.Disabled,
		"attempts": seed.Attempts, "category_id": seed.CategoryID, "user_id": seed.UsrID, "information_seed": seed.InformationSeed,
		"previous": previous, "current": current, "config": config}
	seedID := seed.ID
	return emitInformationSeedObservationsTx(tx, dbms, informationSeedObservationEvent{SourceKind: cfg.TimeSeriesSourceInformationSeed,
		Event: event, Identity: fmt.Sprintf("seed:%d:%s:%s:%s", seed.ID, event, previous, current), ObservedAt: observedAt,
		Scope:  TimeSeriesScope{InformationSeedID: &seedID, SubjectType: string(cfg.TimeSeriesSourceInformationSeed), SubjectID: &seedID},
		Fields: fields, Provenance: map[string]interface{}{"information_seed_id": seed.ID, "transition": event,
			"previous_value": previous, "current_value": current, "status": seed.Status, "disabled": seed.Disabled,
			"attempts": seed.Attempts, "category_id": seed.CategoryID, "user_id": seed.UsrID}})
}

// UpdateInformationSeed replaces mutable fields for an existing information seed.
func UpdateInformationSeed(db *Handler, seed *InformationSeed) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if seed == nil || seed.ID == 0 {
		return fmt.Errorf("information seed ID must be provided")
	}
	seedText := strings.TrimSpace(seed.InformationSeed)
	if seedText == "" {
		return fmt.Errorf("information seed text is required")
	}
	status := strings.TrimSpace(seed.Status)
	if status == "" {
		status = "new"
	}
	priority, engine := strings.TrimSpace(seed.Priority), strings.TrimSpace(seed.Engine)
	config, err := informationSeedConfigString(seed.Config)
	if err != nil {
		return err
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed update: %s", (*db).DBMS())
	}
	tx, err := (*db).Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	p := newInformationSeedPlaceholders(dbms)
	query := `UPDATE InformationSeed SET category_id = ` + p.Next() + `, usr_id = ` + p.Next() + `, information_seed = ` + p.Next() + `, status = ` + p.Next() + `, priority = ` + p.Next() + `, engine = ` + p.Next() + `, disabled = ` + p.Next() + `, config = ` + p.Next()
	if dbms == DBPostgresStr {
		query += `::jsonb`
	}
	query += `, last_updated_at = CURRENT_TIMESTAMP WHERE information_seed_id = ` + p.Next()
	result, err := tx.Exec(query, seed.CategoryID, seed.UsrID, seedText, status, priority, engine, seed.Disabled, config, seed.ID)
	if err != nil {
		return fmt.Errorf("failed to update information seed %d: %w", seed.ID, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return fmt.Errorf("no information seed found with ID %d", seed.ID)
	}
	persisted, err := getInformationSeedByIDTx(tx, dbms, seed.ID)
	if err != nil {
		return err
	}
	if err = emitInformationSeedLifecycleObservationsTx(tx, dbms, *persisted, "updated", "", status); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// RemoveInformationSeed marks an information seed deleted when supported.
func RemoveInformationSeed(db *Handler, id uint64) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if id == 0 {
		return fmt.Errorf("information seed ID must be provided")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed removal: %s", (*db).DBMS())
	}
	hasDeletedAt, err := tableColumnExists(db, "InformationSeed", "deleted_at")
	if err != nil {
		return err
	}
	p := newInformationSeedPlaceholders(dbms)
	query := `DELETE FROM InformationSeed WHERE information_seed_id = ` + p.Next()
	if hasDeletedAt {
		p = newInformationSeedPlaceholders(dbms)
		query = `UPDATE InformationSeed SET deleted_at = CURRENT_TIMESTAMP, disabled = ` + p.Next() + ` WHERE information_seed_id = ` + p.Next()
	}
	var result sql.Result
	if hasDeletedAt {
		result, err = (*db).Exec(query, true, id)
	} else {
		result, err = (*db).Exec(query, id)
	}
	if err != nil {
		return fmt.Errorf("failed to remove information seed %d: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return fmt.Errorf("no information seed found with ID %d", id)
	}
	return nil
}
