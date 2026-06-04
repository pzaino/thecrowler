// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Scheduler polls and processes information seeds.
type Scheduler struct {
	DB     *cdb.Handler
	Runner *Runner
	Config cfg.InformationSeedConfig
	Engine string
}

// StartScheduler starts an information-seed polling loop when enabled. The
// returned cancel function stops future polling and in-flight workers observe the
// cancellation through their context.
func StartScheduler(parent context.Context, db *cdb.Handler, config cfg.InformationSeedConfig, runner *Runner, engine string) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	if !config.Enabled {
		return cancel
	}
	scheduler := Scheduler{DB: db, Runner: runner, Config: config, Engine: strings.TrimSpace(engine)}
	if scheduler.Engine == "" {
		scheduler.Engine = "infoseed"
	}
	go scheduler.Run(ctx)
	return cancel
}

// Run polls ClaimInformationSeeds until ctx is cancelled.
func (s Scheduler) Run(ctx context.Context) {
	queryTimer := time.Duration(s.Config.QueryTimer) * time.Second
	if queryTimer <= 0 {
		queryTimer = 5 * time.Minute
	}
	processingTimeout := ParseDurationOrDefault(s.Config.ProcessingTimeout, 30*time.Minute)
	retryAfter := time.Duration(s.Config.RetryInterval) * time.Second
	if retryAfter <= 0 {
		retryAfter = time.Minute
	}
	limit := s.Config.MaxConcurrentSeeds
	if limit < 1 {
		limit = 1
	}
	ticker := time.NewTicker(queryTimer)
	defer ticker.Stop()
	for {
		s.runOnce(ctx, limit, processingTimeout, retryAfter)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s Scheduler) runOnce(ctx context.Context, limit int, processingTimeout, retryAfter time.Duration) {
	if s.DB == nil || s.Runner == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "information seed scheduler is not configured")
		return
	}
	seeds, err := cdb.ClaimInformationSeeds(s.DB, limit, "", s.Engine, processingTimeout, retryAfter)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "claiming information seeds: %v", err)
		return
	}
	if len(seeds) == 0 {
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for _, seed := range seeds {
		if seed.Disabled || strings.EqualFold(seed.Status, "disabled") {
			continue
		}
		wg.Add(1)
		go func(seed cdb.InformationSeed) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if _, err := s.Runner.RunSeed(ctx, seed); err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "processing information seed %d: %v", seed.ID, err)
			}
		}(seed)
	}
	wg.Wait()
}

// ParseDurationOrDefault parses Go duration strings and human-readable values
// like "30 minutes" or "1 hour".
func ParseDurationOrDefault(value string, fallback time.Duration) time.Duration {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	var amount float64
	var unit string
	if _, err := fmt.Sscanf(value, "%f %s", &amount, &unit); err != nil || amount <= 0 {
		return fallback
	}
	switch strings.TrimSuffix(unit, "s") {
	case "second", "sec":
		return time.Duration(amount * float64(time.Second))
	case "minute", "min":
		return time.Duration(amount * float64(time.Minute))
	case "hour", "hr":
		return time.Duration(amount * float64(time.Hour))
	case "day":
		return time.Duration(amount * float64(24*time.Hour))
	default:
		return fallback
	}
}
