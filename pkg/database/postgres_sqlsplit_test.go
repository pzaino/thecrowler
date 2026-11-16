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
	"strings"
	"testing"
)

// splitPostgresSQLStatements separates statements without being confused by
// comments, quoted literals or dollar-quoted function bodies.
func splitPostgresSQLStatements(script string) []string {
	var statements []string
	var current strings.Builder
	flush := func() {
		statement := strings.TrimSpace(current.String())
		if statement != "" {
			statements = append(statements, statement)
		}
		current.Reset()
	}
	for i := 0; i < len(script); {
		switch {
		case strings.HasPrefix(script[i:], "--"):
			for i < len(script) && script[i] != '\n' {
				i++
			}
		case strings.HasPrefix(script[i:], "/*"):
			i += 2
			for i+1 < len(script) && !strings.HasPrefix(script[i:], "*/") {
				i++
			}
			if i+1 < len(script) {
				i += 2
			} else {
				i = len(script)
			}
		case script[i] == '\'':
			current.WriteByte(script[i])
			i++
			for i < len(script) {
				current.WriteByte(script[i])
				if script[i] == '\'' {
					if i+1 < len(script) && script[i+1] == '\'' {
						current.WriteByte(script[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case script[i] == '"':
			current.WriteByte(script[i])
			i++
			for i < len(script) {
				current.WriteByte(script[i])
				if script[i] == '"' {
					if i+1 < len(script) && script[i+1] == '"' {
						current.WriteByte(script[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case script[i] == '$':
			j := i + 1
			for j < len(script) && (script[j] == '_' || script[j] >= '0' && script[j] <= '9' ||
				script[j] >= 'A' && script[j] <= 'Z' || script[j] >= 'a' && script[j] <= 'z') {
				j++
			}
			if j < len(script) && script[j] == '$' {
				tag := script[i : j+1]
				current.WriteString(tag)
				i = j + 1
				end := strings.Index(script[i:], tag)
				if end < 0 {
					current.WriteString(script[i:])
					i = len(script)
				} else {
					current.WriteString(script[i : i+end+len(tag)])
					i += end + len(tag)
				}
			} else {
				current.WriteByte(script[i])
				i++
			}
		case script[i] == ';':
			flush()
			i++
		default:
			current.WriteByte(script[i])
			i++
		}
	}
	flush()
	return statements
}

func TestSplitPostgresSQLStatements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "leading line comment with semicolon",
			sql:  "-- this comment contains ; and must not split\nCREATE TABLE t (id INT);",
			want: []string{"CREATE TABLE t (id INT)"},
		},
		{
			name: "block comment with semicolon",
			sql:  "/* a ; block ; comment */\nSELECT 1;",
			want: []string{"SELECT 1"},
		},
		{
			name: "single quoted semicolon and escaped quote",
			sql:  "INSERT INTO t VALUES ('a;b''c');",
			want: []string{"INSERT INTO t VALUES ('a;b''c')"},
		},
		{
			name: "double quoted identifier with semicolon",
			sql:  `SELECT "column;name" FROM t;`,
			want: []string{`SELECT "column;name" FROM t`},
		},
		{
			name: "dollar quoted function body",
			sql:  "CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; PERFORM 2; END $$ LANGUAGE plpgsql;\nSELECT f();",
			want: []string{"CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; PERFORM 2; END $$ LANGUAGE plpgsql", "SELECT f()"},
		},
		{
			name: "tagged dollar quoted body",
			sql:  "DO $body$ BEGIN RAISE NOTICE 'a;b'; END $body$;",
			want: []string{"DO $body$ BEGIN RAISE NOTICE 'a;b'; END $body$"},
		},
		{
			name: "multiple statements and trailing comment",
			sql:  "SELECT 1;\nSELECT 2;\n-- trailing ; comment\n",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "comment only",
			sql:  "-- nothing but a ; comment\n/* and another ; */\n",
			want: nil,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := splitPostgresSQLStatements(test.sql)
			if len(got) != len(test.want) {
				t.Fatalf("statements = %#v, want %#v", got, test.want)
			}
			for i := range got {
				if strings.TrimSpace(got[i]) != test.want[i] {
					t.Fatalf("statement %d = %q, want %q", i, got[i], test.want[i])
				}
			}
		})
	}
}
