package mail

import (
	"context"
	"fmt"

	browser "github.com/pzaino/thecrowler/pkg/browser"
)

// NewProcessor returns a processor that parses RFC 5322 messages and maps the
// decoded result into the normalized Document model. sourceID identifies the
// configured mail source that produced the message. An optional extraction
// configuration enables conservative HTML cleanup without changing the stored
// HTMLBody. Cleanup is disabled when no configuration is supplied.
func NewProcessor(sourceID string, extraction ...ExtractionConfig) Processor {
	var config ExtractionConfig
	if len(extraction) != 0 {
		config = extraction[0]
	}
	return &messageProcessor{
		sourceID:   sourceID,
		parser:     NewParser(),
		extraction: config,
	}
}

type messageProcessor struct {
	sourceID   string
	parser     Parser
	extraction ExtractionConfig
}

func (p *messageProcessor) Process(ctx context.Context, message RawMessage) (Document, error) {
	parsed, err := p.parser.Parse(ctx, message)
	if err != nil {
		return Document{}, err
	}

	return documentFromParsedMessage(p.sourceID, parsed, p.extraction)
}

func documentFromParsedMessage(sourceID string, parsed ParsedMessage, extraction ExtractionConfig) (Document, error) {
	extractedText := parsed.TextBody
	var links []Link
	if parsed.HTMLBody != "" {
		htmlForExtraction := parsed.HTMLBody
		if extraction.CleanupHTML {
			cleanedHTML, err := cleanupEmailHTML(parsed.HTMLBody)
			if err != nil {
				return Document{}, err
			}
			htmlForExtraction = cleanedHTML
		}

		content, err := browser.ExtractStaticHTML(htmlForExtraction)
		if err != nil {
			return Document{}, fmt.Errorf("mail: normalize HTML body: %w", err)
		}

		extractedText = content.Text
		links = make([]Link, 0, len(content.Links))
		for _, link := range content.Links {
			links = append(links, Link{
				URL:            link.Href,
				Text:           link.Text,
				Source:         "html",
				Classification: ClassifyLink(link.Href),
			})
		}
	}

	return Document{
		SourceID:      sourceID,
		Ref:           parsed.Ref,
		MessageID:     parsed.MessageID,
		ThreadID:      parsed.ThreadID,
		Date:          parsed.Date,
		From:          parsed.From,
		To:            parsed.To,
		CC:            parsed.CC,
		BCC:           parsed.BCC,
		ReplyTo:       parsed.ReplyTo,
		Subject:       parsed.Subject,
		Headers:       documentHeaders(parsed),
		TextBody:      parsed.TextBody,
		HTMLBody:      parsed.HTMLBody,
		ExtractedText: extractedText,
		Links:         links,
		Attachments:   parsed.Attachments,
		Security:      normalizeSecurity(parsed.Headers),
		Warnings:      parsed.Warnings,
	}, nil
}

func documentHeaders(parsed ParsedMessage) HeaderSet {
	dateHeaders := parsed.RawHeaders
	if len(dateHeaders["Date"]) == 0 {
		dateHeaders = parsed.Headers
	}
	_, originalDate, _ := normalizeDate(dateHeaders)
	if originalDate == "" {
		originalDate = firstHeaderValue(dateHeaders, "Date")
	}
	return HeaderSet{
		MessageID:    parsed.MessageID,
		InReplyTo:    normalizeMessageID(parsed.Headers, "In-Reply-To"),
		References:   normalizeReferences(parsed.Headers),
		ListID:       normalizeListID(parsed.Headers),
		OriginalDate: originalDate,
		Values:       parsed.Headers,
		Raw:          parsed.RawHeaders,
	}
}

func firstHeaderValue(headers HeaderMap, name string) string {
	values := headers[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
