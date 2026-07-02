package crawler

import (
	"reflect"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestExecuteActionPlanItemPreservesExecutionOrder(t *testing.T) {
	var got []string
	item := cfg.ExecutionPlanItem{
		Rulesets:   []string{"ruleset"},
		RuleGroups: []string{"group"},
		Rules:      []string{"rule"},
	}

	executeActionPlanItem(item,
		func() { got = append(got, "ruleset") },
		func() { got = append(got, "group") },
		func() { got = append(got, "rule") },
	)

	want := []string{"ruleset", "group", "rule"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execution order = %#v, want %#v", got, want)
	}
}
