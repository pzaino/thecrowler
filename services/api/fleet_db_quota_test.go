package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func validQuotaDetails(t *testing.T) map[string]any {
	t.Helper()
	report := cdb.FleetDBHeartbeatReport{
		SchemaVersion: "1", ParentEventID: "round-1", GeneratedAt: time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC),
		EffectiveMaxOpen: 9, ReservedConnections: 3, UsableConnections: 6,
		MemberCount: 2, AllocationCount: 2, Valid: true,
		Members: []cdb.FleetMemberAllocation{
			{OriginType: cdb.FleetMemberCrowlerAPI, OriginName: "api-a", MaxConnections: 3},
			{OriginType: cdb.FleetMemberCrowlerEngine, OriginName: "api-a", MaxConnections: 3},
		},
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

func installAPIQuotaTest(t *testing.T) *[][2]int {
	t.Helper()
	apiDBQuota.mu.Lock()
	oldDynamic, oldQuota := apiDBQuota.dynamic, apiDBQuota.quota
	oldGenerated, oldParent := apiDBQuota.lastGeneratedAt, apiDBQuota.lastParentEventID
	oldIdle, oldValid := apiDBQuota.effectiveMaxIdle, apiDBQuota.hasValidReport
	apiDBQuota.dynamic, apiDBQuota.quota, apiDBQuota.lastGeneratedAt = false, 0, time.Time{}
	apiDBQuota.lastParentEventID, apiDBQuota.effectiveMaxIdle, apiDBQuota.hasValidReport = "", 0, false
	apiDBQuota.mu.Unlock()
	oldSetter, oldGate := setAPIConnectionLimits, dbAdmission
	calls := &[][2]int{}
	setAPIConnectionLimits = func(_ *cdb.Handler, open, idle int) error {
		*calls = append(*calls, [2]int{open, idle})
		return nil
	}
	dbAdmission = newDBAdmissionGate(99)
	t.Cleanup(func() {
		apiDBQuota.mu.Lock()
		apiDBQuota.dynamic, apiDBQuota.quota = oldDynamic, oldQuota
		apiDBQuota.lastGeneratedAt, apiDBQuota.lastParentEventID = oldGenerated, oldParent
		apiDBQuota.effectiveMaxIdle, apiDBQuota.hasValidReport = oldIdle, oldValid
		apiDBQuota.mu.Unlock()
		setAPIConnectionLimits, dbAdmission = oldSetter, oldGate
	})
	return calls
}

func quotaDetailsForAPI(t *testing.T, parent string, at time.Time, quota int) map[string]any {
	t.Helper()
	d := validQuotaDetails(t)
	d["parent_event_id"], d["generated_at"] = parent, at.Format(time.RFC3339Nano)
	d["effective_max_open"], d["usable_connections"] = quota+3, quota
	d["member_count"], d["allocation_count"] = 1, 1
	d["members"] = []any{map[string]any{"origin_type": "crowler-api", "origin_name": "api-a", "max_connections": quota}}
	return d
}

func TestDecodeFleetDBReportAcceptsAuthoritativeQuotasWithoutRecalculation(t *testing.T) {
	report, err := decodeFleetDBReport(validQuotaDetails(t))
	if err != nil {
		t.Fatalf("decodeFleetDBReport: %v", err)
	}
	if got := report.Members[0].MaxConnections; got != 3 {
		t.Fatalf("API quota = %d, want coordinator assignment 3", got)
	}
	if report.Members[0].OriginType == report.Members[1].OriginType {
		t.Fatal("fixture must exercise type-plus-name identity")
	}
}

func TestDecodeFleetDBReportRejectsMalformedPayloads(t *testing.T) {
	tests := map[string]func(map[string]any){
		"schema":            func(d map[string]any) { d["schema_version"] = "2" },
		"timestamp":         func(d map[string]any) { d["generated_at"] = "not-a-time" },
		"invalid":           func(d map[string]any) { d["valid"] = false },
		"nonpositive quota": func(d map[string]any) { d["members"].([]any)[0].(map[string]any)["max_connections"] = 0 },
		"malformed member":  func(d map[string]any) { d["members"].([]any)[0].(map[string]any)["origin_type"] = "other" },
		"unknown field":     func(d map[string]any) { d["surprise"] = true },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			details := validQuotaDetails(t)
			mutate(details)
			if _, err := decodeFleetDBReport(details); err == nil {
				t.Fatal("malformed report accepted")
			}
		})
	}
}

func TestAPIQuotaFinalizedReportsResizePoolAndAdmissionWithoutCancellingWork(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "api-a")
	calls := installAPIQuotaTest(t)
	c := cfg.Config{Database: cfg.Database{MaxConns: 20, MaxIdleConns: 8}}
	c.Events.HeartbeatEnabled = true
	configureAPIQuota(c)
	if err := applyAPIQuotaAfterConnect(c); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 15, 4, 0, 0, 0, time.UTC)
	processHeartbeatReport(cdb.Event{Details: quotaDetailsForAPI(t, "small-census", now, 8)})
	for i := 0; i < 6; i++ {
		if !dbAdmission.Acquire(0) {
			t.Fatalf("admission %d unexpectedly rejected", i)
		}
	}
	processHeartbeatReport(cdb.Event{Details: quotaDetailsForAPI(t, "larger-census", now.Add(time.Second), 3)})
	if dbAdmission.Limit() != 3 || dbAdmission.InUse() != 6 {
		t.Fatalf("shrink cancelled work or missed limit: limit=%d in-use=%d", dbAdmission.Limit(), dbAdmission.InUse())
	}
	if dbAdmission.Acquire(0) {
		t.Fatal("new work admitted above the smaller finalized quota")
	}
	for i := 0; i < 6; i++ {
		dbAdmission.Release()
	}
	processHeartbeatReport(cdb.Event{Details: quotaDetailsForAPI(t, "smaller-census", now.Add(2*time.Second), 11)})
	if dbAdmission.Limit() != 11 {
		t.Fatalf("scale-down quota = %d, want 11", dbAdmission.Limit())
	}
	want := [][2]int{{1, 1}, {8, 8}, {3, 3}, {11, 8}}
	if len(*calls) != len(want) {
		t.Fatalf("SQL pool calls = %v, want %v", *calls, want)
	}
	for i := range want {
		if (*calls)[i] != want[i] {
			t.Fatalf("SQL pool calls = %v, want %v", *calls, want)
		}
	}
}

func TestAPIQuotaConcurrentReportDeliveryAndGateUse(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "api-a")
	installAPIQuotaTest(t)
	c := cfg.Config{}
	c.Events.HeartbeatEnabled = true
	configureAPIQuota(c)
	now := time.Date(2026, 8, 15, 5, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	reports := make([]map[string]any, 24)
	for i := 1; i <= 24; i++ {
		reports[i-1] = quotaDetailsForAPI(t, string(rune('a'+i)), now.Add(time.Duration(i)*time.Second), i)
	}
	for i := 1; i <= 24; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			processHeartbeatReport(cdb.Event{Details: reports[i-1]})
			if dbAdmission.Acquire(0) {
				dbAdmission.Release()
			}
		}()
	}
	wg.Wait()
	if dbAdmission.Limit() != 24 {
		t.Fatalf("latest concurrent quota = %d, want 24", dbAdmission.Limit())
	}
}
