package mail

import (
	"bytes"
	"context"
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

	parsed := ParsedMessage{
		Ref:         message.Ref,
		MessageID:   envelope.GetHeader("Message-ID"),
		Subject:     envelope.GetHeader("Subject"),
		Headers:     envelopeHeaders(envelope),
		TextBody:    envelope.Text,
		HTMLBody:    envelope.HTML,
		Attachments: envelopeAttachments(envelope),
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
	attachments := make([]Attachment, 0, len(envelope.Attachments)+len(envelope.Inlines))
	for _, part := range envelope.Attachments {
		attachments = append(attachments, attachmentFromPart(part, false))
	}
	for _, part := range envelope.Inlines {
		attachments = append(attachments, attachmentFromPart(part, true))
	}
	return attachments
}

func attachmentFromPart(part *enmime.Part, inline bool) Attachment {
	content := append([]byte(nil), part.Content...)
	return Attachment{
		ID:          part.ContentID,
		PartID:      part.PartID,
		Filename:    part.FileName,
		MediaType:   part.ContentType,
		Disposition: part.Disposition,
		ContentID:   part.ContentID,
		Size:        int64(len(content)),
		Inline:      inline,
		Content:     io.NopCloser(bytes.NewReader(content)),
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
