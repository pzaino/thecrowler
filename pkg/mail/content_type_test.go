package mail

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachmentMediaTypesFromFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fixture      string
		declared     string
		wantDeclared string
		wantDetected string
	}{
		{
			name:         "PDF overrides generic declaration",
			fixture:      "sample.pdf",
			declared:     "application/octet-stream; name=report.pdf",
			wantDeclared: "application/octet-stream",
			wantDetected: "application/pdf",
		},
		{
			name:         "HTML fills missing declaration",
			fixture:      "sample.html",
			wantDetected: "text/html",
		},
		{
			name:         "plain text overrides contradictory declaration",
			fixture:      "sample.txt",
			declared:     "application/pdf",
			wantDeclared: "application/pdf",
			wantDetected: "text/plain",
		},
		{
			name:         "CSV refines generic text declaration",
			fixture:      "sample.csv",
			declared:     "text/plain; charset=utf-8",
			wantDeclared: "text/plain",
			wantDetected: "text/csv",
		},
		{
			name:         "EML overrides generic declaration",
			fixture:      "sample.eml",
			declared:     "application/octet-stream",
			wantDeclared: "application/octet-stream",
			wantDetected: "message/rfc822",
		},
		{
			name:         "empty content has safe binary fallback",
			fixture:      "empty",
			wantDetected: "application/octet-stream",
		},
		{
			name:         "unknown binary has safe binary fallback",
			fixture:      "unknown.bin",
			wantDetected: "application/octet-stream",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(filepath.Join("testdata", "attachments", test.fixture))
			if err != nil {
				t.Fatalf("read fixture %s: %v", test.fixture, err)
			}

			declared, detected := attachmentMediaTypes(test.declared, content)
			if declared != test.wantDeclared || detected != test.wantDetected {
				t.Errorf("attachmentMediaTypes(%q, %s) = (%q, %q), want (%q, %q)",
					test.declared, test.fixture, declared, detected, test.wantDeclared, test.wantDetected)
			}
		})
	}
}

func TestAttachmentMediaTypesRetainsCompatibleSpecificDeclaration(t *testing.T) {
	t.Parallel()

	declared, detected := attachmentMediaTypes("application/json; charset=utf-8", []byte(`{"ok":true}`))
	if declared != "application/json" || detected != "application/json" {
		t.Fatalf("attachmentMediaTypes() = (%q, %q), want compatible declared type", declared, detected)
	}
}

func TestAttachmentContentSniffingIsBounded(t *testing.T) {
	t.Parallel()

	content := append(bytes.Repeat([]byte{0}, attachmentSniffBytes), []byte("%PDF-1.7")...)
	if detected := sniffAttachmentMediaType(content); detected != "application/octet-stream" {
		t.Fatalf("sniffAttachmentMediaType() = %q, inspected content beyond %d-byte limit", detected, attachmentSniffBytes)
	}
}
