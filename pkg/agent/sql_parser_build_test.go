package agent

import "testing"

// Runs in both CGO_ENABLED=0 and CGO_ENABLED=1 test jobs; a build without
// either Parse implementation must fail at compile time.
func TestSQLParserBuildModes(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		statements int
		reject     bool
	}{
		{"select", "SELECT 1", 1, false},
		{"multiple", "SELECT 1; SELECT 2", 2, false},
		{"comment-only", "-- ignored", 0, false},
		{"invalid-syntax", "SELECT FROM WHERE", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := parsePostgresSQL(tc.sql)
			if tc.reject {
				if err == nil {
					t.Fatalf("expected parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if tree == nil || len(tree.Stmts) != tc.statements {
				t.Fatalf("got %v statements, want %d", tree, tc.statements)
			}
		})
	}
	// Both parsers must classify malformed or multi-statement input as denied.
	for _, sql := range []string{"SELECT FROM WHERE", "SELECT 1; SELECT 2"} {
		if got := DBQueryClassifier.Classify(sql); got != SQLRejected {
			t.Errorf("Classify(%q) = %q, want rejected", sql, got)
		}
	}
}
