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
	"strconv"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
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
func StartScheduler(db *Handler) {
	pq := &EventQueue{}
	heap.Init(pq)

	err := loadEventsFromDB(db, pq)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to initialize scheduler: %v", err)
		return
	}

	go schedulerLoop(db, pq)
}

// loadEventsFromDB loads scheduled events from the database into the priority queue.
func loadEventsFromDB(db *Handler, pq *EventQueue) error {
	rows, err := (*db).ExecuteQuery("SELECT event_id, next_run, recurrence_interval FROM EventSchedules WHERE active")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading events from DB: %v", err)
		return err
	}
	defer rows.Close() // nolint: errcheck // We cannot check for the returned value in a defer

	for rows.Next() {
		var event ScheduledEvent
		if err := rows.Scan(&event.EventID, &event.NextRun, &event.Recurrence); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error scanning row: %v", err)
			continue
		}
		heap.Push(pq, &event)
	}

	if err := rows.Err(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error iterating rows: %v", err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Scheduler initialized with %d events", pq.Len())
	return nil
}

// schedulerLoop continuously processes events from the queue.
func schedulerLoop(db *Handler, pq *EventQueue) {
	timer := time.NewTimer(time.Hour * 24) // Start with a large timeout
	defer timer.Stop()

	for {
		if pq.Len() == 0 {
			timer.Reset(time.Hour * 24)
			<-timer.C
			continue
		}

		queueLock.Lock()
		nextEvent := (*pq)[0]
		now := time.Now()

		if nextEvent.NextRun.After(now) {
			timer.Reset(time.Until(nextEvent.NextRun))
		} else {
			timer.Reset(0)
		}
		queueLock.Unlock()

		<-timer.C

		queueLock.Lock()
		nextEvent = heap.Pop(pq).(*ScheduledEvent)
		queueLock.Unlock()

		if err := processEvent(db, nextEvent); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error processing event %s: %v", nextEvent.EventID, err)
		} else if nextEvent.Recurrence != "" {
			nextEvent.NextRun = calculateNextRun(nextEvent.NextRun, nextEvent.Recurrence)
			heap.Push(pq, nextEvent)

			if err := updateScheduleInDB(db, nextEvent); err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error updating schedule for event %s: %v", nextEvent.EventID, err)
			}
		}
	}
}

// processEvent processes a single event, ensuring no duplicate entries in the Events table.
func processEvent(db *Handler, event *ScheduledEvent) error {
	existingEvent, err := GetEvent(db, event.EventID)
	if err == nil && existingEvent.ID == event.EventID {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Event %s already exists, skipping", event.EventID)
		return nil
	}

	evt, err := GetEvent(db, event.EventID)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to retrieve event %s: %v", event.EventID, err)
		return err
	}

	_, err = CreateEvent(db, evt)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error creating event %s: %v", event.EventID, err)
		return err
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Event %s processed successfully", event.EventID)
	return nil
}

// updateScheduleInDB updates the next run time of a recurring event in the database.
func updateScheduleInDB(db *Handler, event *ScheduledEvent) error {
	_, err := (*db).ExecuteQuery(
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
