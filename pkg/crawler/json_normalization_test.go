// Copyright 2026 Paolo Fabio Zaino
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
	"database/sql/driver"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeJSONProducesJSONBSafeDocument(t *testing.T) {
	input := map[string]any{
		"actual_nul":     "before\x00after",
		"invalid_utf8":   string([]byte{'a', 0xff, 'b'}),
		"literal_escape": `keep \u0000 and \uD800 as text`,
		"not_a_number":   math.NaN(),
		"raw":            json.RawMessage(`{"nested":"value\u0000tail"}`),
	}

	normalized, err := normalizeJSON(input)
	require.NoError(t, err)
	assert.True(t, json.Valid(normalized))
	assert.NotContains(t, string(normalized), `value\u0000tail`)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(normalized, &decoded))
	assert.Equal(t, "beforeafter", decoded["actual_nul"])
	assert.Equal(t, "ab", decoded["invalid_utf8"])
	assert.Equal(t, `keep \u0000 and \uD800 as text`, decoded["literal_escape"])
	assert.Nil(t, decoded["not_a_number"])
	assert.Equal(t, "valuetail", decoded["raw"].(map[string]any)["nested"])
}

func TestDecodeJSONDocumentRepairsCommonBrowserPayloadProblems(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, decoded any)
	}{
		{
			name:  "trailing comma and missing value",
			input: `{"ok":true,"missing":,"items":[1,2,],}`,
			check: func(t *testing.T, decoded any) {
				object := decoded.(map[string]any)
				assert.Nil(t, object["missing"])
				assert.Len(t, object["items"], 2)
			},
		},
		{
			name:  "javascript non-values",
			input: `{"one":undefined,"two":NaN,"three":Infinity}`,
			check: func(t *testing.T, decoded any) {
				object := decoded.(map[string]any)
				assert.Nil(t, object["one"])
				assert.Nil(t, object["two"])
				assert.Nil(t, object["three"])
			},
		},
		{
			name:  "unescaped inner quotes and missing closers",
			input: `{"message":"say "hello" now","nested":[{"ok":true}`,
			check: func(t *testing.T, decoded any) {
				object := decoded.(map[string]any)
				assert.Equal(t, `say "hello" now`, object["message"])
				assert.Len(t, object["nested"], 1)
			},
		},
		{
			name:  "top-level array",
			input: `[1,{"ok":true}]`,
			check: func(t *testing.T, decoded any) {
				assert.Len(t, decoded, 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, ok := decodeJSONDocument(tt.input)
			require.True(t, ok)
			tt.check(t, decoded)
			normalized, err := normalizeJSON(decoded)
			require.NoError(t, err)
			assert.True(t, json.Valid(normalized))
		})
	}
}

func TestDecodeJSONDocumentDoesNotAcceptTrailingGarbage(t *testing.T) {
	decoded, ok := decodeJSONDocument(`{"ok":true} definitely-not-json`)
	assert.False(t, ok)
	assert.Nil(t, decoded)
}

func TestNormalizeJSONKeepsLiteralEscapesValid(t *testing.T) {
	normalized, err := normalizeJSON(map[string]any{"body": `literal \u0000 \uD800`})
	require.NoError(t, err)
	require.True(t, json.Valid(normalized))
	assert.True(t, strings.Contains(string(normalized), `\\u0000`))
	assert.True(t, strings.Contains(string(normalized), `\\uD800`))
}

type jsonbSafeArgument struct{}

func (jsonbSafeArgument) Match(value driver.Value) bool {
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return false
	}
	if !json.Valid(raw) {
		return false
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	return jsonValueHasNoNUL(decoded)
}

func jsonValueHasNoNUL(value any) bool {
	switch typed := value.(type) {
	case string:
		return !strings.ContainsRune(typed, '\x00')
	case map[string]any:
		for key, item := range typed {
			if strings.ContainsRune(key, '\x00') || !jsonValueHasNoNUL(item) {
				return false
			}
		}
	case []any:
		for _, item := range typed {
			if !jsonValueHasNoNUL(item) {
				return false
			}
		}
	}
	return true
}

func TestInsertOrUpdateWebObjectsNormalizesXHRBeforeJSONBInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)

	mock.ExpectQuery(`(?s)INSERT INTO WebObjects`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), jsonbSafeArgument{}).
		WillReturnRows(sqlmock.NewRows([]string{"object_id"}).AddRow(42))
	mock.ExpectExec(`(?s)INSERT INTO WebObjectsIndex`).
		WithArgs(uint64(7), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	pageInfo := &PageInfo{
		BodyText: "page",
		ScrapedData: []ScrapedItem{{
			"xhr": []any{map[string]any{
				"response_body": "before\x00after",
				"request_body":  json.RawMessage(`{"broken":"value\u0000tail",}`),
				"metric":        math.Inf(1),
			}},
		}},
	}

	objectID, details, _, err := insertOrUpdateWebObjects(tx, 7, pageInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(42), objectID)
	assert.True(t, json.Valid(details))
	assert.True(t, jsonbSafeArgument{}.Match(details))
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
