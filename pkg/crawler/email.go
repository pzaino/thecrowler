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
	"sync"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
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

// EmailArtifact is the stable crawler artifact projection of a normalized mail
// document. It deliberately contains attachment metadata rather than attachment
// content, while retaining the parser and transport observations needed by
// indexing, audit, and policy consumers.
type EmailArtifact struct {
	SourceType      string                         `json:"source_type"`
	CanonicalURI    string                         `json:"canonical_uri"`
	Subject         string                         `json:"subject,omitempty"`
	SenderDomain    string                         `json:"sender_domain,omitempty"`
	Date            string                         `json:"date,omitempty"`
	ContentType     string                         `json:"content_type"`
	ExtractedText   string                         `json:"extracted_text,omitempty"`
	Links           []mail.Link                    `json:"links,omitempty"`
	Attachments     []mail.ChildDocumentDescriptor `json:"attachments,omitempty"`
	Provenance      EmailArtifactProvenance        `json:"provenance"`
	Warnings        []mail.ParserWarning           `json:"warnings,omitempty"`
	SecuritySignals mail.SecuritySignals           `json:"security_signals"`
}

// EmailArtifactProvenance identifies the crawler source and provider message
// from which an EmailArtifact was derived without retaining connector objects
// or raw message content.
type EmailArtifactProvenance struct {
	SourceID           uint64                `json:"source_id"`
	DocumentSourceID   string                `json:"document_source_id,omitempty"`
	DocumentID         string                `json:"document_id"`
	IdentityStrategy   mail.IdentityStrategy `json:"identity_strategy,omitempty"`
	ContentFingerprint string                `json:"content_fingerprint,omitempty"`
	Provider           string                `json:"provider,omitempty"`
	AccountID          string                `json:"account_id,omitempty"`
	MailboxID          string                `json:"mailbox_id,omitempty"`
	MailboxName        string                `json:"mailbox_name,omitempty"`
	UID                uint32                `json:"uid,omitempty"`
	UIDValidity        uint32                `json:"uid_validity,omitempty"`
	ProviderMessageID  string                `json:"provider_message_id,omitempty"`
	ProviderThreadID   string                `json:"provider_thread_id,omitempty"`
	MessageID          string                `json:"message_id,omitempty"`
	ThreadID           string                `json:"thread_id,omitempty"`
}

// EmailResultHandler accepts documents after they have been adapted to crawler
// results. Tests and callers can inject fakes without implementing connectors.
type EmailResultHandler interface {
	HandleEmailResult(ctx context.Context, result EmailCrawlResult) error
}

// WebCrawlQueue accepts policy-approved links for the crawler's existing web
// fetching path. Email ingestion never submits a link unless its message-scoped
// mail policy returns LinkDecisionEnqueue.
type WebCrawlQueue interface {
	EnqueueWebCrawl(ctx context.Context, link LinkItem) error
}

type emailPolicyResultHandler struct {
	policy mail.LinkPolicy
	queue  WebCrawlQueue
	next   EmailResultHandler
}

func (handler emailPolicyResultHandler) HandleEmailResult(ctx context.Context, result EmailCrawlResult) error {
	evaluator := mail.NewLinkPolicyEvaluator(handler.policy)
	retained := make([]mail.Link, 0, len(result.Document.Links))
	approved := make([]LinkItem, 0, len(result.Document.Links))
	pageURL := emailDocumentURL(result.Source.URL, result.Document.ID)

	for _, link := range result.Document.Links {
		switch evaluator.Evaluate(link) {
		case mail.LinkDecisionEnqueue:
			retained = append(retained, link)
			approved = append(approved, LinkItem{PageURL: pageURL, Link: link.URL})
		case mail.LinkDecisionRecordOnly:
			retained = append(retained, link)
		}
	}

	result.Document.Links = retained
	result.Page = emailDocumentPage(result.Source, result.Document)
	if err := handler.next.HandleEmailResult(ctx, result); err != nil {
		return err
	}
	if handler.queue == nil {
		return nil
	}
	for _, link := range approved {
		if err := handler.queue.EnqueueWebCrawl(ctx, link); err != nil {
			return fmt.Errorf("enqueue email link %s: %w", link.Link, err)
		}
	}
	return nil
}

type bufferedWebCrawlQueue struct {
	mu    sync.Mutex
	links []LinkItem
}

func (queue *bufferedWebCrawlQueue) EnqueueWebCrawl(ctx context.Context, link LinkItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.links = append(queue.links, link)
	return nil
}

func (queue *bufferedWebCrawlQueue) drain() []LinkItem {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	links := append([]LinkItem(nil), queue.links...)
	queue.links = nil
	return links
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

	policyHandler := emailPolicyResultHandler{
		policy: config.Extraction.Links,
		queue:  args.WebCrawlQueue,
		next:   resultHandler,
	}
	return runner.RunSource(ctx, mail.SourceRunRequest{
		SourceID: strconv.FormatUint(args.Src.ID, 10),
		Config:   config,
		Emitter:  emailResultEmitter{source: args.Src, handler: policyHandler},
	})
}

func emailDocumentPage(source cdb.Source, document mail.Document) PageInfo {
	body := document.ExtractedText
	if strings.TrimSpace(body) == "" {
		body = document.TextBody
	}

	pageURL := emailDocumentURL(source.URL, document.ID)
	links := make([]LinkItem, len(document.Links))
	for index, link := range document.Links {
		links[index] = LinkItem{PageURL: pageURL, Link: link.URL}
	}

	artifact := emailDocumentArtifact(source, pageURL, document, body)
	return PageInfo{
		URL:          pageURL,
		sourceID:     source.ID,
		Title:        document.Subject,
		Summary:      document.Subject,
		BodyText:     body,
		HTML:         document.HTMLBody,
		DetectedType: artifact.ContentType,
		ScrapedData:  []ScrapedItem{{"email": artifact}},
		Links:        links,
	}
}

func emailDocumentArtifact(source cdb.Source, canonicalURI string, document mail.Document, extractedText string) EmailArtifact {
	date := ""
	if !document.Date.IsZero() {
		date = document.Date.UTC().Format(time.RFC3339Nano)
	}

	return EmailArtifact{
		SourceType:    "email",
		CanonicalURI:  canonicalURI,
		Subject:       document.Subject,
		SenderDomain:  emailSenderDomain(document.From),
		Date:          date,
		ContentType:   "message/rfc822",
		ExtractedText: extractedText,
		Links:         append([]mail.Link(nil), document.Links...),
		Attachments:   document.AttachmentDocumentDescriptors(canonicalURI),
		Provenance: EmailArtifactProvenance{
			SourceID:           source.ID,
			DocumentSourceID:   document.SourceID,
			DocumentID:         document.ID,
			IdentityStrategy:   document.IdentityStrategy,
			ContentFingerprint: document.ContentFingerprint,
			Provider:           document.Ref.Provider,
			AccountID:          document.Ref.AccountID,
			MailboxID:          document.Ref.Mailbox.ID,
			MailboxName:        document.Ref.Mailbox.Name,
			UID:                document.Ref.UID,
			UIDValidity:        document.Ref.UIDValidity,
			ProviderMessageID:  document.Ref.ProviderMessageID,
			ProviderThreadID:   document.Ref.ProviderThreadID,
			MessageID:          document.MessageID,
			ThreadID:           document.ThreadID,
		},
		Warnings:        append([]mail.ParserWarning(nil), document.Warnings...),
		SecuritySignals: document.Security,
	}
}

func emailSenderDomain(addresses []mail.Address) string {
	for _, address := range addresses {
		value := address.Normalized
		if value == "" {
			value = address.Address
		}
		at := strings.LastIndexByte(value, '@')
		if at < 0 || at == len(value)-1 {
			continue
		}
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value[at+1:])), ".")
		if domain != "" {
			return domain
		}
	}
	return ""
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

func crawlEmailWebLinks(processCtx *ProcessContext, sel vdi.SeleniumInstance, links []LinkItem) error {
	if len(links) == 0 {
		return nil
	}
	if err := processCtx.RefreshVDIConnection(sel); err != nil {
		return fmt.Errorf("prepare web crawl queue for email links: %w", err)
	}

	workerCount := processCtx.config.Crawler.Workers
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(links) {
		workerCount = len(links)
	}

	jobs := make(chan LinkItem, len(links))
	errChan := make(chan error, workerCount)
	for workerID := 1; workerID <= workerCount; workerID++ {
		processCtx.wg.Add(1)
		go func(id int) {
			defer processCtx.wg.Done()
			if err := worker(processCtx, id, jobs); err != nil {
				errChan <- err
			}
		}(workerID)
	}
	for _, link := range links {
		jobs <- link
	}
	close(jobs)
	processCtx.Status.TotalLinks.Add(int32(len(links))) // #nosec G115 -- link count is bounded by mail policy limits.
	processCtx.wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return fmt.Errorf("crawl policy-approved email link: %w", err)
		}
	}
	return nil
}

var _ mail.Emitter = emailResultEmitter{}
var _ EmailResultHandler = emailIndexResultHandler{}
