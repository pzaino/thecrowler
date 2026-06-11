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
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestEmailDocumentPageMapsDeterministicArtifactMetadata(t *testing.T) {
	messageDate := time.Date(2026, time.June, 10, 23, 45, 12, 345000000, time.FixedZone("sender", -7*60*60))
	document := mail.Document{
		ID:                 "mail:provider_id:account:message/42",
		IdentityStrategy:   mail.IdentityProviderID,
		ContentFingerprint: strings.Repeat("f", 64),
		SourceID:           "configured-source",
		Ref: mail.MessageRef{
			Provider:          "gmail",
			AccountID:         "account-1",
			Mailbox:           mail.Mailbox{ID: "all", Name: "All Mail"},
			UID:               42,
			UIDValidity:       9,
			ProviderMessageID: "provider-message-42",
			ProviderThreadID:  "provider-thread-7",
		},
		MessageID:     "<message-42@example.test>",
		ThreadID:      "thread-7",
		Date:          messageDate,
		From:          []mail.Address{{Address: "invalid"}, {Normalized: "Sender@Sub.Example.TEST."}},
		Subject:       "Quarterly report",
		TextBody:      "plain fallback",
		ExtractedText: "normalized body",
		HTMLBody:      "<p>normalized body</p>",
		Links: []mail.Link{{
			URL:            "https://example.test/report",
			Text:           "report",
			Title:          "Quarterly report",
			Source:         "html",
			Classification: mail.LinkNormal,
		}},
		Attachments: []mail.Attachment{{
			ID:                "attachment-1",
			Filename:          "report.pdf",
			MediaType:         "application/octet-stream",
			DetectedMediaType: "application/pdf",
			Disposition:       "attachment",
			Size:              2048,
			SHA256:            strings.Repeat("a", 64),
			Content:           io.NopCloser(strings.NewReader("must not be indexed")),
		}},
		Warnings: []mail.ParserWarning{{
			Category: mail.WarningOversizedPart,
			Code:     "part_truncated",
			Message:  "part exceeded extraction limit",
			PartID:   "2",
		}},
		Security: mail.SecuritySignals{
			SPF:                   "pass",
			DKIM:                  "pass",
			DMARC:                 "pass",
			TLS:                   true,
			AuthenticationResults: []string{"mx.example.test; spf=pass"},
		},
	}
	source := cdb.Source{ID: 42, URL: "email://account"}

	first := emailDocumentPage(source, document)
	second := emailDocumentPage(source, document)
	firstJSON, err := json.Marshal(first.ScrapedData)
	if err != nil {
		t.Fatalf("marshal first mapped artifact: %v", err)
	}
	secondJSON, err := json.Marshal(second.ScrapedData)
	if err != nil {
		t.Fatalf("marshal second mapped artifact: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("mapping is not deterministic:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
	if strings.Contains(string(firstJSON), "must not be indexed") {
		t.Fatalf("mapped artifact exposed attachment content: %s", firstJSON)
	}

	artifact, ok := first.ScrapedData[0]["email"].(EmailArtifact)
	if !ok {
		t.Fatalf("mapped email metadata type = %T, want EmailArtifact", first.ScrapedData[0]["email"])
	}
	if artifact.SourceType != "email" || artifact.CanonicalURI != first.URL {
		t.Errorf("artifact source identity = %#v", artifact)
	}
	if artifact.Subject != document.Subject || artifact.SenderDomain != "sub.example.test" {
		t.Errorf("artifact message metadata = %#v", artifact)
	}
	if artifact.Date != "2026-06-11T06:45:12.345Z" {
		t.Errorf("artifact date = %q", artifact.Date)
	}
	if artifact.ContentType != "message/rfc822" || artifact.ExtractedText != document.ExtractedText {
		t.Errorf("artifact content mapping = %#v", artifact)
	}
	if len(artifact.Links) != 1 || artifact.Links[0] != document.Links[0] {
		t.Errorf("artifact links = %#v", artifact.Links)
	}
	if len(artifact.Attachments) != 1 {
		t.Fatalf("artifact attachments = %#v", artifact.Attachments)
	}
	attachment := artifact.Attachments[0]
	if attachment.ParentID != document.ID || attachment.ParentURI != first.URL ||
		attachment.ContentType != "application/pdf" || attachment.Filename != "report.pdf" ||
		attachment.Size != 2048 || attachment.SHA256 != strings.Repeat("a", 64) {
		t.Errorf("artifact attachment metadata = %#v", attachment)
	}
	if artifact.Provenance.SourceID != source.ID ||
		artifact.Provenance.DocumentSourceID != document.SourceID ||
		artifact.Provenance.DocumentID != document.ID ||
		artifact.Provenance.Provider != "gmail" ||
		artifact.Provenance.ProviderMessageID != "provider-message-42" ||
		artifact.Provenance.IdentityStrategy != mail.IdentityProviderID {
		t.Errorf("artifact provenance = %#v", artifact.Provenance)
	}
	if len(artifact.Warnings) != 1 || artifact.Warnings[0].Code != "part_truncated" {
		t.Errorf("artifact warnings = %#v", artifact.Warnings)
	}
	if artifact.SecuritySignals.SPF != "pass" || artifact.SecuritySignals.DKIM != "pass" ||
		artifact.SecuritySignals.DMARC != "pass" || !artifact.SecuritySignals.TLS {
		t.Errorf("artifact security signals = %#v", artifact.SecuritySignals)
	}
}

func TestEmailDocumentPageUsesTextFallbackAndEmptySenderDomain(t *testing.T) {
	page := emailDocumentPage(cdb.Source{ID: 7, URL: "imap://account#mailbox=INBOX"}, mail.Document{
		ID:       "message with spaces",
		From:     []mail.Address{{Address: "not-an-email"}},
		TextBody: "plain fallback",
	})

	if page.URL != "imap://account#mailbox=INBOX&message=message+with+spaces" {
		t.Errorf("page canonical URI = %q", page.URL)
	}
	if page.BodyText != "plain fallback" {
		t.Errorf("page body = %q", page.BodyText)
	}
	artifact := page.ScrapedData[0]["email"].(EmailArtifact)
	if artifact.ExtractedText != "plain fallback" {
		t.Errorf("artifact extracted text = %q", artifact.ExtractedText)
	}
	if artifact.SenderDomain != "" || artifact.Date != "" {
		t.Errorf("artifact optional metadata = %#v", artifact)
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

type recordingWebCrawlQueue struct {
	links []LinkItem
	err   error
}

func (queue *recordingWebCrawlQueue) EnqueueWebCrawl(_ context.Context, link LinkItem) error {
	if queue.err != nil {
		return queue.err
	}
	queue.links = append(queue.links, link)
	return nil
}

func TestCrawlEmailDefaultsAllRemoteLinksToRecordOnly(t *testing.T) {
	config := testMailSourceConfig()
	queue := &recordingWebCrawlQueue{}
	handler := &recordingEmailResultHandler{}
	runner := &fakeEmailRunner{document: mail.Document{
		ID: "default-policy",
		Links: []mail.Link{
			{URL: "https://example.test/article", Classification: mail.LinkNormal},
			{URL: "https://accounts.example.test/reset-password?token=secret", Classification: mail.LinkAuthAction},
			{URL: "mailto:owner@example.test", Classification: mail.LinkMailto},
		},
	}}

	err := crawlEmail(context.Background(), &Pars{
		Src:                cdb.Source{ID: 7, URL: "maildir:///tmp/mail"},
		EmailConfig:        &config,
		EmailRunner:        runner,
		EmailResultHandler: handler,
		WebCrawlQueue:      queue,
	})
	if err != nil {
		t.Fatalf("crawlEmail() error = %v", err)
	}
	if len(queue.links) != 0 {
		t.Fatalf("default policy queued links = %#v, want none", queue.links)
	}
	if got := handler.result.Page.Links; len(got) != 2 || got[0].Link != "https://example.test/article" || got[1].Link != "https://accounts.example.test/reset-password?token=secret" {
		t.Errorf("recorded links = %#v, want normal and authentication audit links", got)
	}
}

func TestCrawlEmailEnqueuesOnlyExplicitPolicyApprovals(t *testing.T) {
	config := testMailSourceConfig()
	config.Extraction.Links.FollowRemote = true
	config.Extraction.Links.AllowedSchemes = []string{"https"}
	config.Extraction.Links.Allowlist = []string{"safe.example.test"}
	config.Extraction.Links.SuppressUnsubscribe = true
	queue := &recordingWebCrawlQueue{}
	handler := &recordingEmailResultHandler{}
	runner := &fakeEmailRunner{document: mail.Document{
		ID: "policy-approved",
		Links: []mail.Link{
			{URL: "https://safe.example.test/article", Classification: mail.LinkNormal},
			{URL: "https://other.example.test/article", Classification: mail.LinkNormal},
			{URL: "http://safe.example.test/insecure", Classification: mail.LinkNormal},
			{URL: "https://safe.example.test/unsubscribe?token=secret", Classification: mail.LinkUnsubscribe},
			{URL: "https://safe.example.test/reset-password?token=secret", Classification: mail.LinkAuthAction},
			{URL: "cid:inline-image", Classification: mail.LinkCID},
			{URL: "mailto:owner@example.test", Classification: mail.LinkMailto},
			{URL: "javascript:alert(1)", Classification: mail.LinkUnknown},
			{URL: "https://user:secret@safe.example.test/private", Classification: mail.LinkNormal},
		},
	}}

	err := crawlEmail(context.Background(), &Pars{
		Src:                cdb.Source{ID: 8, URL: "imap://mail.example.test"},
		EmailConfig:        &config,
		EmailRunner:        runner,
		EmailResultHandler: handler,
		WebCrawlQueue:      queue,
	})
	if err != nil {
		t.Fatalf("crawlEmail() error = %v", err)
	}
	want := []LinkItem{{
		PageURL: "imap://mail.example.test#message=policy-approved",
		Link:    "https://safe.example.test/article",
	}}
	if !reflect.DeepEqual(queue.links, want) {
		t.Fatalf("queued links = %#v, want %#v", queue.links, want)
	}

	recorded := handler.result.Page.Links
	if len(recorded) != 4 {
		t.Fatalf("recorded links = %#v, want four retained audit links", recorded)
	}
	for _, suppressed := range []string{"unsubscribe", "cid:", "mailto:", "javascript:", "user:secret"} {
		for _, link := range recorded {
			if strings.Contains(link.Link, suppressed) {
				t.Errorf("suppressed link %q was retained in %#v", suppressed, recorded)
			}
		}
	}
}

func TestCrawlEmailPropagatesWebQueueFailure(t *testing.T) {
	config := testMailSourceConfig()
	config.Extraction.Links.FollowRemote = true
	config.Extraction.Links.Allowlist = []string{"safe.example.test"}
	wantErr := errors.New("queue unavailable")

	err := crawlEmail(context.Background(), &Pars{
		Src:         cdb.Source{ID: 9, URL: "maildir:///tmp/mail"},
		EmailConfig: &config,
		EmailRunner: &fakeEmailRunner{document: mail.Document{
			ID:    "queue-error",
			Links: []mail.Link{{URL: "https://safe.example.test/article"}},
		}},
		EmailResultHandler: &recordingEmailResultHandler{},
		WebCrawlQueue:      &recordingWebCrawlQueue{err: wantErr},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("crawlEmail() error = %v, want %v", err, wantErr)
	}
}
