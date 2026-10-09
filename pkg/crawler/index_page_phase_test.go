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

package crawler

import (
	"fmt"
	"strings"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestFormatIndexPagePhaseClassifiesTimeout(t *testing.T) {
	timeoutErr := fmt.Errorf("%w: %v", cdb.ErrTransactionTimeout, "sql: transaction has already been committed or rolled back")
	line := formatIndexPagePhase("indexPage", "upsert_webobject", 1500*time.Millisecond, 42, 7, timeoutErr)

	for _, want := range []string{
		"operation=indexPage",
		"phase=upsert_webobject",
		"elapsed=1.5s",
		"source_id=42",
		"index_id=7",
		"timeout=true",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("diagnostic line %q does not contain %q", line, want)
		}
	}
}

func TestFormatIndexPagePhaseNonTimeout(t *testing.T) {
	line := formatIndexPagePhase("indexPage", "commit", time.Second, 0, 0, fmt.Errorf("boom"))
	if !strings.Contains(line, "timeout=false") {
		t.Fatalf("expected non-timeout classification, got %q", line)
	}
	if !strings.Contains(line, "phase=commit") {
		t.Fatalf("expected phase in diagnostic, got %q", line)
	}
}

func TestLogIndexPagePhaseDoesNotPanic(t *testing.T) {
	logIndexPagePhase("indexPage", "commit", time.Now().Add(-time.Second), 1, 2, nil)
	logIndexPagePhase("indexPage", "commit", time.Now().Add(-time.Second), 1, 2, cdb.ErrTransactionTimeout)
}
