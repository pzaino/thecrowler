package agent

import "testing"

func FuzzValidateAgentConfig_NoPanics(f *testing.F) {
	seeds := []string{
		`{"jobs":[{"name":"Legacy Agent","process":"serial","trigger_type":"manual","trigger_name":"legacy","steps":[{"action":"RunCommand","params":{"command":"echo ok"}}]}]}`,
		"jobs:\n  - name: Legacy YAML Agent\n    process: serial\n    trigger_type: manual\n    trigger_name: legacy_yaml\n    steps:\n      - action: RunCommand\n        params:\n          command: echo yaml\n",
		`{}`,
		`[]`,
		`not-valid-json-or-yaml`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload string) {
		_ = ValidateAgentConfig([]byte(payload), "json", ValidationModeLenient, nil)
		_ = ValidateAgentConfig([]byte(payload), "yaml", ValidationModeLenient, nil)
		_ = ValidateAgentConfig([]byte(payload), "json", ValidationModeStrict, nil)
		_ = ValidateAgentConfig([]byte(payload), "yaml", ValidationModeStrict, nil)
	})
}
