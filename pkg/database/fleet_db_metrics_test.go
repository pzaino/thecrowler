package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestFleetDBMetricsRegisterAndUpdate(t *testing.T) {
	m := NewFleetDBMetrics(FleetMemberCrowlerAPI, "api-a")
	r := prometheus.NewRegistry()
	for _, collector := range m.Collectors() {
		if err := r.Register(collector); err != nil {
			t.Fatalf("register collector: %v", err)
		}
	}
	m.UpdatePool(sql.DBStats{OpenConnections: 7, InUse: 3, Idle: 4})
	if got := testutil.ToFloat64(m.PoolOpen); got != 7 {
		t.Fatalf("open = %v, want 7", got)
	}
	if got := testutil.ToFloat64(m.PoolInUse); got != 3 {
		t.Fatalf("in use = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.PoolIdle); got != 4 {
		t.Fatalf("idle = %v, want 4", got)
	}

	m.ApplyReport(5, FleetDBHeartbeatReport{GeneratedAt: time.Now(), MemberCount: 3, UsableConnections: 15})
	if got := testutil.ToFloat64(m.Quota); got != 5 {
		t.Fatalf("quota = %v, want 5", got)
	}
	if got := testutil.ToFloat64(m.FleetMembers); got != 3 {
		t.Fatalf("members = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.FleetUsable); got != 15 {
		t.Fatalf("usable = %v, want 15", got)
	}
}
