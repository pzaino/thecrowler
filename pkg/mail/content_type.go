package mail

import (
	"bytes"
	"encoding/csv"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

const attachmentSniffBytes = 4096

// attachmentMediaTypes keeps the untrusted declared type separate from the
// resolved type used by downstream attachment handling. Sniffing is limited to
// a small prefix even though decoded content is already bounded by the parser.
func attachmentMediaTypes(declared string, content []byte) (string, string) {
	declared = normalizeMediaType(declared)
	sniffed := sniffAttachmentMediaType(content)

	switch {
	case sniffed == "":
		if declared != "" {
			return declared, declared
		}
		return "", "application/octet-stream"
	case declared == "":
		return "", sniffed
	case !mediaTypesClearlyInconsistent(declared, sniffed):
		return declared, declared
	default:
		return declared, sniffed
	}
}

func normalizeMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.ToLower(mediaType)
}

func sniffAttachmentMediaType(content []byte) string {
	if len(content) == 0 {
		return ""
	}

	truncated := len(content) > attachmentSniffBytes
	sample := content
	if truncated {
		sample = sample[:attachmentSniffBytes]
	}

	trimmed := bytes.TrimSpace(sample)
	if len(trimmed) == 0 {
		return "text/plain"
	}
	if bytes.HasPrefix(trimmed, []byte("%PDF-")) {
		return "application/pdf"
	}

	httpSample := sample
	if len(httpSample) > 512 {
		httpSample = httpSample[:512]
	}
	detected := normalizeMediaType(http.DetectContentType(httpSample))
	if detected == "text/html" {
		return detected
	}
	if looksLikeRFC822(sample) {
		return "message/rfc822"
	}
	if looksLikeCSV(sample, truncated) {
		return "text/csv"
	}
	return detected
}

func looksLikeRFC822(sample []byte) bool {
	headerBlock, _, found := bytes.Cut(sample, []byte("\n\n"))
	if !found {
		headerBlock, _, found = bytes.Cut(sample, []byte("\r\n\r\n"))
	}
	if !found || len(headerBlock) == 0 {
		return false
	}

	knownHeaders := 0
	mailHeaders := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(headerBlock), "\r\n", "\n"), "\n") {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "date", "from", "to", "cc", "bcc", "subject", "message-id", "mime-version", "content-type", "content-transfer-encoding":
			knownHeaders++
		case "received", "return-path", "reply-to", "sender", "in-reply-to", "references":
			knownHeaders++
			mailHeaders++
		default:
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "from", "to", "subject", "message-id", "mime-version":
			mailHeaders++
		}
	}
	return knownHeaders >= 2 && mailHeaders >= 1
}

func looksLikeCSV(sample []byte, truncated bool) bool {
	if !utf8.Valid(sample) || bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	if truncated {
		if end := bytes.LastIndexByte(sample, '\n'); end >= 0 {
			sample = sample[:end+1]
		}
	}
	if bytes.Count(sample, []byte{'\n'}) < 1 {
		return false
	}

	reader := csv.NewReader(bytes.NewReader(sample))
	reader.FieldsPerRecord = 0
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 || len(records[0]) < 2 {
		return false
	}
	fieldCount := len(records[0])
	for _, record := range records[1:] {
		if len(record) != fieldCount {
			return false
		}
	}
	return true
}

func mediaTypesClearlyInconsistent(declared, sniffed string) bool {
	if declared == sniffed {
		return false
	}
	if declared == "application/octet-stream" {
		return sniffed != "application/octet-stream"
	}

	switch sniffed {
	case "application/pdf", "message/rfc822", "text/html", "text/csv":
		return true
	case "text/plain":
		return !isTextualMediaType(declared)
	case "application/octet-stream":
		return false
	default:
		return false
	}
}

func isTextualMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/ld+json", "application/xml", "application/xhtml+xml",
		"application/javascript", "application/sql", "application/x-www-form-urlencoded":
		return true
	default:
		return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
	}
}
