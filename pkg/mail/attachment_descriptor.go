package mail

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
)

// ChildDocumentDescriptor contains the metadata needed to publish or schedule
// an attachment as a child document without reading or completely indexing its
// content. ContentType is the detected media type when available and otherwise
// falls back to the declared media type.
type ChildDocumentDescriptor struct {
	ID           string               `json:"id,omitempty" yaml:"id,omitempty"`
	ParentID     string               `json:"parent_id,omitempty" yaml:"parent_id,omitempty"`
	ParentURI    string               `json:"parent_uri,omitempty" yaml:"parent_uri,omitempty"`
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
	for index, attachment := range attachments {
		descriptors[index] = ChildDocumentDescriptor{
			ID:           attachment.ID,
			ParentID:     parent.ID,
			ParentURI:    parent.URI,
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
