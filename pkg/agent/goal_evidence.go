// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Evidence-layer errors are typed so the goal runtime can distinguish a
// transient empty read from a policy/spec failure.
var (
	ErrEvidenceUnavailable = errors.New("evidence unavailable")
	ErrEvidenceDenied      = errors.New("evidence denied")
	ErrEvidenceInvalid     = errors.New("evidence request invalid")
	ErrEvidenceBudget      = errors.New("evidence budget exhausted")
)

// GoalEvidenceRegistry is the operator-registered, read-only evidence adapter
// registry. One provider per kind keeps evaluation deterministic.
type GoalEvidenceRegistry struct {
	mu        sync.RWMutex
	providers map[EvidenceKind]EvidenceProvider
}

// NewGoalEvidenceRegistry creates an empty registry.
func NewGoalEvidenceRegistry() *GoalEvidenceRegistry {
	return &GoalEvidenceRegistry{providers: map[EvidenceKind]EvidenceProvider{}}
}

// Register adds one provider and rejects duplicate kinds.
func (r *GoalEvidenceRegistry) Register(provider EvidenceProvider) error {
	if r == nil || provider == nil {
		return fmt.Errorf("nil evidence provider")
	}
	kind := provider.Kind()
	if !goalEvidenceKinds[kind] || kind == EvidenceToolObservation {
		return fmt.Errorf("invalid evidence provider kind %q", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = map[EvidenceKind]EvidenceProvider{}
	}
	if _, exists := r.providers[kind]; exists {
		return fmt.Errorf("duplicate evidence provider for kind %q", kind)
	}
	r.providers[kind] = provider
	return nil
}

// Get returns the provider for a kind.
func (r *GoalEvidenceRegistry) Get(kind EvidenceKind) (EvidenceProvider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[kind]
	return provider, ok
}

// Kinds returns registered kinds in deterministic order.
func (r *GoalEvidenceRegistry) Kinds() []EvidenceKind {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EvidenceKind, 0, len(r.providers))
	for kind := range r.providers {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NewDefaultGoalEvidenceRegistry builds the v1 providers over the trusted
// read APIs. tool_observation is ledger-only and has no provider.
func NewDefaultGoalEvidenceRegistry(db cdb.Handler) *GoalEvidenceRegistry {
	registry := NewGoalEvidenceRegistry()
	_ = registry.Register(&sourceStatusProvider{db: db})
	_ = registry.Register(&sourceMetadataProvider{db: db})
	_ = registry.Register(&indexedPageProvider{db: db})
	_ = registry.Register(&scrapedFieldProvider{db: db})
	_ = registry.Register(&artifactProvider{db: db})
	return registry
}

// goalEvidenceLedger is a run-scoped, immutable-after-record ledger.
type goalEvidenceLedger struct {
	mu        sync.Mutex
	ledgerID  string
	runID     string
	runNonce  string
	nonceHash string
	traceID   string
	goalID    string
	specHash  string
	agentID   string
	owner     string
	allowed   map[uint64]bool
	policy    EvidencePolicy
	budgets   GoalBudgets
	deadline  time.Time
	records   []EvidenceRecord
	seen      map[string]string
	bytes     int
	counter   int
}

func NewGoalEvidenceLedger(runID, runNonce, traceID, goalID, specHash, agentID, owner string, allowed []uint64, policy EvidencePolicy, budgets GoalBudgets, deadline time.Time) *goalEvidenceLedger {
	if len(policy.Allow) == 0 {
		policy.Allow = []EvidenceKind{EvidenceToolObservation}
	}
	if policy.DefaultMaxAge <= 0 {
		policy.DefaultMaxAge = defaultGoalEvidenceMaxAge
	}
	if policy.MaxPayloadBytes <= 0 {
		policy.MaxPayloadBytes = defaultGoalPayloadBytes
	}
	if budgets.MaxEvidenceRecords <= 0 {
		budgets.MaxEvidenceRecords = defaultGoalEvidenceRecords
	}
	if budgets.MaxEvidenceBytes <= 0 {
		budgets.MaxEvidenceBytes = defaultGoalEvidenceBytes
	}
	sum := sha256.Sum256([]byte(runNonce))
	idBytes := make([]byte, 16)
	_, _ = rand.Read(idBytes)
	ledger := &goalEvidenceLedger{
		ledgerID: hex.EncodeToString(idBytes),
		runID:    runID, runNonce: runNonce,
		nonceHash: hex.EncodeToString(sum[:]),
		traceID:   traceID, goalID: goalID, specHash: specHash,
		agentID: agentID, owner: owner,
		allowed: sliceToSourceSet(allowed),
		policy:  policy, budgets: budgets, deadline: deadline,
		seen: map[string]string{},
	}
	return ledger
}

func (l *goalEvidenceLedger) Record(ctx context.Context, obs EvidenceObservation) (string, error) {
	if l == nil {
		return "", fmt.Errorf("nil evidence ledger")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", ErrEvidenceUnavailable
		}
	}
	if !kindAllowed(l.policy.Allow, obs.Kind) {
		return "", fmt.Errorf("%w: kind %q is not allowed", ErrEvidenceDenied, obs.Kind)
	}
	for _, sourceID := range obs.SourceIDs {
		if len(l.allowed) > 0 && !l.allowed[sourceID] {
			return "", fmt.Errorf("%w: source %d is outside the run scope", ErrEvidenceDenied, sourceID)
		}
	}
	payload := sanitizeToolResult(obs.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: payload is not serializable", ErrEvidenceInvalid)
	}
	if len(encoded) > l.policy.MaxPayloadBytes {
		return "", fmt.Errorf("%w: evidence payload exceeds %d bytes", ErrEvidenceBudget, l.policy.MaxPayloadBytes)
	}
	observedAt := obs.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	selector := obs.SelectorSHA256
	if selector == "" {
		selector = selectorHash(EvidenceSelector{})
	}
	sourceCopy := append([]uint64(nil), obs.SourceIDs...)
	sort.Slice(sourceCopy, func(i, j int) bool { return sourceCopy[i] < sourceCopy[j] })
	payloadSum := sha256.Sum256(encoded)
	payloadHash := hex.EncodeToString(payloadSum[:])
	dedupeKey := string(obs.Kind) + "|" + selector + "|" + payloadHash + "|" + sourceIDsKey(sourceCopy) + "|" + fmt.Sprintf("%d", obs.DataUpdatedAt.UnixNano())

	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.seen[dedupeKey]; ok {
		return existing, nil
	}
	if len(l.records)+1 > l.budgets.MaxEvidenceRecords {
		return "", fmt.Errorf("%w: evidence record budget exhausted", ErrEvidenceBudget)
	}
	if l.bytes+len(encoded) > l.budgets.MaxEvidenceBytes {
		return "", fmt.Errorf("%w: evidence byte budget exhausted", ErrEvidenceBudget)
	}
	l.counter++
	expiresAt := observedAt.Add(l.policy.DefaultMaxAge)
	if !l.deadline.IsZero() && expiresAt.After(l.deadline) {
		expiresAt = l.deadline
	}
	dataUpdatedAt := obs.DataUpdatedAt.UTC()
	record := EvidenceRecord{
		LedgerID: l.ledgerID,
		RunID:    l.runID, RunNonceHash: l.nonceHash, TraceID: l.traceID,
		GoalID: l.goalID, GoalSpecHash: l.specHash,
		AgentID: l.agentID, Owner: l.owner,
		Kind: obs.Kind, Producer: obs.Producer, TrustClass: trustForEvidence(obs),
		SourceIDs: sourceCopy, SourceUID: obs.SourceUID,
		Operation: obs.Operation, ObservedAt: observedAt, DataUpdatedAt: dataUpdatedAt,
		ExpiresAt: expiresAt,
		Payload:   payload, PayloadSHA256: payloadHash, SelectorSHA256: selector,
	}
	idSum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%d",
		l.nonceHash, record.Kind, record.SelectorSHA256, record.PayloadSHA256,
		record.ObservedAt.UnixNano(), l.counter)))
	record.EvidenceID = "ev_" + hex.EncodeToString(idSum[:])[:32]
	record.RecordSHA256 = recordHash(record)
	l.records = append(l.records, record)
	l.seen[dedupeKey] = record.EvidenceID
	l.bytes += len(encoded)
	return record.EvidenceID, nil
}

func (l *goalEvidenceLedger) Snapshot() []EvidenceRecord {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]EvidenceRecord, len(l.records))
	for i, record := range l.records {
		out[i] = record
		out[i].SourceIDs = append([]uint64(nil), record.SourceIDs...)
		out[i].Payload = cloneMap(record.Payload)
	}
	return out
}

// ID returns the ledger identity used to bind evidence to this run.
func (l *goalEvidenceLedger) ID() string {
	if l == nil {
		return ""
	}
	return l.ledgerID
}

// RunNonceHash returns the public hash of the run nonce.
func (l *goalEvidenceLedger) RunNonceHash() string {
	if l == nil {
		return ""
	}
	return l.nonceHash
}

func (l *goalEvidenceLedger) Count() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.records)
}

func kindAllowed(allow []EvidenceKind, kind EvidenceKind) bool {
	for _, allowed := range allow {
		if allowed == kind {
			return true
		}
	}
	return false
}

func trustForEvidence(obs EvidenceObservation) TrustClass {
	if obs.Kind == EvidenceToolObservation {
		return TrustToolObservation
	}
	return TrustSystemVerified
}

func sourceIDsKey(ids []uint64) string {
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(fmt.Sprintf("%d,", id))
	}
	return b.String()
}

func recordHash(record EvidenceRecord) string {
	data, _ := json.Marshal(struct {
		LedgerID, RunID, RunNonceHash, GoalID, GoalSpecHash string
		AgentID, Owner                                      string
		Kind                                                EvidenceKind
		Producer                                            string
		TrustClass                                          TrustClass
		SourceIDs                                           []uint64
		ObservedAt, DataUpdatedAt                           time.Time
		PayloadSHA256                                       string
		SelectorSHA256                                      string
	}{
		record.LedgerID, record.RunID, record.RunNonceHash, record.GoalID, record.GoalSpecHash,
		record.AgentID, record.Owner, record.Kind, record.Producer,
		record.TrustClass, record.SourceIDs, record.ObservedAt, record.DataUpdatedAt,
		record.PayloadSHA256, record.SelectorSHA256,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func selectorHash(selector EvidenceSelector) string {
	data, _ := json.Marshal(selector)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func argumentsHash(args map[string]any) string {
	data, _ := json.Marshal(args)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// refreshGoalEvidence reads every operator-defined evidence selector used by
// the criteria and records the observations. tool_observation is recorded
// during authorized tool execution, not refreshed here.
func refreshGoalEvidence(ctx context.Context, spec *AgentGoalSpec, registry *GoalEvidenceRegistry, ledger *goalEvidenceLedger, queries map[string]EvidenceQueryResult, now time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if spec == nil || ledger == nil {
		return fmt.Errorf("%w: missing goal spec or ledger", ErrEvidenceInvalid)
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return ErrEvidenceUnavailable
		}
	}
	if queries == nil {
		queries = map[string]EvidenceQueryResult{}
	}
	seen := map[string]bool{}
	var selectors []EvidenceSelector
	var kinds []EvidenceKind
	collectEvidenceSelectors(spec.Acceptance.Criteria, &selectors, &kinds)
	for _, kind := range spec.Evidence.Allow {
		if kind == EvidenceToolObservation {
			continue
		}
		if _, ok := registry.Get(kind); !ok {
			return fmt.Errorf("%w: no provider for evidence kind %q", ErrEvidenceInvalid, kind)
		}
	}
	for i, selector := range selectors {
		kind := kinds[i]
		if kind == EvidenceToolObservation {
			continue
		}
		key := string(kind) + "|" + selectorHash(selector)
		if seen[key] {
			continue
		}
		seen[key] = true
		provider, ok := registry.Get(kind)
		if !ok {
			return fmt.Errorf("%w: no provider for evidence kind %q", ErrEvidenceInvalid, kind)
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return ErrEvidenceUnavailable
			}
		}
		sourceIDs := spec.AllowedSources
		if selector.SourceID != nil {
			sourceIDs = []uint64{*selector.SourceID}
		}
		limit := spec.Budgets.MaxEvidenceRecords
		request := EvidenceRequest{
			Selector: selector, Limit: limit,
			MaxAge: spec.Evidence.DefaultMaxAge, Now: now, SourceIDs: sourceIDs,
		}
		queryKey := evidenceQueryKey(kind, selector)
		observations, err := provider.Read(ctx, request)
		if err != nil {
			state := EvidenceQueryFailed
			reason := "evidence_query_failed"
			if errors.Is(err, ErrEvidenceUnavailable) {
				state = EvidenceQueryUnavailable
				reason = "evidence_query_unavailable"
			}
			queries[queryKey] = EvidenceQueryResult{
				Kind: kind, SelectorSHA256: selectorHash(selector),
				State: state, Limit: limit, Reason: reason,
			}
			continue
		}
		state := EvidenceQueryCompleted
		reason := "evidence_query_completed"
		if limit > 0 && len(observations) >= limit {
			state = EvidenceQueryTruncated
			reason = "evidence_query_truncated"
		}
		queries[queryKey] = EvidenceQueryResult{
			Kind: kind, SelectorSHA256: selectorHash(selector),
			State: state, Count: len(observations), Limit: limit, Reason: reason,
		}
		for _, obs := range observations {
			if err := validateProviderObservation(kind, selector, spec.AllowedSources, obs); err != nil {
				return fmt.Errorf("%w: %v", ErrEvidenceInvalid, err)
			}
			obs.SelectorSHA256 = selectorHash(selector)
			if obs.ObservedAt.IsZero() {
				obs.ObservedAt = now
			}
			if obs.Producer == "" {
				obs.Producer = fmt.Sprintf("evidence:%s", kind)
			}
			if _, err := ledger.Record(ctx, obs); err != nil {
				return err
			}
		}
	}
	return nil
}

func evidenceQueryKey(kind EvidenceKind, selector EvidenceSelector) string {
	return string(kind) + "|" + selectorHash(selector)
}

// validateProviderObservation enforces that a provider returned the requested
// kind and an explicit source identity compatible with the selector and run
// scope. Provider-supplied selector hashes and UIDs are not trusted.
func validateProviderObservation(kind EvidenceKind, selector EvidenceSelector, allowed []uint64, obs EvidenceObservation) error {
	if obs.Kind != kind {
		return fmt.Errorf("provider returned kind %q for requested %q", obs.Kind, kind)
	}
	switch kind {
	case EvidenceSourceStatus, EvidenceSourceMetadata, EvidenceIndexedPage, EvidenceScrapedField, EvidenceArtifact:
		if len(obs.SourceIDs) == 0 {
			return fmt.Errorf("provider returned %q evidence without a source identity", kind)
		}
		for _, sourceID := range obs.SourceIDs {
			if len(allowed) > 0 && !containsSource(allowed, sourceID) {
				return fmt.Errorf("provider returned source %d outside run scope", sourceID)
			}
			if selector.SourceID != nil && sourceID != *selector.SourceID {
				return fmt.Errorf("provider returned source %d for selector source %d", sourceID, *selector.SourceID)
			}
		}
	case EvidenceToolObservation:
		// Tool observations are admitted by the tool execution path.
		return fmt.Errorf("tool observations are not provider-refreshed")
	default:
		return fmt.Errorf("unsupported evidence kind %q", kind)
	}
	return nil
}

func collectEvidenceSelectors(criteria []CriterionSpec, selectors *[]EvidenceSelector, kinds *[]EvidenceKind) {
	for _, criterion := range criteria {
		collectPredicateSelectors(criterion.Predicate, selectors, kinds)
	}
}

func collectPredicateSelectors(predicate Predicate, selectors *[]EvidenceSelector, kinds *[]EvidenceKind) {
	if predicate.Kind == PredicateAll || predicate.Kind == PredicateAny {
		for _, child := range predicate.Predicates {
			collectPredicateSelectors(child, selectors, kinds)
		}
		return
	}
	if predicate.Kind == PredicateEvidenceExists || predicate.Kind == PredicateEvidenceCount ||
		predicate.Kind == PredicateFieldEquals || predicate.Kind == PredicateFieldIn ||
		predicate.Kind == PredicateFieldNumber || predicate.Kind == PredicateEvidenceScope {
		selector := EvidenceSelector{}
		if predicate.Where != nil {
			selector = *predicate.Where
		}
		*selectors = append(*selectors, selector)
		*kinds = append(*kinds, predicate.Evidence)
	}
}

// --- v1 providers ---

type sourceStatusProvider struct{ db cdb.Handler }

func (p *sourceStatusProvider) Kind() EvidenceKind { return EvidenceSourceStatus }

func (p *sourceStatusProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	if p == nil || p.db == nil {
		return nil, ErrEvidenceUnavailable
	}
	ids := req.SourceIDs
	if req.Selector.SourceID != nil {
		ids = []uint64{*req.Selector.SourceID}
	}
	if len(ids) == 0 {
		return nil, ErrEvidenceInvalid
	}
	var out []EvidenceObservation
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, ErrEvidenceUnavailable
		}
		source, err := cdb.GetSourceByID(&p.db, id)
		if err != nil || source == nil {
			continue
		}
		rows, err := cdb.GetSourceStatusByUIDContext(ctx, &p.db, source.UID)
		if err != nil {
			return nil, fmt.Errorf("%w: source status read failed", ErrEvidenceUnavailable)
		}
		for _, row := range rows {
			status := nullStringValue(row.Status)
			if req.Selector.Status != "" && status != req.Selector.Status {
				continue
			}
			dataUpdatedAt := parseStatusTime(nullStringValue(row.LastUpdatedAt))
			if dataUpdatedAt.IsZero() {
				dataUpdatedAt = parseStatusTime(nullStringValue(row.LastCrawledAt))
			}
			out = append(out, EvidenceObservation{
				Kind: EvidenceSourceStatus, Producer: "db:source_status",
				SourceIDs: []uint64{id}, SourceUID: source.UID, Operation: "read",
				ObservedAt: req.Now, DataUpdatedAt: dataUpdatedAt, Payload: map[string]any{
					"source_id": id, "uid": source.UID, "status": status,
					"priority":   nullStringValue(row.Priority),
					"restricted": row.Restricted, "disabled": row.Disabled,
					"flags":           row.Flags,
					"created_at":      nullStringValue(row.CreatedAt),
					"last_updated_at": nullStringValue(row.LastUpdatedAt),
					"last_crawled_at": nullStringValue(row.LastCrawledAt),
				},
			})
		}
	}
	return out, nil
}

type sourceMetadataProvider struct{ db cdb.Handler }

func (p *sourceMetadataProvider) Kind() EvidenceKind { return EvidenceSourceMetadata }

func (p *sourceMetadataProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	if p == nil || p.db == nil {
		return nil, ErrEvidenceUnavailable
	}
	if req.Selector.SourceID == nil {
		return nil, ErrEvidenceInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrEvidenceUnavailable
	}
	source, err := cdb.GetSourceByID(&p.db, *req.Selector.SourceID)
	if err == nil && ctx.Err() != nil {
		return nil, ErrEvidenceUnavailable
	}
	if err != nil || source == nil {
		return nil, ErrEvidenceUnavailable
	}
	return []EvidenceObservation{{
		Kind: EvidenceSourceMetadata, Producer: "db:source_metadata",
		SourceIDs: []uint64{source.ID}, SourceUID: source.UID, Operation: "read",
		ObservedAt: req.Now, Payload: map[string]any{
			"source_id": source.ID, "uid": source.UID, "name": source.Name,
			"url": source.URL, "priority": source.Priority,
			"category": source.CategoryID, "restricted": source.Restricted,
			"flags": source.Flags,
		},
	}}, nil
}

type indexedPageProvider struct{ db cdb.Handler }

func (p *indexedPageProvider) Kind() EvidenceKind { return EvidenceIndexedPage }

func (p *indexedPageProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	if p == nil || p.db == nil {
		return nil, ErrEvidenceUnavailable
	}
	if !strings.EqualFold(p.db.DBMS(), cdb.DBPostgresStr) {
		return nil, ErrEvidenceUnavailable
	}
	ids := req.SourceIDs
	if req.Selector.SourceID != nil {
		ids = []uint64{*req.Selector.SourceID}
	}
	if len(ids) == 0 {
		return nil, ErrEvidenceInvalid
	}
	limit := req.Limit
	if limit <= 0 || limit > hardGoalEvidenceRecords {
		limit = hardGoalEvidenceRecords
	}
	var out []EvidenceObservation
	for _, sourceID := range ids {
		if err := ctx.Err(); err != nil {
			return nil, ErrEvidenceUnavailable
		}
		args := []any{sourceID}
		query := `SELECT si.index_id, s.source_uid, si.page_url, si.created_at, si.last_updated_at
			FROM SearchIndex si
			JOIN SourceSearchIndex ssi ON ssi.index_id = si.index_id
			JOIN Sources s ON s.source_id = ssi.source_id
			WHERE s.deleted_at IS NULL AND si.deleted_at IS NULL AND s.source_id = $1`
		if req.Selector.PageURL != "" {
			args = append(args, req.Selector.PageURL)
			query += " AND si.page_url = $2"
		}
		args = append(args, limit)
		query += fmt.Sprintf(" ORDER BY si.last_updated_at DESC LIMIT $%d", len(args))
		rows, err := p.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("%w: indexed page read failed", ErrEvidenceUnavailable)
		}
		for rows.Next() {
			var indexID uint64
			var sourceUID, pageURL string
			var createdAt, updatedAt sql.NullTime
			if err := rows.Scan(&indexID, &sourceUID, &pageURL, &createdAt, &updatedAt); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("%w: indexed page scan failed", ErrEvidenceInvalid)
			}
			out = append(out, EvidenceObservation{
				Kind: EvidenceIndexedPage, Producer: "db:indexed_page",
				SourceIDs: []uint64{sourceID}, SourceUID: sourceUID, Operation: "read",
				ObservedAt: req.Now, DataUpdatedAt: updatedAt.Time,
				Payload: map[string]any{
					"index_id": indexID, "source_uid": sourceUID, "page_url": pageURL,
					"created_at":      nullTimeValue(createdAt),
					"last_updated_at": nullTimeValue(updatedAt),
				},
			})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: indexed page read failed", ErrEvidenceUnavailable)
		}
		_ = rows.Close()
	}
	return out, nil
}

type scrapedFieldProvider struct{ db cdb.Handler }

func (p *scrapedFieldProvider) Kind() EvidenceKind { return EvidenceScrapedField }

func (p *scrapedFieldProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	if p == nil || p.db == nil {
		return nil, ErrEvidenceUnavailable
	}
	if req.Selector.Field == "" || req.Selector.Value == "" || req.Selector.SourceID == nil {
		return nil, ErrEvidenceInvalid
	}
	source, err := cdb.GetSourceByID(&p.db, *req.Selector.SourceID)
	if err != nil || source == nil {
		return nil, ErrEvidenceUnavailable
	}
	limit := req.Limit
	if limit <= 0 || limit > hardGoalEvidenceRecords {
		limit = hardGoalEvidenceRecords
	}
	rows, err := cdb.SearchScrapedDataField(ctx, &p.db, req.Selector.Field, req.Selector.Value,
		cdb.SearchFunctionOptions{Limit: limit, SourceUID: source.UID})
	if err != nil {
		return nil, ErrEvidenceUnavailable
	}
	out := make([]EvidenceObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, EvidenceObservation{
			Kind: EvidenceScrapedField, Producer: "db:scraped_field",
			SourceIDs: []uint64{source.ID}, SourceUID: source.UID, Operation: "read",
			ObservedAt: req.Now, Payload: map[string]any{
				"source_uid": source.UID, "page_url": nullStringValue(row.PageURL),
				"json_field":      nullStringValue(row.JSONField),
				"created_at":      nullTimeValue(row.CreatedAt),
				"last_updated_at": nullTimeValue(row.LastUpdatedAt),
				"rank":            row.Rank,
			},
		})
	}
	return out, nil
}

type artifactProvider struct{ db cdb.Handler }

func (p *artifactProvider) Kind() EvidenceKind { return EvidenceArtifact }

func (p *artifactProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	if p == nil || p.db == nil {
		return nil, ErrEvidenceUnavailable
	}
	if req.Selector.Field == "" || req.Selector.Value == "" || req.Selector.SourceID == nil {
		return nil, ErrEvidenceInvalid
	}
	source, err := cdb.GetSourceByID(&p.db, *req.Selector.SourceID)
	if err != nil || source == nil {
		return nil, ErrEvidenceUnavailable
	}
	limit := req.Limit
	if limit <= 0 || limit > hardGoalEvidenceRecords {
		limit = hardGoalEvidenceRecords
	}
	rows, err := cdb.SearchArtifactsField(ctx, &p.db, req.Selector.Field, req.Selector.Value,
		cdb.SearchFunctionOptions{Limit: limit, SourceUID: source.UID})
	if err != nil {
		return nil, ErrEvidenceUnavailable
	}
	out := make([]EvidenceObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, EvidenceObservation{
			Kind: EvidenceArtifact, Producer: "db:artifact",
			SourceIDs: []uint64{source.ID}, SourceUID: source.UID, Operation: "read",
			ObservedAt: req.Now, DataUpdatedAt: row.LastUpdatedAt.Time, Payload: map[string]any{
				"source_type": row.SourceType, "artifact_id": row.ArtifactID,
				"source_uid": source.UID, "page_url": nullStringValue(row.PageURL),
				"created_at":      nullTimeValue(row.CreatedAt),
				"last_updated_at": nullTimeValue(row.LastUpdatedAt),
				"rank":            row.Rank,
			},
		})
	}
	return out, nil
}

// readGoalResources evaluates direct resource_status checks. The evaluator is
// pure; all trusted reads happen here.
func readGoalResources(ctx context.Context, db cdb.Handler, spec *AgentGoalSpec, runID string) map[string]ResourceObservation {
	if ctx == nil {
		ctx = context.Background()
	}
	out := map[string]ResourceObservation{}
	if spec == nil {
		return out
	}
	for _, criterion := range spec.Acceptance.Criteria {
		collectResourcePredicates(criterion.Predicate, func(predicate Predicate) {
			key := resourceObservationKey(predicate.Resource, predicate.ResourceID)
			if _, exists := out[key]; exists {
				return
			}
			obs := ResourceObservation{
				Resource: predicate.Resource, ID: predicate.ResourceID,
				ObservedAt: time.Now().UTC(), RunID: runID,
				GoalID: spec.GoalID, GoalSpecHash: spec.SpecHash,
			}
			if ctx != nil && ctx.Err() != nil {
				obs.Err = "cancelled"
				out[key] = obs
				return
			}
			if predicate.Resource == "information_seed" {
				// v1 has no approved Information Seed authorization scope.
				obs.Err = "information_seed_scope_unsupported"
				out[key] = obs
				return
			}
			if db == nil {
				obs.Err = "database unavailable"
				out[key] = obs
				return
			}
			source, err := cdb.GetSourceByID(&db, predicate.ResourceID)
			if err != nil || source == nil {
				obs.Err = "source_not_found"
				out[key] = obs
				return
			}
			rows, err := cdb.GetSourceStatusByUIDContext(ctx, &db, source.UID)
			if err != nil {
				obs.Err = "source_status_unavailable"
				out[key] = obs
				return
			}
			status := ""
			last := time.Time{}
			dataUpdated := time.Time{}
			for _, row := range rows {
				updated := parseStatusTime(nullStringValue(row.LastUpdatedAt))
				if updated.IsZero() {
					updated = parseStatusTime(nullStringValue(row.LastCrawledAt))
				}
				if status == "" || updated.After(last) {
					status = nullStringValue(row.Status)
					last = updated
					dataUpdated = updated
				}
			}
			obs.Status = status
			obs.DataUpdatedAt = dataUpdated
			obs.Source = "db:source_status"
			out[key] = obs
		})
	}
	return out
}

func parseStatusTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func collectResourcePredicates(predicate Predicate, fn func(Predicate)) {
	if predicate.Kind == PredicateAll || predicate.Kind == PredicateAny {
		for _, child := range predicate.Predicates {
			collectResourcePredicates(child, fn)
		}
		return
	}
	if predicate.Kind == PredicateResourceStatus {
		fn(predicate)
	}
}

func resourceObservationKey(resource string, id uint64) string {
	return fmt.Sprintf("%s:%d", resource, id)
}
