package scraper

import (
	"context"
	"errors"
	"net/http"
	"time"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
)

// ContextIDProvider identifies an isolated scraping execution.
type ContextIDProvider interface {
	ContextID() string
}

// RuleCallRequest describes a named runtime rule call without carrying page data or credentials.
type RuleCallRequest struct {
	Kind       string
	Name       string
	Parameters map[string]interface{}
	Timeout    time.Duration
	Caller     string
	RuleName   string
	Step       int
}

// RuleCallResult is the value returned by a runtime rule call.
type RuleCallResult struct {
	Value interface{}
}

// RuleCaller executes runtime-defined rule calls.
type RuleCaller interface {
	CallRule(context.Context, RuleCallRequest) (RuleCallResult, error)
}

// PluginRequest describes a plugin invocation.
type PluginRequest struct {
	Name       string
	Data       []byte
	Parameters map[string]interface{}
	Caller     string
	RuleName   string
	Step       int
}

// PluginRunner executes scraper plugins.
type PluginRunner interface {
	RunPlugin(context.Context, PluginRequest) (interface{}, error)
}

// AgentRequest describes an agent invocation.
type AgentRequest struct {
	Name       string
	Data       []byte
	Parameters map[string]interface{}
	Caller     string
	RuleName   string
	Step       int
}

// AgentRunner executes scraper agents.
type AgentRunner interface {
	RunAgent(context.Context, AgentRequest) (interface{}, error)
}

// HTTPTransformRequest describes an external HTTP transformation.
type HTTPTransformRequest struct {
	URL     string
	Body    []byte
	Headers http.Header
	Timeout time.Duration
}

// HTTPTransformer performs cancellable external HTTP transformations.
type HTTPTransformer interface {
	TransformHTTP(context.Context, HTTPTransformRequest) ([]byte, error)
}

// EnvironmentProperties controls the lifetime and visibility of an environment value.
type EnvironmentProperties struct {
	Persistent   bool
	Static       bool
	SessionValid bool
	Shared       bool
	Source       string
	Type         string
}

// EnvironmentLookup reads runtime environment values.
type EnvironmentLookup interface {
	LookupEnvironment(context.Context, string) (interface{}, bool, error)
}

// EnvironmentSetter writes runtime environment values.
type EnvironmentSetter interface {
	SetEnvironment(context.Context, string, interface{}, EnvironmentProperties) error
}

// EnvironmentCleaner removes non-persistent values created for one execution.
type EnvironmentCleaner interface {
	ClearEnvironment(context.Context) error
}

// ConfigurationLookup reads runtime configuration without exposing its concrete representation.
type ConfigurationLookup interface {
	LookupConfiguration(context.Context, string) (interface{}, bool)
}

// CrowlerMetaEditor lets rules enrich the crawler-owned crowler_meta document.
type CrowlerMetaEditor interface {
	SetCrowlerMetaSection(context.Context, string, map[string]interface{}) error
	SetCrowlerMetaTag(context.Context, string, string, interface{}) error
	DeleteCrowlerMetaSection(context.Context, string) error
	DeleteCrowlerMetaTag(context.Context, string, string) error
	AddCrowlerMetaObjectType(context.Context, ...string) error
}

// Failure identifies a failed rule or post-processing step without including data or secrets.
type Failure struct {
	RuleName string
	Step     int
	Kind     string
	Name     string
	Err      error
}

// FailureReporter receives sanitized rule and step failures.
type FailureReporter interface {
	ReportFailure(context.Context, Failure)
}

// RuleHook runs at a defined point in a scraping rule lifecycle.
type RuleHook func(context.Context, *rs.ScrapingRule) error

// WaitConditionHook delegates a scraping wait condition to a browser runtime.
type WaitConditionHook func(context.Context, rs.WaitCondition) error

// ConditionsMatcher evaluates crawler-owned rule conditions.
type ConditionsMatcher func(context.Context, map[string]interface{}) (bool, error)

// ResultAugmenter adds crawler-owned extraction fields without moving selection into scraper.
type ResultAugmenter func(context.Context, *rs.ScrapingRule, map[string]interface{}) map[string]interface{}

// Runtime contains optional, narrowly-scoped capabilities used by scraper orchestration.
// A zero Runtime is valid for pure extraction and pure post-processing steps.
type Runtime struct {
	ContextIDs      ContextIDProvider
	RuleCalls       RuleCaller
	Plugins         PluginRunner
	Agents          AgentRunner
	HTTP            HTTPTransformer
	Environment     EnvironmentLookup
	EnvSetter       EnvironmentSetter
	EnvCleaner      EnvironmentCleaner
	Configuration   ConfigurationLookup
	CrowlerMeta     CrowlerMetaEditor
	Failures        FailureReporter
	BeforeRule      RuleHook
	BeforeApply     RuleHook
	WaitCondition   WaitConditionHook
	MatchConditions ConditionsMatcher
	AugmentResult   ResultAugmenter
	MatchValue      ValueMatcher
}

// NoopRuntime returns an explicit runtime with no side-effecting capabilities.
func NoopRuntime() *Runtime { return &Runtime{} }

func runtimeOrZero(runtime *Runtime) Runtime {
	if runtime == nil {
		return Runtime{}
	}
	return *runtime
}

// UnavailableCapabilityError reports a missing optional runtime capability.
type UnavailableCapabilityError struct{ Capability string }

func (e UnavailableCapabilityError) Error() string {
	return e.Capability + " runtime capability is unavailable"
}

func reportFailure(ctx context.Context, runtime Runtime, failure Failure) {
	if runtime.Failures != nil {
		runtime.Failures.ReportFailure(ctx, failure)
	}
}

// HTTPClientTransformer uses an explicitly bounded HTTP client.
type HTTPClientTransformer struct {
	Client *http.Client
}

// TransformHTTP executes a request with context cancellation and a finite timeout.
func (t HTTPClientTransformer) TransformHTTP(ctx context.Context, req HTTPTransformRequest) ([]byte, error) {
	if t.Client == nil {
		return nil, errors.New("HTTP client is required")
	}
	if t.Client.Timeout <= 0 && req.Timeout <= 0 {
		return nil, errors.New("HTTP client or request timeout is required")
	}
	return transformHTTP(ctx, t.Client, req)
}

func environmentProperties(properties rs.EnvProperties) EnvironmentProperties {
	return EnvironmentProperties{
		Persistent: properties.Persistent, Static: properties.Static,
		SessionValid: properties.SessionValid, Shared: properties.Shared,
		Source: properties.Source, Type: properties.Type,
	}
}
