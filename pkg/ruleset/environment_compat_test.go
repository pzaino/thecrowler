package ruleset

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEnvironmentValueDecoding(t *testing.T) {
	tests := []struct {
		name, value string
		want        interface{}
	}{
		{"string", `"hello"`, "hello"}, {"integer", `42`, int64(42)},
		{"float", `4.25`, 4.25}, {"boolean", `true`, true}, {"null", `null`, nil},
		{"object", `{"count":2}`, map[string]interface{}{"count": int64(2)}},
		{"objects", `[{"id":1},{"id":2}]`, []interface{}{map[string]interface{}{"id": int64(1)}, map[string]interface{}{"id": int64(2)}}},
		{"heterogeneous", `["first",2,false,null,{"n":3}]`, []interface{}{"first", int64(2), false, nil, map[string]interface{}{"n": int64(3)}}},
		{"empty", `[]`, []interface{}{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, field := range []string{"values", "value"} {
				var got EnvSetting
				if err := json.Unmarshal([]byte(`{"key":"example","`+field+`":`+test.value+`}`), &got); err != nil {
					t.Fatalf("decode %s: %v", field, err)
				}
				if !reflect.DeepEqual(got.Values, test.want) {
					t.Errorf("%s: got %#v (%T), want %#v (%T)", field, got.Values, got.Values, test.want, test.want)
				}
			}
		})
	}
}

func TestEnvironmentHeterogeneousArrayDoesNotPanic(t *testing.T) {
	inputs := []string{`{"values":["text",1]}`, `{"values":[1,"text"]}`, `{"values":[true,{}]}`}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("unmarshal panicked: %v", recovered)
				}
			}()
			var setting EnvSetting
			if err := json.Unmarshal([]byte(input), &setting); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnvironmentAliasConflict(t *testing.T) {
	var setting EnvSetting
	if err := json.Unmarshal([]byte(`{"value":1,"values":2}`), &setting); err == nil {
		t.Error("JSON conflict was accepted")
	}
	if err := yaml.Unmarshal([]byte("value: 1\nvalues: 2\n"), &setting); err == nil {
		t.Error("YAML conflict was accepted")
	}
}

func TestEnvironmentJSONYAMLParity(t *testing.T) {
	jsonInput := `{"key":"all","values":["x",7,2.5,true,null,{"nested":[1,false]}]}`
	yamlInput := "key: all\nvalues: [x, 7, 2.5, true, null, {nested: [1, false]}]\n"
	var fromJSON, fromYAML EnvSetting
	if err := json.Unmarshal([]byte(jsonInput), &fromJSON); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(yamlInput), &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Fatalf("JSON %#v differs from YAML %#v", fromJSON, fromYAML)
	}
}

func TestLoggingConfigurationJSONAndYAML(t *testing.T) {
	want := LoggingConfiguration{LogLevel: "INFO", LogMessage: "rule matched"}
	jsonData, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	yamlData, err := yaml.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var jsonGot, yamlGot LoggingConfiguration
	if err := json.Unmarshal(jsonData, &jsonGot); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(yamlData, &yamlGot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonGot, want) || !reflect.DeepEqual(yamlGot, want) {
		t.Fatalf("round trip: JSON %#v, YAML %#v", jsonGot, yamlGot)
	}
}
