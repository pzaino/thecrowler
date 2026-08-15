package database

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
)

// FleetDBMetrics contains the process-local view of the SQL pool and the last
// authoritative fleet allocation. Labels identify only the bounded service
// identity; report identifiers and times are deliberately not labels.
type FleetDBMetrics struct {
	labels                               prometheus.Labels
	Quota, PoolOpen, PoolInUse, PoolIdle prometheus.Gauge
	FleetMembers, FleetUsable            prometheus.Gauge
}

// NewFleetDBMetrics constructs (but does not register) the database collectors.
func NewFleetDBMetrics(serviceType, instance string) *FleetDBMetrics {
	labels := prometheus.Labels{"service_type": serviceType, "instance": instance}
	newGauge := func(name, help string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help, ConstLabels: labels})
	}
	return &FleetDBMetrics{
		labels:       labels,
		Quota:        newGauge("crowler_db_connection_quota", "Current process SQL-pool connection quota."),
		PoolOpen:     newGauge("crowler_db_pool_open_connections", "Current number of open database/sql connections."),
		PoolInUse:    newGauge("crowler_db_pool_in_use_connections", "Current number of database/sql connections in use."),
		PoolIdle:     newGauge("crowler_db_pool_idle_connections", "Current number of idle database/sql connections."),
		FleetMembers: newGauge("crowler_db_fleet_members", "Recognized members in the last applied fleet report."),
		FleetUsable:  newGauge("crowler_db_fleet_usable_connections", "Usable SQL-pool capacity in the last applied fleet report."),
	}
}

// Collectors returns all collectors for registration or Pushgateway use.
func (m *FleetDBMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.Quota, m.PoolOpen, m.PoolInUse, m.PoolIdle, m.FleetMembers, m.FleetUsable}
}

// UpdatePool copies database/sql's process-local counters without a query.
func (m *FleetDBMetrics) UpdatePool(stats sql.DBStats) {
	m.PoolOpen.Set(float64(stats.OpenConnections))
	m.PoolInUse.Set(float64(stats.InUse))
	m.PoolIdle.Set(float64(stats.Idle))
}

// ApplyReport records metadata only after a consumer has accepted the report.
func (m *FleetDBMetrics) ApplyReport(quota int, report FleetDBHeartbeatReport) {
	m.Quota.Set(float64(quota))
	m.FleetMembers.Set(float64(report.MemberCount))
	m.FleetUsable.Set(float64(report.UsableConnections))
}
