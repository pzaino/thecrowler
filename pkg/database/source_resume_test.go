// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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
	"context"
	"errors"
	"testing"
)

func TestSourceEnabledForResume(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	if _, err := db.Exec(`INSERT INTO Sources(source_id, disabled, status, usr_id) VALUES
		(42, FALSE, 'completed', 7),
		(43, TRUE, 'completed', 7),
		(44, FALSE, 'completed', 7)`); err != nil {
		t.Fatalf("insert resume sources: %v", err)
	}
	if _, err := db.Exec(`UPDATE Sources SET deleted_at=CURRENT_TIMESTAMP WHERE source_id=44`); err != nil {
		t.Fatalf("delete resume source: %v", err)
	}

	enabled, err := SourceEnabledForResume(context.Background(), &handler, 42)
	if err != nil || !enabled {
		t.Fatalf("enabled source = %v err=%v", enabled, err)
	}
	enabled, err = SourceEnabledForResume(context.Background(), &handler, 43)
	if err != nil || enabled {
		t.Fatalf("disabled source = %v err=%v", enabled, err)
	}
	enabled, err = SourceEnabledForResume(context.Background(), &handler, 44)
	if err != nil || enabled {
		t.Fatalf("deleted source = %v err=%v", enabled, err)
	}
	enabled, err = SourceEnabledForResume(context.Background(), &handler, 99)
	if err != nil || enabled {
		t.Fatalf("missing source = %v err=%v", enabled, err)
	}

	manager := &SQLiteHandler{db: db, dbms: DBMySQLStr}
	var unsupported Handler = manager
	if _, err := SourceEnabledForResume(context.Background(), &unsupported, 42); !errors.Is(err, ErrAsyncGoalPersistenceUnsupported) {
		t.Fatalf("unsupported backend error = %v", err)
	}

	_ = db.Close()
	if _, err := SourceEnabledForResume(context.Background(), &handler, 42); err == nil {
		t.Fatal("closed database returned success")
	}
}
