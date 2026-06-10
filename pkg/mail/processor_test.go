package mail

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProcessorDecodesTextPlainBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  string
		wantText string
	}{
		{
			name:     "UTF-8 7bit",
			fixture:  "plain_7bit_utf8.eml",
			wantText: "Hello from plain text.\nSecond line.\n",
		},
		{
			name:     "ISO-8859-1 quoted-printable",
			fixture:  "plain_quoted_printable_iso_8859_1.eml",
			wantText: "Olá, seu café está pronto.\n",
		},
		{
			name:     "Windows-1252 base64",
			fixture:  "plain_base64_windows_1252.eml",
			wantText: "Preço: “Café – €10”\r\n",
		},
		{
			name:     "UTF-8 8bit",
			fixture:  "plain_8bit_utf8.eml",
			wantText: "こんにちは、世界。\n",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			messageFile, err := os.Open(filepath.Join("testdata", test.fixture))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer messageFile.Close()

			ref := MessageRef{Provider: "fixture", AccountID: "account-1", UID: 42}
			document, err := NewProcessor("source-1").Process(context.Background(), RawMessage{
				Ref:    ref,
				RFC822: messageFile,
			})
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}

			if document.SourceID != "source-1" {
				t.Errorf("SourceID = %q, want source-1", document.SourceID)
			}
			if !reflect.DeepEqual(document.Ref, ref) {
				t.Errorf("Ref = %#v, want %#v", document.Ref, ref)
			}
			if document.TextBody != test.wantText {
				t.Errorf("TextBody = %q, want %q", document.TextBody, test.wantText)
			}
			if document.ExtractedText != test.wantText {
				t.Errorf("ExtractedText = %q, want %q", document.ExtractedText, test.wantText)
			}
		})
	}
}

func TestProcessorStaticallyNormalizesHTMLBodyWithoutNetworkAccess(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	htmlBody := fmt.Sprintf(`<html><body>
		<h1>Hello</h1>
		<script>fetch(%q); document.write("injected")</script>
		<img src="%s/tracking.png" alt="remote image">
		<iframe src="%s/frame.html">frame fallback</iframe>
		<a href="%s/safe?one=1&amp;two=2" onclick="fetch('/clicked')">safe link</a>
		<div hidden><a href="%s/hidden">hidden link</a></div>
	</body></html>`, server.URL+"/script", server.URL, server.URL, server.URL, server.URL)

	raw := strings.Join([]string{
		"From: sender@example.test",
		"To: recipient@example.test",
		"Subject: Static HTML normalization",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=mail-boundary",
		"",
		"--mail-boundary",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Plain fallback that should not replace HTML extraction.",
		"--mail-boundary",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		htmlBody,
		"--mail-boundary--",
		"",
	}, "\r\n")

	document, err := NewProcessor("source-html").Process(context.Background(), RawMessage{
		Ref:    MessageRef{Provider: "fixture", AccountID: "account-html", UID: 7},
		RFC822: io.NopCloser(strings.NewReader(raw)),
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if document.HTMLBody != htmlBody {
		t.Errorf("HTMLBody changed during normalization:\n got: %q\nwant: %q", document.HTMLBody, htmlBody)
	}
	if document.ExtractedText != "Hello safe link" {
		t.Errorf("ExtractedText = %q, want %q", document.ExtractedText, "Hello safe link")
	}
	wantLinks := []Link{{
		URL:            server.URL + "/safe?one=1&two=2",
		Text:           "safe link",
		Source:         "html",
		Classification: LinkNormal,
	}}
	if !reflect.DeepEqual(document.Links, wantLinks) {
		t.Errorf("Links = %#v, want %#v", document.Links, wantLinks)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("network requests during normalization = %d, want 0", got)
	}
}

func TestProcessorNormalizesMalformedHTMLOnlyMessage(t *testing.T) {
	t.Parallel()

	htmlBody := `<main><p>HTML only <strong>message<a href="../details?view=full&amp;lang=en">Details`
	raw := strings.Join([]string{
		"From: sender@example.test",
		"To: recipient@example.test",
		"Subject: Malformed HTML normalization",
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=utf-8",
		"",
		htmlBody,
	}, "\r\n")

	document, err := NewProcessor("source-html-only").Process(context.Background(), RawMessage{
		Ref:    MessageRef{Provider: "fixture", AccountID: "account-html", UID: 8},
		RFC822: io.NopCloser(strings.NewReader(raw)),
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if document.TextBody != "" {
		t.Errorf("TextBody = %q, want empty", document.TextBody)
	}
	if document.HTMLBody != htmlBody {
		t.Errorf("HTMLBody changed during normalization:\n got: %q\nwant: %q", document.HTMLBody, htmlBody)
	}
	if document.ExtractedText != "HTML only message Details" {
		t.Errorf("ExtractedText = %q, want %q", document.ExtractedText, "HTML only message Details")
	}
	wantLinks := []Link{{
		URL:            "../details?view=full&lang=en",
		Text:           "Details",
		Source:         "html",
		Classification: LinkNormal,
	}}
	if !reflect.DeepEqual(document.Links, wantLinks) {
		t.Errorf("Links = %#v, want %#v", document.Links, wantLinks)
	}
}

func TestProcessorOptionallyCleansEmailHTMLForExtraction(t *testing.T) {
	t.Parallel()

	htmlBody := `<html><body>
		<h1>Current message</h1>
		<script>document.write("script payload")</script>
		<div class="preheader" style="opacity: 0; font-size: 0">Hidden preview copy</div>
		<a href="https://tracker.example.test/open"><img width="1" height="1" src="https://tracker.example.test/pixel.gif"></a>
		<div class="gmail_quote">Recognized quoted history</div>
		<p class="preheader-content gmail_quote_summary">Legitimate visible content</p>
		<a href="https://example.test/details">Visible link</a>
	</body></html>`
	parsed := ParsedMessage{HTMLBody: htmlBody}

	disabled, err := documentFromParsedMessage("source-disabled", parsed, ExtractionConfig{})
	if err != nil {
		t.Fatalf("documentFromParsedMessage() with cleanup disabled error = %v", err)
	}
	enabled, err := documentFromParsedMessage("source-enabled", parsed, ExtractionConfig{CleanupHTML: true})
	if err != nil {
		t.Fatalf("documentFromParsedMessage() with cleanup enabled error = %v", err)
	}

	if disabled.HTMLBody != htmlBody || enabled.HTMLBody != htmlBody {
		t.Fatalf("HTMLBody changed during cleanup:\n disabled: %q\n  enabled: %q\n     want: %q", disabled.HTMLBody, enabled.HTMLBody, htmlBody)
	}
	if disabled.ExtractedText != "Current message Hidden preview copy Recognized quoted history Legitimate visible content Visible link" {
		t.Errorf("disabled ExtractedText = %q", disabled.ExtractedText)
	}
	if enabled.ExtractedText != "Current message Legitimate visible content Visible link" {
		t.Errorf("enabled ExtractedText = %q", enabled.ExtractedText)
	}
	if strings.Contains(enabled.ExtractedText, "script payload") {
		t.Errorf("enabled ExtractedText retained script content: %q", enabled.ExtractedText)
	}

	wantDisabledLinks := []Link{
		{URL: "https://tracker.example.test/open", Source: "html", Classification: LinkTracking},
		{URL: "https://example.test/details", Text: "Visible link", Source: "html", Classification: LinkNormal},
	}
	if !reflect.DeepEqual(disabled.Links, wantDisabledLinks) {
		t.Errorf("disabled Links = %#v, want %#v", disabled.Links, wantDisabledLinks)
	}
	wantEnabledLinks := []Link{{URL: "https://example.test/details", Text: "Visible link", Source: "html", Classification: LinkNormal}}
	if !reflect.DeepEqual(enabled.Links, wantEnabledLinks) {
		t.Errorf("enabled Links = %#v, want %#v", enabled.Links, wantEnabledLinks)
	}
}
