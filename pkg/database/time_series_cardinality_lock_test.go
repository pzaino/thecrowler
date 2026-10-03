// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import "testing"

func TestPostgresCardinalityIdentityLockKey(t *testing.T) {
	base := postgresCardinalityIdentityLockKey("TimeSeriesDimensionSlots", 42, "username", "identity-a")
	if repeat := postgresCardinalityIdentityLockKey("TimeSeriesDimensionSlots", 42, "username", "identity-a"); repeat != base {
		t.Fatalf("lock key is not deterministic: got %d then %d", base, repeat)
	}

	tests := []struct {
		name string
		key  int64
	}{
		{"table", postgresCardinalityIdentityLockKey("TimeSeriesSeriesSlots", 42, "username", "identity-a")},
		{"metric", postgresCardinalityIdentityLockKey("TimeSeriesDimensionSlots", 43, "username", "identity-a")},
		{"dimension", postgresCardinalityIdentityLockKey("TimeSeriesDimensionSlots", 42, "media_pk", "identity-a")},
		{"identity", postgresCardinalityIdentityLockKey("TimeSeriesDimensionSlots", 42, "username", "identity-b")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.key == base {
				t.Fatalf("changed %s produced the same lock key %d", test.name, base)
			}
		})
	}
}
