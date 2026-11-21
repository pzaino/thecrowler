package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func validMCPMutationAuditEvent(occurredAt time.Time) MCPMutationAuditEvent {
	return MCPMutationAuditEvent{
		OccurredAt:      occurredAt,
		EventType:       MCPMutationAuditEventAdmissionDenied,
		ActorType:       "agent",
		ActorID:         "agent-1",
		ActorIssuer:     "test-issuer",
		RunID:           "run-1",
		TraceID:         "trace-1",
		AgentID:         "agent-1",
		PrincipalUserID: 7,
		ServerID:        "fixture",
		ToolAlias:       "mcp_fixture_mutate_counter",
		ReasonCode:      "invalid_arguments",
		RedactedDetail:  []byte(`{"stage":"proposal","call_id":"call-1","policy_id":"policy-1"}`),
	}
}

func TestInsertMCPMutationAuditDeniedAdmission(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if err := InsertMCPMutationAudit(context.Background(), &handler, validMCPMutationAuditEvent(now)); err != nil {
		t.Fatalf("insert denied admission audit: %v", err)
	}
	var eventType, actorType, actorID, actorIssuer, runID, traceID string
	var agentID, serverID, alias, reason, detail string
	var principal sql.NullInt64
	if err := db.QueryRow(`SELECT event_type, actor_type, actor_id, actor_issuer,
		run_id, trace_id, agent_id, principal_user_id, server_id, tool_alias,
		reason_code, redacted_detail FROM MCPMutationAudit`).Scan(
		&eventType, &actorType, &actorID, &actorIssuer, &runID, &traceID,
		&agentID, &principal, &serverID, &alias, &reason, &detail); err != nil {
		t.Fatalf("read denied admission audit: %v", err)
	}
	if eventType != MCPMutationAuditEventAdmissionDenied || actorType != "agent" ||
		actorID != "agent-1" || actorIssuer != "test-issuer" || runID != "run-1" ||
		traceID != "trace-1" || agentID != "agent-1" || !principal.Valid ||
		principal.Int64 != 7 || serverID != "fixture" ||
		alias != "mcp_fixture_mutate_counter" || reason != "invalid_arguments" {
		t.Fatalf("unexpected durable audit row: %s %s %s %s %s %s %s %v %s %s %s %s",
			eventType, actorType, actorID, actorIssuer, runID, traceID, agentID,
			principal, serverID, alias, reason, detail)
	}
	for _, fragment := range []string{`"stage":"proposal"`, `"call_id":"call-1"`, `"policy_id":"policy-1"`} {
		if !strings.Contains(detail, fragment) {
			t.Fatalf("audit detail missing %s: %s", fragment, detail)
		}
	}
	for _, forbidden := range []string{"fixture-resource", "token=", "https://", "secret"} {
		if strings.Contains(detail, forbidden) {
			t.Fatalf("audit detail leaked %q: %s", forbidden, detail)
		}
	}
}

func TestInsertMCPMutationAuditRejectsUnboundedContent(t *testing.T) {
	_, handler := newMCPMutationSQLiteTestDB(t)
	now := time.Now().UTC()
	oversizedDetail := []byte(`{"stage":"proposal","padding":"` + strings.Repeat("a", mcpMutationAuditMaxDetailBytes) + `"}`)
	cases := []struct {
		name  string
		event MCPMutationAuditEvent
	}{
		{name: "event type", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.EventType = "not a safe event"
			return e
		}()},
		{name: "actor type", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.ActorType = "root"
			return e
		}()},
		{name: "missing actor", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.ActorID = ""
			return e
		}()},
		{name: "reason code", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.ReasonCode = "raw error: token=secret"
			return e
		}()},
		{name: "identifier hash", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.ProposalID = "not-a-hash"
			return e
		}()},
		{name: "detail array", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.RedactedDetail = []byte(`[{"stage":"proposal"}]`)
			return e
		}()},
		{name: "nested detail", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.RedactedDetail = []byte(`{"stage":"proposal","nested":{"secret":"value"}}`)
			return e
		}()},
		{name: "uppercase detail key", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.RedactedDetail = []byte(`{"Stage":"proposal"}`)
			return e
		}()},
		{name: "oversized detail", event: func() MCPMutationAuditEvent {
			e := validMCPMutationAuditEvent(now)
			e.RedactedDetail = oversizedDetail
			return e
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := InsertMCPMutationAudit(context.Background(), &handler, tc.event); err == nil {
				t.Fatalf("invalid audit event was accepted")
			}
			var count int
			if err := handler.(*SQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationAudit`).Scan(&count); err != nil {
				t.Fatalf("count audit rows: %v", err)
			}
			if count != 0 {
				t.Fatalf("invalid audit event inserted %d rows", count)
			}
		})
	}
}

func TestCleanupMCPMutationAuditBoundedRetention(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)
	insert := func(eventType string, occurredAt time.Time) {
		t.Helper()
		event := validMCPMutationAuditEvent(occurredAt)
		event.EventType = eventType
		if err := InsertMCPMutationAudit(context.Background(), &handler, event); err != nil {
			t.Fatalf("insert %s: %v", eventType, err)
		}
	}
	insert("mcp_mutation_admission_denied", old)
	insert("mcp_mutation_registration_denied", old)
	insert("mcp_mutation_proposal_created", old)
	insert("mcp_mutation_approval_expired", old)
	insert("mcp_mutation_future_event", old)
	insert("mcp_mutation_admission_denied", now)

	cutoff := now.Add(-90 * 24 * time.Hour)
	removed, err := CleanupMCPMutationAudit(context.Background(), &handler, cutoff, 2)
	if err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if removed != 2 {
		t.Fatalf("first cleanup removed %d rows, want 2", removed)
	}
	removed, err = CleanupMCPMutationAudit(context.Background(), &handler, cutoff, 100)
	if err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if removed != 2 {
		t.Fatalf("second cleanup removed %d rows, want 2", removed)
	}
	removed, err = CleanupMCPMutationAudit(context.Background(), &handler, cutoff, 100)
	if err != nil {
		t.Fatalf("third cleanup: %v", err)
	}
	if removed != 0 {
		t.Fatalf("third cleanup removed %d rows, want 0", removed)
	}
	var known, unknown, recent int
	if err := db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN occurred_at < ? AND event_type IN ('mcp_mutation_admission_denied','mcp_mutation_registration_denied','mcp_mutation_proposal_created','mcp_mutation_approval_expired') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event_type='mcp_mutation_future_event' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN occurred_at >= ? THEN 1 ELSE 0 END),0)
		FROM MCPMutationAudit`, cutoff, cutoff).Scan(&known, &unknown, &recent); err != nil {
		t.Fatalf("count remaining audit rows: %v", err)
	}
	if known != 0 || unknown != 1 || recent != 1 {
		t.Fatalf("remaining audit rows known=%d unknown=%d recent=%d", known, unknown, recent)
	}
}

func TestCleanupMCPMutationAuditRejectsMissingCutoff(t *testing.T) {
	_, handler := newMCPMutationSQLiteTestDB(t)
	if _, err := CleanupMCPMutationAudit(context.Background(), &handler, time.Time{}, 10); err == nil {
		t.Fatalf("missing cutoff was accepted")
	}
}

func TestInsertMCPMutationAuditAfterDatabaseClose(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}
	if err := InsertMCPMutationAudit(context.Background(), &handler, validMCPMutationAuditEvent(time.Now().UTC())); err == nil {
		t.Fatalf("audit insert succeeded after database close")
	}
}
