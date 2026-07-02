// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");

package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const InformationSeedCreatedChannel = "information_seed_created"

var informationSeedWakeups = struct {
	sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan struct{}
}{subscribers: map[uint64]chan struct{}{}}

// SubscribeInformationSeedWakeups registers an in-process wake-up subscriber for
// information seed creation notifications. The returned channel is intentionally
// buffered with capacity one so duplicate wake-ups coalesce while the scheduler
// is already awake or processing.
func SubscribeInformationSeedWakeups() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	informationSeedWakeups.Lock()
	informationSeedWakeups.nextID++
	id := informationSeedWakeups.nextID
	informationSeedWakeups.subscribers[id] = ch
	informationSeedWakeups.Unlock()

	cancel := func() {
		informationSeedWakeups.Lock()
		delete(informationSeedWakeups.subscribers, id)
		informationSeedWakeups.Unlock()
	}
	return ch, cancel
}

// NotifyInformationSeedCreated wakes any in-process information seed scheduler
// and, for PostgreSQL, emits a LISTEN/NOTIFY event so other processes can wake
// without waiting for their polling interval. SQLite and MySQL do not have a
// portable LISTEN/NOTIFY primitive here, so their durable recovery path remains
// the scheduler's periodic polling.
func NotifyInformationSeedCreated(ctx context.Context, db *Handler, seedID uint64) error {
	if seedID == 0 {
		return fmt.Errorf("information seed ID must be provided")
	}
	publishInformationSeedWakeup()

	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if normalizeInformationSeedDBMS((*db).DBMS()) != DBPostgresStr {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := (*db).ExecContext(ctx, "SELECT pg_notify($1, $2)", InformationSeedCreatedChannel, strconv.FormatUint(seedID, 10))
	if err != nil {
		return fmt.Errorf("failed to notify PostgreSQL information seed creation: %w", err)
	}
	return nil
}

// CreateInformationSeedAndNotify inserts a seed and wakes enabled seed
// schedulers when the newly-created row is immediately claimable.
func CreateInformationSeedAndNotify(ctx context.Context, db *Handler, seed *InformationSeed) (uint64, error) {
	id, err := CreateInformationSeed(db, seed)
	if err != nil {
		return 0, err
	}
	if shouldNotifyInformationSeedCreated(seed) {
		// Notification is an optimization; the scheduler keeps polling as recovery,
		// so a wake-up failure must not turn a successful insert into an API error.
		_ = NotifyInformationSeedCreated(ctx, db, id)
	}
	return id, nil
}

func shouldNotifyInformationSeedCreated(seed *InformationSeed) bool {
	if seed == nil || seed.Disabled {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(seed.Status))
	return status == "" || status == "new" || status == "pending"
}

// WakeInformationSeedSchedulers wakes in-process scheduler subscribers without
// emitting a database notification. It is used by database listeners after they
// receive a cross-process notification.
func WakeInformationSeedSchedulers() {
	publishInformationSeedWakeup()
}

func publishInformationSeedWakeup() {
	informationSeedWakeups.Lock()
	defer informationSeedWakeups.Unlock()
	for _, ch := range informationSeedWakeups.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
