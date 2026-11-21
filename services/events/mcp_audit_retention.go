package main

import (
	"strings"
	"time"
)

const (
	// defaultMCPMutationAuditRetention keeps denied-admission and approval
	// audit evidence for a quarter by default.
	defaultMCPMutationAuditRetention = 90 * 24 * time.Hour
	// minMCPMutationAuditRetention prevents a typo from deleting the audit
	// trail almost immediately.
	minMCPMutationAuditRetention = 24 * time.Hour
	// mcpMutationAuditCleanupInterval avoids a per-minute table scan while
	// still keeping retention bounded.
	mcpMutationAuditCleanupInterval = time.Hour
	// mcpMutationAuditCleanupTimeout bounds one maintenance query so a
	// database outage cannot stall the janitor goroutine indefinitely.
	mcpMutationAuditCleanupTimeout = 30 * time.Second
)

func mcpMutationAuditRetention(value string) time.Duration {
	if strings.TrimSpace(value) == "" {
		return defaultMCPMutationAuditRetention
	}
	retention := parseDuration(value)
	if retention <= 0 {
		return defaultMCPMutationAuditRetention
	}
	if retention < minMCPMutationAuditRetention {
		return minMCPMutationAuditRetention
	}
	return retention
}
