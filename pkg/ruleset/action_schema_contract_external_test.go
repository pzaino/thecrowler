package ruleset_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/pzaino/thecrowler/pkg/browser/actions"
)

// TestCanonicalActionsMatchSchema lives in an external ruleset test package so
// importing actions (which itself imports ruleset) does not create an import
// cycle.
func TestCanonicalActionsMatchSchema(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract test")
	}
	schemaPath := filepath.Join(filepath.Dir(source), "..", "..", "schemas", "crowler-ruleset-schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read ruleset schema: %v", err)
	}

	var schema struct {
		Properties struct {
			RuleGroups struct {
				Items struct {
					Properties struct {
						ActionRules struct {
							Items struct {
								Properties struct {
									ActionType struct {
										Enum []string `json:"enum"`
									} `json:"action_type"`
								} `json:"properties"`
							} `json:"items"`
						} `json:"action_rules"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"rule_groups"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode ruleset schema: %v", err)
	}

	schemaActions := schema.Properties.RuleGroups.Items.Properties.ActionRules.Items.Properties.ActionType.Enum
	if len(schemaActions) == 0 {
		t.Fatal("action_type.enum is empty or missing")
	}
	sort.Strings(schemaActions)
	canonicalActions := actions.CanonicalActionKeys()
	sort.Strings(canonicalActions)
	if !reflect.DeepEqual(schemaActions, canonicalActions) {
		t.Fatalf("action_type.enum = %v, canonical actions = %v", schemaActions, canonicalActions)
	}

	for _, action := range canonicalActions {
		if !actions.CanonicalActionHasHandler(action) {
			t.Errorf("canonical action %q has no handler", action)
		}
	}
}
