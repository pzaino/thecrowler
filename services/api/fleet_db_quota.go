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

// apiDBQuotaState owns both controls governed by a fleet report. Holding mu
// while changing them prevents concurrent notifications from interleaving a
// pool update and its corresponding admission-gate update.
type apiDBQuotaState struct {
	mu                sync.Mutex
	dynamic           bool
	quota             int
	lastGeneratedAt   time.Time
	lastParentEventID string
	effectiveMaxIdle  int
	hasValidReport    bool
}

var apiDBQuota apiDBQuotaState
var apiDBMetrics = cdb.NewFleetDBMetrics(cdb.FleetMemberCrowlerAPI, cmn.GetMicroServiceName())

// Kept behind a variable so quota transitions can be tested without opening a
// database connection.
var setAPIConnectionLimits = cdb.SetConnectionLimits

func configureAPIQuota(c cfg.Config) {
	_, maxIdle := cdb.DetermineConnectionLimits(c)
	apiDBQuota.mu.Lock()
	defer apiDBQuota.mu.Unlock()
	apiDBQuota.dynamic = c.Events.HeartbeatEnabled
	apiDBQuota.effectiveMaxIdle = maxIdle
	if !apiDBQuota.dynamic {
		apiDBQuota.quota = cdb.ResolveEffectiveMaxOpenConnections(c)
	}
}

// applyAPIQuotaAfterConnect is called after every new connection, including a
// configuration reload. Dynamic mode can therefore never expose the static
// pool maximum, even briefly after Connect returns.
func applyAPIQuotaAfterConnect(c cfg.Config) error {
	apiDBQuota.mu.Lock()
	defer apiDBQuota.mu.Unlock()
	quota := apiDBQuota.quota
	if apiDBQuota.dynamic && !apiDBQuota.hasValidReport {
		quota = 1
	}
	if !apiDBQuota.dynamic {
		quota = cdb.ResolveEffectiveMaxOpenConnections(c)
	}
	return applyAPIQuotaLocked(quota)
}

func applyAPIQuotaLocked(quota int) error {
	idle := apiDBQuota.effectiveMaxIdle
	if idle > quota {
		idle = quota
	}
	if err := setAPIConnectionLimits(&dbHandler, quota, idle); err != nil {
		return err
	}
	dbAdmission.SetLimit(quota)
	apiDBQuota.quota = quota
	return nil
}

func processHeartbeatReport(event cdb.Event) {
	report, err := decodeFleetDBReport(event.Details)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "API: rejected heartbeat quota report: %v", err)
		return
	}
	localName := strings.TrimSpace(cmn.GetMicroServiceName())
	quota := 0
	for _, member := range report.Members {
		if strings.ToLower(strings.TrimSpace(member.OriginType)) == cdb.FleetMemberCrowlerAPI && strings.TrimSpace(member.OriginName) == localName {
			quota = member.MaxConnections
			break
		}
	}
	if quota == 0 {
		return
	}

	apiDBQuota.mu.Lock()
	defer apiDBQuota.mu.Unlock()
	if !apiDBQuota.dynamic {
		return
	}
	generated := report.GeneratedAt.UTC()
	if generated.Before(apiDBQuota.lastGeneratedAt) ||
		(generated.Equal(apiDBQuota.lastGeneratedAt) && report.ParentEventID <= apiDBQuota.lastParentEventID) {
		return
	}
	previous := apiDBQuota.quota
	if err := applyAPIQuotaLocked(quota); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "API: failed to apply heartbeat quota: %v", err)
		return
	}
	apiDBMetrics.ApplyReport(quota, report)
	apiDBQuota.lastGeneratedAt = generated
	apiDBQuota.lastParentEventID = report.ParentEventID
	apiDBQuota.hasValidReport = true
	if previous != quota {
		cmn.DebugMsg(cmn.DbgLvlInfo, "database quota changed service_type=%s instance=%s previous_quota=%d new_quota=%d fleet_members=%d effective_max=%d usable_fleet_capacity=%d heartbeat_parent_id=%s report_generated_at=%s", cdb.FleetMemberCrowlerAPI, localName, previous, quota, report.MemberCount, report.EffectiveMaxOpen, report.UsableConnections, report.ParentEventID, generated.Format(time.RFC3339Nano))
	}
}

func decodeFleetDBReport(details map[string]any) (cdb.FleetDBHeartbeatReport, error) {
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
		report.MemberCount < 0 || report.AllocationCount != len(report.Members) || report.MemberCount != len(report.Members) {
		return report, fmt.Errorf("invalid report payload")
	}
	if report.ReservedConnections+report.UsableConnections != report.EffectiveMaxOpen {
		return report, fmt.Errorf("inconsistent report capacity")
	}
	seen := make(map[cdb.FleetMember]struct{}, len(report.Members))
	allocated := 0
	for _, m := range report.Members {
		identity := cdb.FleetMember{OriginType: strings.ToLower(strings.TrimSpace(m.OriginType)), OriginName: strings.TrimSpace(m.OriginName)}
		if m.MaxConnections <= 0 || identity.OriginName == "" || len(cdb.NormalizeFleetMembers([]cdb.FleetMember{identity})) != 1 {
			return report, fmt.Errorf("malformed member entry")
		}
		if _, exists := seen[identity]; exists {
			return report, fmt.Errorf("duplicate member entry")
		}
		seen[identity] = struct{}{}
		allocated += m.MaxConnections
	}
	if len(report.Members) > 0 && allocated != report.UsableConnections {
		return report, fmt.Errorf("inconsistent member allocations")
	}
	return report, nil
}
