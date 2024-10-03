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
	Persistent bool   `yaml:"persistent"` // Whether the entry should be persistent
	Static     bool   `yaml:"static"`     // Whether the entry should be static
	Source     string `yaml:"source"`     // The source of the key-value entry
	CtxID      string // Context ID for more specific identification
	Type       string // The type of the stored value (e.g., "string", "[]string")
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
func NewKVStoreProperty(persistent bool, static bool, source string, ctxID string, valueType string) Properties {
	return Properties{
		Persistent: persistent,
		Static:     static,
		Source:     source,
		CtxID:      ctxID,
		Type:       valueType, // Store the original type
	}
}

// createKeyWithCtx combines the key and CtxID to create a unique key.
func createKeyWithCtx(key string, ctxID string) string {
	return fmt.Sprintf("%s:%s", key, ctxID)
}

// Set sets a value (either string or []string) along with its properties for a given key and context.
func (kv *KeyValueStore) Set(key string, value interface{}, properties Properties) error {
	// Store the type of the value in the properties
	properties.Type = reflect.TypeOf(value).String()

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

// Get retrieves the value (which could be string or []string) and properties for a given key and context.
func (kv *KeyValueStore) Get(key string, ctxID string) (interface{}, Properties, error) {
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

	for key := range kv.store {
		if strings.HasSuffix(key, ctxID) {
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

// DeleteAll clears all key-value pairs from the store.
func (kv *KeyValueStore) DeleteAll() {
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	kv.store = make(map[string]Entry)
}

// AllKeys returns a slice of all keys in the store (ignoring context).
func (kv *KeyValueStore) AllKeys() []string {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		keys = append(keys, key)
	}
	return keys
}

// Keys returns a slice of all keys in the store for a given context.
func (kv *KeyValueStore) Keys(ctxID string) []string {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		if key[len(key)-len(ctxID):] == ctxID {
			keys = append(keys, key)
		}
	}
	return keys
}
