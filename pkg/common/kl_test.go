// Package common package is used to store common functions and variables
package common

import (
	"errors"
	"fmt"
	"testing"
)

func TestNewKeyValueStore(t *testing.T) {
	kvStore := NewKeyValueStore()

	if kvStore == nil {
		t.Fatalf("Expected non-nil KeyValueStore, got nil")
	}

	if kvStore.store == nil {
		t.Fatalf("Expected non-nil store map, got nil")
	}

	if len(kvStore.store) != 0 {
		t.Fatalf("Expected empty store map, got %d elements", len(kvStore.store))
	}
}

func TestCreateKeyWithCtx(t *testing.T) {
	tests := []struct {
		key      string
		ctxID    string
		expected string
	}{
		{"username", "123", "username:123"},
		{"email", "456", "email:456"},
		{"", "789", ":789"},
		{"password", "", "password:"},
		{"", "", ":"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			result := createKeyWithCtx(tt.key, tt.ctxID)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestKeyValueStore_Set(t *testing.T) {
	kvStore := NewKeyValueStore()

	tests := []struct {
		key        string
		value      string
		properties Properties
	}{
		{"username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"}},
		{"email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"}},
		{"password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.properties.CtxID), func(t *testing.T) {
			kvStore.Set(tt.key, tt.value, tt.properties)

			fullKey := createKeyWithCtx(tt.key, tt.properties.CtxID)
			kvStore.mutex.RLock()
			entry, exists := kvStore.store[fullKey]
			kvStore.mutex.RUnlock()

			if !exists {
				t.Fatalf("Expected key %s to exist", fullKey)
			}

			if entry.Value != tt.value {
				t.Errorf("Expected value %s, got %s", tt.value, entry.Value)
			}

			if entry.Properties != tt.properties {
				t.Errorf("Expected properties %+v, got %+v", tt.properties, entry.Properties)
			}
		})
	}
}

func TestKeyValueStore_Get(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})

	tests := []struct {
		key      string
		ctxID    string
		expected string
		err      error
	}{
		{"username", "123", "admin", nil},
		{"email", "456", "admin@example.com", nil},
		{"password", "", "secret", nil},
		{"username", "999", "", errors.New("key not found for context")},
		{"nonexistent", "123", "", errors.New("key not found for context")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			value, _, err := kvStore.Get(tt.key, tt.ctxID)

			if err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}

			if value != tt.expected {
				t.Errorf("Expected value %s, got %s", tt.expected, value)
			}
		})
	}
}

func TestKeyValueStore_GetBySource(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "testSource", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "testSource", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "anotherSource", CtxID: ""})

	tests := []struct {
		key      string
		source   string
		expected string
		err      error
	}{
		{"username", "testSource", "admin", nil},
		{"email", "testSource", "admin@example.com", nil},
		{"password", "anotherSource", "secret", nil},
		{"username", "nonexistentSource", "", errors.New("key not found for the specified source")},
		{"nonexistent", "testSource", "", errors.New("key not found for the specified source")},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.source), func(t *testing.T) {
			if i < kvStore.Size() {
				value, _, err := kvStore.GetBySource(tt.key, tt.source)

				if err != nil && err.Error() != tt.err.Error() {
					t.Fatalf("Expected error %v, got %v", tt.err, err)
				}

				if value != tt.expected {
					t.Errorf("Expected value %s, got %s", tt.expected, value)
				}
			}
		})
	}
}

func TestKeyValueStore_Size(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Test empty store
	if size := kvStore.Size(); size != 0 {
		t.Fatalf("Expected size 0, got %d", size)
	}

	// Add some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})

	// Test size after adding entries
	if size := kvStore.Size(); size != 3 {
		t.Fatalf("Expected size 3, got %d", size)
	}

	// Delete an entry and test size
	err := kvStore.Delete("username", "123")
	if err != nil {
		t.Fatalf("Error deleting key: %v", err)
	}
	if size := kvStore.Size(); size != 2 {
		t.Fatalf("Expected size 2, got %d", size)
	}

	// Clear all entries and test size
	kvStore.DeleteAll()
	if size := kvStore.Size(); size != 0 {
		t.Fatalf("Expected size 0, got %d", size)
	}
}

func TestKeyValueStore_GetWithCtx(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "testSource", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "testSource", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "anotherSource", CtxID: ""})

	tests := []struct {
		key      string
		source   string
		ctxID    string
		expected string
		err      error
	}{
		{"username", "testSource", "123", "admin", nil},
		{"email", "testSource", "456", "admin@example.com", nil},
		{"password", "anotherSource", "", "secret", nil},
		{"username", "wrongSource", "123", "", errors.New("source mismatch")},
		{"username", "testSource", "999", "", errors.New("key not found")},
		{"nonexistent", "testSource", "123", "", errors.New("key not found")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s_%s", tt.key, tt.source, tt.ctxID), func(t *testing.T) {
			value, _, err := kvStore.GetWithCtx(tt.key, tt.source, tt.ctxID)

			if err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}

			if value != tt.expected {
				t.Errorf("Expected value %s, got %s", tt.expected, value)
			}
		})
	}
}

func TestKeyValueStore_Delete(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})

	tests := []struct {
		key      string
		ctxID    string
		expected error
	}{
		{"username", "123", nil},
		{"email", "456", nil},
		{"password", "", nil},
		{"nonexistent", "123", errors.New("key not found for context")},
		{"username", "999", errors.New("key not found for context")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			err := kvStore.Delete(tt.key, tt.ctxID)

			if err != nil && err.Error() != tt.expected.Error() {
				t.Fatalf("Expected error %v, got %v", tt.expected, err)
			}

			if err == nil {
				// Verify the key has been deleted
				_, _, getErr := kvStore.Get(tt.key, tt.ctxID)
				if getErr == nil {
					t.Errorf("Expected key %s to be deleted, but it still exists", tt.key)
				}
			}
		})
	}
}

func TestKeyValueStore_DeleteNonPersistent(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	kvStore.Set("session", "xyz", Properties{Persistent: false, Source: "test", CtxID: "789"})

	// Ensure the store has the expected number of entries before deletion
	if size := kvStore.Size(); size != 4 {
		t.Fatalf("Expected size 4, got %d", size)
	}

	// Perform the deletion of non-persistent entries
	kvStore.DeleteNonPersistent()

	// Ensure the store has the expected number of entries after deletion
	if size := kvStore.Size(); size != 2 {
		t.Fatalf("Expected size 2, got %d", size)
	}

	// Verify that only persistent entries remain
	tests := []struct {
		key      string
		ctxID    string
		expected string
		err      error
	}{
		{"username", "123", "admin", nil},
		{"password", "", "secret", nil},
		{"email", "456", "", errors.New("key not found for context")},
		{"session", "789", "", errors.New("key not found for context")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			value, _, err := kvStore.Get(tt.key, tt.ctxID)

			if err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}

			if value != tt.expected {
				t.Errorf("Expected value %s, got %s", tt.expected, value)
			}
		})
	}
}

func TestKeyValueStore_DeleteAll(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})

	// Ensure the store has the expected number of entries before deletion
	if size := kvStore.Size(); size != 3 {
		t.Fatalf("Expected size 3, got %d", size)
	}

	// Perform the deletion of all entries
	kvStore.DeleteAll()

	// Ensure the store is empty after deletion
	if size := kvStore.Size(); size != 0 {
		t.Fatalf("Expected size 0, got %d", size)
	}

	// Verify that all entries have been deleted
	tests := []struct {
		key   string
		ctxID string
		err   error
	}{
		{"username", "123", errors.New("key not found for context")},
		{"email", "456", errors.New("key not found for context")},
		{"password", "", errors.New("key not found for context")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			_, _, err := kvStore.Get(tt.key, tt.ctxID)

			if err == nil || err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}
		})
	}
}

func TestKeyValueStore_Keys(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Test with an empty store
	keys := kvStore.Keys()
	if len(keys) != 0 {
		t.Fatalf("Expected 0 keys, got %d", len(keys))
	}

	// Add some entries
	kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})

	// Test with a populated store
	keys = kvStore.Keys()
	expectedKeys := []string{
		createKeyWithCtx("username", "123"),
		createKeyWithCtx("email", "456"),
		createKeyWithCtx("password", ""),
	}

	if len(keys) != len(expectedKeys) {
		t.Fatalf("Expected %d keys, got %d", len(expectedKeys), len(keys))
	}

	for _, expectedKey := range expectedKeys {
		found := false
		for _, key := range keys {
			if key == expectedKey {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected key %s not found in keys", expectedKey)
		}
	}
}
