package mail

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
