// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

type fakeEmailRunner struct {
	request  mail.SourceRunRequest
	document mail.Document
	err      error
	calls    int
}

func (runner *fakeEmailRunner) RunSource(ctx context.Context, request mail.SourceRunRequest) error {
	runner.calls++
	runner.request = request
	if runner.err != nil {
		return runner.err
	}
	if runner.document.ID != "" {
		return request.Emitter.Emit(ctx, runner.document)
	}
	return nil
}

type recordingEmailResultHandler struct {
	result EmailCrawlResult
	err    error
	calls  int
}

func (handler *recordingEmailResultHandler) HandleEmailResult(_ context.Context, result EmailCrawlResult) error {
	handler.calls++
	handler.result = result
	return handler.err
}

func TestCrawlEmailRequiresConfigurationAndDependencies(t *testing.T) {
	config := testMailSourceConfig()
	tests := []struct {
		name        string
		args        *Pars
		wantMissing string
	}{
		{name: "nil arguments", wantMissing: "crawler arguments"},
		{name: "missing configuration", args: &Pars{}, wantMissing: "mail configuration"},
		{
			name:        "missing result handler",
			args:        &Pars{EmailConfig: &config, EmailRunner: &fakeEmailRunner{}},
			wantMissing: "email result handler",
		},
		{
			name:        "missing pipeline dependencies",
			args:        &Pars{EmailConfig: &config, EmailResultHandler: &recordingEmailResultHandler{}},
			wantMissing: "mail pipeline dependencies",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := crawlEmail(context.Background(), tt.args)
			if !errors.Is(err, ErrEmailCrawlingNotConfigured) {
				t.Fatalf("crawlEmail() error = %v, want ErrEmailCrawlingNotConfigured", err)
			}

			var notConfigured *EmailCrawlingNotConfiguredError
			if !errors.As(err, &notConfigured) {
				t.Fatalf("crawlEmail() error type = %T, want *EmailCrawlingNotConfiguredError", err)
			}
			if notConfigured.Missing != tt.wantMissing {
				t.Errorf("missing input = %q, want %q", notConfigured.Missing, tt.wantMissing)
			}
		})
	}
}

func TestCrawlEmailRunsPipelineAndAdaptsDocuments(t *testing.T) {
	config := testMailSourceConfig()
	source := cdb.Source{ID: 42, URL: "email://account"}
	runner := &fakeEmailRunner{document: mail.Document{
		ID:            "message/42",
		SourceID:      "42",
		Subject:       "Quarterly report",
		TextBody:      "plain body",
		ExtractedText: "normalized body",
		HTMLBody:      "<p>normalized body</p>",
		Links:         []mail.Link{{URL: "https://example.test/report"}},
	}}
	handler := &recordingEmailResultHandler{}

	err := crawlEmail(context.Background(), &Pars{
		Src:                source,
		EmailConfig:        &config,
		EmailRunner:        runner,
		EmailResultHandler: handler,
	})
	if err != nil {
		t.Fatalf("crawlEmail() error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if runner.request.SourceID != "42" {
		t.Errorf("runner source ID = %q, want 42", runner.request.SourceID)
	}
	if runner.request.Config.Connector.Provider != config.Connector.Provider {
		t.Errorf("runner provider = %q, want %q", runner.request.Config.Connector.Provider, config.Connector.Provider)
	}
	if handler.calls != 1 {
		t.Fatalf("result handler calls = %d, want 1", handler.calls)
	}
	if handler.result.Page.URL != "email://account#message=message%2F42" {
		t.Errorf("page URL = %q", handler.result.Page.URL)
	}
	if handler.result.Page.Title != "Quarterly report" || handler.result.Page.BodyText != "normalized body" {
		t.Errorf("adapted page = %+v", handler.result.Page)
	}
	if handler.result.Page.DetectedType != "message/rfc822" {
		t.Errorf("detected type = %q", handler.result.Page.DetectedType)
	}
	if len(handler.result.Page.Links) != 1 || handler.result.Page.Links[0].Link != "https://example.test/report" {
		t.Errorf("adapted links = %+v", handler.result.Page.Links)
	}
}

func TestCrawlEmailResolvesNestedSourceConfiguration(t *testing.T) {
	config := testMailSourceConfig()
	encoded, err := json.Marshal(map[string]any{"custom": map[string]any{"email": config}})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(encoded)
	runner := &fakeEmailRunner{}

	err = crawlEmail(context.Background(), &Pars{
		Src:                cdb.Source{ID: 7, URL: "maildir:///tmp/mail", Config: &raw},
		EmailRunner:        runner,
		EmailResultHandler: &recordingEmailResultHandler{},
	})
	if err != nil {
		t.Fatalf("crawlEmail() error = %v", err)
	}
	if runner.request.Config.Connector.Endpoint != config.Connector.Endpoint {
		t.Errorf("resolved endpoint = %q, want %q", runner.request.Config.Connector.Endpoint, config.Connector.Endpoint)
	}
}

func TestCrawlEmailReturnsRunnerAndResultErrors(t *testing.T) {
	config := testMailSourceConfig()
	t.Run("runner", func(t *testing.T) {
		wantErr := errors.New("email reconciliation failed")
		err := crawlEmail(context.Background(), &Pars{
			Src:                cdb.Source{ID: 1},
			EmailConfig:        &config,
			EmailRunner:        &fakeEmailRunner{err: wantErr},
			EmailResultHandler: &recordingEmailResultHandler{},
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("crawlEmail() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("result handler", func(t *testing.T) {
		wantErr := errors.New("index failed")
		err := crawlEmail(context.Background(), &Pars{
			Src:                cdb.Source{ID: 1},
			EmailConfig:        &config,
			EmailRunner:        &fakeEmailRunner{document: mail.Document{ID: "message"}},
			EmailResultHandler: &recordingEmailResultHandler{err: wantErr},
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("crawlEmail() error = %v, want %v", err, wantErr)
		}
	})
}

func testMailSourceConfig() mail.SourceConfig {
	config := mail.DefaultSourceConfig()
	config.Connector.Provider = "maildir"
	config.Connector.Endpoint = "maildir:///tmp/mail"
	return config
}
