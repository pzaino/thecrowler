// Package common package is used to store common functions and variables
package common

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
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
		key          string
		value        interface{} // Can be either string or []string
		properties   Properties
		expectedType string // Expected type for the stored value
	}{
		{"username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"}, "string"},
		{"email", []string{"admin@example.com", "user@example.com"}, Properties{Persistent: false, Source: "test", CtxID: "456"}, "[]string"},
		{"password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""}, "string"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.properties.CtxID), func(t *testing.T) {
			err := kvStore.Set(tt.key, tt.value, tt.properties)
			if err != nil {
				t.Fatalf("Error setting key: %v", err)
			}

			fullKey := createKeyWithCtx(tt.key, tt.properties.CtxID)
			kvStore.mutex.RLock()
			entry, exists := kvStore.store[fullKey]
			kvStore.mutex.RUnlock()

			if !exists {
				t.Fatalf("Expected key %s to exist", fullKey)
			}

			if !reflect.DeepEqual(entry.Value, tt.value) {
				t.Errorf("Expected value %v, got %v", tt.value, entry.Value)
			}

			// Check if the stored type matches the expected type
			if entry.Properties.Type != tt.expectedType {
				t.Errorf("Expected type %v, got %v", tt.expectedType, entry.Properties.Type)
			}

			if entry.Properties.Source != tt.properties.Source || entry.Properties.Persistent != tt.properties.Persistent {
				t.Errorf("Expected properties %+v, got %+v", tt.properties, entry.Properties)
			}
		})
	}
}

func TestKeyValueStore_Get(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", []string{"admin@example.com", "user@example.com"}, Properties{Persistent: false, Source: "test", CtxID: "456", Type: "[]string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: "", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	tests := []struct {
		key          string
		ctxID        string
		expected     interface{} // Can be string or []string
		expectedType string      // Expected type for the stored value
		err          error
	}{
		{"username", "123", "admin", "string", nil},
		{"email", "456", []string{"admin@example.com", "user@example.com"}, "[]string", nil},
		{"password", "", "secret", "string", nil},
		{"username", "999", nil, "", errors.New("key not found for context")},
		{"nonexistent", "123", nil, "", errors.New("key not found for context")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			value, props, err := kvStore.Get(tt.key, tt.ctxID)

			if err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}

			if !reflect.DeepEqual(value, tt.expected) {
				t.Errorf("Expected value %v, got %v", tt.expected, value)
			}

			// Check if the stored type matches the expected type
			if props.Type != tt.expectedType {
				t.Errorf("Expected type %v, got %v", tt.expectedType, props.Type)
			}
		})
	}
}

func TestKeyValueStore_GetWithCtx(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "testSource", CtxID: "123", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "testSource", CtxID: "456", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "anotherSource", CtxID: "", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	tests := []struct {
		key          string
		source       string
		ctxID        string
		expected     interface{}
		expectedType string // Expected type for the stored value
		err          error
	}{
		{"username", "testSource", "123", "admin", "string", nil},
		{"email", "testSource", "456", "admin@example.com", "string", nil},
		{"password", "anotherSource", "", "secret", "string", nil},
		{"username", "wrongSource", "123", nil, "", errors.New("source mismatch")},
		{"username", "testSource", "999", nil, "", errors.New("key not found")},
		{"nonexistent", "testSource", "123", nil, "", errors.New("key not found")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s_%s", tt.key, tt.source, tt.ctxID), func(t *testing.T) {
			value, props, err := kvStore.GetWithCtx(tt.key, tt.source, tt.ctxID)

			// Check error
			if err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}

			// Use reflect.DeepEqual to compare the values
			if !reflect.DeepEqual(value, tt.expected) {
				t.Errorf("Expected value %v, got %v", tt.expected, value)
			}

			// Check if the stored type matches the expected type
			if props.Type != tt.expectedType {
				t.Errorf("Expected type %v, got %v", tt.expectedType, props.Type)
			}
		})
	}
}

func TestKeyValueStore_GetBySource(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "testSource", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", []string{"admin@example.com", "user@example.com"}, Properties{Persistent: false, Source: "testSource", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "anotherSource", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	tests := []struct {
		key      string
		source   string
		expected interface{} // Can be string or []string
		err      error
	}{
		{"username", "testSource", "admin", nil},
		{"email", "testSource", []string{"admin@example.com", "user@example.com"}, nil},
		{"password", "anotherSource", "secret", nil},
		{"username", "nonexistentSource", nil, errors.New("key not found for the specified source")},
		{"nonexistent", "testSource", nil, errors.New("key not found for the specified source")},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.source), func(t *testing.T) {
			if i < kvStore.Size() {
				value, _, err := kvStore.GetBySource(tt.key, tt.source)

				if err != nil && err.Error() != tt.err.Error() {
					t.Fatalf("Expected error %v, got %v", tt.err, err)
				}

				if !reflect.DeepEqual(value, tt.expected) {
					t.Errorf("Expected value %v, got %v", tt.expected, value)
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
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	// Test size after adding entries
	if size := kvStore.Size(); size != 3 {
		t.Fatalf("Expected size 3, got %d", size)
	}

	// Delete an entry and test size
	err = kvStore.Delete("username", "123", true)
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

func TestKeyValueStore_Delete(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

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
			err := kvStore.Delete(tt.key, tt.ctxID, true)

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
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("session", "xyz", Properties{Persistent: false, Source: "test", CtxID: "789"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

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
		expected interface{}
		err      error
	}{
		{"username", "123", "admin", nil},
		{"password", "", "secret", nil},
		{"email", "456", nil, errors.New("key not found for context")},   // Changed to nil for deleted keys
		{"session", "789", nil, errors.New("key not found for context")}, // Changed to nil for deleted keys
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%s", tt.key, tt.ctxID), func(t *testing.T) {
			value, _, err := kvStore.Get(tt.key, tt.ctxID)

			// Check if the error matches
			if err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("Expected error %v, got %v", tt.err, err)
			}

			// Only check the value if there's no error (i.e., the key exists)
			if err == nil && !reflect.DeepEqual(value, tt.expected) {
				t.Errorf("Expected value %v, got %v", tt.expected, value)
			}
		})
	}
}

func TestKeyValueStore_DeleteAll(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

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

func TestKeyValueStore_AllKeysAndCIDs(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Test with an empty store
	keys := kvStore.AllKeysAndCIDs()
	if len(keys) != 0 {
		t.Fatalf("Expected 0 keys, got %d", len(keys))
	}

	// Add some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	// Test with a populated store
	keys = kvStore.AllKeysAndCIDs()
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

func TestNewKVStoreProperty(t *testing.T) {
	tests := []struct {
		persistent   bool
		static       bool
		SessionValid bool
		Shared       bool
		source       string
		ctxID        string
		expected     Properties
	}{
		{true, false, true, false, "source1", "ctx1", Properties{Persistent: true, Static: false, SessionValid: true, Shared: false, Source: "source1", CtxID: "ctx1", Type: "string"}},
		{false, true, true, false, "source2", "ctx2", Properties{Persistent: false, Static: true, SessionValid: true, Shared: false, Source: "source2", CtxID: "ctx2", Type: "string"}},
		{true, true, true, false, "source3", "ctx3", Properties{Persistent: true, Static: true, SessionValid: true, Shared: false, Source: "source3", CtxID: "ctx3", Type: "string"}},
		{false, false, true, false, "source4", "ctx4", Properties{Persistent: false, Static: false, SessionValid: true, Shared: false, Source: "source4", CtxID: "ctx4", Type: "string"}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v_%v_%v_%v_%s_%s_%s", tt.persistent, tt.static, tt.SessionValid, tt.Shared, tt.source, tt.ctxID, "string"), func(t *testing.T) {
			result := NewKVStoreProperty(tt.persistent, tt.static, tt.SessionValid, tt.Shared, tt.source, tt.ctxID, "string")
			if result != tt.expected {
				t.Errorf("expected %+v, got %+v", tt.expected, result)
			}
		})
	}
}

func TestKeyValueStore_Keys(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Test with an empty store
	keys := kvStore.Keys("123")
	if len(keys) != 0 {
		t.Fatalf("Expected 0 keys, got %d", len(keys))
	}

	// Add some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	// Test with a populated store for a specific context
	keys = kvStore.Keys("123")
	expectedKeys := []string{
		"username",
		"password",
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

	// Test with a different context
	keys = kvStore.Keys("456")
	expectedKeys = []string{
		"email",
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

func TestKeyValueStore_DeleteByCID(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Static: false, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Static: false, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("password", "secret", Properties{Persistent: true, Static: true, Source: "test", CtxID: "456"}) // Static (implicitly persistent)
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("session", "xyz", Properties{Persistent: false, Static: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	tests := []struct {
		ctxID       string
		flags       []bool
		expectedRem int // Expected number of remaining entries for the given CtxID
	}{
		{"123", nil, 1},          // Only non-persistent entries should be deleted, 1 persistent remains
		{"456", nil, 1},          // Only non-persistent entries should be deleted, 1 persistent remains
		{"456", []bool{true}, 0}, // All entries should be deleted
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("DeleteByCID_%s", tt.ctxID), func(t *testing.T) {
			kvStore.DeleteByCID(tt.ctxID, tt.flags...)

			// Count the number of remaining entries for the given CtxID
			remaining := 0
			for key := range kvStore.store {
				parts := strings.Split(key, ":")
				if len(parts) == 2 && parts[1] == tt.ctxID {
					remaining++
				}
			}

			// Verify the number of remaining entries for the specific CtxID
			if remaining != tt.expectedRem {
				t.Errorf("Expected %d remaining entries for CtxID %s, got %d", tt.expectedRem, tt.ctxID, remaining)
			}
		})
	}
}

func TestKeyValueStore_DeleteNonPersistentByCID(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("session", "xyz", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	tests := []struct {
		ctxID       string
		expectedRem int // Expected number of remaining entries for the given CtxID
	}{
		{"123", 1}, // Only persistent entry should remain
		{"456", 1}, // Only persistent entry should remain
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("DeleteNonPersistentByCID_%s", tt.ctxID), func(t *testing.T) {
			kvStore.DeleteNonPersistentByCID(tt.ctxID)

			// Count the number of remaining entries for the given CtxID
			remaining := 0
			for key := range kvStore.store {
				parts := strings.Split(key, ":")
				if len(parts) == 2 && parts[1] == tt.ctxID {
					remaining++
				}
			}

			// Verify the number of remaining entries for the specific CtxID
			if remaining != tt.expectedRem {
				t.Errorf("Expected %d remaining entries for CtxID %s, got %d", tt.expectedRem, tt.ctxID, remaining)
			}
		})
	}
}

func TestKeyValueStore_AllKeys(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Test with an empty store
	keys := kvStore.AllKeys()
	if len(keys) != 0 {
		t.Fatalf("Expected 0 keys, got %d", len(keys))
	}

	// Add some entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Source: "test", CtxID: "123"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("email", "admin@example.com", Properties{Persistent: false, Source: "test", CtxID: "456"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}
	err = kvStore.Set("password", "secret", Properties{Persistent: true, Source: "test", CtxID: ""})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	// Test with a populated store
	keys = kvStore.AllKeys()
	expectedKeys := []string{"username", "email", "password"}

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

func TestKeyValueStore_ToJSON(t *testing.T) {
	kvStore := NewKeyValueStore()

	// Prepopulate the store with string and []string entries
	err := kvStore.Set("username", "admin", Properties{Persistent: true, Static: false, Source: "test", CtxID: "123", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("email", []string{"admin@example.com", "user@example.com"}, Properties{Persistent: false, Static: false, Source: "test", CtxID: "456", Type: "[]string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	err = kvStore.Set("password", "secret", Properties{Persistent: true, Static: false, Source: "test", CtxID: "", Type: "string"})
	if err != nil {
		t.Fatalf("Error setting key: %v", err)
	}

	expectedJSON := `{"email:456":["admin@example.com","user@example.com"],"password:":"secret","username:123":"admin"}`

	// Convert the store to JSON
	jsonResult := kvStore.ToJSON()

	// Check if the JSON output matches the expected result
	if jsonResult != expectedJSON {
		t.Errorf("Expected JSON %s, got %s", expectedJSON, jsonResult)
	}
}
