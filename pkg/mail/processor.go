package mail

import (
	"context"
)

// NewProcessor returns a processor that parses RFC 5322 messages and maps the
// decoded result into the normalized Document model. sourceID identifies the
// configured mail source that produced the message.
func NewProcessor(sourceID string) Processor {
	return &messageProcessor{
		sourceID: sourceID,
		parser:   NewParser(),
	}
}

type messageProcessor struct {
	sourceID string
	parser   Parser
}

func (p *messageProcessor) Process(ctx context.Context, message RawMessage) (Document, error) {
	parsed, err := p.parser.Parse(ctx, message)
	if err != nil {
		return Document{}, err
	}

	return documentFromParsedMessage(p.sourceID, parsed), nil
}

func documentFromParsedMessage(sourceID string, parsed ParsedMessage) Document {
	textBody := parsed.TextBody

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
		TextBody:      textBody,
		HTMLBody:      parsed.HTMLBody,
		ExtractedText: textBody,
		Attachments:   parsed.Attachments,
		Security:      normalizeSecurity(parsed.Headers),
		Warnings:      parsed.Warnings,
	}
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
