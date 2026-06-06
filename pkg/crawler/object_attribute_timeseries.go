// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package crawler

import (
	"database/sql"
	"fmt"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	tse "github.com/pzaino/thecrowler/pkg/timeseries"
)

type crawlerTimeSeriesLogger struct{}

func (crawlerTimeSeriesLogger) Printf(format string, args ...interface{}) {
	cmn.DebugMsg(cmn.DbgLvlError, format, args...)
}

type crawlerObjectAttributeScopeResolver struct{ tx *sql.Tx }

func (r crawlerObjectAttributeScopeResolver) ResolveScopes(input tse.ObjectAttributeInput) ([]cdb.TimeSeriesScope, error) {
	if r.tx == nil {
		return nil, fmt.Errorf("object-attribute scope transaction is nil")
	}
	rows, err := r.tx.Query(`
		SELECT DISTINCT woi.index_id, ssi.source_id,
		       sisi.source_information_seed_id, sisi.information_seed_id, em.entity_id
		FROM WebObjectsIndex woi
		JOIN SourceSearchIndex ssi ON ssi.index_id = woi.index_id
		LEFT JOIN SourceInformationSeedIndex sisi
		  ON sisi.source_id = ssi.source_id AND sisi.deleted_at IS NULL
		LEFT JOIN EntityMemberships em
		  ON em.object_type = $1 AND em.object_id = $2
		WHERE woi.object_id = $2`, input.ObjectType, input.ObjectID)
	if err != nil {
		return nil, fmt.Errorf("resolve object-attribute source links: %w", err)
	}
	defer rows.Close()
	scopes := []cdb.TimeSeriesScope{}
	for rows.Next() {
		var indexID, sourceID uint64
		var sourceSeedID, seedID, entityID sql.NullInt64
		if err = rows.Scan(&indexID, &sourceID, &sourceSeedID, &seedID, &entityID); err != nil {
			return nil, err
		}
		objectID := input.ObjectID
		scope := cdb.TimeSeriesScope{IndexID: &indexID, SourceID: &sourceID, ObjectType: input.ObjectType, ObjectID: &objectID}
		if sourceSeedID.Valid {
			value := uint64(sourceSeedID.Int64)
			scope.SourceInformationSeedID = &value
		}
		if seedID.Valid {
			value := uint64(seedID.Int64)
			scope.InformationSeedID = &value
		}
		if entityID.Valid {
			value := uint64(entityID.Int64)
			scope.EntityID = &value
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

func loadObjectAttributeSiblings(tx *sql.Tx, objectID uint64, objectType string) (map[string]interface{}, error) {
	rows, err := tx.Query(`SELECT attribute_key, normalized_value FROM ObjectAttributes WHERE object_type = $1 AND object_id = $2 ORDER BY created_at, attribute_id`, objectType, objectID)
	if err != nil {
		return nil, fmt.Errorf("load sibling object attributes: %w", err)
	}
	defer rows.Close()
	values := map[string]interface{}{}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		if existing, ok := values[key]; ok {
			switch typed := existing.(type) {
			case []interface{}:
				values[key] = append(typed, value)
			default:
				values[key] = []interface{}{typed, value}
			}
		} else {
			values[key] = value
		}
	}
	return values, rows.Err()
}

type crawlerTimeSeriesCardinalityGuard struct{ tx *sql.Tx }

func (g crawlerTimeSeriesCardinalityGuard) Exceeded(metric cdb.TimeSeriesMetric, scope cdb.TimeSeriesScope, dimensions map[string]interface{}, policy cfg.TimeSeriesCardinalityConfig) (bool, error) {
	if g.tx == nil {
		return false, fmt.Errorf("time-series cardinality transaction is nil")
	}
	if policy.MaxSeriesPerMetric > 0 {
		canonical, err := cdb.CanonicalTimeSeriesJSON(dimensions)
		if err != nil {
			return false, err
		}
		var exists bool
		err = g.tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM TimeSeriesObservations WHERE metric_id = $1 AND source_id IS NOT DISTINCT FROM $2 AND index_id IS NOT DISTINCT FROM $3 AND entity_id IS NOT DISTINCT FROM $4 AND object_type IS NOT DISTINCT FROM $5 AND object_id IS NOT DISTINCT FROM $6 AND COALESCE(dimensions, '{}'::jsonb) = $7::jsonb AND deleted_at IS NULL)`, metric.ID, scope.SourceID, scope.IndexID, scope.EntityID, nullableTimeSeriesString(scope.ObjectType), scope.ObjectID, string(canonical)).Scan(&exists)
		if err != nil {
			return false, err
		}
		if !exists {
			var series int
			err = g.tx.QueryRow(`SELECT COUNT(DISTINCT concat_ws('|', information_seed_id, source_id, index_id, entity_id, object_type, object_id, dimensions::text)) FROM TimeSeriesObservations WHERE metric_id = $1 AND deleted_at IS NULL`, metric.ID).Scan(&series)
			if err != nil {
				return false, err
			}
			if series >= policy.MaxSeriesPerMetric {
				return true, nil
			}
		}
	}
	if policy.MaxValuesPerDimension > 0 {
		for key, value := range dimensions {
			encoded, err := cdb.CanonicalTimeSeriesJSON(value)
			if err != nil {
				return false, err
			}
			var exists bool
			err = g.tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM TimeSeriesObservations WHERE metric_id = $1 AND dimensions -> $2 = $3::jsonb AND deleted_at IS NULL)`, metric.ID, key, string(encoded)).Scan(&exists)
			if err != nil {
				return false, err
			}
			if exists {
				continue
			}
			var values int
			err = g.tx.QueryRow(`SELECT COUNT(DISTINCT dimensions -> $2) FROM TimeSeriesObservations WHERE metric_id = $1 AND deleted_at IS NULL AND dimensions ? $2`, metric.ID, key).Scan(&values)
			if err != nil {
				return false, err
			}
			if values >= policy.MaxValuesPerDimension {
				return true, nil
			}
		}
	}
	return false, nil
}

func nullableTimeSeriesString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
