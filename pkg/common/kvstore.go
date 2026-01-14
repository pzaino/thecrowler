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

// Package common package is used to store common functions and variables
package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// KVStore is the global key-value store
var (
	KVStore *KeyValueStore
)

const (
	// private
	counterName = "counter"

	// Public constants for shared callback actions

	// ActionKVSCSet Set action
	ActionKVSCSet = "set"
	// ActionKVSCUpdate Update action
	ActionKVSCUpdate = "update"
	// ActionKVSCDelete Delete action
	ActionKVSCDelete = "delete"

	// ErrKVKeyNotFound is the generic message for key not found errors
	ErrKVKeyNotFound = "key not found in key-value store"
)

// KVSErrorIsKeyNotFound checks if the given error indicates that a key was not found in the key-value store.
func KVSErrorIsKeyNotFound(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(strings.TrimSpace(err.Error()))

	if errMsg == ErrKVKeyNotFound {
		return true
	}
	if strings.HasPrefix(errMsg, "key") && strings.Contains(err.Error(), "not found") {
		return true
	}

	return strings.Contains(err.Error(), ErrKVKeyNotFound)
}

// Properties defines the additional attributes for each key-value entry.
type Properties struct {
	Persistent   bool   `yaml:"persistent"`    // Whether the entry should be persistent
	Static       bool   `yaml:"static"`        // Whether the entry should be static
	SessionValid bool   `yaml:"session_valid"` // Whether the entry should be valid for the session
	Shared       bool   `yaml:"shared"`        // Whether the key should be shared across the cluster
	Source       string `yaml:"source"`        // The source of the key-value entry
	CtxID        string // Context ID for more specific identification
	Type         string // The type of the stored value (e.g., "string", "[]string")
}

// Entry represents a key-value pair along with its properties.
type Entry struct {
	Value      any
	Properties Properties
}

// CounterValue represents a counter with leasing capabilities.
type CounterValue struct {
	// Hard limit
	Max int64 `json:"max"`

	// Current total acquired slots
	Current int64 `json:"current"`

	// Active leases by ID
	Leases map[string]CounterLease `json:"leases"`

	// Monotonic version for reconciliation
	Version uint64 `json:"version"`

	// Optional, for future RPS windowing
	Window *CounterWindow `json:"window,omitempty"`
}

// CounterInfo provides a summary of the counter's state.
type CounterInfo struct {
	Max       int64  `json:"max"`
	Current   int64  `json:"current"`
	Available int64  `json:"available"`
	Leases    int    `json:"leases"`
	Version   uint64 `json:"version"`
}

// CounterLease represents a lease on a counter.
type CounterLease struct {
	// Number of slots acquired
	Slots int64 `json:"slots"`

	// Owner identity (engine ID, worker ID, etc.)
	Owner string `json:"owner"`

	// When the lease was acquired
	AcquiredAt time.Time `json:"acquired_at"`

	// Lease expiration
	ExpiresAt time.Time `json:"expires_at"`
}

// CounterWindow represents a time window for "rate limiting" or other similar purposes.
type CounterWindow struct {
	WindowSize  time.Duration `json:"window_size"`
	WindowStart time.Time     `json:"window_start"`
	Count       int64         `json:"count"`
}

// CreateCounter creates a new counter in the key-value store.
// It returns true if the counter was created, false if it already exists.
func (kv *KeyValueStore) CreateCounter(
	key string,
	counterValue CounterValue,
	source string,
) bool {
	if kv == nil {
		return false
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	fullKey := createKeyWithCtx(key, "")
	_, exists := kv.store[fullKey]
	if exists {
		return false
	}

	kv.store[fullKey] = Entry{
		Value: counterValue,
		Properties: Properties{
			Persistent:   true,
			Static:       true,
			SessionValid: false,
			Shared:       true,
			Source:       source,
			CtxID:        "",
			Type:         counterName,
		},
	}

	if kv.sharedCallback != nil {
		go kv.sharedCallback("set", key, counterValue, kv.store[fullKey].Properties)
	}

	return true
}

// CreateCounterBase creates a new counter in the key-value store.
func (kv *KeyValueStore) CreateCounterBase(
	key string,
	maxVal int64,
	source string,
) error {
	if kv == nil {
		return errors.New("key-value store is nil")
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	if maxVal <= 0 {
		return errors.New("counter max must be > 0")
	}

	fullKey := createKeyWithCtx(key, "")
	_, exists := kv.store[fullKey]
	if exists {
		return errors.New("counter already exists")
	}

	counter := CounterValue{
		Max:     maxVal,
		Current: 0,
		Leases:  make(map[string]CounterLease),
		Version: 1,
	}

	kv.store[fullKey] = Entry{
		Value: counter,
		Properties: Properties{
			Persistent:   true,
			Static:       true,
			SessionValid: false,
			Shared:       true,
			Source:       source,
			CtxID:        "",
			Type:         counterName,
		},
	}

	if kv.sharedCallback != nil {
		go kv.sharedCallback("set", key, counter, kv.store[fullKey].Properties)
	}

	return nil
}

// WithCounter is a helper function to safely access and modify a counter entry.
// we can't return the counter value directly because we need to ensure thread safety
// Important Note: WithCounter should only be used for mutations, not for read-only access.
// For read-only access, use Get and type assert the value to CounterValue.
func (kv *KeyValueStore) WithCounter(
	key string,
	fn func(entry *Entry, cv *CounterValue) error,
) error {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	fullKey := createKeyWithCtx(key, "")
	entry, ok := kv.store[fullKey]
	if !ok {
		return errors.New("counter not found")
	}

	if entry.Properties.Type != counterName {
		return errors.New("key is not a counter")
	}

	cv, ok := entry.Value.(CounterValue)
	if !ok {
		return errors.New("counter corrupted")
	}

	// mutate safely
	if err := fn(&entry, &cv); err != nil {
		return err
	}

	// write back explicitly
	entry.Value = cv
	kv.store[fullKey] = entry

	return nil
}

// TryAcquire attempts to acquire `slots` capacity from the named counter.
// It is non-blocking and atomic.
// On success it returns (leaseID, true, nil).
// If capacity is insufficient it returns ("", false, nil).
// Errors are returned only for invalid usage or corrupted state.
func (kv *KeyValueStore) TryAcquire(
	key string,
	slots int64,
	ttl time.Duration,
	owner string,
) (string, bool, error) {
	if kv == nil {
		return "", false, errors.New("key-value store is nil")
	}

	if strings.TrimSpace(key) == "" {
		return "", false, errors.New("counter key is empty")
	}
	if slots <= 0 {
		return "", false, errors.New("slots must be > 0")
	}
	if ttl <= 0 {
		return "", false, errors.New("ttl must be > 0")
	}

	now := time.Now()
	var leaseID string
	acquired := false

	var props Properties
	var val any
	err := kv.WithCounter(key, func(entry *Entry, cv *CounterValue) error {
		// --- Sanity checks ---
		if cv.Max <= 0 {
			return errors.New("counter max is invalid")
		}
		if cv.Current < 0 {
			return errors.New("counter current is negative")
		}
		if cv.Current > cv.Max {
			return errors.New("counter current exceeds max")
		}

		// --- Lazy cleanup of expired leases ---
		cleaned := false
		for id, lease := range cv.Leases {
			if now.After(lease.ExpiresAt) {
				cv.Current -= lease.Slots
				delete(cv.Leases, id)
				cleaned = true
			}
		}
		if cleaned {
			cv.Version++
		}

		// Re-check invariant after cleanup
		if cv.Current < 0 {
			return errors.New("counter corrupted after lease cleanup")
		}

		available := cv.Max - cv.Current
		if available < slots {
			// Not enough capacity, fail without modifying state
			acquired = false
			return nil
		}

		// --- Acquire new lease ---
		leaseID = uuid.NewString() // or your preferred ID generator

		cv.Leases[leaseID] = CounterLease{
			Slots:      slots,
			Owner:      owner,
			AcquiredAt: now,
			ExpiresAt:  now.Add(ttl),
		}

		cv.Current += slots
		cv.Version++

		// Get updated entry info for callback
		props = entry.Properties
		val = entry.Value

		// Mark as successfully acquired
		acquired = true
		return nil
	})

	if err != nil {
		return "", false, err
	}

	if !acquired {
		return "", false, nil
	}

	// Notify other nodes only on successful acquisition
	if kv.sharedCallback != nil {
		kv.sharedCallback(
			"update",
			key,
			val,
			props,
		)
	}

	return leaseID, true, nil
}

// Release releases a previously acquired lease from the named counter.
// It is atomic and non-blocking.
// It returns an error if the counter or lease does not exist or if state is corrupted.
func (kv *KeyValueStore) Release(
	key string,
	leaseID string,
) error {
	if kv == nil {
		return errors.New("key-value store is nil")
	}

	if strings.TrimSpace(key) == "" {
		return errors.New("counter key is empty")
	}
	if strings.TrimSpace(leaseID) == "" {
		return errors.New("leaseID is empty")
	}

	var (
		released bool
		props    Properties
		val      any
	)

	err := kv.WithCounter(key, func(entry *Entry, cv *CounterValue) error {
		lease, exists := cv.Leases[leaseID]
		if !exists {
			return errors.New("lease not found")
		}

		// Remove lease
		delete(cv.Leases, leaseID)

		// Update counters
		cv.Current -= lease.Slots
		if cv.Current < 0 {
			return errors.New("counter corrupted: current < 0 after release")
		}

		cv.Version++

		// Capture updated state for callback
		props = entry.Properties
		val = entry.Value

		released = true
		return nil
	})

	if err != nil {
		return err
	}

	if !released {
		// This should never happen, but keep it defensive
		return errors.New("release failed without error")
	}

	// Notify other nodes
	if kv.sharedCallback != nil {
		kv.sharedCallback(
			"update",
			key,
			val,
			props,
		)
	}

	return nil
}

// GetCounterInfo returns a safe, read-only snapshot of a counter state.
func (kv *KeyValueStore) GetCounterInfo(key string) (*CounterInfo, error) {
	if kv == nil {
		return nil, errors.New("key-value store is nil")
	}

	if strings.TrimSpace(key) == "" {
		return nil, errors.New("counter key is empty")
	}

	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	fullKey := createKeyWithCtx(key, "")
	entry, exists := kv.store[fullKey]
	if !exists {
		return nil, errors.New("counter not found")
	}

	if entry.Properties.Type != counterName {
		return nil, errors.New("key is not a counter")
	}

	cv, ok := entry.Value.(CounterValue)
	if !ok {
		return nil, errors.New("counter value corrupted")
	}

	if cv.Current < 0 || cv.Current > cv.Max {
		return nil, errors.New("counter invariant violated")
	}

	info := &CounterInfo{
		Max:       cv.Max,
		Current:   cv.Current,
		Available: cv.Max - cv.Current,
		Leases:    len(cv.Leases),
		Version:   cv.Version,
	}

	return info, nil
}

// SharedCallback is a callback function type for shared key-value store operations.
// It's used to notify the cluster sharing API when a key-value pair is set or updated.
// It needs to be implemented by the code that will use KVStore.
// The callback function should accept the action (set, update or delete), key, value, and properties as parameters.
type SharedCallback func(action, key string, value interface{}, props Properties)

// SetSharedCallback sets the callback function for shared key-value store operations.
// This function should be called by the code that will use KVStore to set the callback.
func (kv *KeyValueStore) SetSharedCallback(callback SharedCallback) {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	kv.sharedCallback = callback
}

// KeyValueStore stores key-value pairs with properties and ensures thread safety.
type KeyValueStore struct {
	store          map[string]Entry
	mutex          sync.RWMutex
	sharedCallback SharedCallback
}

// NewKeyValueStore initializes the key-value store.
func NewKeyValueStore() *KeyValueStore {
	return &KeyValueStore{
		store: make(map[string]Entry),
	}
}

// NewKVStoreProperty initializes a new Properties object.
func NewKVStoreProperty(persistent bool, static bool,
	sessionValid bool, shared bool,
	source string, ctxID string, Type string) Properties {
	return Properties{
		Persistent:   persistent,
		Static:       static,
		SessionValid: sessionValid,
		Source:       source,
		Shared:       shared,
		CtxID:        ctxID,
		Type:         Type,
	}
}

// NewKVStoreEmptyProperty initializes a new Properties object with default values.
func NewKVStoreEmptyProperty() Properties {
	return Properties{
		Persistent:   false,
		Static:       false,
		SessionValid: true,
		Shared:       false,
		Source:       "",
		CtxID:        "",
		Type:         "",
	}
}

// createKeyWithCtx combines the key and CtxID to create a unique key.
func createKeyWithCtx(key string, ctxID string) string {
	return fmt.Sprintf("%s:%s", strings.TrimSpace(key), strings.TrimSpace(ctxID))
}

// Set sets a value (either string or []string) along with its properties for a given key and context.
func (kv *KeyValueStore) Set(key string, value interface{}, properties Properties) error {
	if kv == nil {
		kv = NewKeyValueStore()
	}

	// Check if the key is empty
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("key cannot be empty")
	}

	// Check if value is nil
	if value == nil {
		value = ""
	}

	// Store the type of the value in the properties
	if properties == (Properties{}) {
		properties.Type = reflect.TypeOf(value).String()
	} else if strings.TrimSpace(properties.Type) == "" {
		properties.Type = reflect.TypeOf(value).String()
	}

	fullKey := createKeyWithCtx(key, properties.CtxID)
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	setEntry := true
	if properties.Static {
		properties.Persistent = true
		// Search for existing key with the same context
		if entry, exists := kv.store[fullKey]; exists {
			setEntry = false
			// If the existing entry is not static, update it
			if !entry.Properties.Static {
				kv.store[fullKey] = Entry{
					Value:      value,
					Properties: properties,
				}
			}
		}
	}
	if setEntry {
		kv.store[fullKey] = Entry{
			Value:      value,
			Properties: properties,
		}
	}
	// If the entry is shared, call the shared callback
	if properties.Shared && kv.sharedCallback != nil {
		go kv.sharedCallback("set", key, value, properties)
	}
	return nil
}

// Increment increments the value for a given key and context by a specified step and it's thread safe.
func (kv *KeyValueStore) Increment(key, ctxID string, step int64) (int64, error) {
	if kv == nil {
		kv = NewKeyValueStore()
	}
	fullKey := createKeyWithCtx(key, ctxID)

	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		// If the key does not exist, create it with the step value
		entry = Entry{
			Value:      step,
			Properties: NewKVStoreEmptyProperty(),
		}
		kv.store[fullKey] = entry
		return step, nil
	}

	if entry.Properties.Type == "counter" {
		return 0, errors.New("counter values must use TryAcquire/Release")
	}

	var valInt int64
	switch v := entry.Value.(type) {
	case int:
		valInt = int64(v)
	case int64:
		valInt = v
	case float64:
		valInt = int64(v)
	case float32:
		valInt = int64(v)
	default:
		return 0, fmt.Errorf("value is not numeric")
	}

	valInt += step
	entry.Value = valInt
	kv.store[fullKey] = entry

	// If the entry is shared, call the shared callback
	if kv.store[fullKey].Properties.Shared && kv.sharedCallback != nil {
		go kv.sharedCallback("update", key, entry.Value, entry.Properties)
	}

	return valInt, nil
}

// Decrement decrements the value for a given key and context by a specified step and it's thread safe.
func (kv *KeyValueStore) Decrement(key, ctxID string, step int64) (int64, error) {
	return kv.Increment(key, ctxID, -step)
}

func (kv *KeyValueStore) getProperties(key string, ctxID string) (Properties, error) {
	if kv == nil {
		kv = NewKeyValueStore()
	}

	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		msg := fmt.Sprintf("key '%s' not found for context '%s'", key, ctxID)
		return Properties{}, errors.New(msg)
	}
	return entry.Properties, nil
}

// Get retrieves the value (which could be string or []string) and properties for a given key and context.
func (kv *KeyValueStore) Get(key string, ctxID string) (any, Properties, error) {
	if kv == nil {
		kv = NewKeyValueStore()
	}

	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		msg := fmt.Sprintf("key '%s' not found for context '%s'", key, ctxID)
		return nil, Properties{}, errors.New(msg)
	}
	return entry.Value, entry.Properties, nil
}

// GetBySource retrieves the value and properties for a given key and source.
func (kv *KeyValueStore) GetBySource(key string, source string) (interface{}, Properties, error) {
	if kv == nil {
		kv = NewKeyValueStore()
	}
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	for fullKey, entry := range kv.store {
		if entry.Properties.Source == source && fullKey[:len(key)] == key {
			return entry.Value, entry.Properties, nil
		}
	}
	msg := fmt.Sprintf("key '%s' not found for source '%s'", key, source)
	return nil, Properties{}, errors.New(msg)
}

// GetWithCtx retrieves the value for a given key, considering both Source and CtxID if provided.
func (kv *KeyValueStore) GetWithCtx(key string, source string, ctxID string) (interface{}, Properties, error) {
	if kv == nil {
		kv = NewKeyValueStore()
	}
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		msg := fmt.Sprintf("key '%s' not found for context '%s'", key, ctxID)
		return nil, Properties{}, errors.New(msg)
	}

	if source != "" && entry.Properties.Source != source {
		return nil, Properties{}, errors.New("source mismatch")
	}

	return entry.Value, entry.Properties, nil
}

// Size returns the number of key-value pairs in the store.
func (kv *KeyValueStore) Size() int {
	if kv == nil {
		return 0
	}
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	return len(kv.store)
}

// Delete removes a key-value pair by key and context.
// Flags can be used to specify whether to delete persistent.
// If no flags are provided, only non-persistent entries are deleted.
// Flag[0] set is to delete persistent entries
func (kv *KeyValueStore) Delete(key string, ctxID string, flags ...bool) error {
	if kv == nil {
		return errors.New("key-value store is nil")
	}
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	if _, exists := kv.store[fullKey]; !exists {
		msg := fmt.Sprintf("key '%s' not found for context '%s'", key, ctxID)
		return errors.New(msg)
	}

	if kv.store[fullKey].Properties.Type == counterName {
		return errors.New("cannot delete counter")
	}

	// Check if the entry should be removed
	removeEntry := true
	if len(flags) == 0 {
		if kv.store[fullKey].Properties.Persistent {
			removeEntry = false
		}
	} else {
		if !flags[0] && kv.store[fullKey].Properties.Persistent {
			removeEntry = false
		}
	}
	if removeEntry {
		// If the entry is shared, call the shared callback
		if kv.store[fullKey].Properties.Shared && kv.sharedCallback != nil {
			go kv.sharedCallback("delete", key, kv.store[fullKey].Value, kv.store[fullKey].Properties)
		}
		// Delete the entry
		delete(kv.store, fullKey)
	}
	return nil
}

// DeleteByCID removes all key-value pairs for a given context.
// Flags can be used to specify whether to delete persistent.
// If no flags are provided, only non-persistent entries are deleted.
// Flag[0] set is to delete persistent entries
func (kv *KeyValueStore) DeleteByCID(ctxID string, flags ...bool) {
	if kv == nil {
		return
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if strings.HasSuffix(key, ":"+ctxID) {
			removeEntry := true

			// If no flags are provided, only delete non-persistent entries
			if len(flags) == 0 {
				if kv.store[key].Properties.Persistent {
					removeEntry = false
				}
			} else {
				// Handle persistent flag logic
				if !flags[0] && kv.store[key].Properties.Persistent {
					removeEntry = false
				}
			}

			// Perform the deletion
			if removeEntry {
				// If the entry is shared, call the shared callback
				if kv.store[key].Properties.Shared && kv.sharedCallback != nil {
					go kv.sharedCallback("delete", key, kv.store[key].Value, kv.store[key].Properties)
				}
				// Delete the entry
				delete(kv.store, key)
			}
		}
	}
}

// DeleteNonPersistent removes all key-value pairs that are not persistent.
func (kv *KeyValueStore) DeleteNonPersistent() {
	if kv == nil {
		return
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	for key, entry := range kv.store {
		if !entry.Properties.Persistent {
			// If the entry is shared, call the shared callback
			if entry.Properties.Shared && kv.sharedCallback != nil {
				go kv.sharedCallback("delete", key, entry.Value, entry.Properties)
			}
			// Delete the entry
			delete(kv.store, key)
		}
	}
}

// DeleteNonPersistentByCID removes all key-value pairs for a given context that are not persistent.
func (kv *KeyValueStore) DeleteNonPersistentByCID(ctxID string) {
	if kv == nil {
		return
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if strings.HasSuffix(key, ":"+ctxID) && !kv.store[key].Properties.Persistent {
			// If the entry is shared, call the shared callback
			if kv.store[key].Properties.Shared && kv.sharedCallback != nil {
				go kv.sharedCallback("delete", key, kv.store[key].Value, kv.store[key].Properties)
			}
			// Delete the entry
			delete(kv.store, key)
		}
	}
}

// DeleteAll clears all key-value pairs from the store.
func (kv *KeyValueStore) DeleteAll() {
	if kv == nil {
		return
	}
	if kv.store == nil {
		return
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	foundCounters := false

	// Check for all shared entries and call the shared callback to notify the cluster
	for key, entry := range kv.store {
		if entry.Properties.Type == counterName {
			foundCounters = true
			continue // skip deleting counters
		}
		if entry.Properties.Shared && kv.sharedCallback != nil {
			go kv.sharedCallback("delete", key, entry.Value, entry.Properties)
		}
	}

	// Clear the store
	if !foundCounters {
		kv.store = make(map[string]Entry)
		return
	}
	// We have Counters, so we need to delete entries one by one
	for key := range kv.store {
		if kv.store[key].Properties.Type == counterName {
			continue // skip deleting counters
		}
		delete(kv.store, key)
	}
}

// DeleteAllByCID clears all key-value pairs for a given context from the store.
func (kv *KeyValueStore) DeleteAllByCID(ctxID string) {
	if kv == nil {
		return
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if kv.store[key].Properties.Type == counterName {
			continue // skip deleting counters
		}
		if strings.HasSuffix(key, ":"+ctxID) {
			// If the entry is shared, call the shared callback
			if kv.store[key].Properties.Shared && kv.sharedCallback != nil {
				go kv.sharedCallback("delete", key, kv.store[key].Value, kv.store[key].Properties)
			}
			// Delete the entry
			delete(kv.store, key)
		}
	}
}

// CleanSession clears all key-value pairs that are session valid for the given CID.
func (kv *KeyValueStore) CleanSession(ctxID string) {
	if kv == nil {
		return
	}
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if strings.HasSuffix(key, ":"+ctxID) && kv.store[key].Properties.SessionValid {
			// If the entry is shared, call the shared callback
			if kv.store[key].Properties.Shared && kv.sharedCallback != nil {
				go kv.sharedCallback("delete", key, kv.store[key].Value, kv.store[key].Properties)
			}
			// Delete the entry
			delete(kv.store, key)
		}
		// Clean up also the non-persistent entries that are not session valid
		if strings.HasSuffix(key, ":"+ctxID) && !kv.store[key].Properties.Persistent && !kv.store[key].Properties.SessionValid {
			// If the entry is shared, call the shared callback
			if kv.store[key].Properties.Shared && kv.sharedCallback != nil {
				go kv.sharedCallback("delete", key, kv.store[key].Value, kv.store[key].Properties)
			}
			// Delete the entry
			delete(kv.store, key)
		}
	}
}

// AllKeys returns a slice of all keys (without the CIDs) in the store (ignoring context).
func (kv *KeyValueStore) AllKeys() []string {
	if kv == nil {
		return []string{}
	}
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		if kv.store[key].Properties.CtxID == "" {
			keys = append(keys, key[:len(key)-1]) // strip trailing colon
			continue
		}

		// Remove the context ID from the key
		key = key[:len(key)-len(kv.store[key].Properties.CtxID)-1]
		keys = append(keys, key)
	}
	return keys
}

// AllKeysAndCIDs returns a slice of all keys in the store (ignoring context).
func (kv *KeyValueStore) AllKeysAndCIDs() []string {
	if kv == nil {
		return []string{}
	}
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		keys = append(keys, key)
	}
	return keys
}

// Keys returns a slice of all keys (without the CID) in the store for a given context.
func (kv *KeyValueStore) Keys(ctxID string) []string {
	if kv == nil {
		return []string{}
	}
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		if key[len(key)-len(ctxID):] == ctxID {
			// Remove the context ID from the key
			key = key[:len(key)-len(ctxID)-1]
			keys = append(keys, key)
		}
	}
	return keys
}

// ToJSON converts the key-value store to a JSON string.
// It uses json.Marshal to convert the value to the correct JSON format.
func (kv *KeyValueStore) ToJSON() string {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	// Create a map to hold the JSON structure
	jsonMap := make(map[string]interface{})

	// Iterate through the key-value store and add entries to the map
	for key, entry := range kv.store {
		jsonMap[key] = entry.Value
	}

	// Marshal the map into a JSON string
	jsonBytes, err := json.Marshal(jsonMap)
	if err != nil {
		// If there's an error in marshaling, return an empty JSON object
		return "{}"
	}

	// Return the generated JSON string
	return string(jsonBytes)
}
