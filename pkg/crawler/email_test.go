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
	"errors"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

type recordingEmailCrawlHandler struct {
	request EmailCrawlRequest
	err     error
	calls   int
}

func (handler *recordingEmailCrawlHandler) CrawlEmail(_ context.Context, request EmailCrawlRequest) error {
	handler.calls++
	handler.request = request
	return handler.err
}

func TestCrawlEmailRequiresConfigurationAndDependencies(t *testing.T) {
	tests := []struct {
		name        string
		args        *Pars
		wantMissing string
	}{
		{name: "nil arguments", wantMissing: "mail configuration"},
		{name: "missing configuration", args: &Pars{}, wantMissing: "mail configuration"},
		{
			name:        "missing dependencies",
			args:        &Pars{EmailConfig: &mail.SourceConfig{}},
			wantMissing: "email crawl dependencies",
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

func TestCrawlEmailDelegatesProviderNeutralRequest(t *testing.T) {
	config := &mail.SourceConfig{
		Connector: mail.ConnectorConfig{Provider: "test-provider", Endpoint: "mail.example.test"},
	}
	source := cdb.Source{ID: 42, URL: "email://account"}
	handler := &recordingEmailCrawlHandler{}

	err := crawlEmail(context.Background(), &Pars{
		Src:          source,
		EmailConfig:  config,
		EmailHandler: handler,
	})
	if err != nil {
		t.Fatalf("crawlEmail() error = %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}
	if handler.request.Source.ID != source.ID || handler.request.Source.URL != source.URL {
		t.Errorf("handler source = %+v, want %+v", handler.request.Source, source)
	}
	if handler.request.Config.Connector.Provider != config.Connector.Provider ||
		handler.request.Config.Connector.Endpoint != config.Connector.Endpoint {
		t.Errorf("handler connector = %+v, want %+v", handler.request.Config.Connector, config.Connector)
	}
}

func TestCrawlEmailReturnsHandlerError(t *testing.T) {
	wantErr := errors.New("email ingestion failed")
	handler := &recordingEmailCrawlHandler{err: wantErr}

	err := crawlEmail(context.Background(), &Pars{
		EmailConfig:  &mail.SourceConfig{},
		EmailHandler: handler,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("crawlEmail() error = %v, want %v", err, wantErr)
	}
}
