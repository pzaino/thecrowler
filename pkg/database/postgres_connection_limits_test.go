package database

import (
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestDetermineConnectionLimits(t *testing.T) {
	tests := []struct {
		name       string
		database   cfg.Database
		open, idle int
	}{
		{"defaults", cfg.Database{}, 8, 2},
		{"explicit", cfg.Database{MaxConns: 31, MaxIdleConns: 7}, 31, 7},
		{"none explicit", cfg.Database{OptimizeFor: " none ", MaxConns: 30, MaxIdleConns: 6}, 30, 6},
		{"write", cfg.Database{OptimizeFor: " WRITE ", MaxConns: 31, MaxIdleConns: 7}, 10, 2},
		{"query", cfg.Database{OptimizeFor: "query", MaxConns: 31, MaxIdleConns: 7}, 12, 4},
		{"unknown fallback", cfg.Database{OptimizeFor: "read", MaxConns: 31, MaxIdleConns: 7}, 8, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOpen, gotIdle := DetermineConnectionLimits(cfg.Config{Database: tt.database})
			if gotOpen != tt.open || gotIdle != tt.idle {
				t.Errorf("got (%d, %d), want (%d, %d)", gotOpen, gotIdle, tt.open, tt.idle)
			}
		})
	}
}
