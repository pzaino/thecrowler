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
	"fmt"
	"net/url"
	"strconv"
	"strings"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

// ErrEmailCrawlingNotConfigured identifies email sources that cannot be
// dispatched because their provider-neutral configuration or dependencies are absent.
var ErrEmailCrawlingNotConfigured = errors.New("email crawling not configured")

// EmailCrawlingNotConfiguredError describes the missing email crawl input.
type EmailCrawlingNotConfiguredError struct {
	Missing string
	Err     error
}

// Error implements error.
func (err *EmailCrawlingNotConfiguredError) Error() string {
	if err == nil || err.Missing == "" {
		return ErrEmailCrawlingNotConfigured.Error()
	}
	if err.Err != nil {
		return fmt.Sprintf("%s: %s: %v", ErrEmailCrawlingNotConfigured, err.Missing, err.Err)
	}
	return fmt.Sprintf("%s: missing %s", ErrEmailCrawlingNotConfigured, err.Missing)
}

// Unwrap exposes the source configuration error when one is available.
func (err *EmailCrawlingNotConfiguredError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

// Is lets callers use errors.Is with ErrEmailCrawlingNotConfigured.
func (err *EmailCrawlingNotConfiguredError) Is(target error) bool {
	return target == ErrEmailCrawlingNotConfigured
}

// EmailCrawlResult is the crawler-facing representation of one normalized mail
// document. Document is retained for consumers that need complete mail
// metadata, while Page is suitable for the existing crawler indexing path.
type EmailCrawlResult struct {
	Source   cdb.Source
	Document mail.Document
	Page     PageInfo
}

// EmailResultHandler accepts documents after they have been adapted to crawler
// results. Tests and callers can inject fakes without implementing connectors.
type EmailResultHandler interface {
	HandleEmailResult(ctx context.Context, result EmailCrawlResult) error
}

type emailResultEmitter struct {
	source  cdb.Source
	handler EmailResultHandler
}

func (emitter emailResultEmitter) Emit(ctx context.Context, document mail.Document) error {
	return emitter.handler.HandleEmailResult(ctx, EmailCrawlResult{
		Source:   emitter.source,
		Document: document,
		Page:     emailDocumentPage(emitter.source, document),
	})
}

func crawlEmail(ctx context.Context, args *Pars) error {
	if args == nil {
		return &EmailCrawlingNotConfiguredError{Missing: "crawler arguments"}
	}
	return crawlEmailWithResultHandler(ctx, args, args.EmailResultHandler)
}

func crawlEmailWithResultHandler(ctx context.Context, args *Pars, resultHandler EmailResultHandler) error {
	if args == nil {
		return &EmailCrawlingNotConfiguredError{Missing: "crawler arguments"}
	}

	var rawConfig json.RawMessage
	if args.Src.Config != nil {
		rawConfig = *args.Src.Config
	}
	config, err := mail.ResolveSourceConfig(args.EmailConfig, rawConfig)
	if err != nil {
		return &EmailCrawlingNotConfiguredError{Missing: "mail configuration", Err: err}
	}
	if resultHandler == nil {
		return &EmailCrawlingNotConfiguredError{Missing: "email result handler"}
	}

	runner := args.EmailRunner
	if runner == nil {
		if args.EmailDependencies == nil {
			return &EmailCrawlingNotConfiguredError{Missing: "mail pipeline dependencies"}
		}
		runner = mail.NewPipelineRunner(*args.EmailDependencies)
	}

	return runner.RunSource(ctx, mail.SourceRunRequest{
		SourceID: strconv.FormatUint(args.Src.ID, 10),
		Config:   config,
		Emitter:  emailResultEmitter{source: args.Src, handler: resultHandler},
	})
}

func emailDocumentPage(source cdb.Source, document mail.Document) PageInfo {
	body := document.ExtractedText
	if strings.TrimSpace(body) == "" {
		body = document.TextBody
	}

	links := make([]LinkItem, 0, len(document.Links))
	pageURL := emailDocumentURL(source.URL, document.ID)
	for _, link := range document.Links {
		links = append(links, LinkItem{PageURL: pageURL, Link: link.URL})
	}

	metadata := ScrapedItem{"email": document}
	return PageInfo{
		URL:          pageURL,
		sourceID:     source.ID,
		Title:        document.Subject,
		Summary:      document.Subject,
		BodyText:     body,
		HTML:         document.HTMLBody,
		DetectedType: "message/rfc822",
		ScrapedData:  []ScrapedItem{metadata},
		Links:        links,
	}
}

func emailDocumentURL(sourceURL, documentID string) string {
	separator := "#"
	if strings.Contains(sourceURL, "#") {
		separator = "&"
	}
	return sourceURL + separator + "message=" + url.QueryEscape(documentID)
}

type emailIndexResultHandler struct {
	processCtx *ProcessContext
}

func (handler emailIndexResultHandler) HandleEmailResult(_ context.Context, result EmailCrawlResult) error {
	if handler.processCtx == nil {
		return errors.New("email crawler process context is nil")
	}
	page := result.Page
	page.Config = &handler.processCtx.config
	if _, err := indexPage(handler.processCtx, page.URL, &page); err != nil {
		return fmt.Errorf("index email document %s: %w", result.Document.ID, err)
	}
	if handler.processCtx.Status != nil {
		handler.processCtx.Status.TotalPages.Add(1)
		handler.processCtx.Status.TotalScraped.Add(1)
	}
	return nil
}

var _ mail.Emitter = emailResultEmitter{}
var _ EmailResultHandler = emailIndexResultHandler{}
