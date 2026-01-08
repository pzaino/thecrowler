// cmd/runPluginTests/main.go
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

const (
	testPlugin = "test_plugin"
)

type cliOptions struct {
	ConfigPath  string
	PType       string
	PluginsCSV  string
	TestsCSV    string
	ListOnly    bool
	DebugLevel  int
	TestTimeout int
}

// TestCase struct to hold a single test case
type TestCase struct {
	PluginName string
	TestPath   string
	TestName   string // derived from filename, mostly for display
	Body       string
}

// TestPlan struct to hold the overall test plan
type TestPlan struct {
	Plugins        map[string]plg.JSPlugin
	TestsByPlugin  map[string][]TestCase
	MissingPlugins map[string]struct{} // set of missing plugin names
}

// TestResult struct to hold the result of a single test case
type TestResult struct {
	Plugin   string
	TestName string
	Passed   bool
	Error    string
}

// ExecResult struct to hold the overall execution result
type ExecResult struct {
	Results []TestResult
	Failed  int
}

func pluginNameFromTestFile(path string) string {
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".tests.js") {
		return strings.TrimSuffix(base, ".tests.js")
	}
	return ""
}

func buildTestPlan(
	reg *plg.JSPluginRegister,
	tests []plg.TestFile,
) (*TestPlan, error) {

	if reg == nil {
		return nil, fmt.Errorf("plugin registry is nil")
	}

	plan := &TestPlan{
		Plugins:        make(map[string]plg.JSPlugin),
		TestsByPlugin:  make(map[string][]TestCase),
		MissingPlugins: make(map[string]struct{}),
	}

	// Copy plugins into a plain map for stable access
	for _, name := range reg.Order {
		p := reg.Registry[name]
		//fmt.Printf("Loaded plugin: %s (type: %s)\n", name, p.PType)
		if p.PType != testPlugin {
			plan.Plugins[name] = p
		}
	}

	for _, tf := range tests {
		pluginName := pluginNameFromTestFile(tf.Path)
		if tf.Path == testPlugin {
			pluginName = tf.Name
		}
		//fmt.Printf("Discovered test file: %s (plugin: %s)\n", tf.Path, pluginName)
		if pluginName == "" {
			// Ignore files that do not follow *.tests.js
			continue
		}

		tc := TestCase{
			PluginName: pluginName,
			TestPath:   tf.Path,
			TestName:   tf.Name,
			Body:       tf.Body,
		}

		pluginNameProcessed := pluginName
		if tf.Path == testPlugin {
			// remove "_tests" suffix if present
			pluginNameProcessed = strings.TrimSuffix(pluginNameProcessed, "_tests")
			//fmt.Printf("  -> special test plugin detected, mapped to plugin: %s\n", pluginNameProcessed)
		}

		if _, ok := plan.Plugins[pluginNameProcessed]; !ok {
			// Plugin not loaded: record and skip execution
			plan.MissingPlugins[pluginNameProcessed] = struct{}{}
			continue
		}

		plan.TestsByPlugin[pluginNameProcessed] =
			append(plan.TestsByPlugin[pluginNameProcessed], tc)

		// Remove current plugin from plugins because it's a test-only file
		delete(plan.Plugins, pluginName)
		delete(reg.Registry, pluginName)
	}

	return plan, nil
}

func main() {
	opts := parseFlags()

	// Immediately set Plugin's mode to TestMode
	plg.TestMode = true

	// Keep failures explicit and non-panicky for CI.
	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "crowler-plugtest: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() cliOptions {
	var o cliOptions

	flag.StringVar(&o.ConfigPath, "config", "./config.yaml", "Path to config.yaml")
	flag.StringVar(&o.PType, "ptype", "engine_plugin", "Plugin type filter: engine_plugin|event_plugin|api_plugin|vdi_plugin|lib_plugin|all")
	flag.StringVar(&o.PluginsCSV, "plugins", "", "Extra plugin paths/globs (comma-separated), loaded in addition to config plugins")
	flag.StringVar(&o.TestsCSV, "tests", "", "Unit-test paths/globs (comma-separated). Example: ./plugins/*/*.test.js")
	flag.BoolVar(&o.ListOnly, "list", false, "List discovered plugins/tests and exit")
	flag.IntVar(&o.DebugLevel, "debug", 0, "Debug level (best-effort)")
	flag.IntVar(
		&o.TestTimeout,
		"test-timeout",
		30,
		"Per-plugin test timeout in seconds",
	)

	flag.Parse()
	return o
}

func run(opts cliOptions) error {
	// Best-effort debug setup: do not hard fail if common pkg changes.
	_ = opts.DebugLevel
	cmn.DebugMsg(cmn.DbgLvlInfo, "crowler-plugtest starting")

	// Load config.yaml
	conf, err := cfg.LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config %s failed: %w", opts.ConfigPath, err)
	}
	if conf.IsEmpty() {
		return fmt.Errorf("loading config %s returned nil config", opts.ConfigPath)
	}

	// Prepare DB handler (optional for now)
	// We create it only so the CLI already matches the runtime shape.
	// If it fails, we continue with nil and keep the error visible in logs.
	var db *cdb.Handler
	db, _ = tryInitDB(&conf)

	// Load plugins using the same loader used by the engine
	reg := plg.NewJSPluginRegister()
	pType := normalizePType(opts.PType)

	// Load from config paths first
	reg.LoadPluginsFromConfig(&conf, pType)

	// Load extra plugin globs provided by CLI
	extraPluginGlobs := splitCSV(opts.PluginsCSV)
	if len(extraPluginGlobs) > 0 {
		if err := loadExtraPlugins(reg, extraPluginGlobs, pType); err != nil {
			return err
		}
	}

	// Discover unit-test files (no execution yet)
	testGlobs := splitCSV(opts.TestsCSV)
	if len(testGlobs) == 0 {
		// Conservative default: only look in ./plugins for now.
		testGlobs = []string{
			"./plugins/*.tests.js",
			"./plugins/*/*.tests.js",
		}
	}

	tests, err := discoverTests(testGlobs)
	if err != nil {
		return err
	}

	// Discover tests from loaded plugins
	tests = append(tests, plg.DiscoverTestsFromPlugins(reg)...)

	// Build test plan mapping tests to loaded plugins
	plan, err := buildTestPlan(reg, tests)
	if err != nil {
		return fmt.Errorf("building test plan failed: %w", err)
	}

	execOrder := make([]string, 0, len(plan.Plugins))
	for name := range plan.Plugins {
		execOrder = append(execOrder, name)
	}
	sort.Strings(execOrder) // optional, for stable output

	// Output plan
	printTestPlan(plan, execOrder)

	if opts.ListOnly {
		return nil
	}

	execRes, err := runTestPlan(plan, reg.Order, db, opts.TestTimeout)
	if err != nil {
		return err
	}

	printExecResult(execRes)

	if execRes.Failed > 0 {
		return fmt.Errorf("%d test(s) failed", execRes.Failed)
	}

	// Execution will come after we update pkg/plugin to load tests + run in same VM/runtime.
	// For now, make the CLI succeed if discovery succeeded.
	_ = db
	return nil
}

func tryInitDB(conf *cfg.Config) (*cdb.Handler, error) {
	// I am intentionally not hard-failing here because unit tests should be runnable
	// without a DB when possible. Later we can add -require-db to force it.
	//
	// If your database package exposes a canonical constructor, plug it here.
	// If it changed, this function can be adapted without touching the CLI contract.
	handler, err := cdb.NewHandler(*conf)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlWarn, "DB init failed (continuing without DB): %v", err)
		return nil, err
	}
	return &handler, nil
}

func loadExtraPlugins(reg *plg.JSPluginRegister, globs []string, pType string) error {
	// Build a minimal cfg.PluginConfig reusing the existing BulkLoadPlugins path.
	pc := cfg.PluginConfig{
		Path: globs,
		// Host empty means "local" path in BulkLoadPlugins. :contentReference[oaicite:3]{index=3}
	}

	plugins, err := plg.BulkLoadPlugins(pc, pType)
	if err != nil {
		return fmt.Errorf("loading extra plugins failed: %w", err)
	}

	for _, p := range plugins {
		reg.Register(p.Name, *p)
	}
	return nil
}

func discoverTests(globs []string) ([]plg.TestFile, error) {
	seen := make(map[string]struct{})
	var out []plg.TestFile

	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}

		matches, err := filepath.Glob(g)
		if err != nil {
			return nil, fmt.Errorf("bad test glob %q: %w", g, err)
		}

		for _, path := range matches {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}

			b, err := os.ReadFile(path) // #nosec G304 // this is a test tool
			if err != nil {
				return nil, fmt.Errorf("reading test file %s failed: %w", path, err)
			}

			body := string(b)
			name := parseNameFromHeader(body, path) // reuse plugin naming convention logic :contentReference[oaicite:4]{index=4}
			out = append(out, plg.TestFile{Path: path, Name: name, Body: body})
		}
	}

	return out, nil
}

func printTestPlan(plan *TestPlan, order []string) {
	fmt.Println("=== Plugin Test Plan ===")

	if len(plan.Plugins) == 0 {
		fmt.Println("No plugins loaded")
		return
	}

	for _, name := range order {
		tests := plan.TestsByPlugin[name]
		fmt.Printf("- %s\n", name)

		if len(tests) == 0 {
			fmt.Println("  (no tests)")
			continue
		}

		for _, t := range tests {
			fmt.Printf("  - %s (%s)\n", t.TestName, t.TestPath)
		}
	}

	if len(plan.MissingPlugins) > 0 {
		fmt.Println()
		fmt.Println("=== Tests skipped (missing plugins) ===")
		for p := range plan.MissingPlugins {
			fmt.Printf("- %s\n", p)
		}
	}
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizePType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "all" {
		return "" // BulkLoadPlugins treats empty as "no filter" :contentReference[oaicite:5]{index=5}
	}
	switch s {
	// This tool is not suitable for `vdi_plugin` because those plugins
	// require a full VDI environment to run tests properly.
	case "engine_plugin", "event_plugin", "api_plugin", "lib_plugin":
		return s
	default:
		// Keep it strict for CI. Users get a clear error.
		panicOnInvalidPType(s)
		return ""
	}
}

func panicOnInvalidPType(s string) {
	// Using panic here keeps the code short; we catch nothing in main,
	// so it becomes a clean non-zero exit in CI with a clear message.
	panic(errors.New("invalid -ptype: " + s))
}

// parseNameFromHeader matches the existing plugin naming convention:
// first non-empty line can be: // name: <plugin_name>
// otherwise fallback to filename without extension. :contentReference[oaicite:6]{index=6}
func parseNameFromHeader(body string, file string) string {
	line0 := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			line0 = line
			break
		}
	}
	name := ""
	if strings.HasPrefix(line0, "//") {
		line0 = strings.TrimSpace(line0[2:])
		if strings.HasPrefix(strings.ToLower(line0), "name:") {
			name = strings.TrimSpace(line0[5:])
		}
	}
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)))
	}
	return name
}

func runTestPlan(
	plan *TestPlan,
	order []string,
	db *cdb.Handler,
	timeout int,
) (*ExecResult, error) {

	if plan == nil {
		return nil, fmt.Errorf("nil test plan")
	}

	res := &ExecResult{
		Results: make([]TestResult, 0),
	}

	for _, pluginName := range order {
		plugin, ok := plan.Plugins[pluginName]
		if !ok {
			continue
		}

		tests := plan.TestsByPlugin[pluginName]
		if len(tests) == 0 {
			continue
		}

		if timeout <= 0 {
			timeout = 30
		}

		for _, tc := range tests {
			testResults, err := plg.ExecEnginePluginTest(
				&plugin,
				tc.Body,
				db,
				plg.TestOptions{
					Timeout: timeout, // seconds, configurable later
					Params:  map[string]interface{}{},
				},
			)

			if err != nil {
				// Hard execution failure: report as a failed test entry
				res.Results = append(res.Results, TestResult{
					Plugin:   pluginName,
					TestName: tc.TestName,
					Passed:   false,
					Error:    err.Error(),
				})
				res.Failed++
				continue
			}

			for _, tr := range testResults {
				entry := TestResult{
					Plugin:   pluginName,
					TestName: tr.Name,
					Passed:   tr.Passed,
					Error:    tr.Error,
				}

				res.Results = append(res.Results, entry)

				if !tr.Passed {
					res.Failed++
				}
			}
		}
	}

	return res, nil
}

func printExecResult(res *ExecResult) {
	fmt.Println()
	fmt.Println("=== Test Results ===")

	for _, r := range res.Results {
		if r.Passed {
			fmt.Printf("[PASS] %s :: %s\n", r.Plugin, r.TestName)
		} else {
			fmt.Printf("[FAIL] %s :: %s\n", r.Plugin, r.TestName)
			fmt.Printf("       %s\n", r.Error)
		}
	}

	fmt.Println()
	fmt.Printf("Total: %d, Failed: %d\n", len(res.Results), res.Failed)
}
