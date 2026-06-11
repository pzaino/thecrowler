package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	browser "github.com/pzaino/thecrowler/pkg/browser"
)

// NewProcessor returns a processor that parses RFC 5322 messages and maps the
// decoded result into the normalized Document model. sourceID identifies the
// configured mail source that produced the message. An optional extraction
// configuration enables conservative HTML cleanup without changing the stored
// HTMLBody. Cleanup is disabled when no configuration is supplied.
func NewProcessor(sourceID string, extraction ...ExtractionConfig) Processor {
	var config ExtractionConfig
	parser := NewParser()
	if len(extraction) != 0 {
		config = extraction[0]
		parser = NewParser(WithAttachmentPolicy(config.Attachments, defaultAttachmentLimits()))
	}
	return &messageProcessor{
		sourceID:   sourceID,
		parser:     parser,
		extraction: config,
	}
}

// NewProcessorWithLimits returns a processor that applies the source's
// attachment extraction policy and resource limits during MIME traversal.
func NewProcessorWithLimits(sourceID string, extraction ExtractionConfig, limits Limits) Processor {
	return &messageProcessor{
		sourceID:   sourceID,
		parser:     NewParser(WithAttachmentPolicy(extraction.Attachments, limits)),
		extraction: extraction,
	}
}

func defaultAttachmentLimits() Limits {
	return Limits{
		MaxAttachmentBytes:      defaultMaxAttachmentBytes,
		MaxTotalAttachmentBytes: defaultMaxTotalAttachmentBytes,
		MaxAttachments:          defaultMaxAttachments,
		MaxEmbeddedMessageDepth: defaultMaxEmbeddedMessageDepth,
	}
}

type messageProcessor struct {
	sourceID   string
	parser     Parser
	extraction ExtractionConfig
}

func (p *messageProcessor) Process(ctx context.Context, message RawMessage) (Document, error) {
	if message.RFC822 == nil {
		return Document{}, malformedParseError("message stream is nil", nil)
	}

	hash := sha256.New()
	message.RFC822 = struct {
		io.Reader
		io.Closer
	}{
		Reader: io.TeeReader(message.RFC822, hash),
		Closer: message.RFC822,
	}
	parsed, err := p.parser.Parse(ctx, message)
	if err != nil {
		return Document{}, err
	}

	document, err := documentFromParsedMessage(p.sourceID, parsed, p.extraction)
	if err != nil {
		return Document{}, err
	}
	fingerprint := hex.EncodeToString(hash.Sum(nil))
	document.ContentFingerprint = fingerprint
	identity, err := StableMessageIdentity(p.sourceID, parsed.Ref, fingerprint)
	if err == nil {
		document.ID = identity.ID
		document.IdentityStrategy = identity.Strategy
	}
	return document, nil
}

func documentFromParsedMessage(sourceID string, parsed ParsedMessage, extraction ExtractionConfig) (Document, error) {
	childDocuments := make([]Document, 0, len(parsed.ChildMessages))
	for _, child := range parsed.ChildMessages {
		document, err := documentFromParsedMessage(sourceID, child, extraction)
		if err != nil {
			return Document{}, err
		}
		childDocuments = append(childDocuments, document)
	}

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
		ParentAttachmentPartID: parsed.ParentAttachmentPartID,
		SourceID:               sourceID,
		Ref:                    parsed.Ref,
		MessageID:              parsed.MessageID,
		ThreadID:               parsed.ThreadID,
		Date:                   parsed.Date,
		From:                   parsed.From,
		To:                     parsed.To,
		CC:                     parsed.CC,
		BCC:                    parsed.BCC,
		ReplyTo:                parsed.ReplyTo,
		Subject:                parsed.Subject,
		Headers:                documentHeaders(parsed),
		TextBody:               parsed.TextBody,
		HTMLBody:               parsed.HTMLBody,
		ExtractedText:          extractedText,
		Links:                  links,
		Attachments:            parsed.Attachments,
		ChildDocuments:         childDocuments,
		Security:               normalizeSecurity(parsed.Headers),
		Warnings:               parsed.Warnings,
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
