// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");

package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	InformationSeedCandidateDecisionAccepted = "accepted"
	InformationSeedCandidateDecisionRejected = "rejected"
)

// InformationSeedCandidate records the durable decision evidence for one
// candidate considered during an information seed run. Accepted candidates may
// also have SourceInformationSeedIndex provenance; rejected candidates never
// create Source rows and are represented only in this audit table.
type InformationSeedCandidate struct {
	ID                uint64
	InformationSeedID uint64
	NormalizedURL     string
	Host              string
	Provider          string
	Query             string
	Rank              int
	Score             float64
	DecisionStatus    string
	RejectionReason   string
	Metadata          *json.RawMessage
	RunAttempt        int
	CreatedAt         sql.NullTime
	LastUpdatedAt     sql.NullTime
}

// InformationSeedCandidateFilter describes pagination for listing candidate
// decisions by information seed.
type InformationSeedCandidateFilter struct {
	Limit  int
	Offset int
}

// UpsertInformationSeedCandidateDecisions inserts or updates information seed
// candidate decision evidence. The operation is idempotent for a single seed,
// normalized URL, provider, query, rank, and run attempt tuple.
func UpsertInformationSeedCandidateDecisions(db *Handler, candidates []InformationSeedCandidate) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if len(candidates) == 0 {
		return nil
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed candidate decisions: %s", (*db).DBMS())
	}

	tx, err := (*db).Begin()
	if err != nil {
		return fmt.Errorf("failed to start information seed candidate transaction: %w", err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	query := informationSeedCandidateUpsertQuery(dbms)
	for _, candidate := range candidates {
		metadata, err := informationSeedCandidateMetadataString(candidate.Metadata)
		if err != nil {
			return err
		}
		args, err := informationSeedCandidateArgs(candidate, metadata)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(query, args...); err != nil {
			return fmt.Errorf("failed to upsert information seed %d candidate %q: %w", candidate.InformationSeedID, candidate.NormalizedURL, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit information seed candidate transaction: %w", err)
	}
	committed = true
	return nil
}

// ListInformationSeedCandidateDecisions returns candidate decision evidence for
// one seed in newest-decision order with stable pagination.
func ListInformationSeedCandidateDecisions(db *Handler, seedID uint64, filter InformationSeedCandidateFilter) ([]InformationSeedCandidate, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if seedID == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for information seed candidate listing: %s", (*db).DBMS())
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	p1 := informationSeedPlaceholderForDBMS(dbms, 1)
	p2 := informationSeedPlaceholderForDBMS(dbms, 2)
	p3 := informationSeedPlaceholderForDBMS(dbms, 3)
	query := fmt.Sprintf(`
		SELECT information_seed_candidate_id, information_seed_id, normalized_url,
			host, provider, query, rank, score, decision_status, rejection_reason,
			metadata, run_attempt, created_at, last_updated_at
		FROM InformationSeedCandidate
		WHERE information_seed_id = %s
		ORDER BY run_attempt DESC, last_updated_at DESC, information_seed_candidate_id DESC
		LIMIT %s OFFSET %s`, p1, p2, p3)
	rows, err := (*db).ExecuteQuery(query, seedID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list information seed %d candidate decisions: %w", seedID, err)
	}
	return scanInformationSeedCandidateRows(rows)
}

func informationSeedCandidateUpsertQuery(dbms string) string {
	switch dbms {
	case DBPostgresStr:
		return `
			INSERT INTO InformationSeedCandidate (
				information_seed_id, normalized_url, host, provider, query, rank, score,
				decision_status, rejection_reason, metadata, run_attempt
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
			ON CONFLICT (information_seed_id, normalized_url, provider, query, rank, run_attempt)
			DO UPDATE SET
				host = EXCLUDED.host,
				score = EXCLUDED.score,
				decision_status = EXCLUDED.decision_status,
				rejection_reason = EXCLUDED.rejection_reason,
				metadata = EXCLUDED.metadata,
				last_updated_at = NOW()`
	case DBMySQLStr:
		return `
			INSERT INTO InformationSeedCandidate (
				information_seed_id, normalized_url, host, provider, query, rank, score,
				decision_status, rejection_reason, metadata, run_attempt
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				host = VALUES(host),
				score = VALUES(score),
				decision_status = VALUES(decision_status),
				rejection_reason = VALUES(rejection_reason),
				metadata = VALUES(metadata),
				last_updated_at = CURRENT_TIMESTAMP`
	default:
		return `
			INSERT INTO InformationSeedCandidate (
				information_seed_id, normalized_url, host, provider, query, rank, score,
				decision_status, rejection_reason, metadata, run_attempt
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, json($10), $11)
			ON CONFLICT (information_seed_id, normalized_url, provider, query, rank, run_attempt)
			DO UPDATE SET
				host = excluded.host,
				score = excluded.score,
				decision_status = excluded.decision_status,
				rejection_reason = excluded.rejection_reason,
				metadata = excluded.metadata,
				last_updated_at = CURRENT_TIMESTAMP`
	}
}

func informationSeedCandidateArgs(candidate InformationSeedCandidate, metadata *string) ([]interface{}, error) {
	if candidate.InformationSeedID == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}
	normalizedURL := strings.TrimSpace(candidate.NormalizedURL)
	if normalizedURL == "" {
		return nil, fmt.Errorf("candidate normalized URL is required")
	}
	status := strings.ToLower(strings.TrimSpace(candidate.DecisionStatus))
	if status != InformationSeedCandidateDecisionAccepted && status != InformationSeedCandidateDecisionRejected {
		return nil, fmt.Errorf("candidate decision status must be accepted or rejected")
	}
	if status == InformationSeedCandidateDecisionAccepted {
		candidate.RejectionReason = ""
	}
	return []interface{}{
		candidate.InformationSeedID,
		normalizedURL,
		strings.TrimSpace(candidate.Host),
		strings.TrimSpace(candidate.Provider),
		strings.TrimSpace(candidate.Query),
		candidate.Rank,
		candidate.Score,
		status,
		strings.TrimSpace(candidate.RejectionReason),
		nullableArg(metadata),
		candidate.RunAttempt,
	}, nil
}

func informationSeedCandidateMetadataString(metadata *json.RawMessage) (*string, error) {
	if metadata == nil {
		return nil, nil
	}
	if len(*metadata) == 0 {
		return nil, fmt.Errorf("candidate metadata must be valid JSON when provided")
	}
	if !json.Valid(*metadata) {
		return nil, fmt.Errorf("candidate metadata must be valid JSON")
	}
	metadataString := string(*metadata)
	return &metadataString, nil
}

func scanInformationSeedCandidateRows(rows *sql.Rows) ([]InformationSeedCandidate, error) {
	defer rows.Close()
	candidates := []InformationSeedCandidate{}
	for rows.Next() {
		var candidate InformationSeedCandidate
		var metadata sql.NullString
		if err := rows.Scan(
			&candidate.ID,
			&candidate.InformationSeedID,
			&candidate.NormalizedURL,
			&candidate.Host,
			&candidate.Provider,
			&candidate.Query,
			&candidate.Rank,
			&candidate.Score,
			&candidate.DecisionStatus,
			&candidate.RejectionReason,
			&metadata,
			&candidate.RunAttempt,
			&candidate.CreatedAt,
			&candidate.LastUpdatedAt,
		); err != nil {
			return nil, err
		}
		if metadata.Valid {
			raw := json.RawMessage(metadata.String)
			candidate.Metadata = &raw
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}
