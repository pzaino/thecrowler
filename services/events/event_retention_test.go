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

package main

// Event retention contract (WP-REL230-RC2 section 4D).
//
// Two independent mechanisms act on the Events table:
//
//  1. the consumer acknowledgement policy, events.automatic_events_removal;
//  2. the janitor, which deletes rows whose expires_at has passed.
//
// Every event is created with a finite expires_at (two minutes for the crawler
// completion event, five minutes by default in CreateEvent), so "never" means
// "the consumer never removes or expires the row", not "the row is permanent".
// This is the documented behaviour; the RC1 observation of crawl_completed rows
// disappearing under "never" was TTL expiry by the janitor, not a defect.

import (
	"testing"
)

func TestEventRemovalActionPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		policy     string
		success    bool
		wantAction eventRemovalOutcome
	}{
		{"empty policy expires", "", true, eventRemovalExpire},
		{"empty policy expires on failure", "", false, eventRemovalExpire},
		{"always deletes on success", "always", true, eventRemovalDelete},
		{"always deletes on failure", "always", false, eventRemovalDelete},
		{"on_success deletes on success", "on_success", true, eventRemovalDelete},
		{"on_success keeps on failure", "on_success", false, eventRemovalKeep},
		{"on_failure deletes on failure", "on_failure", false, eventRemovalDelete},
		{"on_failure keeps on success", "on_failure", true, eventRemovalKeep},
		{"never keeps on success", "never", true, eventRemovalKeep},
		{"never keeps on failure", "never", false, eventRemovalKeep},
		{"unknown keeps", "something-else", true, eventRemovalKeep},
		{"policy is trimmed and case-folded", "  On_Success  ", true, eventRemovalDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventRemovalAction(tc.policy, tc.success); got != tc.wantAction {
				t.Fatalf("policy=%q success=%v action=%v want %v",
					tc.policy, tc.success, got, tc.wantAction)
			}
		})
	}
}

// The janitor itself is covered by
// event_retention_postgres_integration_test.go. It uses `now()` in SQL, which
// PostgreSQL and MySQL accept but SQLite does not; the SQLite janitor therefore
// logs "no such function: now" and deletes nothing. That portability gap is
// pre-existing, unrelated to this release gate, and recorded as a P3 limitation
// rather than fixed here.
