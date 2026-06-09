package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/jhillyerd/enmime/v2"
)

// NewParser returns the package's default RFC 5322 and MIME parser.
//
// The concrete implementation is intentionally private so callers depend only
// on the provider-neutral Parser contract and ParsedMessage model.
func NewParser() Parser {
	return &mimeParser{parser: enmime.NewParser()}
}

type mimeParser struct {
	parser *enmime.Parser
}

func (p *mimeParser) Parse(ctx context.Context, message RawMessage) (ParsedMessage, error) {
	if err := ctx.Err(); err != nil {
		return ParsedMessage{}, err
	}
	if message.RFC822 == nil {
		return ParsedMessage{}, malformedParseError("message stream is nil", nil)
	}

	envelope, err := p.parser.ReadEnvelope(contextReader{ctx: ctx, reader: message.RFC822})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return ParsedMessage{}, contextErr
		}
		return ParsedMessage{}, malformedParseError("could not parse RFC 5322 message", err)
	}

	textBody, htmlBody := envelopeBodies(envelope.Root)

	parsed := ParsedMessage{
		Ref:         message.Ref,
		MessageID:   envelope.GetHeader("Message-ID"),
		Subject:     envelope.GetHeader("Subject"),
		Headers:     envelopeHeaders(envelope),
		TextBody:    textBody,
		HTMLBody:    htmlBody,
		Attachments: envelopeAttachments(envelope),
		Warnings:    envelopeWarnings(envelope.Root),
	}

	if parsed.Date, err = envelope.Date(); err != nil && !errors.Is(err, mail.ErrHeaderNotPresent) {
		return ParsedMessage{}, malformedParseError("could not parse Date header", err)
	}
	if parsed.From, err = envelopeAddresses(envelope, "From"); err != nil {
		return ParsedMessage{}, err
	}
	if parsed.To, err = envelopeAddresses(envelope, "To"); err != nil {
		return ParsedMessage{}, err
	}
	if parsed.CC, err = envelopeAddresses(envelope, "Cc"); err != nil {
		return ParsedMessage{}, err
	}
	if parsed.BCC, err = envelopeAddresses(envelope, "Bcc"); err != nil {
		return ParsedMessage{}, err
	}
	if parsed.ReplyTo, err = envelopeAddresses(envelope, "Reply-To"); err != nil {
		return ParsedMessage{}, err
	}

	return parsed, nil
}

func envelopeBodies(root *enmime.Part) (string, string) {
	alternative := firstMultipartAlternative(root)
	if alternative != nil {
		return bodyParts(alternative.FirstChild, true)
	}
	return bodyParts(root, false)
}

func bodyParts(root *enmime.Part, firstOnly bool) (string, string) {
	var textParts []string
	var htmlBody string
	walkParts(root, func(part *enmime.Part) bool {
		if isAttachmentPart(part) {
			// An attachment can itself be a multipart or message/rfc822 tree. Keep
			// it as one opaque attachment instead of indexing any child bodies.
			return false
		}
		switch part.ContentType {
		case "text/plain":
			if !firstOnly || len(textParts) == 0 {
				textParts = append(textParts, string(part.Content))
			}
		case "text/html":
			if htmlBody == "" {
				htmlBody = string(part.Content)
			}
		}
		return true
	})
	return strings.Join(textParts, "\n--\n"), htmlBody
}

func firstMultipartAlternative(part *enmime.Part) *enmime.Part {
	for current := part; current != nil; current = current.NextSibling {
		if isAttachmentPart(current) {
			continue
		}
		if current.ContentType == "multipart/alternative" {
			return current
		}
		if alternative := firstMultipartAlternative(current.FirstChild); alternative != nil {
			return alternative
		}
	}
	return nil
}

func walkParts(part *enmime.Part, visit func(*enmime.Part) bool) {
	for current := part; current != nil; current = current.NextSibling {
		if visit(current) && current.FirstChild != nil {
			walkParts(current.FirstChild, visit)
		}
	}
}

func isAttachmentPart(part *enmime.Part) bool {
	if part == nil {
		return false
	}
	disposition := strings.ToLower(strings.TrimSpace(part.Disposition))
	return disposition == "attachment" || disposition == "inline" ||
		part.FileName != "" || part.ContentType == "application/octet-stream"
}

func envelopeWarnings(root *enmime.Part) []ParserWarning {
	var warnings []ParserWarning
	walkParts(root, func(part *enmime.Part) bool {
		for _, problem := range part.Errors {
			warnings = append(warnings, ParserWarning{
				Code:    warningCode(problem.Name),
				Message: problem.Detail,
				PartID:  part.PartID,
			})
		}
		return true
	})
	return warnings
}

func warningCode(name string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(name))
}

func envelopeHeaders(envelope *enmime.Envelope) HeaderMap {
	headers := make(HeaderMap)
	for _, key := range envelope.GetHeaderKeys() {
		canonicalKey := textproto.CanonicalMIMEHeaderKey(key)
		headers[canonicalKey] = envelope.GetHeaderValues(key)
	}
	return headers
}

func envelopeAddresses(envelope *enmime.Envelope, header string) ([]Address, error) {
	if envelope.GetHeader(header) == "" {
		return nil, nil
	}

	addresses, err := envelope.AddressList(header)
	if err != nil {
		return nil, malformedParseError(fmt.Sprintf("could not parse %s header", header), err)
	}

	parsed := make([]Address, 0, len(addresses))
	for _, address := range addresses {
		parsed = append(parsed, Address{
			Name:       address.Name,
			Address:    address.Address,
			Normalized: strings.ToLower(address.Address),
		})
	}
	return parsed, nil
}

func envelopeAttachments(envelope *enmime.Envelope) []Attachment {
	var attachments []Attachment
	walkParts(envelope.Root, func(part *enmime.Part) bool {
		if !isAttachmentPart(part) {
			return true
		}
		attachments = append(attachments, attachmentFromPart(part))
		// Do not expose nested MIME parts inside an attached message or
		// multipart payload as independent indexable attachments.
		return false
	})
	return attachments
}

func attachmentFromPart(part *enmime.Part) Attachment {
	content := append([]byte(nil), part.Content...)
	digest := sha256.Sum256(content)
	disposition := strings.ToLower(strings.TrimSpace(part.Disposition))
	transferEncoding := strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")))
	return Attachment{
		ID:               part.ContentID,
		PartID:           part.PartID,
		Filename:         part.FileName,
		MediaType:        part.ContentType,
		Disposition:      disposition,
		ContentID:        part.ContentID,
		TransferEncoding: transferEncoding,
		Size:             int64(len(content)),
		SHA256:           hex.EncodeToString(digest[:]),
		Inline:           disposition == "inline",
		Content:          io.NopCloser(bytes.NewReader(content)),
	}
}

func malformedParseError(message string, cause error) error {
	return &Error{
		Kind:      ErrorMalformed,
		Operation: "parse message",
		Message:   message,
		Cause:     cause,
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
