package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

const (
	memoryModeNone       = "none"
	memoryModeEphemeral  = "ephemeral"
	memoryModePersistent = "persistent"
)

// MemoryRecord stores one namespaced memory value.
type MemoryRecord struct {
	Namespace string
	Key       string
	Value     map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt *time.Time
}

// MemoryStore is the common contract for ephemeral and persistent memory.
type MemoryStore interface {
	Get(namespace, key string, now time.Time) (MemoryRecord, bool, error)
	Set(namespace, key string, value map[string]any, ttl time.Duration, now time.Time) error
	List(namespace string, now time.Time) ([]MemoryRecord, error)
	Delete(namespace, key string) error
	DeleteExpired(now time.Time) error
	ClearNamespace(namespace string) error
}

// PersistentMemoryBackend allows plugging storage backends for persistent mode.
type PersistentMemoryBackend interface {
	Get(ctx context.Context, namespace, key string, now time.Time) (MemoryRecord, bool, error)
	Set(ctx context.Context, namespace, key string, value map[string]any, ttl time.Duration, now time.Time) error
	List(ctx context.Context, namespace string, now time.Time) ([]MemoryRecord, error)
	Delete(ctx context.Context, namespace, key string) error
	DeleteExpired(ctx context.Context, now time.Time) error
	ClearNamespace(ctx context.Context, namespace string) error
}

type inMemoryStore struct {
	mu      sync.RWMutex
	byNSKey map[string]MemoryRecord
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{byNSKey: map[string]MemoryRecord{}}
}

func (s *inMemoryStore) Get(namespace, key string, now time.Time) (MemoryRecord, bool, error) {
	s.mu.RLock()
	rec, ok := s.byNSKey[memoryCompositeKey(namespace, key)]
	s.mu.RUnlock()
	if !ok {
		return MemoryRecord{}, false, nil
	}
	if rec.ExpiresAt != nil && now.After(*rec.ExpiresAt) {
		_ = s.Delete(namespace, key)
		return MemoryRecord{}, false, nil
	}
	return rec, true, nil
}

func (s *inMemoryStore) Set(namespace, key string, value map[string]any, ttl time.Duration, now time.Time) error {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(key) == "" {
		return fmt.Errorf("namespace and key are required")
	}
	var expiresAt *time.Time
	if ttl > 0 {
		t := now.Add(ttl)
		expiresAt = &t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memoryCompositeKey(namespace, key)
	rec := MemoryRecord{Namespace: namespace, Key: key, Value: cloneMap(value), UpdatedAt: now, ExpiresAt: expiresAt}
	if existing, ok := s.byNSKey[k]; ok {
		rec.CreatedAt = existing.CreatedAt
	} else {
		rec.CreatedAt = now
	}
	s.byNSKey[k] = rec
	return nil
}

func (s *inMemoryStore) List(namespace string, now time.Time) ([]MemoryRecord, error) {
	s.mu.RLock()
	out := make([]MemoryRecord, 0)
	for _, rec := range s.byNSKey {
		if rec.Namespace != namespace {
			continue
		}
		if rec.ExpiresAt != nil && now.After(*rec.ExpiresAt) {
			continue
		}
		out = append(out, MemoryRecord{Namespace: rec.Namespace, Key: rec.Key, Value: cloneMap(rec.Value), CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt, ExpiresAt: rec.ExpiresAt})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	return out, nil
}

func (s *inMemoryStore) DeleteExpired(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, rec := range s.byNSKey {
		if rec.ExpiresAt != nil && now.After(*rec.ExpiresAt) {
			delete(s.byNSKey, key)
		}
	}
	return nil
}

func (s *inMemoryStore) Delete(namespace, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byNSKey, memoryCompositeKey(namespace, key))
	return nil
}

func (s *inMemoryStore) ClearNamespace(namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, rec := range s.byNSKey {
		if rec.Namespace == namespace {
			delete(s.byNSKey, key)
		}
	}
	return nil
}

type persistentStoreAdapter struct {
	backend PersistentMemoryBackend
}

func (p *persistentStoreAdapter) Get(namespace, key string, now time.Time) (MemoryRecord, bool, error) {
	return p.backend.Get(context.Background(), namespace, key, now)
}

func (p *persistentStoreAdapter) Set(namespace, key string, value map[string]any, ttl time.Duration, now time.Time) error {
	return p.backend.Set(context.Background(), namespace, key, value, ttl, now)
}

func (p *persistentStoreAdapter) List(namespace string, now time.Time) ([]MemoryRecord, error) {
	return p.backend.List(context.Background(), namespace, now)
}

func (p *persistentStoreAdapter) DeleteExpired(now time.Time) error {
	return p.backend.DeleteExpired(context.Background(), now)
}

func (p *persistentStoreAdapter) Delete(namespace, key string) error {
	return p.backend.Delete(context.Background(), namespace, key)
}

func (p *persistentStoreAdapter) ClearNamespace(namespace string) error {
	return p.backend.ClearNamespace(context.Background(), namespace)
}

// PostgresMemoryBackend stores persistent memory rows in the Memory table.
type PostgresMemoryBackend struct {
	db *cdb.Handler
}

func NewPostgresMemoryBackend(db *cdb.Handler) *PostgresMemoryBackend {
	return &PostgresMemoryBackend{db: db}
}

func (p *PostgresMemoryBackend) Get(_ context.Context, namespace, key string, now time.Time) (MemoryRecord, bool, error) {
	if p == nil || p.db == nil {
		return MemoryRecord{}, false, fmt.Errorf("missing database handler")
	}
	q := `SELECT created_at, last_updated_at, expires_at, details
	FROM Memory
	WHERE owner_name = $1 AND name = $2 AND deleted_at IS NULL
	AND (expires_at IS NULL OR expires_at > $3)
	ORDER BY last_updated_at DESC LIMIT 1`
	var createdAt, updatedAt time.Time
	var expiresAt sql.NullTime
	var details []byte
	err := (*p.db).QueryRow(q, namespace, key, now).Scan(&createdAt, &updatedAt, &expiresAt, &details)
	if err != nil {
		if err == sql.ErrNoRows {
			return MemoryRecord{}, false, nil
		}
		return MemoryRecord{}, false, err
	}
	val := map[string]any{}
	if err := json.Unmarshal(details, &val); err != nil {
		return MemoryRecord{}, false, err
	}
	var exp *time.Time
	if expiresAt.Valid {
		exp = &expiresAt.Time
	}
	return MemoryRecord{Namespace: namespace, Key: key, Value: val, CreatedAt: createdAt, UpdatedAt: updatedAt, ExpiresAt: exp}, true, nil
}

func (p *PostgresMemoryBackend) Set(_ context.Context, namespace, key string, value map[string]any, ttl time.Duration, now time.Time) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("missing database handler")
	}
	details, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var expiresAt any = nil
	if ttl > 0 {
		expiresAt = now.Add(ttl)
	}
	memoryID := deriveMemoryID(namespace, key)
	q := `INSERT INTO Memory(memory_id, owner_name, agent_name, component, memory_type, event_type, name, expires_at, details)
	VALUES($1, $2, $3, 'agent_runtime', 'short_term', 'state', $4, $5, $6::jsonb)
	ON CONFLICT (memory_id) DO UPDATE SET
	last_updated_at = CURRENT_TIMESTAMP,
	expires_at = EXCLUDED.expires_at,
	details = EXCLUDED.details,
	deleted_at = NULL`
	_, err = (*p.db).ExecuteQuery(q, memoryID, namespace, namespace, key, expiresAt, string(details))
	return err
}

func (p *PostgresMemoryBackend) List(_ context.Context, namespace string, now time.Time) ([]MemoryRecord, error) {
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("missing database handler")
	}
	q := `SELECT name, created_at, last_updated_at, expires_at, details
	FROM Memory
	WHERE owner_name = $1 AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > $2)
	ORDER BY last_updated_at ASC`
	rows, err := (*p.db).ExecuteQuery(q, namespace, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryRecord{}
	for rows.Next() {
		var key string
		var createdAt, updatedAt time.Time
		var expiresAt sql.NullTime
		var details []byte
		if err := rows.Scan(&key, &createdAt, &updatedAt, &expiresAt, &details); err != nil {
			return nil, err
		}
		val := map[string]any{}
		if err := json.Unmarshal(details, &val); err != nil {
			return nil, err
		}
		var exp *time.Time
		if expiresAt.Valid {
			exp = &expiresAt.Time
		}
		out = append(out, MemoryRecord{Namespace: namespace, Key: key, Value: val, CreatedAt: createdAt, UpdatedAt: updatedAt, ExpiresAt: exp})
	}
	return out, rows.Err()
}

func (p *PostgresMemoryBackend) Delete(_ context.Context, namespace, key string) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("missing database handler")
	}
	_, err := (*p.db).ExecuteQuery(`UPDATE Memory SET deleted_at = CURRENT_TIMESTAMP WHERE owner_name = $1 AND name = $2 AND deleted_at IS NULL`, namespace, key)
	return err
}

func (p *PostgresMemoryBackend) DeleteExpired(_ context.Context, now time.Time) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("missing database handler")
	}
	_, err := (*p.db).ExecuteQuery(`UPDATE Memory SET deleted_at = CURRENT_TIMESTAMP WHERE deleted_at IS NULL AND expires_at IS NOT NULL AND expires_at <= $1`, now)
	return err
}

func (p *PostgresMemoryBackend) ClearNamespace(_ context.Context, namespace string) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("missing database handler")
	}
	_, err := (*p.db).ExecuteQuery(`UPDATE Memory SET deleted_at = CURRENT_TIMESTAMP WHERE owner_name = $1 AND deleted_at IS NULL`, namespace)
	return err
}

type agentMemoryRuntime struct {
	ephemeral        MemoryStore
	persistent       MemoryStore
	now              func() time.Time
	defaultWriteKey  string
	defaultMemoryKey string
}

func newAgentMemoryRuntime() *agentMemoryRuntime {
	persistent := newInMemoryStore()
	return &agentMemoryRuntime{
		ephemeral:        newInMemoryStore(),
		persistent:       persistent,
		now:              time.Now,
		defaultWriteKey:  "last_result",
		defaultMemoryKey: "memory_context",
	}
}

func (mr *agentMemoryRuntime) setPersistentBackend(backend PersistentMemoryBackend) {
	if backend == nil {
		mr.persistent = newInMemoryStore()
		return
	}
	mr.persistent = &persistentStoreAdapter{backend: backend}
}

func (mr *agentMemoryRuntime) modeFor(identity *AgentIdentity) string {
	if identity == nil || identity.Memory == nil {
		return memoryModePersistent
	}
	scope := strings.ToLower(strings.TrimSpace(identity.Memory.Scope))
	switch scope {
	case memoryModeNone, memoryModeEphemeral, memoryModePersistent:
		return scope
	default:
		return memoryModePersistent
	}
}

func (mr *agentMemoryRuntime) namespaceFor(identity *AgentIdentity) string {
	if identity == nil {
		return "anonymous-agent"
	}
	if identity.Memory != nil && strings.TrimSpace(identity.Memory.Namespace) != "" {
		return strings.TrimSpace(identity.Memory.Namespace)
	}
	if strings.TrimSpace(identity.AgentID) != "" {
		return strings.TrimSpace(identity.AgentID)
	}
	return normalizeAgentID(identity.Name)
}

func (mr *agentMemoryRuntime) ttlFor(identity *AgentIdentity, params map[string]any) time.Duration {
	parseTTL := func(raw any) time.Duration {
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return 0
		}
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return 0
		}
		return d
	}
	if params != nil {
		if d := parseTTL(params["memory_ttl"]); d > 0 {
			return d
		}
	}
	if identity != nil && identity.Memory != nil {
		return parseTTL(identity.Memory.TTL)
	}
	return 0
}

func (mr *agentMemoryRuntime) retentionFor(identity *AgentIdentity) int {
	if identity == nil || identity.Memory == nil {
		return 0
	}
	return identity.Memory.Retention
}

func (mr *agentMemoryRuntime) storeFor(mode string) MemoryStore {
	if mode == memoryModeEphemeral {
		return mr.ephemeral
	}
	return mr.persistent
}

func (mr *agentMemoryRuntime) inject(params map[string]any, identity *AgentIdentity) error {
	if params == nil {
		return nil
	}
	now := mr.now().UTC()
	_ = mr.ephemeral.DeleteExpired(now)
	_ = mr.persistent.DeleteExpired(now)
	mode := mr.modeFor(identity)
	ns := mr.namespaceFor(identity)
	if mode == memoryModeNone {
		params[mr.defaultMemoryKey] = map[string]any{"mode": memoryModeNone, "namespace": ns, "records": map[string]any{}}
		return nil
	}
	store := mr.storeFor(mode)
	recs, err := store.List(ns, now)
	if err != nil {
		return err
	}
	byKey := map[string]any{}
	for _, rec := range recs {
		byKey[rec.Key] = cloneMap(rec.Value)
	}
	if readKey, _ := params["memory_read_key"].(string); strings.TrimSpace(readKey) != "" {
		if rec, ok, err := store.Get(ns, readKey, now); err != nil {
			return err
		} else if ok {
			params["memory_read_value"] = cloneMap(rec.Value)
		}
	}
	params[mr.defaultMemoryKey] = map[string]any{"mode": mode, "namespace": ns, "records": byKey}
	return nil
}

func (mr *agentMemoryRuntime) persistStepResult(params map[string]any, result map[string]any, identity *AgentIdentity) error {
	if params == nil || identity == nil {
		return nil
	}
	mode := mr.modeFor(identity)
	if mode == memoryModeNone {
		return nil
	}
	writeKey, _ := params["memory_write_key"].(string)
	writeKey = strings.TrimSpace(writeKey)
	if writeKey == "" {
		writeKey = mr.defaultWriteKey
	}
	writeVal := map[string]any{}
	if explicit, ok := params["memory_write_value"].(map[string]any); ok {
		writeVal = cloneMap(explicit)
	} else if explicitSI, ok := params["memory_write_value"].(map[string]interface{}); ok {
		writeVal = cloneMap(explicitSI)
	} else if resp, ok := result[StrResponse].(map[string]any); ok {
		writeVal = cloneMap(resp)
	} else if respSI, ok := result[StrResponse].(map[string]interface{}); ok {
		writeVal = cloneMap(respSI)
	} else {
		writeVal = cloneMap(result)
	}
	if len(writeVal) == 0 {
		return nil
	}
	ns := mr.namespaceFor(identity)
	now := mr.now().UTC()
	store := mr.storeFor(mode)
	if err := store.Set(ns, writeKey, writeVal, mr.ttlFor(identity, params), now); err != nil {
		return err
	}
	retention := mr.retentionFor(identity)
	if retention > 0 {
		recs, err := store.List(ns, now)
		if err != nil {
			return err
		}
		if len(recs) > retention {
			overflow := recs[:len(recs)-retention]
			for _, rec := range overflow {
				_ = store.Delete(ns, rec.Key)
			}
		}
	}
	return nil
}

func memoryCompositeKey(namespace, key string) string {
	return strings.TrimSpace(namespace) + "::" + strings.TrimSpace(key)
}

func deriveMemoryID(namespace, key string) string {
	h := sha256.Sum256([]byte(memoryCompositeKey(namespace, key)))
	return hex.EncodeToString(h[:])
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
