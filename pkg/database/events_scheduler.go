// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package database is responsible for handling the database setup, configuration and abstraction.
package database

import (
	"container/heap"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const (
	every  = "every"
	second = "second"
	minute = "minute"
	hour   = "hour"
	day    = "day"
	week   = "week"
	month  = "month"
	year   = "year"
)

var queueLock sync.Mutex

// ScheduledEvent represents a scheduled event.
type ScheduledEvent struct {
	EventID    string
	NextRun    time.Time
	Recurrence string
	Event      Event
}

// EventQueue is a priority queue of scheduled events.
type EventQueue []*ScheduledEvent

// Len returns the length of the queue.
func (pq EventQueue) Len() int { return len(pq) }

// Less compares two events by their next run time.
func (pq EventQueue) Less(i, j int) bool {
	return pq[i].NextRun.Before(pq[j].NextRun)
}

// Swap swaps two events in the queue.
func (pq EventQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

// Push and Pop use pointer receivers because they modify the slice's length,
func (pq *EventQueue) Push(x interface{}) {
	*pq = append(*pq, x.(*ScheduledEvent))
}

// Pop removes the last element from the queue.
func (pq *EventQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

// StartScheduler initializes the event scheduler and starts the processing loop.
func StartScheduler(db *Handler, cfg cfg.Config) {
	pq := &EventQueue{}
	heap.Init(pq)

	err := loadEventsFromDB(db, pq)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to initialize scheduler: %v", err)
		return
	}

	go schedulerLoop(db, pq)
	go schedulerListenerLoop(db, pq, cfg)
}

func schedulerListenerLoop(db *Handler, pq *EventQueue, cfg cfg.Config) {
	listener := (*db).NewListener()
	if listener == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "DB does not support listeners")
		return
	}

	err := listener.Connect(
		cfg,
		2*time.Second,
		30*time.Second,
		func(ev ListenerEventType, err error) {
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Scheduler listener event %v: %v", ev, err)
			}
		},
	)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to connect scheduler listener: %v", err)
		return
	}
	defer listener.Close()

	if err := listener.Listen("eventscheduler"); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to LISTEN on eventscheduler: %v", err)
		return
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Scheduler listening for schedule updates")

	for n := range listener.Notify() {
		eventID := n.Extra()
		cmn.DebugMsg(cmn.DbgLvlDebug, "Scheduler notified for event %s", eventID)

		if err := loadSingleSchedule(db, pq, eventID); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to load schedule %s: %v", eventID, err)
		}
	}
}

func loadSingleSchedule(db *Handler, pq *EventQueue, eventID string) error {
	row := (*db).QueryRow(`
		SELECT event_id, next_run, recurrence_interval, details
		FROM EventSchedules
		WHERE event_id = $1 AND active
	`, eventID)

	var ev ScheduledEvent
	var payload []byte

	if err := row.Scan(
		&ev.EventID,
		&ev.NextRun,
		&ev.Recurrence,
		&payload,
	); err != nil {
		return err
	}

	if err := json.Unmarshal(payload, &ev.Event); err != nil {
		return err
	}

	queueLock.Lock()
	defer queueLock.Unlock()

	heap.Push(pq, &ev)

	return nil
}

// loadEventsFromDB loads scheduled events from the database into the priority queue.
func loadEventsFromDB(db *Handler, pq *EventQueue) error {
	rows, err := (*db).ExecuteQuery("SELECT event_id, next_run, recurrence_interval, details FROM EventSchedules WHERE active")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading events from DB: %v", err)
		return err
	}
	defer rows.Close() // nolint: errcheck // We cannot check for the returned value in a defer

	for rows.Next() {
		var event ScheduledEvent
		var payload []byte
		if err := rows.Scan(&event.EventID, &event.NextRun, &event.Recurrence, &payload); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error scanning row: %v", err)
			continue
		}
		if err := json.Unmarshal(payload, &event.Event); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error unmarshaling event payload: %v", err)
			continue
		}
		queueLock.Lock()
		heap.Push(pq, &event)
		queueLock.Unlock()
	}

	if err := rows.Err(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error iterating rows: %v", err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Scheduler initialized with %d events", pq.Len())
	return nil
}

// schedulerLoop continuously processes events from the queue.
func schedulerLoop(db *Handler, pq *EventQueue) {
	timer := time.NewTimer(time.Hour * 24)
	defer timer.Stop()

	for {
		queueLock.Lock()
		empty := pq.Len() == 0
		queueLock.Unlock()
		if empty {
			timer.Reset(time.Hour * 24)
			<-timer.C
			continue
		}

		queueLock.Lock()
		nextEvent := (*pq)[0]
		now := time.Now()

		var d time.Duration
		if nextEvent.NextRun.After(now) {
			d = time.Until(nextEvent.NextRun)
			if d < 0 {
				d = 0
			}
		} else {
			d = 0
		}
		queueLock.Unlock()

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)

		<-timer.C

		queueLock.Lock()
		nextEvent = heap.Pop(pq).(*ScheduledEvent)
		queueLock.Unlock()

		// Execute the scheduled event
		if err := processEvent(db, nextEvent); err != nil {
			cmn.DebugMsg(
				cmn.DbgLvlError,
				"Error processing event %s: %v",
				nextEvent.EventID,
				err,
			)
			continue
		}

		// One-shot event: deactivate schedule and do NOT requeue
		rec := strings.ToLower(strings.TrimSpace(nextEvent.Recurrence))
		if rec == "" || rec == "never" {

			_, err := (*db).Exec(
				"UPDATE EventSchedules SET active = false WHERE event_id = $1",
				nextEvent.EventID,
			)
			if err != nil {
				cmn.DebugMsg(
					cmn.DbgLvlError,
					"Failed to deactivate one-shot schedule %s: %v",
					nextEvent.EventID,
					err,
				)
			}
			continue
		}

		// Recurring event: compute next run and requeue
		nextEvent.NextRun = calculateNextRun(nextEvent.NextRun, nextEvent.Recurrence)

		queueLock.Lock()
		heap.Push(pq, nextEvent)
		queueLock.Unlock()

		if err := updateScheduleInDB(db, nextEvent); err != nil {
			cmn.DebugMsg(
				cmn.DbgLvlError,
				"Error updating schedule for event %s: %v",
				nextEvent.EventID,
				err,
			)
		}
	}
}

// processEvent processes a single event, ensuring no duplicate entries in the Events table.
func processEvent(db *Handler, event *ScheduledEvent) error {
	evt := event.Event

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	evt.Timestamp = time.Now().Format(time.RFC3339)
	evt.Action = ""
	_, err := CreateEvent(ctx, db, evt)
	cancel()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error creating event %s: %v", event.EventID, err)
		return err
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Event %s processed successfully", event.EventID)
	return nil
}

// updateScheduleInDB updates the next run time of a recurring event in the database.
func updateScheduleInDB(db *Handler, event *ScheduledEvent) error {
	_, err := (*db).Exec(
		"UPDATE EventSchedules SET next_run = $1 WHERE event_id = $2",
		event.NextRun, event.EventID,
	)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to update schedule for event %s: %v", event.EventID, err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Schedule updated for event %s", event.EventID)
	}
	return err
}

// calculateNextRun calculates the next run time for a recurring event.
func calculateNextRun(lastRun time.Time, recurrence string) time.Time {
	recurrence = strings.ToLower(strings.TrimSpace(recurrence))

	// Determine the prefix of the recurrence string
	// if the recurrence strings has no spaces, it is a single word
	prefix := strings.Split(recurrence, " ")[0]
	prefix = strings.TrimSpace(prefix)

	// Process the recurrence string to determine if the request is single word or a full sentence
	recTime := ""
	recUnit := ""
	if prefix == every {
		recurrence = strings.TrimPrefix(recurrence, "every ")
		recTime = strings.Split(recurrence, " ")[0]
		recTime = strings.TrimSpace(recTime)
		recUnit = strings.Split(recurrence, " ")[1]
		recUnit = strings.TrimSpace(recUnit)
		if recTime == second ||
			recTime == minute ||
			recTime == hour ||
			recTime == day ||
			recTime == week ||
			recTime == month ||
			recTime == year {
			recUnit = recTime
			recTime = "1"
		}
	}

	// Process the prefix to determine the next run time
	switch prefix {
	case "daily":
		return lastRun.Add(24 * time.Hour)
	case "hourly":
		return lastRun.Add(time.Hour)
	case "weekly":
		return lastRun.Add(7 * 24 * time.Hour)
	case "monthly":
		return lastRun.AddDate(0, 1, 0)
	case "yearly":
		return lastRun.AddDate(1, 0, 0)
	case "never":
		return lastRun
	case every:
		switch recUnit {
		case second, "seconds":
			recTimeInt, err := time.ParseDuration(recTime + "s")
			if err != nil {
				return lastRun
			}
			return lastRun.Add(recTimeInt)
		case minute, "minutes":
			recTimeInt, err := time.ParseDuration(recTime + "m")
			if err != nil {
				return lastRun
			}
			return lastRun.Add(recTimeInt)
		case hour, "hours":
			recTimeInt, err := time.ParseDuration(recTime + "h")
			if err != nil {
				return lastRun
			}
			return lastRun.Add(recTimeInt)
		case day, "days":
			recTimeInt, err := strconv.Atoi(recTime)
			if err != nil {
				return lastRun
			}
			return lastRun.Add(time.Duration(recTimeInt) * 24 * time.Hour)
		case week, "weeks":
			recTimeInt, err := strconv.Atoi(recTime)
			if err != nil {
				return lastRun
			}
			return lastRun.Add(time.Duration(recTimeInt) * 7 * 24 * time.Hour)
		case month, "months":
			recTimeInt, err := strconv.Atoi(recTime)
			if err != nil {
				return lastRun
			}
			return lastRun.AddDate(0, recTimeInt, 0)
		case year, "years":
			recTimeInt, err := strconv.Atoi(recTime)
			if err != nil {
				return lastRun
			}
			return lastRun.AddDate(recTimeInt, 0, 0)
		default:
			return lastRun
		}
	default:
		return lastRun
	}
}
