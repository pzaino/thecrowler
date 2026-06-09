package mail

import (
	"context"
	"strings"
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
		Warnings:      parsed.Warnings,
	}
}

func documentHeaders(parsed ParsedMessage) HeaderSet {
	return HeaderSet{
		MessageID:    parsed.MessageID,
		InReplyTo:    firstHeaderValue(parsed.Headers, "In-Reply-To"),
		References:   strings.Fields(firstHeaderValue(parsed.Headers, "References")),
		ListID:       firstHeaderValue(parsed.Headers, "List-Id"),
		OriginalDate: firstHeaderValue(parsed.Headers, "Date"),
		Values:       parsed.Headers,
	}
}

func firstHeaderValue(headers HeaderMap, name string) string {
	values := headers[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
