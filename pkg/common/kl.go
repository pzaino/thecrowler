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
	"sync"
)

var (
	// KVStore is the global key-value store
	KVStore *KeyValueStore
)

// Properties defines the additional attributes for each key-value entry.
type Properties struct {
	Persistent bool   // Whether the entry should be persistent
	Source     string // The source of the key-value entry
	CtxID      string // Context ID for more specific identification
}

// Entry represents a key-value pair along with its properties.
type Entry struct {
	Value      string
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

// createKeyWithCtx combines the key and CtxID to create a unique key.
func createKeyWithCtx(key string, ctxID string) string {
	return fmt.Sprintf("%s:%s", key, ctxID)
}

// Set sets a value along with its properties for a given key and context.
func (kv *KeyValueStore) Set(key string, value string, properties Properties) {
	fullKey := createKeyWithCtx(key, properties.CtxID)
	kv.mutex.Lock()
	defer kv.mutex.Unlock()
	kv.store[fullKey] = Entry{
		Value:      value,
		Properties: properties,
	}
}

// Size returns the number of key-value pairs in the store.
func (kv *KeyValueStore) Size() int {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()
	return len(kv.store)
}

// Get retrieves the value and properties for a given key and context.
func (kv *KeyValueStore) Get(key string, ctxID string) (string, Properties, error) {
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		return "", Properties{}, errors.New("key not found for context")
	}
	return entry.Value, entry.Properties, nil
}

// GetBySource retrieves the value and properties for a given key and source.
func (kv *KeyValueStore) GetBySource(key string, source string) (string, Properties, error) {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	for fullKey, entry := range kv.store {
		if entry.Properties.Source == source && fullKey[:len(key)] == key {
			return entry.Value, entry.Properties, nil
		}
	}
	return "", Properties{}, errors.New("key not found for the specified source")
}

// GetWithCtx retrieves the value for a given key, considering both Source and CtxID if provided.
func (kv *KeyValueStore) GetWithCtx(key string, source string, ctxID string) (string, Properties, error) {
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	entry, exists := kv.store[fullKey]
	if !exists {
		return "", Properties{}, errors.New("key not found")
	}

	if source != "" && entry.Properties.Source != source {
		return "", Properties{}, errors.New("source mismatch")
	}

	return entry.Value, entry.Properties, nil
}

// Delete removes a key-value pair by key and context.
func (kv *KeyValueStore) Delete(key string, ctxID string) error {
	fullKey := createKeyWithCtx(key, ctxID)
	kv.mutex.Lock()
	defer kv.mutex.Unlock()

	if _, exists := kv.store[fullKey]; !exists {
		return errors.New("key not found for context")
	}

	delete(kv.store, fullKey)
	return nil
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

// Keys returns a slice of all keys in the store (ignoring context).
func (kv *KeyValueStore) Keys() []string {
	kv.mutex.RLock()
	defer kv.mutex.RUnlock()

	keys := make([]string, 0, len(kv.store))
	for key := range kv.store {
		keys = append(keys, key)
	}
	return keys
}

/*
func main() {
     kvStore := NewKeyValueStore()

    // Simulate concurrent writing with different contexts
    var wg sync.WaitGroup
    wg.Add(2)

    // Thread 1 with CtxID "123"
    go func() {
        defer wg.Done()
        kvStore.Set("username", "admin", "123")
        fmt.Println("Thread 1 finished writing")
    }()

    // Thread 2 with CtxID "456"
    go func() {
        defer wg.Done()
        kvStore.Set("username", "user2", "456")
        fmt.Println("Thread 2 finished writing")
    }()

    wg.Wait()

    // Retrieve values from different contexts
    user1, _ := kvStore.Get("username", "123")
    user2, _ := kvStore.Get("username", "456")

    fmt.Println("User 1:", user1)
    fmt.Println("User 2:", user2)
}
*/
