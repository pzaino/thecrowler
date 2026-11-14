package main

import (
	"testing"
	"time"
)

func TestMCPMutationAuditRetention(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"empty uses ninety days", "", 90 * 24 * time.Hour},
		{"native duration", "2160h", 2160 * time.Hour},
		{"friendly duration", "30 days", 30 * 24 * time.Hour},
		{"below floor", "1h", 24 * time.Hour},
		{"invalid uses floor", "not a duration", 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpMutationAuditRetention(tc.value); got != tc.want {
				t.Fatalf("retention = %s, want %s", got, tc.want)
			}
		})
	}
}
