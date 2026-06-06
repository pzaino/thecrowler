// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package crawler

import (
	"database/sql"
	"fmt"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	tse "github.com/pzaino/thecrowler/pkg/timeseries"
)

type crawlerIndexedArtifactScopeResolver struct{ tx *sql.Tx }

func (r crawlerIndexedArtifactScopeResolver) ResolveIndexedArtifactScopes(input tse.IndexedArtifactInput) ([]cdb.TimeSeriesScope, error) {
	if r.tx == nil {
		return nil, fmt.Errorf("indexed-artifact scope transaction is nil")
	}
	rows, err := r.tx.Query(`
		SELECT DISTINCT ssi.source_id,
		       sisi.source_information_seed_id, sisi.information_seed_id, em.entity_id
		FROM SourceSearchIndex ssi
		LEFT JOIN SourceInformationSeedIndex sisi
		  ON sisi.source_id = ssi.source_id AND sisi.deleted_at IS NULL
		LEFT JOIN WebObjectsIndex woi ON woi.index_id = ssi.index_id
		LEFT JOIN EntityMemberships em
		  ON em.object_type = 'webobject' AND em.object_id = woi.object_id
		WHERE ssi.index_id = $1`, input.IndexID)
	if err != nil {
		return nil, fmt.Errorf("resolve indexed-artifact source links: %w", err)
	}
	defer rows.Close()
	scopes := []cdb.TimeSeriesScope{}
	for rows.Next() {
		var sourceID uint64
		var sourceSeedID, seedID, entityID sql.NullInt64
		if err = rows.Scan(&sourceID, &sourceSeedID, &seedID, &entityID); err != nil {
			return nil, err
		}
		indexID := input.IndexID
		scope := cdb.TimeSeriesScope{IndexID: &indexID, SourceID: &sourceID}
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

func newCrawlerIndexedArtifactEmitter(tx *sql.Tx, currCfg *cfg.Config) *tse.Emitter {
	if currCfg == nil {
		return nil
	}
	return &tse.Emitter{
		Repository:     cdb.TransactionTimeSeriesRepository{Tx: tx, DBMS: cdb.DBPostgresStr},
		ArtifactScopes: crawlerIndexedArtifactScopeResolver{tx: tx},
		Cardinality:    crawlerTimeSeriesCardinalityGuard{tx: tx},
		Config:         &currCfg.TimeSeries,
		Logger:         crawlerTimeSeriesLogger{},
	}
}
