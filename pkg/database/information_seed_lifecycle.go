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
	"fmt"
	"strings"
)

// RerunInformationSeed queues a completed or errored information seed for a new
// discovery attempt. Seeds that are already in a scheduler-visible state are not
// mutated, but schedulers are still woken so pending work can be claimed without
// waiting for the next poll.
func RerunInformationSeed(db *Handler, id uint64) error {
	seed, err := GetInformationSeedByID(db, id)
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(seed.Status)) {
	case "completed", "error":
		if err := UpdateInformationSeedStatus(db, id, "pending", ""); err != nil {
			return err
		}
	case "new", "pending", "processing":
		// Leave the existing lifecycle state intact. New/pending rows are already
		// claimable when enabled; processing rows are governed by stale-claim
		// recovery. In both cases a wakeup is harmless and avoids a poll delay.
	default:
		return fmt.Errorf("information seed %d cannot be rerun from status %q", id, seed.Status)
	}

	WakeInformationSeedSchedulers()
	return nil
}

// DisableInformationSeed prevents a seed from being claimed by schedulers.
func DisableInformationSeed(db *Handler, id uint64) error {
	return SetInformationSeedDisabled(db, id, true)
}

// EnableInformationSeed clears the disabled flag. When queuePending is true, the
// seed is also moved to pending so the scheduler can claim it immediately.
func EnableInformationSeed(db *Handler, id uint64, queuePending bool) error {
	if err := SetInformationSeedDisabled(db, id, false); err != nil {
		return err
	}
	if queuePending {
		if err := UpdateInformationSeedStatus(db, id, "pending", ""); err != nil {
			return err
		}
	}
	WakeInformationSeedSchedulers()
	return nil
}
