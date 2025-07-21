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
	"sync"
	"sync/atomic"
)

// SafeMutex is a thread-safe mutex that ensures that the lock is only released if it was previously locked.
type SafeMutex struct {
	mu     sync.Mutex
	locked uint32
}

// Lock acquires the lock and sets the locked state to true.
func (m *SafeMutex) Lock() {
	m.mu.Lock()
	atomic.StoreUint32(&m.locked, 1)
}

// Unlock releases the lock only if it was previously locked.
func (m *SafeMutex) Unlock() {
	if !atomic.CompareAndSwapUint32(&m.locked, 1, 0) {
		return // or panic/log
	}
	m.mu.Unlock()
}
