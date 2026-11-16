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

// Pure contract for the proposed fenced source-crawl attempt ownership. This
// file intentionally has no database access and no production caller. It
// records the identity and validation rules that the WP-05B3C2 design asks the
// maintainer to approve before any DDL is added.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	sourceCrawlAttemptIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	sourceCrawlHashPattern      = regexp.MustCompile(`^[a-f0-9]{64}$`)
	sourceCrawlReasonPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type sourceCrawlAttemptState string

const (
	sourceCrawlAttemptClaimed    sourceCrawlAttemptState = "claimed"
	sourceCrawlAttemptSuperseded sourceCrawlAttemptState = "superseded"
	sourceCrawlAttemptExpired    sourceCrawlAttemptState = "expired"
	sourceCrawlAttemptAbandoned  sourceCrawlAttemptState = "abandoned"
	sourceCrawlAttemptCompleted  sourceCrawlAttemptState = "completed"
	sourceCrawlAttemptFailed     sourceCrawlAttemptState = "failed"
	sourceCrawlAttemptCancelled  sourceCrawlAttemptState = "cancelled"
)

var (
	errSourceCrawlAttemptInvalid  = errors.New("source crawl attempt is invalid")
	errSourceCrawlAttemptStale    = errors.New("source crawl attempt is stale")
	errSourceCrawlAttemptNotOwner = errors.New("source crawl attempt is not the current owner")
)

type sourceCrawlOperationBinding struct {
	OperationID     string
	RequestID       string
	RunID           string
	AgentID         string
	Owner           string
	PrincipalUserID uint64
	ApprovalID      string
	ApprovalSHA256  string
}

type sourceCrawlAttempt struct {
	AttemptID      string
	ClaimRequestID string
	SourceID       uint64
	Generation     uint64
	EngineID       string
	State          sourceCrawlAttemptState
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
	SupersededBy   uint64
	TerminalAt     time.Time
	ReasonCode     string
	Binding        *sourceCrawlOperationBinding
}

// newSourceCrawlAttemptID returns 32 cryptographically random bytes as 64
// lowercase hexadecimal characters. Callers must use a trusted randomness
// source; model, event and web input is never accepted.
func newSourceCrawlAttemptID(reader io.Reader) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", fmt.Errorf("generate source crawl attempt ID: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func (a sourceCrawlAttempt) validate() error {
	if !sourceCrawlAttemptIDPattern.MatchString(a.AttemptID) {
		return fmt.Errorf("%w: attempt_id", errSourceCrawlAttemptInvalid)
	}
	if !sourceCrawlHashPattern.MatchString(a.ClaimRequestID) {
		return fmt.Errorf("%w: claim_request_id", errSourceCrawlAttemptInvalid)
	}
	if a.SourceID == 0 {
		return fmt.Errorf("%w: source_id", errSourceCrawlAttemptInvalid)
	}
	if a.Generation == 0 {
		return fmt.Errorf("%w: generation", errSourceCrawlAttemptInvalid)
	}
	if strings.TrimSpace(a.EngineID) == "" || len(a.EngineID) > 256 {
		return fmt.Errorf("%w: engine_id", errSourceCrawlAttemptInvalid)
	}
	if !a.State.valid() {
		return fmt.Errorf("%w: state", errSourceCrawlAttemptInvalid)
	}
	if !validSourceCrawlAttemptTime(a.ClaimedAt) || !validSourceCrawlAttemptTime(a.LeaseExpiresAt) {
		return fmt.Errorf("%w: timestamps", errSourceCrawlAttemptInvalid)
	}
	if !a.LeaseExpiresAt.After(a.ClaimedAt) {
		return fmt.Errorf("%w: lease_expires_at", errSourceCrawlAttemptInvalid)
	}
	if a.SupersededBy != 0 && a.SupersededBy <= a.Generation {
		return fmt.Errorf("%w: superseded_by", errSourceCrawlAttemptInvalid)
	}
	if a.State == sourceCrawlAttemptSuperseded && a.SupersededBy == 0 {
		return fmt.Errorf("%w: superseded_by required", errSourceCrawlAttemptInvalid)
	}
	if a.State != sourceCrawlAttemptSuperseded && a.SupersededBy != 0 {
		return fmt.Errorf("%w: superseded_by only valid for superseded", errSourceCrawlAttemptInvalid)
	}
	if a.State.terminal() {
		if !validSourceCrawlAttemptTime(a.TerminalAt) {
			return fmt.Errorf("%w: terminal_at", errSourceCrawlAttemptInvalid)
		}
	} else if !a.TerminalAt.IsZero() {
		return fmt.Errorf("%w: terminal_at only valid for terminal state", errSourceCrawlAttemptInvalid)
	}
	if a.ReasonCode != "" && !sourceCrawlReasonPattern.MatchString(a.ReasonCode) {
		return fmt.Errorf("%w: reason_code", errSourceCrawlAttemptInvalid)
	}
	if a.Binding == nil {
		return nil
	}
	return a.Binding.validate()
}

func (b sourceCrawlOperationBinding) validate() error {
	if !sourceCrawlHashPattern.MatchString(b.OperationID) ||
		!sourceCrawlHashPattern.MatchString(b.RequestID) ||
		!sourceCrawlHashPattern.MatchString(b.ApprovalID) ||
		!sourceCrawlHashPattern.MatchString(b.ApprovalSHA256) {
		return fmt.Errorf("%w: operation binding hashes", errSourceCrawlAttemptInvalid)
	}
	if strings.TrimSpace(b.RunID) == "" || len(b.RunID) > 256 ||
		strings.TrimSpace(b.AgentID) == "" || len(b.AgentID) > 256 ||
		strings.TrimSpace(b.Owner) == "" || len(b.Owner) > 256 ||
		b.PrincipalUserID == 0 {
		return fmt.Errorf("%w: operation binding identity", errSourceCrawlAttemptInvalid)
	}
	return nil
}

func (s sourceCrawlAttemptState) valid() bool {
	switch s {
	case sourceCrawlAttemptClaimed, sourceCrawlAttemptSuperseded,
		sourceCrawlAttemptExpired, sourceCrawlAttemptAbandoned,
		sourceCrawlAttemptCompleted, sourceCrawlAttemptFailed,
		sourceCrawlAttemptCancelled:
		return true
	default:
		return false
	}
}

func (s sourceCrawlAttemptState) terminal() bool {
	switch s {
	case sourceCrawlAttemptSuperseded, sourceCrawlAttemptExpired,
		sourceCrawlAttemptAbandoned, sourceCrawlAttemptCompleted,
		sourceCrawlAttemptFailed, sourceCrawlAttemptCancelled:
		return true
	default:
		return false
	}
}

// validateSourceCrawlAttemptFence is the pure equivalent of the future
// current-claim CAS: only the exact attempt ID and generation may mutate the
// current attempt, and the generation must be positive and monotonic.
func validateSourceCrawlAttemptFence(current sourceCrawlAttempt, attemptID string, generation uint64) error {
	if err := current.validate(); err != nil {
		return err
	}
	if generation == 0 {
		return fmt.Errorf("%w: generation", errSourceCrawlAttemptInvalid)
	}
	if generation != current.Generation {
		return errSourceCrawlAttemptStale
	}
	if current.AttemptID != attemptID {
		return errSourceCrawlAttemptNotOwner
	}
	if current.State != sourceCrawlAttemptClaimed {
		return errSourceCrawlAttemptNotOwner
	}
	return nil
}

func validSourceCrawlAttemptTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Nanosecond()%1000 == 0
}
