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
)

// KVStore is the global key-value store
var KVStore *KeyValueStore

// Properties defines the additional attributes for each key-value entry.
type Properties struct {
	Persistent   bool   `yaml:"persistent"`    // Whether the entry should be persistent
	Static       bool   `yaml:"static"`        // Whether the entry should be static
	SessionValid bool   `yaml:"session_valid"` // Whether the entry should be valid for the session
	Source       string `yaml:"source"`        // The source of the key-value entry
	CtxID        string // Context ID for more specific identification
	Type         string // The type of the stored value (e.g., "string", "[]string")
}

// Entry represents a key-value pair along with its properties.
type Entry struct {
	Value      interface{}
	Properties Properties
}

// KeyValueStore stores key-value pairs with properties and ensures thread safety.
type KeyValueStore struct {
	store map[string]Entry
	mutex sync.RWMutex
}

// NewKeyValueStore initializes the key-value store.
func NewKeyValueStore() *KeyValueStore {
	return &KeyValueStore{
		store: make(map[string]Entry),
	}
}

// NewKVStoreProperty initializes a new Properties object.
func NewKVStoreProperty(persistent bool, static bool, sessionValid bool, source string, ctxID string, Type string) Properties {
	return Properties{
		Persistent:   persistent,
		Static:       static,
		SessionValid: sessionValid,
		Source:       source,
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
	return nil
}

// Increment increments the value for a given key and context by a specified step and it's thread safe.
func (kv *KeyValueStore) Increment(key, ctxID string, step int64) (int64, error) {
	fullKey := createKeyWithCtx(key, ctxID)

	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		entry = Entry{
			Value:      step,
			Properties: NewKVStoreEmptyProperty(),
		}
		kv.store[fullKey] = entry
		return step, nil
	}

	var valInt int64
	switch v := entry.Value.(type) {
	case int:
		valInt = int64(v)
	case int64:
		valInt = v
	case float64:
		valInt = int64(v)
	default:
		return 0, fmt.Errorf("value is not numeric")
	}

	valInt += step
	entry.Value = valInt
	kv.store[fullKey] = entry

	return valInt, nil
}

// Decrement decrements the value for a given key and context by a specified step and it's thread safe.
func (kv *KeyValueStore) Decrement(key, ctxID string, step int64) (int64, error) {
	return kv.Increment(key, ctxID, -step)
}

// Get retrieves the value (which could be string or []string) and properties for a given key and context.
func (kv *KeyValueStore) Get(key string, ctxID string) (interface{}, Properties, error) {
	if kv == nil {
		kv = NewKeyValueStore()
	}

	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		return nil, Properties{}, errors.New("key not found for context")
	}
	return entry.Value, entry.Properties, nil
}

// GetBySource retrieves the value and properties for a given key and source.
func (kv *KeyValueStore) GetBySource(key string, source string) (interface{}, Properties, error) {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	for fullKey, entry := range kv.store {
		if entry.Properties.Source == source && fullKey[:len(key)] == key {
			return entry.Value, entry.Properties, nil
		}
	}
	return nil, Properties{}, errors.New("key not found for the specified source")
}

// GetWithCtx retrieves the value for a given key, considering both Source and CtxID if provided.
func (kv *KeyValueStore) GetWithCtx(key string, source string, ctxID string) (interface{}, Properties, error) {
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		return nil, Properties{}, errors.New("key not found")
	}

	if source != "" && entry.Properties.Source != source {
		return nil, Properties{}, errors.New("source mismatch")
	}

	return entry.Value, entry.Properties, nil
}

// Size returns the number of key-value pairs in the store.
func (kv *KeyValueStore) Size() int {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	return len(kv.store)
}

// Delete removes a key-value pair by key and context.
// Flags can be used to specify whether to delete persistent.
// If no flags are provided, only non-persistent entries are deleted.
// Flag[0] set is to delete persistent entries
func (kv *KeyValueStore) Delete(key string, ctxID string, flags ...bool) error {
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	if _, exists := kv.store[fullKey]; !exists {
		return errors.New("key not found for context")
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
		delete(kv.store, fullKey)
	}
	return nil
}

// DeleteByCID removes all key-value pairs for a given context.
// Flags can be used to specify whether to delete persistent.
// If no flags are provided, only non-persistent entries are deleted.
// Flag[0] set is to delete persistent entries
func (kv *KeyValueStore) DeleteByCID(ctxID string, flags ...bool) {
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
				delete(kv.store, key)
			}
		}
	}
}

// DeleteNonPersistent removes all key-value pairs that are not persistent.
func (kv *KeyValueStore) DeleteNonPersistent() {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	for key, entry := range kv.store {
		if !entry.Properties.Persistent {
			delete(kv.store, key)
		}
	}
}

// DeleteNonPersistentByCID removes all key-value pairs for a given context that are not persistent.
func (kv *KeyValueStore) DeleteNonPersistentByCID(ctxID string) {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if strings.HasSuffix(key, ":"+ctxID) && !kv.store[key].Properties.Persistent {
			delete(kv.store, key)
		}
	}
}

// DeleteAll clears all key-value pairs from the store.
func (kv *KeyValueStore) DeleteAll() {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	kv.store = make(map[string]Entry)
}

// DeleteAllByCID clears all key-value pairs for a given context from the store.
func (kv *KeyValueStore) DeleteAllByCID(ctxID string) {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if strings.HasSuffix(key, ":"+ctxID) {
			delete(kv.store, key)
		}
	}
}

// CleanSession clears all key-value pairs that are session valid for the given CID.
func (kv *KeyValueStore) CleanSession(ctxID string) {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	ctxID = strings.TrimSpace(ctxID)
	for key := range kv.store {
		if strings.HasSuffix(key, ":"+ctxID) && kv.store[key].Properties.SessionValid {
			delete(kv.store, key)
		}
		// Clean up also the non-persistent entries that are not session valid
		if strings.HasSuffix(key, ":"+ctxID) && !kv.store[key].Properties.Persistent && !kv.store[key].Properties.SessionValid {
			delete(kv.store, key)
		}
	}
}

// AllKeys returns a slice of all keys (without the CIDs) in the store (ignoring context).
func (kv *KeyValueStore) AllKeys() []string {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		// Remove the context ID from the key
		key = key[:len(key)-len(kv.store[key].Properties.CtxID)-1]
		keys = append(keys, key)
	}
	return keys
}

// AllKeysAndCIDs returns a slice of all keys in the store (ignoring context).
func (kv *KeyValueStore) AllKeysAndCIDs() []string {
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
