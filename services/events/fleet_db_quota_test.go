package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func installEventsQuotaTest(t *testing.T, dynamic bool, maxOpen, maxIdle int) *[][2]int {
	t.Helper()
	oldSetter := setEventsConnectionLimits
	eventsDBQuota.mu.Lock()
	oldDynamic, oldQuota := eventsDBQuota.dynamic, eventsDBQuota.quota
	oldGenerated, oldParent := eventsDBQuota.lastGeneratedAt, eventsDBQuota.lastParentEventID
	oldIdle, oldValid := eventsDBQuota.effectiveMaxIdle, eventsDBQuota.hasValidReport
	eventsDBQuota.dynamic, eventsDBQuota.quota = false, 0
	eventsDBQuota.lastGeneratedAt, eventsDBQuota.lastParentEventID = time.Time{}, ""
	eventsDBQuota.effectiveMaxIdle, eventsDBQuota.hasValidReport = 0, false
	eventsDBQuota.mu.Unlock()
	calls := [][2]int{}
	setEventsConnectionLimits = func(open, idle int) error {
		calls = append(calls, [2]int{open, idle})
		return nil
	}
	c := cfg.Config{Database: cfg.Database{MaxConns: maxOpen, MaxIdleConns: maxIdle}}
	c.Events.HeartbeatEnabled = dynamic
	configureEventsQuota(c)
	t.Cleanup(func() {
		eventsDBQuota.mu.Lock()
		eventsDBQuota.dynamic, eventsDBQuota.quota = oldDynamic, oldQuota
		eventsDBQuota.lastGeneratedAt, eventsDBQuota.lastParentEventID = oldGenerated, oldParent
		eventsDBQuota.effectiveMaxIdle, eventsDBQuota.hasValidReport = oldIdle, oldValid
		eventsDBQuota.mu.Unlock()
		setEventsConnectionLimits = oldSetter
	})
	return &calls
}

func eventsQuotaDetails(t *testing.T, parent string, generated time.Time, members ...cdb.FleetMemberAllocation) map[string]any {
	t.Helper()
	usable := 0
	for _, member := range members {
		usable += member.MaxConnections
	}
	report := cdb.FleetDBHeartbeatReport{
		SchemaVersion: "1", ParentEventID: parent, GeneratedAt: generated,
		EffectiveMaxOpen: usable + 3, ReservedConnections: 3, UsableConnections: usable,
		MemberCount: len(members), AllocationCount: len(members), Members: members, Valid: true,
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var details map[string]any
	if err := json.Unmarshal(b, &details); err != nil {
		t.Fatal(err)
	}
	return details
}

func TestEventsQuotaBootstrapReportResizeAndReconnect(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "events-a")
	calls := installEventsQuotaTest(t, true, 20, 8)
	c := cfg.Config{Database: cfg.Database{MaxConns: 20, MaxIdleConns: 8}}
	c.Events.HeartbeatEnabled = true
	if err := applyEventsQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	processEventsHeartbeatReport(cdb.Event{Details: eventsQuotaDetails(t, "one", now,
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "events-a", MaxConnections: 3})})
	processEventsHeartbeatReport(cdb.Event{Details: eventsQuotaDetails(t, "two", now.Add(time.Second),
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "events-a", MaxConnections: 12})})
	configureEventsQuota(c) // reload must preserve the last valid quota
	if err := applyEventsQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{1, 1}, {3, 3}, {12, 8}, {12, 8}}
	if len(*calls) != len(want) {
		t.Fatalf("pool changes = %v, want %v", *calls, want)
	}
	for i := range want {
		if (*calls)[i] != want[i] {
			t.Fatalf("pool changes = %v, want %v", *calls, want)
		}
	}
}

func TestEventsQuotaExactIdentityAndReportOrdering(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "shared")
	calls := installEventsQuotaTest(t, true, 20, 10)
	now := time.Date(2026, 8, 15, 2, 0, 0, 0, time.UTC)
	valid := eventsQuotaDetails(t, "round-b", now,
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerAPI, OriginName: "shared", MaxConnections: 2},
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "shared", MaxConnections: 4})
	processEventsHeartbeatReport(cdb.Event{Details: valid})
	processEventsHeartbeatReport(cdb.Event{Details: valid}) // duplicate
	processEventsHeartbeatReport(cdb.Event{Details: eventsQuotaDetails(t, "older", now.Add(-time.Second),
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "shared", MaxConnections: 7})})
	processEventsHeartbeatReport(cdb.Event{Details: eventsQuotaDetails(t, "missing", now.Add(time.Second),
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerAPI, OriginName: "shared", MaxConnections: 6})})
	if len(*calls) != 1 || (*calls)[0] != [2]int{4, 4} {
		t.Fatalf("pool changes = %v, want exact Events allocation only", *calls)
	}
}

func TestEventsQuotaRejectsMalformedInvalidAndFailedApplication(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "events-a")
	calls := installEventsQuotaTest(t, true, 20, 10)
	now := time.Date(2026, 8, 15, 3, 0, 0, 0, time.UTC)
	for _, details := range []map[string]any{
		{"schema_version": "1"},
		eventsQuotaDetails(t, "zero", now, cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "events-a", MaxConnections: 0}),
	} {
		processEventsHeartbeatReport(cdb.Event{Details: details})
	}
	setEventsConnectionLimits = func(int, int) error { return errors.New("pool failure") }
	processEventsHeartbeatReport(cdb.Event{Details: eventsQuotaDetails(t, "failed", now,
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "events-a", MaxConnections: 5})})
	if len(*calls) != 0 || eventsDBQuota.hasValidReport || eventsDBQuota.quota != 0 {
		t.Fatalf("rejected reports changed quota state: calls=%v state=%+v", *calls, eventsDBQuota)
	}
}

func TestEventsQuotaStaticModeIgnoresReports(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "events-a")
	calls := installEventsQuotaTest(t, false, 14, 6)
	c := cfg.Config{Database: cfg.Database{MaxConns: 14, MaxIdleConns: 6}}
	if err := applyEventsQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	processEventsHeartbeatReport(cdb.Event{Details: eventsQuotaDetails(t, "round", time.Now().UTC(),
		cdb.FleetMemberAllocation{OriginType: cdb.FleetMemberCrowlerEvents, OriginName: "events-a", MaxConnections: 2})})
	if len(*calls) != 0 {
		t.Fatalf("static mode applied runtime pool limits: %v", *calls)
	}
}
