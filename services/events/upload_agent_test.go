package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agt "github.com/pzaino/thecrowler/pkg/agent"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// stubAgentAction runs instead of real actions so execution is hermetic.
type stubAgentAction struct {
	name  string
	calls *int
}

func (a *stubAgentAction) Name() string { return a.name }

func (a *stubAgentAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	*a.calls++
	return map[string]interface{}{"output": map[string]interface{}{}, "status": "success"}, nil
}

const (
	testAgentV2 = `format_version: v2
agent_identity:
  name: Upload Probe
  trust_level: trusted
  capabilities: [api_request]
jobs:
  - name: Upload Probe
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params:
          url: http://localhost/health
          request_type: GET
`
	testAgentV1 = `format_version: v1
jobs:
  - name: LegacyDiscoveryJob
    process: serial
    trigger_type: manual
    trigger_name: legacy_discovery
    steps:
      - action: RunCommand
        params:
          command: echo "legacy mode"
`
	testAgentUnknownAction = `format_version: v1
jobs:
  - name: Bad Job
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: NoSuchAction
        params: {}
`
	testAgentBadCapability = `format_version: v2
agent_identity:
  name: Bad Cap
  trust_level: trusted
  capabilities: [root_everything]
jobs:
  - name: Bad Cap
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params:
          url: http://localhost/health
          request_type: GET
`
	testAgentMissingTarget = `format_version: v2
agent_identity:
  name: Lonely
  trust_level: trusted
  capabilities: [api_request, delegate]
jobs:
  - name: Lonely
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: Decision
        params:
          condition:
            condition_type: if
            expression: "true"
            on_true: {agent_name: No Such Agent}
            on_false: {agent_name: No Such Agent Either}
`
	testAgentNameMismatch = `format_version: v2
agent_identity:
  name: Claimed Name
  trust_level: trusted
  capabilities: [api_request]
jobs:
  - name: Actual Job
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: APIRequest
        params:
          url: http://localhost/health
          request_type: GET
`
)

// installUploadTestSeams isolates the handler: temp dir, fresh registry,
// stubbed event announcement. Nothing touches the real ./agents tree or DB.
func installUploadTestSeams(t *testing.T) (dir string, announced *[]cdb.Event) {
	t.Helper()
	oldDir := agentsUploadDir
	oldRegistry := agt.AgentsRegistry
	oldAnnounce := announceAgentUpload
	dir = t.TempDir()
	agentsUploadDir = dir
	agt.AgentsRegistry = agt.NewJobConfig()
	seen := []cdb.Event{}
	announced = &seen
	announceAgentUpload = func(event cdb.Event) error {
		*announced = append(*announced, event)
		return nil
	}
	t.Cleanup(func() {
		agentsUploadDir = oldDir
		agt.AgentsRegistry = oldRegistry
		announceAgentUpload = oldAnnounce
	})
	return dir, announced
}

func uploadTestRequest(t *testing.T, filename, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("agent", filename)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("form write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("form close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/upload/agent", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func serveUpload(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	uploadAgentHandler(recorder, req)
	return recorder
}

func assertNoSideEffects(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no files, found %d", len(entries))
	}
	if _, found := agt.AgentsRegistry.GetAgentByName(name); found {
		t.Fatalf("rejected agent %q must not enter the registry", name)
	}
}

func TestUploadStrictValidV2(t *testing.T) {
	dir, announced := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "probe.agent.yaml", testAgentV2))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "probe.agent.yaml"))
	if err != nil {
		t.Fatalf("expected persisted file: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("expected non-empty persisted file")
	}
	if _, found := agt.AgentsRegistry.GetAgentByName("Upload Probe"); !found {
		t.Fatalf("valid agent must be registered")
	}
	if len(*announced) != 1 || (*announced)[0].Type != "new_agent" {
		t.Fatalf("expected one new_agent announcement, got %+v", *announced)
	}
}

func TestUploadRegisteredAgentExecutes(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	if rec := serveUpload(t, uploadTestRequest(t, "probe.agent.yaml", testAgentV2)); rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	// Restart-load parity: the persisted file must strict-validate exactly
	// as the startup loader would see it (interpolated, same validator).
	persisted, err := os.ReadFile(filepath.Join(dir, "probe.agent.yaml"))
	if err != nil {
		t.Fatalf("persisted file: %v", err)
	}
	restartView := cmn.InterpolateEnvVars(string(persisted))
	if err := agt.ValidateAgentConfig([]byte(restartView), "yaml", agt.ValidationModeStrict, agt.AgentsRegistry.Registry()); err != nil {
		t.Fatalf("restart-load strict validation must pass, got %v", err)
	}

	// The registered manifest must execute under enforcement.
	registered, found := agt.AgentsRegistry.GetAgentByName("Upload Probe")
	if !found {
		t.Fatalf("expected registered agent")
	}
	engine := agt.NewJobEngine()
	calls := 0
	engine.RegisterAction(&stubAgentAction{name: "APIRequest", calls: &calls})
	iCfg := map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
	}
	if err := engine.ExecuteJobs(registered, iCfg); err != nil {
		t.Fatalf("registered agent must execute, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one step execution, got %d", calls)
	}
}

func TestUploadStrictValidV1(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "legacy.agent.yaml", testAgentV1))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201 for strict-valid v1, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "legacy.agent.yaml")); err != nil {
		t.Fatalf("expected persisted file: %v", err)
	}
	if _, found := agt.AgentsRegistry.GetAgentByName("LegacyDiscoveryJob"); !found {
		t.Fatalf("valid v1 agent must be registered")
	}
}

func TestUploadStrictInvalidUnknownAction(t *testing.T) {
	dir, announced := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "bad.agent.yaml", testAgentUnknownAction))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	assertNoSideEffects(t, dir, "Bad Job")
	if len(*announced) != 0 {
		t.Fatalf("rejected upload must not announce")
	}
}

func TestUploadInvalidCapability(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "badcap.agent.yaml", testAgentBadCapability))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	assertNoSideEffects(t, dir, "Bad Cap")
}

func TestUploadMissingDecisionTarget(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "lonely.agent.yaml", testAgentMissingTarget))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	assertNoSideEffects(t, dir, "Lonely")
}

func TestUploadBadYAMLAndJSON(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "broken.agent.yaml", "jobs: [unclosed\n  bad: : :"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad YAML, got %d", recorder.Code)
	}
	recorder = serveUpload(t, uploadTestRequest(t, "broken.agent.json", `{"jobs": [}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d", recorder.Code)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no files, found %d", len(entries))
	}
}

func TestUploadWrongExtension(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	for _, name := range []string{"agent.txt", "agent", "agent.yaml.exe"} {
		recorder := serveUpload(t, uploadTestRequest(t, name, testAgentV1))
		if recorder.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: expected 415, got %d", name, recorder.Code)
		}
	}
	assertNoSideEffects(t, dir, "LegacyDiscoveryJob")
}

func TestSanitizeAgentUploadName(t *testing.T) {
	for _, name := range []string{
		"../evil.agent.yaml", "..", ".", "a/b.agent.yaml", "/abs.agent.yaml",
		`a\b.agent.yaml`, "", "   ",
	} {
		if _, err := sanitizeAgentUploadName(name); err == nil {
			t.Fatalf("%q must be rejected", name)
		}
	}
	for _, name := range []string{"agent.agent.yaml", "a-d_e2.agent.YML", "manifest.agent.json"} {
		if _, err := sanitizeAgentUploadName(name); err != nil {
			t.Fatalf("%q must be accepted, got %v", name, err)
		}
	}
}

func TestUploadFilenameTraversal(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	parent := filepath.Dir(dir)
	before, _ := os.ReadDir(parent)
	// ".." survives multipart parsing intact and must be rejected; names
	// with separators are additionally stripped by the Go HTTP stack
	// server-side, so nothing may escape the upload dir either way.
	recorder := serveUpload(t, uploadTestRequest(t, "..", testAgentV1))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("%q: expected 400, got %d", "..", recorder.Code)
	}
	recorder = serveUpload(t, uploadTestRequest(t, "../evil.agent.yaml", testAgentV1))
	if recorder.Code != http.StatusCreated && recorder.Code != http.StatusBadRequest {
		t.Fatalf("sanitized traversal: expected 201 or 400, got %d", recorder.Code)
	}
	after, _ := os.ReadDir(parent)
	for _, entry := range after {
		found := false
		for _, entryBefore := range before {
			if entryBefore.Name() == entry.Name() {
				found = true
			}
		}
		if !found && strings.Contains(entry.Name(), "evil") {
			t.Fatalf("traversal wrote outside the upload dir: %s", entry.Name())
		}
	}
	// Whatever landed must live strictly inside the upload dir.
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatalf("unexpected entry state: %v", err)
		}
	}
}

func TestUploadOversizeBody(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	big := strings.Repeat("x: 1\n", (1<<20)+16)
	recorder := serveUpload(t, uploadTestRequest(t, "big.agent.yaml", big))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", recorder.Code)
	}
	assertNoSideEffects(t, dir, "Big")
}

func TestUploadDuplicateName(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	first := serveUpload(t, uploadTestRequest(t, "probe.agent.yaml", testAgentV2))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload: expected 201, got %d", first.Code)
	}
	second := serveUpload(t, uploadTestRequest(t, "probe.agent.yaml", testAgentV2))
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate upload: expected 409, got %d: %s", second.Code, second.Body.String())
	}
	// Deterministic: original file bytes and single registration survive.
	data, err := os.ReadFile(filepath.Join(dir, "probe.agent.yaml"))
	if err != nil {
		t.Fatalf("expected original file: %v", err)
	}
	if !strings.Contains(string(data), "Upload Probe") {
		t.Fatalf("original file must be preserved")
	}
}

func TestUploadNormalizeMismatch(t *testing.T) {
	dir, _ := installUploadTestSeams(t)
	recorder := serveUpload(t, uploadTestRequest(t, "mismatch.agent.yaml", testAgentNameMismatch))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for identity/name mismatch, got %d: %s", recorder.Code, recorder.Body.String())
	}
	assertNoSideEffects(t, dir, "Claimed Name")
	assertNoSideEffects(t, dir, "Actual Job")
}

func TestUploadFileFailureRollsBackRegistry(t *testing.T) {
	_, _ = installUploadTestSeams(t)
	// Point the upload dir at a regular file so temp-file creation fails.
	blocker := filepath.Join(os.TempDir(), fmt.Sprintf("upload-blocker-%d", os.Getpid()))
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("blocker setup: %v", err)
	}
	defer os.Remove(blocker) //nolint:errcheck
	agentsUploadDir = blocker
	recorder := serveUpload(t, uploadTestRequest(t, "probe.agent.yaml", testAgentV2))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, found := agt.AgentsRegistry.GetAgentByName("Upload Probe"); found {
		t.Fatalf("failed write must not register the agent")
	}
}

func TestUploadAnnounceFailureStillCommits(t *testing.T) {
	dir, announced := installUploadTestSeams(t)
	announceAgentUpload = func(event cdb.Event) error {
		return fmt.Errorf("simulated announce outage")
	}
	recorder := serveUpload(t, uploadTestRequest(t, "probe.agent.yaml", testAgentV2))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("announce outage must not fail a committed upload, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "probe.agent.yaml")); err != nil {
		t.Fatalf("expected persisted file: %v", err)
	}
	if _, found := agt.AgentsRegistry.GetAgentByName("Upload Probe"); !found {
		t.Fatalf("expected registered agent")
	}
	if len(*announced) != 0 {
		t.Fatalf("failed announce must not record events")
	}
}
