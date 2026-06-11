package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// DocumentIdentity identifies a parent document at the indexing or storage
// boundary. ID carries an index-native identity when one is available, while
// URI carries the source URI used by URI-oriented consumers. Callers may set
// either value or both.
type DocumentIdentity struct {
	ID  string `json:"id,omitempty" yaml:"id,omitempty"`
	URI string `json:"uri,omitempty" yaml:"uri,omitempty"`
}

// DocumentRelationship describes how a child document is related to its
// parent.
type DocumentRelationship string

const (
	// RelationshipAttachment identifies a child created from a permitted MIME
	// attachment or inline part.
	RelationshipAttachment DocumentRelationship = "attachment"
	// RelationshipEmbeddedMessage identifies a normalized RFC 5322 document
	// obtained by explicitly enabled deep extraction of an attachment.
	RelationshipEmbeddedMessage DocumentRelationship = "embedded_message"
)

// ChildDocumentDescriptor contains the metadata needed to publish or schedule
// an attachment as a child document without reading or completely indexing its
// content. ContentType is the detected media type when available and otherwise
// falls back to the declared media type.
type ChildDocumentDescriptor struct {
	ID           string               `json:"id,omitempty" yaml:"id,omitempty"`
	ParentID     string               `json:"parent_id,omitempty" yaml:"parent_id,omitempty"`
	ParentURI    string               `json:"parent_uri,omitempty" yaml:"parent_uri,omitempty"`
	PartID       string               `json:"part_id,omitempty" yaml:"part_id,omitempty"`
	Filename     string               `json:"filename,omitempty" yaml:"filename,omitempty"`
	SHA256       string               `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	ContentType  string               `json:"content_type,omitempty" yaml:"content_type,omitempty"`
	Size         int64                `json:"size,omitempty" yaml:"size,omitempty"`
	Disposition  string               `json:"disposition,omitempty" yaml:"disposition,omitempty"`
	Relationship DocumentRelationship `json:"relationship" yaml:"relationship"`
}

// AttachmentDocumentDescriptors converts permitted attachments to child
// document descriptors in their existing order. Attachments on a normalized
// Document have already passed the configured attachment policy. The mapping
// intentionally uses metadata only: it does not read Content or require
// ExtractedText to be populated.
func AttachmentDocumentDescriptors(parent DocumentIdentity, attachments []Attachment) []ChildDocumentDescriptor {
	if len(attachments) == 0 {
		return nil
	}

	descriptors := make([]ChildDocumentDescriptor, len(attachments))
	usedIDs := make(map[string]struct{}, len(attachments))
	for index, attachment := range attachments {
		id := strings.TrimSpace(attachment.ID)
		if _, duplicate := usedIDs[id]; id == "" || duplicate {
			id = stableAttachmentDescriptorID(parent, attachment, index)
		}
		usedIDs[id] = struct{}{}
		descriptors[index] = ChildDocumentDescriptor{
			ID:           id,
			ParentID:     parent.ID,
			ParentURI:    parent.URI,
			PartID:       attachment.PartID,
			Filename:     attachment.Filename,
			SHA256:       attachment.SHA256,
			ContentType:  attachmentContentType(attachment),
			Size:         attachment.Size,
			Disposition:  attachment.Disposition,
			Relationship: RelationshipAttachment,
		}
	}
	return descriptors
}

// AttachmentDocumentDescriptors returns child descriptors for the permitted
// attachments retained by the normalized document. parentURI is optional and
// supplements the document's index-native ID when supplied.
func (d Document) AttachmentDocumentDescriptors(parentURI string) []ChildDocumentDescriptor {
	return AttachmentDocumentDescriptors(DocumentIdentity{ID: d.ID, URI: parentURI}, d.Attachments)
}

func attachmentContentType(attachment Attachment) string {
	if attachment.DetectedMediaType != "" {
		return attachment.DetectedMediaType
	}
	return attachment.MediaType
}

// stableAttachmentDescriptorID supplies a deterministic identity when MIME
// metadata does not provide a usable unique content ID. The ordinal is part of
// the seed so byte-identical duplicate attachments remain distinct children.
func stableAttachmentDescriptorID(parent DocumentIdentity, attachment Attachment, ordinal int) string {
	hash := sha256.New()
	for _, value := range []string{
		parent.ID,
		parent.URI,
		attachment.PartID,
		attachment.Filename,
		attachment.SHA256,
		attachmentContentType(attachment),
		strconv.FormatInt(attachment.Size, 10),
		attachment.Disposition,
		strconv.Itoa(ordinal),
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "attachment:" + hex.EncodeToString(hash.Sum(nil))
}
