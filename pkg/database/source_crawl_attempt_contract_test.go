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

package database

import (
	"bytes"
	"crypto/rand"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var sourceCrawlAttemptTestNow = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

func validSourceCrawlAttempt() sourceCrawlAttempt {
	return sourceCrawlAttempt{
		AttemptID:      strings.Repeat("a", 64),
		ClaimRequestID: strings.Repeat("f", 64),
		SourceID:       42,
		Generation:     7,
		EngineID:       "engine-1",
		State:          sourceCrawlAttemptClaimed,
		ClaimedAt:      sourceCrawlAttemptTestNow,
		LeaseExpiresAt: sourceCrawlAttemptTestNow.Add(5 * time.Minute),
	}
}

func validSourceCrawlBinding() *sourceCrawlOperationBinding {
	return &sourceCrawlOperationBinding{
		OperationID:     strings.Repeat("b", 64),
		RequestID:       strings.Repeat("c", 64),
		RunID:           "run-1",
		AgentID:         "agent-1",
		Owner:           "owner-1",
		PrincipalUserID: 7,
		ApprovalID:      strings.Repeat("d", 64),
		ApprovalSHA256:  strings.Repeat("e", 64),
	}
}

func TestSourceCrawlAttemptIDIsUnguessableAndBounded(t *testing.T) {
	id, err := newSourceCrawlAttemptID(nil)
	if err != nil {
		t.Fatalf("generate attempt ID: %v", err)
	}
	if len(id) != 64 || strings.ToLower(id) != id {
		t.Fatalf("attempt ID = %q", id)
	}
	second, err := newSourceCrawlAttemptID(rand.Reader)
	if err != nil {
		t.Fatalf("generate second attempt ID: %v", err)
	}
	if id == second {
		t.Fatal("two attempt IDs collided")
	}
	if _, err := newSourceCrawlAttemptID(bytes.NewReader(make([]byte, 8))); err == nil {
		t.Fatal("short randomness source was accepted")
	}
}

func TestSourceCrawlAttemptContract(t *testing.T) {
	t.Run("unattributed valid", func(t *testing.T) {
		if err := validSourceCrawlAttempt().validate(); err != nil {
			t.Fatalf("valid unattributed attempt: %v", err)
		}
	})
	t.Run("bound valid", func(t *testing.T) {
		attempt := validSourceCrawlAttempt()
		attempt.Binding = validSourceCrawlBinding()
		if err := attempt.validate(); err != nil {
			t.Fatalf("valid bound attempt: %v", err)
		}
	})
	t.Run("superseded valid", func(t *testing.T) {
		attempt := validSourceCrawlAttempt()
		attempt.State = sourceCrawlAttemptSuperseded
		attempt.SupersededBy = attempt.Generation + 1
		attempt.TerminalAt = sourceCrawlAttemptTestNow.Add(time.Minute)
		if err := attempt.validate(); err != nil {
			t.Fatalf("valid superseded attempt: %v", err)
		}
	})
	t.Run("terminal failed valid", func(t *testing.T) {
		attempt := validSourceCrawlAttempt()
		attempt.State = sourceCrawlAttemptFailed
		attempt.ReasonCode = "crawl_failed"
		attempt.TerminalAt = sourceCrawlAttemptTestNow.Add(time.Minute)
		if err := attempt.validate(); err != nil {
			t.Fatalf("valid failed attempt: %v", err)
		}
	})
}

func TestSourceCrawlAttemptRejectsInvalidIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sourceCrawlAttempt)
	}{
		{"short attempt ID", func(a *sourceCrawlAttempt) { a.AttemptID = "abc" }},
		{"malformed claim request ID", func(a *sourceCrawlAttempt) { a.ClaimRequestID = "request" }},
		{"uppercase attempt ID", func(a *sourceCrawlAttempt) { a.AttemptID = strings.Repeat("A", 64) }},
		{"zero source", func(a *sourceCrawlAttempt) { a.SourceID = 0 }},
		{"zero generation", func(a *sourceCrawlAttempt) { a.Generation = 0 }},
		{"empty engine", func(a *sourceCrawlAttempt) { a.EngineID = " " }},
		{"unknown state", func(a *sourceCrawlAttempt) { a.State = "mystery" }},
		{"non-UTC time", func(a *sourceCrawlAttempt) {
			a.ClaimedAt = a.ClaimedAt.In(time.FixedZone("x", 3600))
			a.LeaseExpiresAt = a.ClaimedAt.Add(time.Minute)
		}},
		{"sub-microsecond time", func(a *sourceCrawlAttempt) {
			a.ClaimedAt = a.ClaimedAt.Add(500 * time.Nanosecond)
		}},
		{"lease before claim", func(a *sourceCrawlAttempt) {
			a.LeaseExpiresAt = a.ClaimedAt
		}},
		{"superseded without target", func(a *sourceCrawlAttempt) {
			a.State = sourceCrawlAttemptSuperseded
			a.TerminalAt = a.ClaimedAt.Add(time.Minute)
		}},
		{"superseded target not newer", func(a *sourceCrawlAttempt) {
			a.State = sourceCrawlAttemptSuperseded
			a.SupersededBy = a.Generation
			a.TerminalAt = a.ClaimedAt.Add(time.Minute)
		}},
		{"superseded target on claimed", func(a *sourceCrawlAttempt) {
			a.SupersededBy = a.Generation + 1
		}},
		{"terminal without time", func(a *sourceCrawlAttempt) {
			a.State = sourceCrawlAttemptCompleted
		}},
		{"terminal time on claimed", func(a *sourceCrawlAttempt) {
			a.TerminalAt = a.ClaimedAt.Add(time.Minute)
		}},
		{"invalid reason", func(a *sourceCrawlAttempt) { a.ReasonCode = "RAW ERROR TEXT" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			attempt := validSourceCrawlAttempt()
			test.mutate(&attempt)
			if err := attempt.validate(); !errors.Is(err, errSourceCrawlAttemptInvalid) {
				t.Fatalf("validation error = %v, want invalid", err)
			}
		})
	}
}

func TestSourceCrawlAttemptRejectsPartialBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sourceCrawlOperationBinding)
	}{
		{"missing operation", func(b *sourceCrawlOperationBinding) { b.OperationID = "" }},
		{"missing request", func(b *sourceCrawlOperationBinding) { b.RequestID = "" }},
		{"missing approval", func(b *sourceCrawlOperationBinding) { b.ApprovalID = "" }},
		{"missing approval hash", func(b *sourceCrawlOperationBinding) { b.ApprovalSHA256 = "" }},
		{"missing run", func(b *sourceCrawlOperationBinding) { b.RunID = "" }},
		{"overlong run", func(b *sourceCrawlOperationBinding) { b.RunID = strings.Repeat("r", 257) }},
		{"missing agent", func(b *sourceCrawlOperationBinding) { b.AgentID = "" }},
		{"missing owner", func(b *sourceCrawlOperationBinding) { b.Owner = "" }},
		{"zero principal", func(b *sourceCrawlOperationBinding) { b.PrincipalUserID = 0 }},
		{"malformed operation", func(b *sourceCrawlOperationBinding) { b.OperationID = "op" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			binding := validSourceCrawlBinding()
			test.mutate(binding)
			attempt := validSourceCrawlAttempt()
			attempt.Binding = binding
			if err := attempt.validate(); !errors.Is(err, errSourceCrawlAttemptInvalid) {
				t.Fatalf("binding validation error = %v, want invalid", err)
			}
		})
	}
}

func TestSourceCrawlAttemptFence(t *testing.T) {
	current := validSourceCrawlAttempt()
	if err := validateSourceCrawlAttemptFence(current, current.AttemptID, current.Generation); err != nil {
		t.Fatalf("exact current fence: %v", err)
	}
	if err := validateSourceCrawlAttemptFence(current, current.AttemptID, current.Generation-1); !errors.Is(err, errSourceCrawlAttemptStale) {
		t.Fatalf("older generation error = %v, want stale", err)
	}
	if err := validateSourceCrawlAttemptFence(current, current.AttemptID, current.Generation+1); !errors.Is(err, errSourceCrawlAttemptStale) {
		t.Fatalf("newer generation error = %v, want stale", err)
	}
	if err := validateSourceCrawlAttemptFence(current, strings.Repeat("f", 64), current.Generation); !errors.Is(err, errSourceCrawlAttemptNotOwner) {
		t.Fatalf("wrong attempt error = %v, want not owner", err)
	}
	notCurrent := current
	notCurrent.State = sourceCrawlAttemptSuperseded
	notCurrent.SupersededBy = current.Generation + 1
	notCurrent.TerminalAt = current.ClaimedAt.Add(time.Minute)
	if err := validateSourceCrawlAttemptFence(notCurrent, notCurrent.AttemptID, notCurrent.Generation); !errors.Is(err, errSourceCrawlAttemptNotOwner) {
		t.Fatalf("non-current state error = %v, want not owner", err)
	}
	if err := validateSourceCrawlAttemptFence(current, current.AttemptID, 0); !errors.Is(err, errSourceCrawlAttemptInvalid) {
		t.Fatalf("zero generation error = %v, want invalid", err)
	}
}

func TestSourceCrawlAttemptHasNoPayloadFields(t *testing.T) {
	forbidden := []string{"prompt", "cookie", "secret", "token", "password", "html", "payload", "response", "body"}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.ToLower(field.Name)
			for _, banned := range forbidden {
				if strings.Contains(name, banned) {
					t.Fatalf("attempt contract field %q looks like raw crawler content", field.Name)
				}
			}
			walk(field.Type)
		}
	}
	walk(reflect.TypeOf(sourceCrawlAttempt{}))
}
