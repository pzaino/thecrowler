package main

import (
	"encoding/json"
	"testing"
	"time"

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
