package ruleset

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestActionConditionDecoding(t *testing.T) {
	tests := []struct {
		name, input   string
		yaml, wantErr bool
		want          ActionCondition
	}{
		{name: "schema JSON", input: `{"type":"element","selector":"#ready"}`, want: ActionCondition{Type: "element", Selector: "#ready"}},
		{name: "legacy JSON", input: `{"language":"en"}`, want: ActionCondition{Type: "language", Language: "en"}},
		{name: "legacy YAML plugin", yaml: true, input: "plugin_call: true\nselector: ready\n", want: ActionCondition{Type: "plugin_call", PluginCall: "ready"}},
		{name: "mixed forms", input: `{"type":"element","element":"#ready","selector":"#ready"}`, wantErr: true},
		{name: "unknown type", input: `{"type":"unknown","selector":"x"}`, wantErr: true},
		{name: "missing selector", input: `{"type":"element"}`, wantErr: true},
		{name: "unknown field", input: `{"type":"element","selector":"x","extra":true}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ActionCondition
			var err error
			if tt.yaml {
				err = yaml.Unmarshal([]byte(tt.input), &got)
			} else {
				err = json.Unmarshal([]byte(tt.input), &got)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("decode error = %v, want error=%v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("condition = %#v, want %#v", got, tt.want)
			}
		})
	}
}
