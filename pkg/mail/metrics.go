package mail

import "github.com/prometheus/client_golang/prometheus"

var (
	mailMessagesDiscovered = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_messages_discovered_total",
		Help: "Total unique mail messages discovered during reconciliation.",
	})
	mailMessagesFetched = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_messages_fetched_total",
		Help: "Total discovered mail messages fetched successfully at least once.",
	})
	mailMessagesParsed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_messages_parsed_total",
		Help: "Total discovered mail messages parsed successfully at least once.",
	})
	mailMessagesCompleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_messages_completed_total",
		Help: "Total mail messages emitted successfully.",
	})
	mailMessagesFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_messages_failed_total",
		Help: "Total discovered mail messages that failed processing or were quarantined.",
	})
	mailMessagesSkipped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_messages_skipped_total",
		Help: "Total mailbox changes skipped because they were not upserts or were duplicate changes.",
	})
	mailAttachments = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_attachments_total",
		Help: "Total attachments retained from successfully parsed mail messages.",
	})
	mailAttachmentsSkipped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_attachments_skipped_total",
		Help: "Total attachments skipped by parsing and extraction policy.",
	})
	mailExtractedLinks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_extracted_links_total",
		Help: "Total static links extracted from successfully parsed mail messages.",
	})
	mailListenerReconnects = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_listener_reconnects_total",
		Help: "Total mail listener reconnect attempts.",
	})
	mailReconciliationRuns = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "crowler_mail_reconciliation_runs_total",
		Help: "Total mailbox reconciliation runs completed.",
	})
)

func init() {
	prometheus.MustRegister(
		mailMessagesDiscovered,
		mailMessagesFetched,
		mailMessagesParsed,
		mailMessagesCompleted,
		mailMessagesFailed,
		mailMessagesSkipped,
		mailAttachments,
		mailAttachmentsSkipped,
		mailExtractedLinks,
		mailListenerReconnects,
		mailReconciliationRuns,
	)
}

func recordMailMessageDiscovered() {
	mailMessagesDiscovered.Inc()
}

func recordMailMessageSkipped() {
	mailMessagesSkipped.Inc()
}

func recordMailListenerReconnect() {
	mailListenerReconnects.Inc()
}

func recordMailReconciliationRun() {
	mailReconciliationRuns.Inc()
}

func recordMailMessageOutcome(outcome messageLifecycleOutcome) {
	if outcome.fetched {
		mailMessagesFetched.Inc()
	}
	if outcome.parsed {
		mailMessagesParsed.Inc()
	}
	if outcome.completed {
		mailMessagesCompleted.Inc()
	}
	if outcome.failed {
		mailMessagesFailed.Inc()
	}
	if outcome.attachments > 0 {
		mailAttachments.Add(float64(outcome.attachments))
	}
	if outcome.skippedAttachments > 0 {
		mailAttachmentsSkipped.Add(float64(outcome.skippedAttachments))
	}
	if outcome.extractedLinks > 0 {
		mailExtractedLinks.Add(float64(outcome.extractedLinks))
	}
}

func documentExtractionMetricCounts(document Document) (attachments, skippedAttachments, links uint64) {
	attachments = uint64(len(document.Attachments))
	links = uint64(len(document.Links))
	for _, warning := range document.Warnings {
		if warning.Category == WarningAttachmentSkipped {
			skippedAttachments++
		}
	}
	for _, child := range document.ChildDocuments {
		childAttachments, childSkippedAttachments, childLinks := documentExtractionMetricCounts(child)
		attachments += childAttachments
		skippedAttachments += childSkippedAttachments
		links += childLinks
	}
	return attachments, skippedAttachments, links
}
