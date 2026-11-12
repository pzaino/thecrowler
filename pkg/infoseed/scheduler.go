// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package infoseed

import (
	"context"
	"errors"
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
	DB       *cdb.Handler
	Runner   *Runner
	Config   cfg.InformationSeedConfig
	Engine   string
	Priority string
}

// StartScheduler starts an information-seed polling loop when enabled. An optional
// priority restricts claims for engines specialized by source priority. The
// returned cancel function stops future polling and in-flight workers observe the
// cancellation through their context.
func StartScheduler(parent context.Context, db *cdb.Handler, config cfg.InformationSeedConfig, runner *Runner, engine string, priorities ...string) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	if !config.Enabled {
		return cancel
	}
	priority := ""
	if len(priorities) > 0 {
		priority = strings.TrimSpace(priorities[0])
	}
	scheduler := Scheduler{DB: db, Runner: runner, Config: config, Engine: strings.TrimSpace(engine), Priority: priority}
	if scheduler.Engine == "" {
		scheduler.Engine = "infoseed"
	}
	wakeups, stopWakeups := cdb.SubscribeInformationSeedWakeups()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scheduler.listenForDatabaseWakeups(ctx)
	}()
	go func() {
		defer wg.Done()
		scheduler.Run(ctx, wakeups)
	}()
	return func() {
		cancel()
		stopWakeups()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			cmn.DebugMsg(cmn.DbgLvlWarn, "information seed scheduler shutdown timed out")
		}
	}
}

// Run polls ClaimInformationSeeds until ctx is cancelled. The wakeups channel is
// optional and only advances the next poll; periodic polling remains the durable
// recovery path for missed or unsupported notifications.
func (s Scheduler) Run(ctx context.Context, wakeups <-chan struct{}) {
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
	leaseDuration := time.Duration(s.Config.LeaseDuration) * time.Second
	maxAttempts := s.Config.MaxAttempts
	ticker := time.NewTicker(queryTimer)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wakeups:
		}
		drainWakeups(wakeups)
		s.runOnce(ctx, limit, processingTimeout, retryAfter, leaseDuration, maxAttempts)
		if s.Config.Retention.Enabled {
			if result, err := cdb.PurgeInformationSeedRetentionContext(ctx, s.DB, s.Config.Retention); err != nil {
				cmn.DebugMsg(cmn.DbgLvlWarn, "information seed retention: %v", redactInformationSeedError(err.Error()))
			} else if result.Considered > 0 {
				recordInformationSeedRetentionDeleted(result.Deleted)
				cmn.DebugMsg(cmn.DbgLvlInfo, "information seed retention considered=%d deleted=%d dry_run=%t", result.Considered, result.Deleted, result.DryRun)
			}
		}
	}
}

func (s Scheduler) listenForDatabaseWakeups(ctx context.Context) {
	if s.DB == nil || *s.DB == nil || strings.ToLower(strings.TrimSpace((*s.DB).DBMS())) == "" {
		return
	}
	if cdb.InformationSeedCreatedChannel == "" || !strings.Contains(strings.ToLower((*s.DB).DBMS()), "postgres") {
		return
	}
	listener := (*s.DB).NewListener()
	if listener == nil {
		return
	}
	defer func() { _ = listener.Close() }()
	if err := listener.Connect(cfg.Config{}, 10*time.Second, 90*time.Second, func(_ cdb.ListenerEventType, err error) {
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlWarn, "information seed listener error: %v", redactInformationSeedError(err.Error()))
		}
	}); err != nil {
		cmn.DebugMsg(cmn.DbgLvlWarn, "information seed listener connect failed; polling remains active: %v", redactInformationSeedError(err.Error()))
		return
	}
	if err := listener.Listen(cdb.InformationSeedCreatedChannel); err != nil {
		cmn.DebugMsg(cmn.DbgLvlWarn, "information seed LISTEN failed; polling remains active: %v", redactInformationSeedError(err.Error()))
		return
	}
	for {
		select {
		case <-ctx.Done():
			_ = listener.UnlistenAll()
			return
		case n, ok := <-listener.Notify():
			if !ok {
				return
			}
			if n != nil {
				cdb.WakeInformationSeedSchedulers()
			}
		}
	}
}

func drainWakeups(wakeups <-chan struct{}) {
	for {
		select {
		case <-wakeups:
		default:
			return
		}
	}
}

func (s Scheduler) runOnce(ctx context.Context, limit int, processingTimeout, retryAfter, leaseDuration time.Duration, maxAttempts int) {
	if s.DB == nil || s.Runner == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "information seed scheduler is not configured")
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err := cdb.ExhaustExpiredInformationSeedClaimsContext(ctx, s.DB, maxAttempts); err != nil {
		cmn.DebugMsg(cmn.DbgLvlWarn, "exhausting expired information seed claims: %v", redactInformationSeedError(err.Error()))
	}
	seeds, err := cdb.ClaimInformationSeedsContext(ctx, s.DB, limit, s.Priority, s.Engine, "", processingTimeout, retryAfter, leaseDuration, maxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			cmn.DebugMsg(cmn.DbgLvlError, "claiming information seeds: %v", redactInformationSeedError(err.Error()))
		}
		return
	}
	if len(seeds) == 0 {
		return
	}
	recordInformationSeedSeedsClaimed(s.Engine, len(seeds))
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for _, seed := range seeds {
		if seed.Disabled || strings.EqualFold(seed.Status, "disabled") {
			s.releaseClaim(seed)
			continue
		}
		wg.Add(1)
		go func(seed cdb.InformationSeed) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				s.releaseClaim(seed)
				return
			}
			if _, err := s.Runner.RunSeed(ctx, seed); err != nil {
				if ctx.Err() == nil {
					cmn.DebugMsg(cmn.DbgLvlError, "processing information seed %d: %v", seed.ID, redactInformationSeedError(err.Error()))
				}
			}
		}(seed)
	}
	wg.Wait()
}

func (s Scheduler) releaseClaim(seed cdb.InformationSeed) {
	if seed.ClaimToken == "" || s.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cdb.ReleaseInformationSeedClaimContext(ctx, s.DB, seed.ID, seed.ClaimToken, seed.FencingToken, "pending", "", nil); err != nil {
		if !errors.Is(err, cdb.ErrInformationSeedClaimLost) {
			cmn.DebugMsg(cmn.DbgLvlWarn, "releasing information seed claim %d: %v", seed.ID, redactInformationSeedError(err.Error()))
		}
		recordInformationSeedClaimFailure("release")
	} else {
		recordInformationSeedClaimRelease("shutdown")
	}
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
