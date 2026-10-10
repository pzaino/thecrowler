package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// repoRootDir resolves the repository root relative to this test file,
// mirroring loadAgentSchema.
func repoRootDir(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("failed to resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

// realEventTypes are event types emitted by in-repo services.
var realEventTypes = map[string]bool{
	"crawl_completed": true, // pkg/crawler, gated by crawler.create_event_when_done
}

// hypotheticalEventTypes are documented deployment-local events with no
// in-repo emitter. Manifests using them must carry a "hypothetical" marker.
var hypotheticalEventTypes = map[string]bool{
	"ingest.market_news": true,
}

// executableExampleFiles lists every manifest users may copy and run.
// agents/tests fixtures are intentionally excluded: they serve
// integration harnesses, not direct execution.
func executableExampleFiles(t *testing.T) []string {
	t.Helper()
	root := repoRootDir(t)
	var files []string
	for _, pattern := range []string{
		"agents/examples/*.agent.yaml",
		"agents/templates/*.yaml",
	} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatalf("no example manifests found")
	}
	return files
}

func loadExampleDefinition(t *testing.T, path string) *AgentDefinition {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: read: %v", path, err)
	}
	// Mirror production startup: interpolate, then strict-validate.
	interpolated := cmn.InterpolateEnvVars(string(raw))
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if err := ValidateAgentConfig([]byte(interpolated), ext, ValidationModeStrict, nil); err != nil {
		t.Fatalf("%s: strict validation: %v", path, err)
	}
	var cfg JobConfig
	if err := yaml.Unmarshal([]byte(interpolated), &cfg); err != nil {
		t.Fatalf("%s: decode: %v", path, err)
	}
	def, err := cfg.NormalizeToAgentDefinition(AgentSourceMetadata{Location: path})
	if err != nil {
		t.Fatalf("%s: normalization: %v", path, err)
	}
	return def
}

// TestExecutableExamplesValidateAndLoad proves every shipped executable
// manifest strict-validates and loads through the production normalizer.
func TestExecutableExamplesValidateAndLoad(t *testing.T) {
	registry := NewAgentRegistry()
	for _, path := range executableExampleFiles(t) {
		def := loadExampleDefinition(t, path)
		if err := registry.Register(def); err != nil {
			t.Fatalf("%s: register: %v", path, err)
		}
	}
}

// TestExecutableExamplesTriggerInventory checks every trigger against the
// known emission inventory: real events, marked hypothetical events,
// same-file agent targets, or operator-driven manual/interval/signal jobs.
func TestExecutableExamplesTriggerInventory(t *testing.T) {
	for _, path := range executableExampleFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: read: %v", path, err)
		}
		def := loadExampleDefinition(t, path)
		jobNames := map[string]bool{}
		for _, job := range def.Jobs {
			jobNames[strings.TrimSpace(job.Name)] = true
		}
		for _, job := range def.Jobs {
			triggerType := strings.ToLower(strings.TrimSpace(job.TriggerType))
			triggerName := strings.TrimSpace(job.TriggerName)
			switch triggerType {
			case "event":
				if realEventTypes[triggerName] {
					continue
				}
				if !hypotheticalEventTypes[triggerName] {
					t.Fatalf("%s: job %q uses unknown event %q", path, job.Name, triggerName)
				}
				if !strings.Contains(strings.ToLower(string(raw)), "hypothetical") {
					t.Fatalf("%s: hypothetical event %q needs a marker comment", path, triggerName)
				}
			case "agent":
				if !jobNames[triggerName] {
					t.Fatalf("%s: agent trigger %q matches no job in the file", path, triggerName)
				}
			case "manual", "interval", "signal":
				if triggerName == "" {
					t.Fatalf("%s: job %q has an empty trigger name", path, job.Name)
				}
			default:
				t.Fatalf("%s: job %q has unknown trigger type %q", path, job.Name, triggerType)
			}
		}
	}
}

// TestCrawlCompletedDispatchSmoke proves a representative crawl_completed
// agent activates through the registry event index when configured.
func TestCrawlCompletedDispatchSmoke(t *testing.T) {
	registry := NewAgentRegistry()
	found := false
	for _, path := range executableExampleFiles(t) {
		def := loadExampleDefinition(t, path)
		if err := registry.Register(def); err != nil {
			t.Fatalf("%s: register: %v", path, err)
		}
	}
	for _, agent := range registry.GetByTrigger("event", "crawl_completed") {
		found = true
		if len(agent.Jobs) == 0 {
			t.Fatalf("crawl_completed agent %q has no jobs", agent.Identity.Name)
		}
	}
	if !found {
		t.Fatalf("no executable example listens on crawl_completed")
	}
}
