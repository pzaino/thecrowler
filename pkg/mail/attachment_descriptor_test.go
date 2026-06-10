package mail

import (
	"errors"
	"io"
	"reflect"
	"testing"
)

type failOnReadCloser struct {
	read bool
}

func (reader *failOnReadCloser) Read([]byte) (int, error) {
	reader.read = true
	return 0, errors.New("attachment content must not be read while creating descriptors")
}

func (*failOnReadCloser) Close() error {
	return nil
}

var _ io.ReadCloser = (*failOnReadCloser)(nil)

func TestAttachmentDocumentDescriptorsDeterministicMapping(t *testing.T) {
	firstContent := &failOnReadCloser{}
	secondContent := &failOnReadCloser{}
	parent := DocumentIdentity{
		ID:  "mail:source-1:message-42",
		URI: "imap://account-1/INBOX;UID=42",
	}
	attachments := []Attachment{
		{
			ID:                "report@example.test",
			Filename:          "report.pdf",
			MediaType:         "application/octet-stream",
			DetectedMediaType: "application/pdf",
			Disposition:       "attachment",
			Size:              2048,
			SHA256:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ExtractedText:     "content that is not needed by the descriptor",
			Content:           firstContent,
		},
		{
			ID:          "logo@example.test",
			Filename:    "logo.svg",
			MediaType:   "image/svg+xml",
			Disposition: "inline",
			Size:        512,
			SHA256:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Inline:      true,
			Content:     secondContent,
		},
	}
	want := []ChildDocumentDescriptor{
		{
			ID:           "report@example.test",
			ParentID:     parent.ID,
			ParentURI:    parent.URI,
			Filename:     "report.pdf",
			SHA256:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ContentType:  "application/pdf",
			Size:         2048,
			Disposition:  "attachment",
			Relationship: RelationshipAttachment,
		},
		{
			ID:           "logo@example.test",
			ParentID:     parent.ID,
			ParentURI:    parent.URI,
			Filename:     "logo.svg",
			SHA256:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ContentType:  "image/svg+xml",
			Size:         512,
			Disposition:  "inline",
			Relationship: RelationshipAttachment,
		},
	}

	first := AttachmentDocumentDescriptors(parent, attachments)
	second := AttachmentDocumentDescriptors(parent, attachments)
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("AttachmentDocumentDescriptors() = %#v, want %#v", first, want)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("repeated descriptor mapping changed:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if firstContent.read || secondContent.read {
		t.Fatal("AttachmentDocumentDescriptors read attachment content")
	}
}

func TestDocumentAttachmentDocumentDescriptorsPreservesAvailableParentIdentity(t *testing.T) {
	tests := []struct {
		name      string
		document  Document
		parentURI string
		wantID    string
		wantURI   string
	}{
		{
			name: "document identity",
			document: Document{
				ID:          "mail:source-1:message-42",
				Attachments: []Attachment{{Filename: "report.txt", MediaType: "text/plain"}},
			},
			wantID: "mail:source-1:message-42",
		},
		{
			name: "parent URI",
			document: Document{
				Attachments: []Attachment{{Filename: "report.txt", MediaType: "text/plain"}},
			},
			parentURI: "imap://account-1/INBOX;UID=42",
			wantURI:   "imap://account-1/INBOX;UID=42",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptors := test.document.AttachmentDocumentDescriptors(test.parentURI)
			if len(descriptors) != 1 {
				t.Fatalf("descriptor count = %d, want 1", len(descriptors))
			}
			if descriptors[0].ParentID != test.wantID || descriptors[0].ParentURI != test.wantURI {
				t.Errorf("parent reference = (%q, %q), want (%q, %q)", descriptors[0].ParentID, descriptors[0].ParentURI, test.wantID, test.wantURI)
			}
		})
	}
}

func TestAttachmentDocumentDescriptorsEmptyInput(t *testing.T) {
	if descriptors := AttachmentDocumentDescriptors(DocumentIdentity{ID: "parent"}, nil); descriptors != nil {
		t.Fatalf("empty descriptor mapping = %#v, want nil", descriptors)
	}
}
