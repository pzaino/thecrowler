package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jhillyerd/enmime/v2"
	"golang.org/x/net/html"
)

const defaultMaxPartBytes int64 = 10 << 20

// ParserOption configures bounded MIME parsing.
type ParserOption func(*mimeParser)

// WithMaxPartBytes sets the largest decoded MIME part retained in a parsed
// message. Oversized parts are represented by metadata and a warning instead
// of making the entire message fail. A non-positive value disables the limit.
func WithMaxPartBytes(maxBytes int64) ParserOption {
	return func(parser *mimeParser) { parser.maxPartBytes = maxBytes }
}

// WithMaxEmbeddedMessageDepth sets the number of attached RFC 5322 messages
// that may be recursively parsed below the root message. A non-positive value
// disables attached-message parsing.
func WithMaxEmbeddedMessageDepth(maxDepth int) ParserOption {
	return func(parser *mimeParser) { parser.maxEmbeddedMessageDepth = maxDepth }
}

// WithAttachmentPolicy enables early attachment filtering. Limits are applied
// before accepted content is copied, hashed, or retained.
func WithAttachmentPolicy(policy AttachmentPolicy, limits Limits) ParserOption {
	return func(parser *mimeParser) {
		policy.AllowedMediaTypes = append([]string(nil), policy.AllowedMediaTypes...)
		policy.BlockedMediaTypes = append([]string(nil), policy.BlockedMediaTypes...)
		parser.attachmentPolicy = &policy
		parser.attachmentLimits = limits
		if limits.MaxEmbeddedMessageDepth > 0 {
			parser.maxEmbeddedMessageDepth = limits.MaxEmbeddedMessageDepth
		}
	}
}

// NewParser returns the package's default RFC 5322 and MIME parser.
//
// The concrete implementation is intentionally private so callers depend only
// on the provider-neutral Parser contract and ParsedMessage model.
func NewParser(options ...ParserOption) Parser {
	parser := &mimeParser{
		parser: enmime.NewParser(
			enmime.SkipMalformedParts(true),
			enmime.SetReadPartErrorPolicy(enmime.AllowCorruptTextPartErrorPolicy),
		),
		maxPartBytes:            defaultMaxPartBytes,
		maxEmbeddedMessageDepth: defaultMaxEmbeddedMessageDepth,
	}
	for _, option := range options {
		if option != nil {
			option(parser)
		}
	}
	return parser
}

type mimeParser struct {
	parser                  *enmime.Parser
	maxPartBytes            int64
	attachmentPolicy        *AttachmentPolicy
	attachmentLimits        Limits
	maxEmbeddedMessageDepth int
}

func (p *mimeParser) Parse(ctx context.Context, message RawMessage) (ParsedMessage, error) {
	if err := ctx.Err(); err != nil {
		return ParsedMessage{}, err
	}
	if message.RFC822 == nil {
		return ParsedMessage{}, malformedParseError("message stream is nil", nil)
	}

	var attachmentPolicy *attachmentPolicyEvaluator
	if p.attachmentPolicy != nil {
		attachmentPolicy = newAttachmentPolicyEvaluator(*p.attachmentPolicy, p.attachmentLimits)
	}
	return p.parseMessage(ctx, message.Ref, message.RFC822, 0, attachmentPolicy)
}

func (p *mimeParser) parseMessage(ctx context.Context, ref MessageRef, reader io.Reader, depth int, attachmentPolicy *attachmentPolicyEvaluator) (ParsedMessage, error) {
	envelope, err := p.parser.ReadEnvelope(contextReader{ctx: ctx, reader: reader})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return ParsedMessage{}, contextErr
		}
		return ParsedMessage{}, malformedParseError("could not parse RFC 5322 message", err)
	}

	textBody, htmlBody := envelopeBodies(envelope.Root, p.maxPartBytes)
	rawHeaders, rawWarnings := boundedHeaders(map[string][]string(envelope.Root.Header), true)
	decodedHeaders, decodedWarnings := boundedHeaders(decodedEnvelopeHeaders(envelope), true)

	attachments, childMessages, attachmentWarnings := p.envelopeAttachments(ctx, ref, envelope.Root, htmlBody, depth, attachmentPolicy)
	if err := ctx.Err(); err != nil {
		return ParsedMessage{}, err
	}
	parsed := ParsedMessage{
		Ref:           ref,
		Headers:       decodedHeaders,
		RawHeaders:    rawHeaders,
		TextBody:      textBody,
		HTMLBody:      htmlBody,
		Attachments:   attachments,
		ChildMessages: childMessages,
		Warnings:      envelopeWarnings(envelope.Root),
	}
	parsed.Warnings = append(parsed.Warnings, attachmentWarnings...)
	parsed.Warnings = append(parsed.Warnings, semanticPartWarnings(envelope.Root, p.maxPartBytes)...)
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

func envelopeBodies(root *enmime.Part, maxPartBytes int64) (string, string) {
	alternative := firstMultipartAlternative(root)
	if alternative != nil {
		return bodyParts(alternative.FirstChild, true, maxPartBytes)
	}
	return bodyParts(root, false, maxPartBytes)
}

func bodyParts(root *enmime.Part, firstOnly bool, maxPartBytes int64) (string, string) {
	var textParts []string
	var htmlBody string
	walkParts(root, func(part *enmime.Part) bool {
		if isAttachmentPart(part) || isEncryptedPart(part) {
			// An attachment can itself be a multipart or message/rfc822 tree. Keep
			// it as one opaque attachment instead of indexing any child bodies.
			return false
		}
		if partOversized(part, maxPartBytes) {
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
				Category: warningCategory(problem),
				Code:     warningCode(problem.Name),
				Message:  problem.Detail,
				PartID:   part.PartID,
			})
		}
		return true
	})
	return warnings
}

func warningCategory(problem *enmime.Error) WarningCategory {
	if problem == nil {
		return ""
	}
	switch problem.Name {
	case enmime.ErrorCharsetConversion, enmime.ErrorCharsetDeclaration:
		return WarningUnknownCharset
	case enmime.ErrorMalformedHeader:
		return WarningMalformedHeader
	case enmime.ErrorMalformedChildPart:
		if strings.HasPrefix(strings.ToLower(problem.Detail), "read header:") {
			return WarningMalformedHeader
		}
	}
	return ""
}

func semanticPartWarnings(root *enmime.Part, maxPartBytes int64) []ParserWarning {
	var warnings []ParserWarning
	walkParts(root, func(part *enmime.Part) bool {
		if partOversized(part, maxPartBytes) {
			warnings = append(warnings, ParserWarning{
				Category: WarningOversizedPart,
				Code:     "part_too_large",
				Message:  "decoded MIME part exceeded retention limit",
				PartID:   part.PartID,
			})
		}
		if protectedKind := protectedPartKind(part); protectedKind != "" {
			warnings = append(warnings, ParserWarning{
				Category: WarningProtectedContent,
				Code:     protectedKind + "_content",
				Message:  "cryptographically protected MIME content was not verified or decrypted",
				PartID:   part.PartID,
			})
			return protectedKind != "encrypted"
		}
		if isUnsupportedPart(part) {
			warnings = append(warnings, ParserWarning{
				Category: WarningUnsupportedPart,
				Code:     "unsupported_media_type",
				Message:  "MIME part media type is not indexable",
				PartID:   part.PartID,
			})
		}
		return true
	})
	return warnings
}

func partOversized(part *enmime.Part, maxPartBytes int64) bool {
	return part != nil && maxPartBytes > 0 && int64(len(part.Content)) > maxPartBytes
}

func protectedPartKind(part *enmime.Part) string {
	if part == nil {
		return ""
	}
	contentType := strings.ToLower(part.ContentType)
	if contentType == "application/pkcs7-mime" || contentType == "application/x-pkcs7-mime" {
		if strings.EqualFold(part.ContentTypeParams["smime-type"], "signed-data") {
			return "signed"
		}
		return "encrypted"
	}
	switch contentType {
	case "multipart/encrypted", "application/pgp-encrypted":
		return "encrypted"
	case "multipart/signed", "application/pkcs7-signature", "application/x-pkcs7-signature", "application/pgp-signature":
		return "signed"
	default:
		return ""
	}
}

func isEncryptedPart(part *enmime.Part) bool {
	return protectedPartKind(part) == "encrypted"
}

func isUnsupportedPart(part *enmime.Part) bool {
	if part == nil || part.FirstChild != nil || isAttachmentPart(part) || protectedPartKind(part) != "" {
		return false
	}
	return part.ContentType != "" && part.ContentType != "text/plain" && part.ContentType != "text/html"
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

func (p *mimeParser) envelopeAttachments(ctx context.Context, ref MessageRef, root *enmime.Part, htmlBody string, depth int, policy *attachmentPolicyEvaluator) ([]Attachment, []ParsedMessage, []ParserWarning) {
	references := htmlCIDReferences(htmlBody)
	matched := make(map[string]bool, len(references))
	var attachments []Attachment
	var childMessages []ParsedMessage
	var warnings []ParserWarning
	walkParts(root, func(part *enmime.Part) bool {
		if !isAttachmentPart(part) {
			return true
		}
		contentID := normalizeContentID(part.ContentID)
		inline := strings.EqualFold(strings.TrimSpace(part.Disposition), "inline") ||
			(contentID != "" && references[contentID] && !matched[contentID])
		declaredType, detectedType := attachmentMediaTypes(part.Header.Get("Content-Type"), part.Content)
		if policy != nil {
			if warning := policy.evaluate(part.PartID, declaredType, detectedType, int64(len(part.Content)), inline); warning != nil {
				warnings = append(warnings, *warning)
				return false
			}
		}
		attachment := attachmentFromPart(part, p.maxPartBytes)
		if inline {
			attachment.Inline = true
			if contentID != "" {
				matched[contentID] = true
			}
		}
		attachments = append(attachments, attachment)

		if policy != nil && isAttachedMessage(part.FileName, declaredType, detectedType) && !attachment.Truncated {
			if depth >= p.maxEmbeddedMessageDepth {
				warnings = append(warnings, ParserWarning{
					Category: WarningAttachmentSkipped,
					Code:     "embedded_message_depth_exceeded",
					Message:  "attached message recursion depth limit was reached",
					PartID:   part.PartID,
				})
			} else {
				child, err := p.parseMessage(ctx, ref, bytes.NewReader(part.Content), depth+1, policy)
				if err != nil {
					if ctx.Err() != nil {
						return false
					}
					warnings = append(warnings, ParserWarning{
						Category: WarningAttachmentSkipped,
						Code:     "malformed_embedded_message",
						Message:  "attached message could not be parsed",
						PartID:   part.PartID,
					})
				} else {
					childMessages = append(childMessages, child)
				}
			}
		}
		// Do not expose nested MIME parts inside an attachment as independent
		// attachments. Attached messages are parsed separately above.
		return false
	})
	return attachments, childMessages, warnings
}

func isAttachedMessage(filename, declaredType, detectedType string) bool {
	return strings.EqualFold(declaredType, "message/rfc822") ||
		strings.EqualFold(detectedType, "message/rfc822") ||
		strings.EqualFold(filepath.Ext(strings.TrimSpace(filename)), ".eml")
}

func attachmentFromPart(part *enmime.Part, maxPartBytes int64) Attachment {
	originalSize := int64(len(part.Content))
	oversized := partOversized(part, maxPartBytes)
	var content []byte
	var digest string
	if !oversized {
		content = append([]byte(nil), part.Content...)
		sum := sha256.Sum256(content)
		digest = hex.EncodeToString(sum[:])
	}
	disposition := strings.ToLower(strings.TrimSpace(part.Disposition))
	transferEncoding := strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding")))
	contentID := normalizeContentID(part.ContentID)
	declaredMediaType, detectedMediaType := attachmentMediaTypes(part.Header.Get("Content-Type"), part.Content)
	return Attachment{
		ID:                contentID,
		PartID:            part.PartID,
		Filename:          part.FileName,
		MediaType:         declaredMediaType,
		DetectedMediaType: detectedMediaType,
		Disposition:       disposition,
		ContentID:         contentID,
		TransferEncoding:  transferEncoding,
		Size:              originalSize,
		SHA256:            digest,
		Inline:            disposition == "inline",
		Truncated:         oversized,
		Content:           retainedContent(content, oversized),
	}
}

func retainedContent(content []byte, oversized bool) io.ReadCloser {
	if oversized {
		return nil
	}
	return io.NopCloser(bytes.NewReader(content))
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
