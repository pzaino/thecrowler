package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SourceRunRequest contains the provider-neutral inputs for one authoritative
// reconciliation run. Emitter is supplied by the caller so normalized mail
// documents can be adapted at the application boundary without leaking
// connector details outside package mail.
type SourceRunRequest struct {
	SourceID string
	Config   SourceConfig
	Emitter  Emitter
}

// SourceRunner reconciles one configured mail source.
type SourceRunner interface {
	RunSource(ctx context.Context, request SourceRunRequest) error
}

// SummarizingSourceRunner additionally exposes the aggregate outcome of a
// source reconciliation while preserving SourceRunner compatibility.
type SummarizingSourceRunner interface {
	SourceRunner
	RunSourceWithSummary(ctx context.Context, request SourceRunRequest) (RunSummary, error)
}

// ConnectorFactory constructs the connector selected by SourceConfig. Provider
// dispatch, credentials, and protocol-specific construction remain in package
// mail implementations of this interface.
type ConnectorFactory interface {
	NewConnector(ctx context.Context, config SourceConfig) (Connector, error)
}

// ConnectorFactoryFunc adapts a function into ConnectorFactory.
type ConnectorFactoryFunc func(context.Context, SourceConfig) (Connector, error)

// NewConnector implements ConnectorFactory.
func (factory ConnectorFactoryFunc) NewConnector(ctx context.Context, config SourceConfig) (Connector, error) {
	return factory(ctx, config)
}

// PipelineDependencies are the replaceable side-effect boundaries used to
// construct a Pipeline for a source. Connector may be injected directly for a
// single source; otherwise ConnectorFactory constructs it from SourceConfig.
type PipelineDependencies struct {
	Connector                 Connector
	ConnectorFactory          ConnectorFactory
	StateStore                StateStore
	Processor                 Processor
	RetryPolicy               RetryPolicy
	Sleep                     func(context.Context, time.Duration) error
	LifecycleEventSink        LifecycleEventSink
	LifecycleEventRetryPolicy RetryPolicy
	LogHook                   LogHook
	Now                       func() time.Time
}

// PipelineRunner constructs and runs Pipeline instances from source config.
type PipelineRunner struct {
	Dependencies PipelineDependencies
}

// NewPipelineRunner returns a source runner backed by Pipeline.
func NewPipelineRunner(dependencies PipelineDependencies) *PipelineRunner {
	return &PipelineRunner{Dependencies: dependencies}
}

// RunSource resolves injected or constructed dependencies, configures the
// provider-neutral pipeline, and performs one authoritative reconciliation.
func (runner *PipelineRunner) RunSource(ctx context.Context, request SourceRunRequest) error {
	_, err := runner.RunSourceWithSummary(ctx, request)
	return err
}

// RunSourceWithSummary performs RunSource and returns its aggregate run summary.
func (runner *PipelineRunner) RunSourceWithSummary(ctx context.Context, request SourceRunRequest) (RunSummary, error) {
	if runner == nil {
		return RunSummary{}, errors.New("mail: pipeline runner is nil")
	}
	if err := ValidateSourceConfig(request.Config); err != nil {
		return RunSummary{}, fmt.Errorf("mail: validate source config: %w", err)
	}
	if strings.TrimSpace(request.SourceID) == "" {
		return RunSummary{}, errors.New("mail: source ID is required")
	}
	if request.Emitter == nil {
		return RunSummary{}, errors.New("mail: document emitter is required")
	}
	if runner.Dependencies.StateStore == nil {
		return RunSummary{}, errors.New("mail: state store is required")
	}

	connector := runner.Dependencies.Connector
	if connector == nil {
		if runner.Dependencies.ConnectorFactory == nil {
			return RunSummary{}, errors.New("mail: connector or connector factory is required")
		}
		var err error
		connector, err = runner.Dependencies.ConnectorFactory.NewConnector(ctx, request.Config)
		if err != nil {
			return RunSummary{}, fmt.Errorf("mail: construct connector: %w", err)
		}
		if connector == nil {
			return RunSummary{}, errors.New("mail: connector factory returned nil connector")
		}
	}

	processor := runner.Dependencies.Processor
	if processor == nil {
		processor = NewProcessorWithLimits(request.SourceID, request.Config.Extraction, request.Config.Crawl.Limits)
	}

	pipeline := NewPipeline(connector, runner.Dependencies.StateStore, processor, request.Emitter)
	pipeline.SourceID = request.SourceID
	pipeline.Provider = strings.ToLower(strings.TrimSpace(request.Config.Connector.Provider))
	pipeline.AccountID = strings.TrimSpace(request.Config.Auth.Identity)
	pipeline.PageSize = request.Config.Reconciliation.PageSize
	if pipeline.PageSize < 1 {
		pipeline.PageSize = request.Config.Crawl.BatchSize
	}
	pipeline.FetchOptions = FetchOptions{
		Headers:     append([]string(nil), request.Config.Extraction.IncludeHeaders...),
		IncludeBody: true,
		MaxBytes:    request.Config.Crawl.Limits.MaxMessageBytes,
	}
	pipeline.RetryPolicy = runner.Dependencies.RetryPolicy
	pipeline.Sleep = runner.Dependencies.Sleep
	pipeline.LifecycleEventSink = runner.Dependencies.LifecycleEventSink
	pipeline.LifecycleEventRetryPolicy = runner.Dependencies.LifecycleEventRetryPolicy
	pipeline.LogHook = runner.Dependencies.LogHook
	pipeline.Now = runner.Dependencies.Now

	summary, err := pipeline.RunWithSummary(ctx)
	if err != nil {
		return summary, fmt.Errorf("mail: reconcile source %s: %w", request.SourceID, err)
	}
	return summary, nil
}

var _ SourceRunner = (*PipelineRunner)(nil)
var _ SummarizingSourceRunner = (*PipelineRunner)(nil)
