package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestHeartbeatCensusUsesTupleIdentity(t *testing.T) {
	heartbeatMu.Lock()
	previous := activeHeartbeat
	activeHeartbeat = &HeartbeatState{ParentID: "round", Responses: make(map[cdb.FleetMember]cdb.Event)}
	heartbeatMu.Unlock()
	t.Cleanup(func() {
		heartbeatMu.Lock()
		activeHeartbeat = previous
		heartbeatMu.Unlock()
	})

	response := func(id, typ, name string) cdb.Event {
		return cdb.Event{ID: id, Type: "crowler_heartbeat_response", Details: map[string]interface{}{
			"parent_event_id": "round", "origin_type": typ, "origin_name": name,
		}}
	}
	for _, event := range []cdb.Event{
		response("1", " CROWLER-API ", "shared"),
		response("2", "crowler-engine", "engine-1"),
		response("3", "crowler-events", "shared"),
		response("4", "plugin", "raw-only"),
	} {
		if !maybeHandleHeartbeatResponse(event) {
			t.Fatalf("response %s was not captured", event.ID)
		}
	}

	heartbeatMu.Lock()
	state := activeHeartbeat
	heartbeatMu.Unlock()
	if len(state.Responses) != 4 {
		t.Fatalf("responses = %d, want 4", len(state.Responses))
	}
	members := make([]cdb.FleetMember, 0, len(state.Responses))
	for member := range state.Responses {
		members = append(members, member)
	}
	got := cdb.NormalizeFleetMembers(members)
	want := []cdb.FleetMember{{OriginType: "crowler-api", OriginName: "shared"}, {OriginType: "crowler-engine", OriginName: "engine-1"}, {OriginType: "crowler-events", OriginName: "shared"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("census = %#v, want %#v", got, want)
	}
}

func TestFinalizeHeartbeatStateDeterministicallyRetainsUnknownResponders(t *testing.T) {
	state := &HeartbeatState{ParentID: "representative", Responses: map[cdb.FleetMember]cdb.Event{
		{OriginType: "unknown", OriginName: "src-9"}:           {ID: "9", Details: map[string]any{"opaque": true}},
		{OriginType: "crowler-events", OriginName: "events-b"}: {ID: "2"},
		{OriginType: "crowler-api", OriginName: "api-a"}:       {ID: "1"},
	}}
	first := finishHeartbeatState(state)
	second := finishHeartbeatState(state)
	want := []cdb.FleetMember{
		{OriginType: "crowler-api", OriginName: "api-a"},
		{OriginType: "crowler-events", OriginName: "events-b"},
		{OriginType: "unknown", OriginName: "src-9"},
	}
	if !reflect.DeepEqual(first.Responders, want) || !reflect.DeepEqual(second.Responders, want) {
		t.Fatalf("responders are not authoritative and deterministic: first=%#v second=%#v", first.Responders, second.Responders)
	}
	if first.Total != 3 || len(first.Raw) != 3 || first.Raw[2].ID != "9" || first.Raw[2].Details["opaque"] != true {
		t.Fatalf("generic/raw unknown response was lost: %#v", first)
	}
}

func TestPersistFleetDBHeartbeatReportOnceWithAssignedQuotas(t *testing.T) {
	previousCreate := createHeartbeatEvent
	previousDatabase := config.Database
	config.Database = cfg.Database{MaxConns: 10}
	var persisted []cdb.Event
	createHeartbeatEvent = func(_ *cdb.Handler, event cdb.Event) (string, error) {
		persisted = append(persisted, event)
		return "report", nil
	}
	t.Cleanup(func() { createHeartbeatEvent = previousCreate; config.Database = previousDatabase })

	state := &HeartbeatState{ParentID: "round", Responses: map[cdb.FleetMember]cdb.Event{
		{OriginType: "crowler-events", OriginName: "z"}: {},
		{OriginType: "crowler-api", OriginName: "a"}:    {},
	}}
	persistFleetDBHeartbeatReport(new(cdb.Handler), state)
	if len(persisted) != 1 || persisted[0].Type != "crowler_heartbeat_report" {
		t.Fatalf("persisted = %#v, want exactly one heartbeat report", persisted)
	}
	b, _ := json.Marshal(persisted[0].Details)
	var report cdb.FleetDBHeartbeatReport
	if err := json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Valid || report.SchemaVersion != "1" || report.ParentEventID != "round" || report.MemberCount != 2 || report.AllocationCount != 2 {
		t.Fatalf("unexpected report metadata: %#v", report)
	}
	if report.EffectiveMaxOpen != 10 || report.ReservedConnections != 3 || report.UsableConnections != 7 || report.GeneratedAt.IsZero() {
		t.Fatalf("unexpected capacity/timestamp fields: %#v", report)
	}
	want := []cdb.FleetMemberAllocation{{OriginType: "crowler-api", OriginName: "a", MaxConnections: 4}, {OriginType: "crowler-events", OriginName: "z", MaxConnections: 3}}
	if !reflect.DeepEqual(report.Members, want) {
		t.Fatalf("allocations = %#v, want %#v", report.Members, want)
	}
}

func TestPersistFleetDBHeartbeatReportMarksInvalidCapacity(t *testing.T) {
	previousCreate := createHeartbeatEvent
	previousDatabase := config.Database
	config.Database = cfg.Database{MaxConns: 3}
	var event cdb.Event
	createHeartbeatEvent = func(_ *cdb.Handler, got cdb.Event) (string, error) { event = got; return "report", nil }
	t.Cleanup(func() { createHeartbeatEvent = previousCreate; config.Database = previousDatabase })
	persistFleetDBHeartbeatReport(new(cdb.Handler), &HeartbeatState{Responses: map[cdb.FleetMember]cdb.Event{
		{OriginType: "crowler-api", OriginName: "api"}: {},
	}})
	if event.Details["valid"] != false || event.Details["reason"] == "" || event.Details["allocation_count"] != float64(0) {
		t.Fatalf("invalid report details = %#v", event.Details)
	}
	if members, ok := event.Details["members"].([]any); !ok || len(members) != 0 {
		t.Fatalf("invalid capacity emitted allocations: %#v", event.Details["members"])
	}
}

func TestHeartbeatTimeoutFinalizesOneAuthoritativeReport(t *testing.T) {
	previousCreate, previousDatabase := createHeartbeatEvent, config.Database
	config.Database = cfg.Database{MaxConns: 8}
	var persisted []cdb.Event
	createHeartbeatEvent = func(_ *cdb.Handler, event cdb.Event) (string, error) {
		persisted = append(persisted, event)
		return "report", nil
	}
	state := &HeartbeatState{ParentID: "final-round", Timeout: time.Millisecond, Responses: map[cdb.FleetMember]cdb.Event{
		{OriginType: "crowler-api", OriginName: "api-a"}: {},
	}}
	heartbeatMu.Lock()
	previousActive := activeHeartbeat
	activeHeartbeat = state
	heartbeatMu.Unlock()
	t.Cleanup(func() {
		createHeartbeatEvent, config.Database = previousCreate, previousDatabase
		heartbeatMu.Lock()
		activeHeartbeat = previousActive
		heartbeatMu.Unlock()
	})
	heartbeatTimeoutWatcher(new(cdb.Handler), state)
	heartbeatTimeoutWatcher(new(cdb.Handler), state)
	if len(persisted) != 1 || persisted[0].Type != "crowler_heartbeat_report" {
		t.Fatalf("finalization persisted %#v, want one authoritative report", persisted)
	}
}
