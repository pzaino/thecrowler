package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/url"
	"regexp"
	"strings"

	"github.com/jhillyerd/enmime/v2"
	"golang.org/x/net/html"
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
	rawHeaders, rawWarnings := boundedHeaders(map[string][]string(envelope.Root.Header), true)
	decodedHeaders, decodedWarnings := boundedHeaders(decodedEnvelopeHeaders(envelope), true)

	parsed := ParsedMessage{
		Ref:         message.Ref,
		Headers:     decodedHeaders,
		RawHeaders:  rawHeaders,
		TextBody:    textBody,
		HTMLBody:    htmlBody,
		Attachments: envelopeAttachments(envelope.Root, htmlBody),
		Warnings:    envelopeWarnings(envelope.Root),
	}
	parsed.Warnings = append(parsed.Warnings, rawWarnings...)
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)
	parsed.MessageID = normalizeMessageID(parsed.Headers, "Message-ID")
	parsed.Subject = normalizeSubject(parsed.Headers)
	parsed.Date, _, decodedWarnings = normalizeDate(parsed.Headers)
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)

	parsed.From, decodedWarnings = normalizeAddresses(parsed.Headers, "From")
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)
	parsed.To, decodedWarnings = normalizeAddresses(parsed.Headers, "To")
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)
	parsed.CC, decodedWarnings = normalizeAddresses(parsed.Headers, "Cc")
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)
	parsed.BCC, decodedWarnings = normalizeAddresses(parsed.Headers, "Bcc")
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)
	parsed.ReplyTo, decodedWarnings = normalizeAddresses(parsed.Headers, "Reply-To")
	parsed.Warnings = append(parsed.Warnings, decodedWarnings...)

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
		part.FileName != "" || part.ContentType == "application/octet-stream" ||
		isRelatedResource(part)
}

func isRelatedResource(part *enmime.Part) bool {
	if part == nil || part.Parent == nil || part.Parent.ContentType != "multipart/related" {
		return false
	}
	return part != relatedRoot(part.Parent)
}

func relatedRoot(related *enmime.Part) *enmime.Part {
	if related == nil {
		return nil
	}
	start := normalizeContentID(related.ContentTypeParams["start"])
	if _, parameters, err := mime.ParseMediaType(related.Header.Get("Content-Type")); err == nil {
		start = normalizeContentID(parameters["start"])
	}
	if start != "" {
		for child := related.FirstChild; child != nil; child = child.NextSibling {
			if normalizeContentID(child.ContentID) == start {
				return child
			}
		}
	}
	return related.FirstChild
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

func decodedEnvelopeHeaders(envelope *enmime.Envelope) map[string][]string {
	headers := make(map[string][]string)
	for _, name := range envelope.GetHeaderKeys() {
		headers[name] = envelope.GetHeaderValues(name)
	}
	return headers
}

func envelopeAttachments(root *enmime.Part, htmlBody string) []Attachment {
	references := htmlCIDReferences(htmlBody)
	matched := make(map[string]bool, len(references))
	var attachments []Attachment
	walkParts(root, func(part *enmime.Part) bool {
		if !isAttachmentPart(part) {
			return true
		}
		attachment := attachmentFromPart(part)
		if attachment.ContentID != "" && references[attachment.ContentID] && !matched[attachment.ContentID] {
			attachment.Inline = true
			matched[attachment.ContentID] = true
		}
		attachments = append(attachments, attachment)
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
	contentID := normalizeContentID(part.ContentID)
	return Attachment{
		ID:               contentID,
		PartID:           part.PartID,
		Filename:         part.FileName,
		MediaType:        part.ContentType,
		Disposition:      disposition,
		ContentID:        contentID,
		TransferEncoding: transferEncoding,
		Size:             int64(len(content)),
		SHA256:           hex.EncodeToString(digest[:]),
		Inline:           disposition == "inline",
		Content:          io.NopCloser(bytes.NewReader(content)),
	}
}

func normalizeContentID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 && strings.EqualFold(value[:4], "cid:") {
		value = strings.TrimSpace(value[4:])
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	} else {
		return ""
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '<' && value[len(value)-1] == '>' {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" || strings.ContainsAny(value, "<>\r\n\t ") {
		return ""
	}
	return strings.ToLower(value)
}

var cidReferencePattern = regexp.MustCompile(`(?i)cid:[^\s"'(),]+`)

func htmlCIDReferences(body string) map[string]bool {
	references := make(map[string]bool)
	if strings.TrimSpace(body) == "" {
		return references
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return references
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attribute := range node.Attr {
				for _, value := range cidReferencePattern.FindAllString(attribute.Val, -1) {
					if contentID := normalizeContentID(value); contentID != "" {
						references[contentID] = true
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return references
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
