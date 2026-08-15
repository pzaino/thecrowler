package main

import (
	"errors"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func resetEngineQuotaForTest(t *testing.T) *[]struct{ open, idle int } {
	t.Helper()
	engineDBQuota.mu.Lock()
	engineDBQuota.dynamic = false
	engineDBQuota.quota = 0
	engineDBQuota.lastGeneratedAt = time.Time{}
	engineDBQuota.lastParentEventID = ""
	engineDBQuota.effectiveMaxIdle = 0
	engineDBQuota.hasValidReport = false
	engineDBQuota.mu.Unlock()
	calls := new([]struct{ open, idle int })
	oldSetter := setEngineConnectionLimits
	setEngineConnectionLimits = func(_ *cdb.Handler, open, idle int) error {
		*calls = append(*calls, struct{ open, idle int }{open, idle})
		return nil
	}
	t.Cleanup(func() { setEngineConnectionLimits = oldSetter })
	return calls
}

func validEngineQuotaDetails() map[string]any {
	return map[string]any{
		"schema_version": "1", "parent_event_id": "round-1",
		"generated_at":       time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC),
		"effective_max_open": 7, "reserved_connections": 3, "usable_connections": 4,
		"member_count": 1, "allocation_count": 1, "valid": true,
		"members": []any{map[string]any{"origin_type": " crowler-engine ", "origin_name": "engine-a", "max_connections": 4}},
	}
}

func TestDecodeEngineFleetDBReportAcceptsDirectSuppliedQuota(t *testing.T) {
	report, err := decodeEngineFleetDBReport(validEngineQuotaDetails())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Members) != 1 || report.Members[0].MaxConnections != 4 {
		t.Fatalf("decoded members = %#v, want the supplied quota 4", report.Members)
	}
}

func TestDecodeEngineFleetDBReportRejectsInvalidReports(t *testing.T) {
	tests := map[string]func(map[string]any){
		"invalid":   func(d map[string]any) { d["valid"] = false },
		"malformed": func(d map[string]any) { d["unexpected"] = true },
		"non-positive quota": func(d map[string]any) {
			d["members"] = []any{map[string]any{"origin_type": "crowler-engine", "origin_name": "engine-a", "max_connections": 0}}
		},
		"duplicate member": func(d map[string]any) {
			d["member_count"], d["allocation_count"], d["usable_connections"], d["effective_max_open"] = 2, 2, 8, 11
			d["members"] = []any{
				map[string]any{"origin_type": "crowler-engine", "origin_name": "engine-a", "max_connections": 4},
				map[string]any{"origin_type": " CROWLER-ENGINE ", "origin_name": "engine-a", "max_connections": 4},
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			details := validEngineQuotaDetails()
			mutate(details)
			if _, err := decodeEngineFleetDBReport(details); err == nil {
				t.Fatal("invalid report was accepted")
			}
		})
	}
}

func TestConfigureEngineDBQuotaUsesHeartbeatWithoutChangingCrawlerSettings(t *testing.T) {
	engineDBQuota.mu.Lock()
	oldDynamic, oldQuota, oldGenerated, oldParent, oldIdle, oldValid := engineDBQuota.dynamic, engineDBQuota.quota, engineDBQuota.lastGeneratedAt, engineDBQuota.lastParentEventID, engineDBQuota.effectiveMaxIdle, engineDBQuota.hasValidReport
	engineDBQuota.mu.Unlock()
	t.Cleanup(func() {
		engineDBQuota.mu.Lock()
		defer engineDBQuota.mu.Unlock()
		engineDBQuota.dynamic, engineDBQuota.quota, engineDBQuota.lastGeneratedAt = oldDynamic, oldQuota, oldGenerated
		engineDBQuota.lastParentEventID, engineDBQuota.effectiveMaxIdle, engineDBQuota.hasValidReport = oldParent, oldIdle, oldValid
	})
	c := cfg.Config{}
	c.Events.HeartbeatEnabled = true
	c.Database.MaxConns = 17
	c.Database.MaxIdleConns = 6
	c.Crawler.MaxSources = 23
	c.Crawler.SourcePriority = "9"
	configureEngineDBQuota(c)
	if !engineDBQuota.dynamic || engineDBQuota.effectiveMaxIdle != 6 {
		t.Fatalf("quota configuration = dynamic %v, idle %d", engineDBQuota.dynamic, engineDBQuota.effectiveMaxIdle)
	}
	if c.Crawler.MaxSources != 23 || c.Crawler.SourcePriority != "9" {
		t.Fatalf("crawler/source claim configuration changed: %#v", c.Crawler)
	}

	c.Events.HeartbeatEnabled = false
	configureEngineDBQuota(c)
	if engineDBQuota.dynamic || engineDBQuota.quota != cdb.ResolveEffectiveMaxOpenConnections(c) {
		t.Fatalf("static quota configuration = dynamic %v, quota %d", engineDBQuota.dynamic, engineDBQuota.quota)
	}
}

func TestEngineQuotaBootstrapReportGrowthShrinkAndReload(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "engine-a")
	calls := resetEngineQuotaForTest(t)
	c := cfg.Config{}
	c.Events.HeartbeatEnabled = true
	c.Database.MaxIdleConns = 6
	configureEngineDBQuota(c)

	if err := applyEngineDBQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0]; got.open != 1 || got.idle != 1 {
		t.Fatalf("bootstrap limits = %+v, want open=1 idle=1", got)
	}

	processEngineHeartbeatReport(cdb.Event{Details: validEngineQuotaDetails()})
	if got := (*calls)[1]; got.open != 4 || got.idle != 4 {
		t.Fatalf("reported growth limits = %+v, want open=4 idle=4", got)
	}

	shrink := validEngineQuotaDetails()
	shrink["parent_event_id"] = "round-2"
	shrink["generated_at"] = time.Date(2026, 8, 13, 1, 3, 0, 0, time.UTC)
	shrink["effective_max_open"], shrink["usable_connections"] = 5, 2
	shrink["members"] = []any{map[string]any{"origin_type": "crowler-engine", "origin_name": "engine-a", "max_connections": 2}}
	processEngineHeartbeatReport(cdb.Event{Details: shrink})
	if got := (*calls)[2]; got.open != 2 || got.idle != 2 {
		t.Fatalf("reported shrink limits = %+v, want open=2 idle=2", got)
	}

	// A recreated pool must receive the last successfully accepted quota.
	if err := applyEngineDBQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[3]; got.open != 2 || got.idle != 2 {
		t.Fatalf("reload limits = %+v, want open=2 idle=2", got)
	}
}

func TestEngineQuotaIgnoresMissingSelfStaleAndFailedReports(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "engine-a")
	calls := resetEngineQuotaForTest(t)
	c := cfg.Config{}
	c.Events.HeartbeatEnabled = true
	configureEngineDBQuota(c)
	processEngineHeartbeatReport(cdb.Event{Details: validEngineQuotaDetails()})
	if len(*calls) != 1 {
		t.Fatalf("valid report applications = %d, want 1", len(*calls))
	}

	missing := validEngineQuotaDetails()
	missing["parent_event_id"] = "round-2"
	missing["generated_at"] = time.Date(2026, 8, 13, 2, 0, 0, 0, time.UTC)
	missing["members"] = []any{map[string]any{"origin_type": "crowler-api", "origin_name": "engine-a", "max_connections": 4}}
	processEngineHeartbeatReport(cdb.Event{Details: missing})

	stale := validEngineQuotaDetails()
	stale["parent_event_id"] = "round-0"
	stale["generated_at"] = time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	processEngineHeartbeatReport(cdb.Event{Details: stale})
	processEngineHeartbeatReport(cdb.Event{Details: validEngineQuotaDetails()}) // duplicate
	if len(*calls) != 1 {
		t.Fatalf("ignored reports changed pool %d times", len(*calls)-1)
	}

	failed := validEngineQuotaDetails()
	failed["parent_event_id"] = "round-3"
	failed["generated_at"] = time.Date(2026, 8, 13, 3, 0, 0, 0, time.UTC)
	setEngineConnectionLimits = func(_ *cdb.Handler, _, _ int) error { return errors.New("pool failure") }
	processEngineHeartbeatReport(cdb.Event{Details: failed})
	if engineDBQuota.lastParentEventID != "round-1" {
		t.Fatalf("failed application advanced ordering to %q", engineDBQuota.lastParentEventID)
	}
}

func TestHeartbeatDisabledLeavesStaticPoolUntouched(t *testing.T) {
	calls := resetEngineQuotaForTest(t)
	c := cfg.Config{}
	c.Database.MaxConns, c.Database.MaxIdleConns = 17, 6
	configureEngineDBQuota(c)
	if err := applyEngineDBQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("static mode applied runtime pool limits: %#v", *calls)
	}
}

func TestHeartbeatResponseIdentityUnchanged(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "engine-a")
	oldStatus := sysPipelineStatus
	empty := make([]crowler.Status, 0)
	sysPipelineStatus = &empty
	t.Cleanup(func() { sysPipelineStatus = oldStatus })
	event := newHeartbeatResponseEvent(cdb.Event{ID: "parent"}, time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC))
	if event.Type != "crowler_heartbeat_response" || event.Details["origin_type"] != "crowler-engine" || event.Details["origin_name"] != "engine-a" {
		t.Fatalf("heartbeat response identity changed: %#v", event)
	}
}
