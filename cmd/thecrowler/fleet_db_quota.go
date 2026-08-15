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

// engineDBQuota serializes report ordering and connection-pool changes. It is
// deliberately independent of crawler admission and worker configuration.
var engineDBQuota struct {
	mu                sync.Mutex
	dynamic           bool
	quota             int
	lastGeneratedAt   time.Time
	lastParentEventID string
	effectiveMaxIdle  int
	hasValidReport    bool
}

// Kept as a variable so quota state transitions can be tested without opening
// a real database. Production always uses the shared pool helper.
var setEngineConnectionLimits = cdb.SetConnectionLimits

func configureEngineDBQuota(c cfg.Config) {
	maxOpen, maxIdle := cdb.DetermineConnectionLimits(c)
	engineDBQuota.mu.Lock()
	defer engineDBQuota.mu.Unlock()
	engineDBQuota.dynamic = c.Events.HeartbeatEnabled
	engineDBQuota.effectiveMaxIdle = maxIdle
	if !engineDBQuota.dynamic {
		engineDBQuota.quota = maxOpen
	}
}

// applyEngineDBQuotaAfterConnect restores the authoritative limit after every
// pool recreation. Before the first valid dynamic report, one connection is
// the safe bootstrap quota.
func applyEngineDBQuotaAfterConnect(c cfg.Config) error {
	engineDBQuota.mu.Lock()
	defer engineDBQuota.mu.Unlock()
	// A disabled heartbeat retains the database driver's configured static
	// limits. In particular, do not require runtime pool control from SQLite.
	if !engineDBQuota.dynamic {
		engineDBQuota.quota = cdb.ResolveEffectiveMaxOpenConnections(c)
		return nil
	}
	quota := engineDBQuota.quota
	if !engineDBQuota.hasValidReport {
		quota = 1
	}
	return applyEngineDBQuotaLocked(quota)
}

func applyEngineDBQuotaLocked(quota int) error {
	idle := engineDBQuota.effectiveMaxIdle
	if idle > quota {
		idle = quota
	}
	if err := setEngineConnectionLimits(&dbHandler, quota, idle); err != nil {
		return err
	}
	engineDBQuota.quota = quota
	return nil
}

func processEngineHeartbeatReport(event cdb.Event) {
	report, err := decodeEngineFleetDBReport(event.Details)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Engine: rejected heartbeat quota report: %v", err)
		return
	}

	self := cdb.FleetMember{OriginType: cdb.FleetMemberCrowlerEngine, OriginName: strings.TrimSpace(cmn.GetMicroServiceName())}
	quota := 0
	for _, allocation := range report.Members {
		member := cdb.FleetMember{OriginType: allocation.OriginType, OriginName: allocation.OriginName}
		normalized := cdb.NormalizeFleetMembers([]cdb.FleetMember{member})
		if len(normalized) == 1 && normalized[0] == self {
			quota = allocation.MaxConnections
			break
		}
	}
	if quota <= 0 {
		return
	}

	engineDBQuota.mu.Lock()
	defer engineDBQuota.mu.Unlock()
	if !engineDBQuota.dynamic {
		return
	}
	generated := report.GeneratedAt.UTC()
	if generated.Before(engineDBQuota.lastGeneratedAt) ||
		(generated.Equal(engineDBQuota.lastGeneratedAt) && report.ParentEventID <= engineDBQuota.lastParentEventID) {
		return
	}
	if err := applyEngineDBQuotaLocked(quota); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Engine: failed to apply heartbeat quota: %v", err)
		return
	}
	engineDBQuota.lastGeneratedAt = generated
	engineDBQuota.lastParentEventID = report.ParentEventID
	engineDBQuota.hasValidReport = true
}

func decodeEngineFleetDBReport(details map[string]any) (cdb.FleetDBHeartbeatReport, error) {
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
	if report.SchemaVersion != "1" || strings.TrimSpace(report.ParentEventID) == "" || report.GeneratedAt.IsZero() || !report.Valid {
		return report, fmt.Errorf("invalid report schema or metadata")
	}
	if report.EffectiveMaxOpen <= 0 || report.ReservedConnections < 0 || report.UsableConnections < 0 ||
		report.MemberCount < 0 || report.AllocationCount != len(report.Members) || report.MemberCount != len(report.Members) ||
		report.ReservedConnections+report.UsableConnections != report.EffectiveMaxOpen {
		return report, fmt.Errorf("invalid report payload")
	}
	seen := make(map[cdb.FleetMember]struct{}, len(report.Members))
	allocated := 0
	for _, allocation := range report.Members {
		member := cdb.FleetMember{OriginType: allocation.OriginType, OriginName: allocation.OriginName}
		normalized := cdb.NormalizeFleetMembers([]cdb.FleetMember{member})
		if allocation.MaxConnections <= 0 || len(normalized) != 1 {
			return report, fmt.Errorf("malformed member entry")
		}
		if _, exists := seen[normalized[0]]; exists {
			return report, fmt.Errorf("duplicate member entry")
		}
		seen[normalized[0]] = struct{}{}
		allocated += allocation.MaxConnections
	}
	if len(report.Members) > 0 && allocated != report.UsableConnections {
		return report, fmt.Errorf("inconsistent member allocations")
	}
	return report, nil
}
