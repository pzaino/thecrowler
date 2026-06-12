package mail

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMailMetricsRegistered(t *testing.T) {
	names := []string{
		"crowler_mail_messages_discovered_total",
		"crowler_mail_messages_fetched_total",
		"crowler_mail_messages_parsed_total",
		"crowler_mail_messages_completed_total",
		"crowler_mail_messages_failed_total",
		"crowler_mail_messages_skipped_total",
		"crowler_mail_attachments_total",
		"crowler_mail_attachments_skipped_total",
		"crowler_mail_extracted_links_total",
		"crowler_mail_listener_reconnects_total",
		"crowler_mail_reconciliation_runs_total",
	}

	count, err := testutil.GatherAndCount(prometheus.DefaultGatherer, names...)
	if err != nil {
		t.Fatalf("GatherAndCount() error = %v", err)
	}
	if count != len(names) {
		t.Fatalf("GatherAndCount() = %d, want %d registered mail metrics", count, len(names))
	}
}

func TestRecordMailMessageMetrics(t *testing.T) {
	discoveredBefore := testutil.ToFloat64(mailMessagesDiscovered)
	skippedBefore := testutil.ToFloat64(mailMessagesSkipped)
	fetchedBefore := testutil.ToFloat64(mailMessagesFetched)
	parsedBefore := testutil.ToFloat64(mailMessagesParsed)
	completedBefore := testutil.ToFloat64(mailMessagesCompleted)
	failedBefore := testutil.ToFloat64(mailMessagesFailed)
	attachmentsBefore := testutil.ToFloat64(mailAttachments)
	skippedAttachmentsBefore := testutil.ToFloat64(mailAttachmentsSkipped)
	linksBefore := testutil.ToFloat64(mailExtractedLinks)
	reconciliationRunsBefore := testutil.ToFloat64(mailReconciliationRuns)

	recordMailMessageDiscovered()
	recordMailMessageSkipped()
	recordMailReconciliationRun()
	recordMailMessageOutcome(messageLifecycleOutcome{
		fetched:            true,
		parsed:             true,
		completed:          true,
		failed:             true,
		attachments:        3,
		skippedAttachments: 2,
		extractedLinks:     4,
	})

	assertCounterDelta(t, "discovered", mailMessagesDiscovered, discoveredBefore, 1)
	assertCounterDelta(t, "skipped", mailMessagesSkipped, skippedBefore, 1)
	assertCounterDelta(t, "fetched", mailMessagesFetched, fetchedBefore, 1)
	assertCounterDelta(t, "parsed", mailMessagesParsed, parsedBefore, 1)
	assertCounterDelta(t, "completed", mailMessagesCompleted, completedBefore, 1)
	assertCounterDelta(t, "failed", mailMessagesFailed, failedBefore, 1)
	assertCounterDelta(t, "attachments", mailAttachments, attachmentsBefore, 3)
	assertCounterDelta(t, "skipped attachments", mailAttachmentsSkipped, skippedAttachmentsBefore, 2)
	assertCounterDelta(t, "extracted links", mailExtractedLinks, linksBefore, 4)
	assertCounterDelta(t, "reconciliation runs", mailReconciliationRuns, reconciliationRunsBefore, 1)
}

func TestDocumentExtractionMetricCountsIncludesChildDocuments(t *testing.T) {
	document := Document{
		Attachments: []Attachment{{}, {}},
		Links:       []Link{{}},
		Warnings: []ParserWarning{
			{Category: WarningAttachmentSkipped},
			{Category: WarningMalformedHeader},
		},
		ChildDocuments: []Document{{
			Attachments: []Attachment{{}},
			Links:       []Link{{}, {}},
			Warnings:    []ParserWarning{{Category: WarningAttachmentSkipped}},
		}},
	}

	attachments, skippedAttachments, links := documentExtractionMetricCounts(document)
	if attachments != 3 || skippedAttachments != 2 || links != 3 {
		t.Fatalf("documentExtractionMetricCounts() = (%d, %d, %d), want (3, 2, 3)", attachments, skippedAttachments, links)
	}
}

func TestIMAPIdleListenerRecordReconnectIncrementsMetric(t *testing.T) {
	before := testutil.ToFloat64(mailListenerReconnects)
	listener := &IMAPIdleListener{}

	listener.recordReconnect()

	assertCounterDelta(t, "listener reconnects", mailListenerReconnects, before, 1)
	if status := listener.Status(); status.ReconnectCount != 1 {
		t.Fatalf("Status().ReconnectCount = %d, want 1", status.ReconnectCount)
	}
}

func assertCounterDelta(t *testing.T, name string, counter prometheus.Counter, before, want float64) {
	t.Helper()
	if got := testutil.ToFloat64(counter) - before; got != want {
		t.Fatalf("%s counter delta = %v, want %v", name, got, want)
	}
}
