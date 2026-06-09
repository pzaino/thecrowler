// Package mail provides reusable abstractions for read-only email ingestion.
//
// The package owns provider-neutral email configuration and models, provider
// connectors, mailbox listeners, MIME parsing, message normalization,
// reconciliation, and durable mailbox state semantics. Provider-specific
// implementations should remain behind the interfaces exposed by this package
// so callers do not need to understand protocol-specific cursors, identifiers,
// or notification behavior.
//
// Package mail does not provide outbound email delivery and does not depend on
// browser-backed crawling.
package mail
