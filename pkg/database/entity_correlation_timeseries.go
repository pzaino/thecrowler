// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// Entity is a generic correlation-created identity.
type Entity struct {
	ID          uint64
	Type        string
	CreatedAt   time.Time
	DeletedAt   *time.Time
	LastUpdated time.Time
}

// EntityMembership associates an indexed object with an entity. Role and Type
// are deliberately generic dimensions rather than domain-specific identities.
type EntityMembership struct {
	EntityID       uint64
	ObjectType     string
	ObjectID       uint64
	Confidence     *float64
	MembershipRole string
	MembershipType string
	Evidence       json.RawMessage
	CreatedAt      time.Time
}

// CorrelationRule contains rule metadata. RuleDefinition is persisted but is
// never copied into observation provenance or dimensions.
type CorrelationRule struct {
	ID            uint64
	Name          string
	Definition    json.RawMessage
	Enabled       bool
	Version       int
	CreatedAt     time.Time
	LastUpdatedAt time.Time
	DeletedAt     *time.Time
}

// ObjectCorrelation is a normalized, ordered relationship between two objects.
type ObjectCorrelation struct {
	ObjectType1 string
	ObjectID1   uint64
	ObjectType2 string
	ObjectID2   uint64
	RuleID      uint64
	EntityID    *uint64
	Score       *float64
	Confidence  *float64
	CreatedAt   time.Time
	LastUpdated time.Time
}

func validateUnitInterval(name string, value *float64) error {
	if value != nil && (*value < 0 || *value > 1) {
		return fmt.Errorf("%s must be between 0 and 1", name)
	}
	return nil
}

func canonicalOptionalJSON(raw json.RawMessage) (interface{}, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	canonical, err := CanonicalTimeSeriesJSON(raw)
	if err != nil {
		return nil, err
	}
	return string(canonical), nil
}

// UpsertEntity persists an entity without assigning any domain-specific schema.
func UpsertEntity(db *Handler, entity *Entity) (*Entity, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if entity == nil {
		return nil, fmt.Errorf("entity is nil")
	}
	if strings.TrimSpace(entity.Type) == "" {
		return nil, fmt.Errorf("entity type is required")
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("begin entity transaction: %w", err)
	}
	defer func() { _ = (*db).Rollback(tx) }()
	p := newInformationSeedPlaceholders(dbms)
	if entity.ID == 0 {
		query := `INSERT INTO Entities (entity_type) VALUES (` + p.Next() + `)`
		if dbms == DBPostgresStr {
			err = tx.QueryRow(query+` RETURNING entity_id`, entity.Type).Scan(&entity.ID)
		} else {
			var result sql.Result
			result, err = tx.Exec(query, entity.Type)
			if err == nil {
				var id int64
				id, err = result.LastInsertId()
				entity.ID = uint64(id)
			}
		}
	} else {
		_, err = tx.Exec(`UPDATE Entities SET entity_type = `+p.Next()+`, deleted_at = NULL, last_updated_at = CURRENT_TIMESTAMP WHERE entity_id = `+p.Next(), entity.Type, entity.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("persist entity: %w", err)
	}
	if err = (*db).Commit(tx); err != nil {
		return nil, fmt.Errorf("commit entity: %w", err)
	}
	return entity, nil
}

// UpsertEntityMembership persists a membership and emits configured
// entity_membership observations in the same transaction.
func UpsertEntityMembership(db *Handler, membership *EntityMembership) error {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return err
	}
	if membership == nil || membership.EntityID == 0 || membership.ObjectID == 0 || strings.TrimSpace(membership.ObjectType) == "" {
		return fmt.Errorf("entity membership entity, object type, and object ID are required")
	}
	if err = validateUnitInterval("membership confidence", membership.Confidence); err != nil {
		return err
	}
	evidence, err := canonicalOptionalJSON(membership.Evidence)
	if err != nil {
		return fmt.Errorf("entity membership evidence: %w", err)
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin entity membership transaction: %w", err)
	}
	defer func() { _ = (*db).Rollback(tx) }()
	args := []interface{}{membership.EntityID, membership.ObjectType, membership.ObjectID, membership.Confidence, nullableString(membership.MembershipRole), nullableString(membership.MembershipType), evidence}
	values := []string{}
	p := newInformationSeedPlaceholders(dbms)
	for range args {
		values = append(values, p.Next())
	}
	if dbms == DBPostgresStr && evidence != nil {
		values[6] += "::jsonb"
	}
	base := `INSERT INTO EntityMemberships (entity_id, object_type, object_id, confidence, membership_role, membership_type, evidence) VALUES (` + strings.Join(values, ",") + `)`
	if dbms == DBMySQLStr {
		base += ` ON DUPLICATE KEY UPDATE confidence=VALUES(confidence), membership_role=VALUES(membership_role), membership_type=VALUES(membership_type), evidence=VALUES(evidence)`
	} else {
		base += ` ON CONFLICT (entity_id, object_type, object_id) DO UPDATE SET confidence=excluded.confidence, membership_role=excluded.membership_role, membership_type=excluded.membership_type, evidence=excluded.evidence`
	}
	if _, err = tx.Exec(base, args...); err != nil {
		return fmt.Errorf("persist entity membership: %w", err)
	}
	if err = emitEntityMembershipObservationsTx(tx, dbms, *membership); err != nil {
		return err
	}
	if err = (*db).Commit(tx); err != nil {
		return fmt.Errorf("commit entity membership: %w", err)
	}
	return nil
}

// UpsertCorrelationRule persists rule metadata. Observations intentionally omit
// the full rule definition so configured metrics cannot leak sensitive rules.
func UpsertCorrelationRule(db *Handler, rule *CorrelationRule) (*CorrelationRule, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if rule == nil || strings.TrimSpace(rule.Name) == "" {
		return nil, fmt.Errorf("correlation rule name is required")
	}
	definition, err := canonicalOptionalJSON(rule.Definition)
	if err != nil {
		return nil, fmt.Errorf("correlation rule definition: %w", err)
	}
	if rule.Version == 0 {
		rule.Version = 1
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("begin correlation rule transaction: %w", err)
	}
	defer func() { _ = (*db).Rollback(tx) }()
	p := newInformationSeedPlaceholders(dbms)
	if rule.ID == 0 {
		values := []string{p.Next(), p.Next(), p.Next(), p.Next()}
		if dbms == DBPostgresStr && definition != nil {
			values[3] += "::jsonb"
		}
		query := `INSERT INTO CorrelationRules (rule_name, enabled, version, rule_definition) VALUES (` + strings.Join(values, ",") + `)`
		if dbms == DBPostgresStr {
			err = tx.QueryRow(query+` RETURNING rule_id`, rule.Name, rule.Enabled, rule.Version, definition).Scan(&rule.ID)
		} else {
			var result sql.Result
			result, err = tx.Exec(query, rule.Name, rule.Enabled, rule.Version, definition)
			if err == nil {
				var id int64
				id, err = result.LastInsertId()
				rule.ID = uint64(id)
			}
		}
	} else {
		values := []string{p.Next(), p.Next(), p.Next(), p.Next(), p.Next()}
		if dbms == DBPostgresStr && definition != nil {
			values[3] += "::jsonb"
		}
		_, err = tx.Exec(`UPDATE CorrelationRules SET rule_name=`+values[0]+`, enabled=`+values[1]+`, version=`+values[2]+`, rule_definition=`+values[3]+`, deleted_at=NULL, last_updated_at=CURRENT_TIMESTAMP WHERE rule_id=`+values[4], rule.Name, rule.Enabled, rule.Version, definition, rule.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("persist correlation rule: %w", err)
	}
	if err = (*db).Commit(tx); err != nil {
		return nil, fmt.Errorf("commit correlation rule: %w", err)
	}
	return rule, nil
}

func normalizeCorrelationOrder(c *ObjectCorrelation) {
	if c.ObjectType1 > c.ObjectType2 || (c.ObjectType1 == c.ObjectType2 && c.ObjectID1 > c.ObjectID2) {
		c.ObjectType1, c.ObjectType2 = c.ObjectType2, c.ObjectType1
		c.ObjectID1, c.ObjectID2 = c.ObjectID2, c.ObjectID1
	}
}

// UpsertObjectCorrelation persists a result and emits both object_correlation
// and correlation_rule observations atomically with it.
func UpsertObjectCorrelation(db *Handler, correlation *ObjectCorrelation) error {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return err
	}
	if correlation == nil || correlation.RuleID == 0 || correlation.ObjectID1 == 0 || correlation.ObjectID2 == 0 || correlation.ObjectType1 == "" || correlation.ObjectType2 == "" {
		return fmt.Errorf("correlation object identities and rule ID are required")
	}
	if err = validateUnitInterval("correlation score", correlation.Score); err != nil {
		return err
	}
	if err = validateUnitInterval("correlation confidence", correlation.Confidence); err != nil {
		return err
	}
	normalizeCorrelationOrder(correlation)
	if correlation.ObjectType1 == correlation.ObjectType2 && correlation.ObjectID1 == correlation.ObjectID2 {
		return fmt.Errorf("correlation objects must be distinct")
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin object correlation transaction: %w", err)
	}
	defer func() { _ = (*db).Rollback(tx) }()
	p := newInformationSeedPlaceholders(dbms)
	args := []interface{}{correlation.ObjectType1, correlation.ObjectID1, correlation.ObjectType2, correlation.ObjectID2, correlation.RuleID, correlation.EntityID, correlation.Score, correlation.Confidence}
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	query := `INSERT INTO ObjectCorrelations (object_type_1, object_id_1, object_type_2, object_id_2, rule_id, entity_id, score, confidence) VALUES (` + strings.Join(values, ",") + `)`
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE entity_id=VALUES(entity_id), score=VALUES(score), confidence=VALUES(confidence), last_updated_at=CURRENT_TIMESTAMP`
	} else {
		query += ` ON CONFLICT (object_type_1, object_id_1, object_type_2, object_id_2, rule_id) DO UPDATE SET entity_id=excluded.entity_id, score=excluded.score, confidence=excluded.confidence, last_updated_at=CURRENT_TIMESTAMP`
	}
	if _, err = tx.Exec(query, args...); err != nil {
		return fmt.Errorf("persist object correlation: %w", err)
	}
	if err = emitObjectCorrelationObservationsTx(tx, dbms, *correlation); err != nil {
		return err
	}
	if err = (*db).Commit(tx); err != nil {
		return fmt.Errorf("commit object correlation: %w", err)
	}
	return nil
}

func emitEntityMembershipObservationsTx(tx *sql.Tx, dbms string, membership EntityMembership) error {
	fields := map[string]interface{}{"entity_id": membership.EntityID, "object_type": membership.ObjectType, "object_id": membership.ObjectID, "membership_role": membership.MembershipRole, "membership_type": membership.MembershipType}
	if membership.Confidence != nil {
		fields["confidence"] = *membership.Confidence
	}
	provenance := map[string]interface{}{"persistence": "entity_membership"}
	if len(membership.Evidence) > 0 {
		var evidence interface{}
		if json.Unmarshal(membership.Evidence, &evidence) == nil {
			provenance["membership_evidence"] = evidence
		}
	}
	entityID, objectID := membership.EntityID, membership.ObjectID
	return emitConfiguredCorrelationEventTx(tx, dbms, informationSeedObservationEvent{SourceKind: cfg.TimeSeriesSourceEntityMembership, Event: "persisted", Identity: fmt.Sprintf("membership:%d:%s:%d", entityID, membership.ObjectType, objectID), ObservedAt: time.Now().UTC(), Scope: TimeSeriesScope{EntityID: &entityID, SubjectType: "entity", SubjectID: &entityID, ObjectType: membership.ObjectType, ObjectID: &objectID}, Fields: fields, Provenance: provenance})
}

func emitObjectCorrelationObservationsTx(tx *sql.Tx, dbms string, correlation ObjectCorrelation) error {
	fields := map[string]interface{}{"rule_id": correlation.RuleID, "object_type_1": correlation.ObjectType1, "object_id_1": correlation.ObjectID1, "object_type_2": correlation.ObjectType2, "object_id_2": correlation.ObjectID2}
	if correlation.Score != nil {
		fields["score"] = *correlation.Score
	}
	if correlation.Confidence != nil {
		fields["confidence"] = *correlation.Confidence
	}
	if correlation.EntityID != nil {
		fields["entity_id"] = *correlation.EntityID
	}
	id1, id2, ruleID := correlation.ObjectID1, correlation.ObjectID2, correlation.RuleID
	scope := TimeSeriesScope{EntityID: correlation.EntityID, SubjectType: correlation.ObjectType1, SubjectID: &id1, ObjectType: correlation.ObjectType1, ObjectID: &id1, CorrelationRuleID: &ruleID, CorrelationObjectType1: correlation.ObjectType1, CorrelationObjectID1: &id1, CorrelationObjectType2: correlation.ObjectType2, CorrelationObjectID2: &id2}
	identity := fmt.Sprintf("correlation:%s:%d:%s:%d:%d", correlation.ObjectType1, id1, correlation.ObjectType2, id2, ruleID)
	provenance := map[string]interface{}{"persistence": "object_correlation", "related_object": map[string]interface{}{"object_type": correlation.ObjectType2, "object_id": id2}}
	if err := emitConfiguredCorrelationEventTx(tx, dbms, informationSeedObservationEvent{SourceKind: cfg.TimeSeriesSourceObjectCorrelation, Event: "persisted", Identity: identity, ObservedAt: time.Now().UTC(), Scope: scope, Fields: fields, Provenance: provenance}); err != nil {
		return err
	}
	return emitConfiguredCorrelationEventTx(tx, dbms, informationSeedObservationEvent{SourceKind: cfg.TimeSeriesSourceCorrelationRule, Event: "correlation_result", Identity: identity, ObservedAt: time.Now().UTC(), Scope: scope, Fields: fields, Provenance: map[string]interface{}{"persistence": "correlation_rule_result", "rule_id": ruleID}})
}

func emitConfiguredCorrelationEventTx(tx *sql.Tx, dbms string, event informationSeedObservationEvent) error {
	// The shared configured emitter applies selector matching, typed conversion,
	// dimensions, privacy, failure policy, change tracking, and idempotent keys.
	return emitInformationSeedObservationsTx(tx, dbms, event)
}

// EntityObservationBackfillRequest bounds one resumable run. AfterObservationID
// is the caller's durable checkpoint; zero starts from the beginning.
type EntityObservationBackfillRequest struct {
	AfterObservationID uint64
	BatchSize          int
	MaxBatches         int
}

// EntityObservationBackfillResult reports both the next checkpoint and the
// exact affected event-time range to pass to an explicit Task 9 reaggregation.
type EntityObservationBackfillResult struct {
	NextObservationID uint64
	Scanned           int
	Updated           int
	Batches           int
	Done              bool
	AffectedStart     *time.Time
	AffectedEnd       *time.Time
}

// BackfillObservationEntities attaches delayed entity memberships to raw
// observations only. It never changes TimeSeriesAggregates; callers must submit
// AffectedStart/AffectedEnd through the explicit reaggregation interface.
func BackfillObservationEntities(db *Handler, request EntityObservationBackfillRequest) (EntityObservationBackfillResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return EntityObservationBackfillResult{}, err
	}
	if request.BatchSize <= 0 {
		request.BatchSize = 500
	}
	if request.BatchSize > 10000 {
		request.BatchSize = 10000
	}
	if request.MaxBatches <= 0 {
		request.MaxBatches = 1
	}
	if request.MaxBatches > 1000 {
		request.MaxBatches = 1000
	}
	result := EntityObservationBackfillResult{NextObservationID: request.AfterObservationID}
	for result.Batches < request.MaxBatches {
		batch, batchErr := backfillObservationEntityBatch(db, dbms, result.NextObservationID, request.BatchSize)
		if batchErr != nil {
			return result, batchErr
		}
		result.Batches++
		result.Scanned += batch.Scanned
		result.Updated += batch.Updated
		if batch.NextObservationID > result.NextObservationID {
			result.NextObservationID = batch.NextObservationID
		}
		mergeAffectedRange(&result, batch.AffectedStart, batch.AffectedEnd)
		if batch.Done {
			result.Done = true
			break
		}
	}
	return result, nil
}

func mergeAffectedRange(result *EntityObservationBackfillResult, start, end *time.Time) {
	if start != nil && (result.AffectedStart == nil || start.Before(*result.AffectedStart)) {
		value := *start
		result.AffectedStart = &value
	}
	if end != nil && (result.AffectedEnd == nil || end.After(*result.AffectedEnd)) {
		value := *end
		result.AffectedEnd = &value
	}
}

func backfillObservationEntityBatch(db *Handler, dbms string, after uint64, limit int) (EntityObservationBackfillResult, error) {
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return EntityObservationBackfillResult{}, fmt.Errorf("begin entity observation backfill: %w", err)
	}
	defer func() { _ = (*db).Rollback(tx) }()
	p := newInformationSeedPlaceholders(dbms)
	query := `SELECT o.observation_id, o.observed_at, o.dimensions, o.provenance, em.entity_id, em.confidence, em.evidence, em.membership_role, em.membership_type
		FROM TimeSeriesObservations o JOIN EntityMemberships em ON em.object_type=o.object_type AND em.object_id=o.object_id
		WHERE o.entity_id IS NULL AND o.deleted_at IS NULL AND o.observation_id > ` + p.Next() + `
		AND em.entity_id=(SELECT MIN(em2.entity_id) FROM EntityMemberships em2 WHERE em2.object_type=o.object_type AND em2.object_id=o.object_id)
		ORDER BY o.observation_id ASC LIMIT ` + p.Next()
	rows, err := tx.Query(query, after, limit)
	if err != nil {
		return EntityObservationBackfillResult{}, fmt.Errorf("select entity observation backfill batch: %w", err)
	}
	type candidate struct {
		id, entity                       uint64
		observed                         time.Time
		dimensions, provenance, evidence sql.NullString
		confidence                       sql.NullFloat64
		role, membershipType             sql.NullString
	}
	items := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.observed, &c.dimensions, &c.provenance, &c.entity, &c.confidence, &c.evidence, &c.role, &c.membershipType); err != nil {
			rows.Close()
			return EntityObservationBackfillResult{}, err
		}
		items = append(items, c)
	}
	if err = rows.Close(); err != nil {
		return EntityObservationBackfillResult{}, err
	}
	result := EntityObservationBackfillResult{NextObservationID: after, Scanned: len(items), Done: len(items) < limit}
	for _, item := range items {
		result.NextObservationID = item.id
		dimensions := decodeJSONObject(item.dimensions.String)
		if _, exists := dimensions["confidence"]; !exists && item.confidence.Valid {
			dimensions["confidence"] = item.confidence.Float64
		}
		if _, exists := dimensions["membership_role"]; !exists && item.role.Valid {
			dimensions["membership_role"] = item.role.String
		}
		if _, exists := dimensions["membership_type"]; !exists && item.membershipType.Valid {
			dimensions["membership_type"] = item.membershipType.String
		}
		provenance := decodeProvenanceObject(item.provenance.String)
		entry := map[string]interface{}{"entity_id": item.entity, "object_membership": true}
		if item.confidence.Valid {
			entry["confidence"] = item.confidence.Float64
		}
		if item.evidence.Valid {
			var evidence interface{}
			if json.Unmarshal([]byte(item.evidence.String), &evidence) == nil {
				entry["evidence"] = evidence
			}
		}
		provenance["entity_membership_backfill"] = appendProvenanceEntry(provenance["entity_membership_backfill"], entry)
		dimensionJSON, jsonErr := CanonicalTimeSeriesJSON(dimensions)
		if jsonErr != nil {
			return result, jsonErr
		}
		provenanceJSON, jsonErr := CanonicalTimeSeriesJSON(provenance)
		if jsonErr != nil {
			return result, jsonErr
		}
		provenanceHash, hashErr := TimeSeriesProvenanceHash(provenanceJSON)
		if hashErr != nil {
			return result, hashErr
		}
		p2 := newInformationSeedPlaceholders(dbms)
		dimArg, provArg := string(dimensionJSON), string(provenanceJSON)
		entityPlaceholder := p2.Next()
		dimPlaceholder := p2.Next()
		provPlaceholder := p2.Next()
		hashPlaceholder := p2.Next()
		idPlaceholder := p2.Next()
		if dbms == DBPostgresStr {
			dimPlaceholder += "::jsonb"
			provPlaceholder += "::jsonb"
		}
		update := `UPDATE TimeSeriesObservations SET entity_id=` + entityPlaceholder + `, dimensions=` + dimPlaceholder + `, provenance=` + provPlaceholder + `, provenance_hash=` + hashPlaceholder + `, last_updated_at=CURRENT_TIMESTAMP WHERE observation_id=` + idPlaceholder + ` AND entity_id IS NULL`
		updateResult, updateErr := tx.Exec(update, item.entity, dimArg, provArg, provenanceHash, item.id)
		if updateErr != nil {
			return result, fmt.Errorf("update entity observation %d: %w", item.id, updateErr)
		}
		affected, _ := updateResult.RowsAffected()
		if affected == 1 {
			result.Updated++
			observed := item.observed.UTC()
			mergeAffectedRange(&result, &observed, &observed)
		}
	}
	if err = (*db).Commit(tx); err != nil {
		return result, fmt.Errorf("commit entity observation backfill: %w", err)
	}
	return result, nil
}

func decodeJSONObject(raw string) map[string]interface{} {
	result := map[string]interface{}{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	return result
}

func decodeProvenanceObject(raw string) map[string]interface{} {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return map[string]interface{}{}
	}
	var value interface{}
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return map[string]interface{}{"existing_provenance_raw": raw}
	}
	if object, ok := value.(map[string]interface{}); ok {
		return object
	}
	return map[string]interface{}{"existing_provenance": value}
}
func appendProvenanceEntry(existing interface{}, entry map[string]interface{}) []interface{} {
	var entries []interface{}
	switch value := existing.(type) {
	case []interface{}:
		entries = append(entries, value...)
	case nil:
	default:
		entries = append(entries, value)
	}
	encoded, _ := CanonicalTimeSeriesJSON(entry)
	for _, current := range entries {
		currentJSON, _ := CanonicalTimeSeriesJSON(current)
		if string(currentJSON) == string(encoded) {
			return entries
		}
	}
	return append(entries, entry)
}

// RunEntityObservationBackfillJob runs a named, persistently checkpointed
// backfill. A completed sweep resets its cursor to zero so memberships created
// later can attach observations older than the previous high-water mark.
func RunEntityObservationBackfillJob(db *Handler, jobName string, batchSize, maxBatches int) (EntityObservationBackfillResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return EntityObservationBackfillResult{}, err
	}
	jobName = strings.TrimSpace(jobName)
	if jobName == "" {
		return EntityObservationBackfillResult{}, fmt.Errorf("entity observation backfill job name is required")
	}
	p := newInformationSeedPlaceholders(dbms)
	var checkpoint uint64
	err = (*db).QueryRow(`SELECT last_observation_id FROM EntityObservationBackfillCheckpoints WHERE job_name=`+p.Next(), jobName).Scan(&checkpoint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return EntityObservationBackfillResult{}, fmt.Errorf("read entity observation backfill checkpoint: %w", err)
	}
	result, err := BackfillObservationEntities(db, EntityObservationBackfillRequest{AfterObservationID: checkpoint, BatchSize: batchSize, MaxBatches: maxBatches})
	if err != nil {
		return result, err
	}
	next := result.NextObservationID
	if result.Done {
		next = 0
	}
	p = newInformationSeedPlaceholders(dbms)
	query := `INSERT INTO EntityObservationBackfillCheckpoints (job_name, last_observation_id, affected_start, affected_end) VALUES (` + strings.Join([]string{p.Next(), p.Next(), p.Next(), p.Next()}, ",") + `)`
	args := []interface{}{jobName, next, result.AffectedStart, result.AffectedEnd}
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE last_observation_id=VALUES(last_observation_id), affected_start=COALESCE(LEAST(affected_start, VALUES(affected_start)), affected_start, VALUES(affected_start)), affected_end=COALESCE(GREATEST(affected_end, VALUES(affected_end)), affected_end, VALUES(affected_end)), last_updated_at=CURRENT_TIMESTAMP`
	} else {
		query += ` ON CONFLICT (job_name) DO UPDATE SET last_observation_id=excluded.last_observation_id, affected_start=CASE WHEN EntityObservationBackfillCheckpoints.affected_start IS NULL OR excluded.affected_start < EntityObservationBackfillCheckpoints.affected_start THEN excluded.affected_start ELSE EntityObservationBackfillCheckpoints.affected_start END, affected_end=CASE WHEN EntityObservationBackfillCheckpoints.affected_end IS NULL OR excluded.affected_end > EntityObservationBackfillCheckpoints.affected_end THEN excluded.affected_end ELSE EntityObservationBackfillCheckpoints.affected_end END, last_updated_at=CURRENT_TIMESTAMP`
	}
	if _, err = (*db).Exec(query, args...); err != nil {
		return result, fmt.Errorf("write entity observation backfill checkpoint: %w", err)
	}
	return result, nil
}
