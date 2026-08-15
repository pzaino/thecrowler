package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// eventsDBQuotaState serializes report ordering and pool changes. The last
// successfully applied report is retained across reloads and reconnects.
type eventsDBQuotaState struct {
	mu                sync.Mutex
	dynamic           bool
	quota             int
	lastGeneratedAt   time.Time
	lastParentEventID string
	effectiveMaxIdle  int
	hasValidReport    bool
}

var eventsDBQuota eventsDBQuotaState
var eventsDBMetrics = cdb.NewFleetDBMetrics(cdb.FleetMemberCrowlerEvents, cmn.GetMicroServiceName())

// This narrow seam keeps pool policy testable without weakening the database
// package's runtime pool-controller contract.
var setEventsConnectionLimits = func(maxOpen, maxIdle int) error {
	return cdb.SetConnectionLimits(&dbHandler, maxOpen, maxIdle)
}

func configureEventsQuota(c cfg.Config) {
	_, maxIdle := cdb.DetermineConnectionLimits(c)
	eventsDBQuota.mu.Lock()
	defer eventsDBQuota.mu.Unlock()
	wasDynamic := eventsDBQuota.dynamic
	eventsDBQuota.dynamic = c.Events.HeartbeatEnabled
	eventsDBQuota.effectiveMaxIdle = maxIdle
	if !eventsDBQuota.dynamic {
		eventsDBQuota.quota = cdb.ResolveEffectiveMaxOpenConnections(c)
		if wasDynamic {
			eventsDBQuota.hasValidReport = false
			eventsDBQuota.lastGeneratedAt = time.Time{}
			eventsDBQuota.lastParentEventID = ""
		}
	}
}

// applyEventsQuotaAfterConnect prevents a newly-created dynamic pool from
// temporarily using the static configured maximum.
func applyEventsQuotaAfterConnect(c cfg.Config) error {
	eventsDBQuota.mu.Lock()
	defer eventsDBQuota.mu.Unlock()
	if !eventsDBQuota.dynamic {
		// Connect owns static pool configuration; runtime pool control is an
		// optional backend capability required only by fleet budgeting.
		eventsDBQuota.quota = cdb.ResolveEffectiveMaxOpenConnections(c)
		return nil
	}
	quota := eventsDBQuota.quota
	if !eventsDBQuota.hasValidReport {
		quota = 1
	}
	return applyEventsQuotaLocked(quota)
}

func applyEventsQuotaLocked(quota int) error {
	idle := eventsDBQuota.effectiveMaxIdle
	if idle > quota {
		idle = quota
	}
	if err := setEventsConnectionLimits(quota, idle); err != nil {
		return err
	}
	eventsDBQuota.quota = quota
	return nil
}

// processEventsHeartbeatReport applies the coordinator's allocation directly;
// it never derives a local allocation or changes state when this member is
// absent, the report is old, or the pool update fails.
func processEventsHeartbeatReport(event cdb.Event) {
	report, err := decodeEventsFleetDBReport(event.Details)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "EVENTS: rejected heartbeat quota report: %v", err)
		return
	}
	localName := strings.TrimSpace(cmn.GetMicroServiceName())
	quota := 0
	for _, member := range report.Members {
		if strings.ToLower(strings.TrimSpace(member.OriginType)) == cdb.FleetMemberCrowlerEvents &&
			strings.TrimSpace(member.OriginName) == localName {
			quota = member.MaxConnections
			break
		}
	}
	if quota <= 0 {
		return
	}

	eventsDBQuota.mu.Lock()
	defer eventsDBQuota.mu.Unlock()
	if !eventsDBQuota.dynamic {
		return
	}
	generated := report.GeneratedAt.UTC()
	if generated.Before(eventsDBQuota.lastGeneratedAt) ||
		(generated.Equal(eventsDBQuota.lastGeneratedAt) && report.ParentEventID <= eventsDBQuota.lastParentEventID) {
		return
	}
	previous := eventsDBQuota.quota
	if err := applyEventsQuotaLocked(quota); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "EVENTS: failed to apply heartbeat quota: %v", err)
		return
	}
	eventsDBMetrics.ApplyReport(quota, report)
	eventsDBQuota.lastGeneratedAt = generated
	eventsDBQuota.lastParentEventID = report.ParentEventID
	eventsDBQuota.hasValidReport = true
	if previous != quota {
		cmn.DebugMsg(cmn.DbgLvlInfo, "database quota changed service_type=%s instance=%s previous_quota=%d new_quota=%d fleet_members=%d effective_max=%d usable_fleet_capacity=%d heartbeat_parent_id=%s report_generated_at=%s", cdb.FleetMemberCrowlerEvents, localName, previous, quota, report.MemberCount, report.EffectiveMaxOpen, report.UsableConnections, report.ParentEventID, generated.Format(time.RFC3339Nano))
	}
}

func decodeEventsFleetDBReport(details map[string]any) (cdb.FleetDBHeartbeatReport, error) {
	b, err := json.Marshal(details)
	if err != nil {
		return cdb.FleetDBHeartbeatReport{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var report cdb.FleetDBHeartbeatReport
	if err := dec.Decode(&report); err != nil {
		return report, err
	}
	if dec.More() {
		return report, fmt.Errorf("multiple report payloads")
	}
	if report.SchemaVersion != "1" || strings.TrimSpace(report.ParentEventID) == "" || report.GeneratedAt.IsZero() || !report.Valid {
		return report, fmt.Errorf("invalid report schema or metadata")
	}
	if report.EffectiveMaxOpen <= 0 || report.ReservedConnections < 0 || report.UsableConnections < 0 ||
		report.MemberCount < 0 || report.AllocationCount != len(report.Members) || report.MemberCount != len(report.Members) ||
		report.ReservedConnections+report.UsableConnections != report.EffectiveMaxOpen {
		return report, fmt.Errorf("invalid report capacity")
	}
	seen := make(map[cdb.FleetMember]struct{}, len(report.Members))
	allocated := 0
	for _, member := range report.Members {
		identity := cdb.FleetMember{OriginType: strings.ToLower(strings.TrimSpace(member.OriginType)), OriginName: strings.TrimSpace(member.OriginName)}
		if member.MaxConnections <= 0 || identity.OriginName == "" || len(cdb.NormalizeFleetMembers([]cdb.FleetMember{identity})) != 1 {
			return report, fmt.Errorf("malformed member entry")
		}
		if _, exists := seen[identity]; exists {
			return report, fmt.Errorf("duplicate member entry")
		}
		seen[identity] = struct{}{}
		allocated += member.MaxConnections
	}
	if len(report.Members) > 0 && allocated != report.UsableConnections {
		return report, fmt.Errorf("inconsistent member allocations")
	}
	return report, nil
}
