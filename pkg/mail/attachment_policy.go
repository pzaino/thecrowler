package mail

import "strings"

const (
	attachmentSkipDisabled       = "attachment_disabled"
	attachmentSkipInlineDisabled = "inline_attachment_disabled"
	attachmentSkipBlocked        = "attachment_media_type_blocked"
	attachmentSkipNotAllowed     = "attachment_media_type_not_allowed"
	attachmentSkipTooLarge       = "attachment_too_large"
	attachmentSkipCount          = "attachment_count_exceeded"
	attachmentSkipTotalSize      = "attachment_total_size_exceeded"
)

type attachmentPolicyEvaluator struct {
	policy        AttachmentPolicy
	limits        Limits
	acceptedCount int
	acceptedBytes int64
}

func newAttachmentPolicyEvaluator(policy AttachmentPolicy, limits Limits) *attachmentPolicyEvaluator {
	policy.AllowedMediaTypes = normalizedMediaTypes(policy.AllowedMediaTypes)
	policy.BlockedMediaTypes = normalizedMediaTypes(policy.BlockedMediaTypes)
	return &attachmentPolicyEvaluator{policy: policy, limits: limits}
}

// evaluate applies cheap metadata and decoded-size checks before attachment
// content is copied, hashed, or exposed to downstream processing. Denylists
// take precedence over allowlists, and rejected attachments do not consume the
// accepted count or aggregate-byte budgets.
func (e *attachmentPolicyEvaluator) evaluate(partID, declaredType, detectedType string, size int64, inline bool) *ParserWarning {
	if !e.policy.Include {
		return attachmentSkipWarning(partID, attachmentSkipDisabled, "attachment emission is disabled")
	}
	if inline && !e.policy.IncludeInline {
		return attachmentSkipWarning(partID, attachmentSkipInlineDisabled, "inline attachment emission is disabled")
	}
	if matchesAnyMediaType(declaredType, e.policy.BlockedMediaTypes) || matchesAnyMediaType(detectedType, e.policy.BlockedMediaTypes) {
		return attachmentSkipWarning(partID, attachmentSkipBlocked, "attachment media type is blocked")
	}
	if len(e.policy.AllowedMediaTypes) > 0 && !matchesAnyMediaType(detectedType, e.policy.AllowedMediaTypes) {
		return attachmentSkipWarning(partID, attachmentSkipNotAllowed, "attachment media type is not allowed")
	}
	if e.limits.MaxAttachmentBytes > 0 && size > e.limits.MaxAttachmentBytes {
		return attachmentSkipWarning(partID, attachmentSkipTooLarge, "attachment exceeded the per-attachment size limit")
	}
	if e.limits.MaxAttachments > 0 && e.acceptedCount >= e.limits.MaxAttachments {
		return attachmentSkipWarning(partID, attachmentSkipCount, "message attachment count limit was reached")
	}
	if e.limits.MaxTotalAttachmentBytes > 0 && size > e.limits.MaxTotalAttachmentBytes-e.acceptedBytes {
		return attachmentSkipWarning(partID, attachmentSkipTotalSize, "message attachment byte limit would be exceeded")
	}

	e.acceptedCount++
	e.acceptedBytes += size
	return nil
}

func attachmentSkipWarning(partID, code, message string) *ParserWarning {
	return &ParserWarning{
		Category: WarningAttachmentSkipped,
		Code:     code,
		Message:  message,
		PartID:   partID,
	}
}

func normalizedMediaTypes(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func matchesAnyMediaType(mediaType string, patterns []string) bool {
	mediaType = normalizeMediaType(mediaType)
	for _, pattern := range patterns {
		if pattern == mediaType {
			return true
		}
		if strings.HasSuffix(pattern, "/*") && strings.HasPrefix(mediaType, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}
