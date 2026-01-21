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

// Path: pkg/database/events.go

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const (
	// EventSeverityInfo represents an informational event.
	EventSeverityInfo = "info"
	// EventSeverityWarning represents a warning event.
	EventSeverityWarning = "warning"
	// EventSeverityError represents an error event.
	EventSeverityError = "error"
)

var (
	eventsSchedulerInitialize bool
)

// GenerateEventUID generates a unique identifier for the event.
func GenerateEventUID(e Event) string {
	// convert e.SourceID into a string
	sID := strconv.FormatUint(e.SourceID, 10)

	// Convert event's Details field from map[string]interface{} to a string
	details := cmn.ConvertMapToString(e.Details)

	// Concatenate all the event's fields and return the SHA256 hash
	eStr := sID + e.Type + e.Severity + e.Timestamp + details

	return cmn.GenerateSHA256(eStr)
}

// InitializeScheduler initializes the centralized scheduler during application startup.
func InitializeScheduler(db *Handler, cfg cfg.Config) {
	eventsSchedulerInitialize = false
	go StartScheduler(db, cfg)
	eventsSchedulerInitialize = true
	cmn.DebugMsg(cmn.DbgLvlInfo, "Event scheduler started")
}

// CreateEvent creates a new event in the database.
// It receives in input an Event struct and returns the Event UID and an error if the operation fails.
func CreateEvent(ctx context.Context, db *Handler, e Event) (string, error) {
	// Fill timestamp if missing
	if e.Timestamp == "" {
		e.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Generate deterministic SHA256 UID
	uid := GenerateEventUID(e)
	e.ID = uid

	// Check if the event has expiration:
	if strings.TrimSpace(e.ExpiresAt) == "" {
		// Add standard 5 minutes expiration
		expTime := time.Now().Add(5 * time.Minute)
		e.ExpiresAt = expTime.Format(time.RFC3339)
	}

	// Start a transaction
	tx, err := (*db).BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return "", err
	}

	// Always rollback unless commit succeeds
	defer func() { _ = tx.Rollback() }()

	var ExpiresAt sql.NullTime
	if strings.TrimSpace(e.ExpiresAt) != "" {
		parsedTime, err := time.Parse(time.RFC3339, e.ExpiresAt)
		if err == nil {
			ExpiresAt = sql.NullTime{Time: parsedTime, Valid: true}
		} else {
			cmn.DebugMsg(cmn.DbgLvlWarn, "CreateEvent: invalid ExpiresAt format '%s', ignoring: %v", e.ExpiresAt, err)
		}
	}

	// Exec inside the transaction
	_, err = tx.ExecContext(ctx, `
        INSERT INTO Events (event_sha256, source_id, event_type, event_severity, event_timestamp, expires_at, details)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `,
		e.ID,
		e.SourceID,
		e.Type,
		e.Severity,
		e.Timestamp,
		ExpiresAt,
		cmn.ConvertMapToString(e.Details),
	)

	if err != nil {
		return "", err
	}

	// Commit
	if err := tx.Commit(); err != nil {
		return "", err
	}

	return uid, nil
}

// CreateEventWithRetries attempts to create a new event in the database with retries.
func CreateEventWithRetries(db *Handler, event Event) (string, error) {
	const maxRetries = 5
	const baseDelay = 50 * time.Millisecond

	var err error
	for i := 0; i < maxRetries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		eID, err := CreateEvent(ctx, db, event)
		cancel()
		if err == nil {
			return eID, nil // success!
		}

		cmn.DebugMsg(cmn.DbgLvlWarn, "CreateEvent failed (attempt %d/%d): %v", i+1, maxRetries, err)

		// classify non-retryable errors
		if pgErr, ok := err.(*pgconn.PgError); ok {
			switch pgErr.Code {
			case "23505": // duplicate key
				return "", err
			case "22P02", "23514": // bad input, constraint fail
				return "", err
			}
		}

		time.Sleep(time.Duration(i+1) * baseDelay) // linear backoff (or switch to exponential if needed)
	}

	// Final failure
	cmn.DebugMsg(cmn.DbgLvlError, "Failed to create event on the DB after retries: %v", err)
	return "", fmt.Errorf("failed to create event after %d retries: %w", maxRetries, err)
}

/*
// ScheduleEvent schedules a new event in the database. Like CreateEvent, it receives an Event struct, but it also needs a scheduling time and returns the Event UID and an error if the operation fails.
func ScheduleEvent(db *Handler, e Event, scheduleTime string) (time.Time, error) {
	// Parse the schedule time
	schedTime, err := time.Parse(time.RFC3339, scheduleTime)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error parsing schedule time:", err)
		return time.Now(), err
	}

	// Ensure the schedule time is not in the past
	now := time.Now()
	if schedTime.Before(now) {
		return schedTime, fmt.Errorf("Schedule time is in the past")
	}

	// Schedule the event creation
	go func(event Event, scheduleTime time.Time) {
		// Calculate the delay
		delay := time.Until(scheduleTime)
		timer := time.NewTimer(delay)

		<-timer.C // Wait for the timer to fire

		// Insert the event into the database
		_, err = CreateEvent(db, event)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error creating scheduled event:", err)
			return
		}

		cmn.DebugMsg(cmn.DbgLvlDebug, "Scheduled event successfully created:", event.ID)
	}(e, schedTime)

	return schedTime, nil
}
*/

// ScheduleEvent schedules a new event using the centralized scheduler.
func ScheduleEvent(db *Handler, e Event, scheduleTime string, recurrence string) (time.Time, error) {
	// Parse the schedule time
	schedTime, err := time.Parse(time.RFC3339, scheduleTime)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error parsing schedule time: %v", err)
		return time.Now(), err
	}

	// Ensure the schedule time is not in the past
	if schedTime.Before(time.Now()) {
		return schedTime, fmt.Errorf("schedule time is in the past")
	}

	// Generate a unique identifier for the event
	e.ID = GenerateEventUID(e)

	eventJSON, err := json.Marshal(e)
	if err != nil {
		return schedTime, fmt.Errorf("failed to marshal event for scheduler: %w", err)
	}

	// Insert the scheduled event into the EventSchedules table
	// NOTE: We re-use the event-id for the schedule ID to reduce over-scheduling for the same event
	_, err = (*db).Exec(`
        INSERT INTO EventSchedules (
			schedule_id,
			event_sched_uid,
			event_id,
			next_run,
			recurrence_interval,
			active,
			details
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id) DO UPDATE
		SET
			next_run = EXCLUDED.next_run,
			recurrence_interval = EXCLUDED.recurrence_interval,
			active = EXCLUDED.active,
			details = EXCLUDED.details,
			last_updated_at = CURRENT_TIMESTAMP;`,
		e.ID,       // schedule_id (intentional reuse)
		e.ID,       // event_sched_uid (intentional reuse, for now)
		e.ID,       // event_id
		schedTime,  // next_run
		recurrence, // recurrence_interval
		true,       // active
		eventJSON,  // details
	)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scheduling event in DB: %v", err)
		return schedTime, err
	}

	_, err = (*db).Exec(
		"NOTIFY eventscheduler, $1",
		e.ID,
	)
	if err != nil {
		cmn.DebugMsg(
			cmn.DbgLvlError,
			"Failed to notify scheduler for event %s: %v",
			e.ID,
			err,
		)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Scheduled event %s at %s with recurrence %s", e.ID, schedTime, recurrence)
	return schedTime, nil
}

// RemoveEvent removes an event from the database.
// It receives in input an Event UID and returns an error if the operation fails.
func RemoveEvent(db *Handler, eventUID string) error {
	// Execute the query
	_, err := (*db).Exec(`DELETE FROM Events WHERE event_sha256 = $1`, eventUID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveEventsBeforeTime removes all events that occurred before a specific time from the database.
// It receives in input a time and returns an error if the operation fails.
func RemoveEventsBeforeTime(db *Handler, time string) error {
	// Execute the query
	_, err := (*db).Exec(`DELETE FROM Events WHERE event_timestamp < $1`, time)
	if err != nil {
		return err
	}

	return nil
}

// GetEvent retrieves an event from the database.
// It receives in input an Event UID and returns the Event struct and an error if the operation fails.
func GetEvent(db *Handler, eventUID string) (Event, error) {
	var e Event

	// Execute the query
	row := (*db).QueryRow(`SELECT * FROM Events WHERE event_sha256 = $1`, eventUID)
	err := row.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
	if err != nil {
		return e, err
	}

	return e, nil
}

// GetEvents retrieves all the events from the database.
// It returns a slice of Event structs and an error if the operation fails.
func GetEvents(db *Handler) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events`)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySource retrieves all the events related to a specific source from the database.
// It receives in input a Source ID and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySource(db *Handler, sourceID uint64) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE source_id = $1`, sourceID)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsByType retrieves all the events of a specific type from the database.
// It receives in input an event type and returns a slice of Event structs and an error if the operation fails.
func GetEventsByType(db *Handler, eventType string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE event_type = $1`, eventType)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySeverity retrieves all the events of a specific severity from the database.
// It receives in input an event severity and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySeverity(db *Handler, eventSeverity string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE event_severity = $1`, eventSeverity)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsByTime retrieves all the events that occurred in a specific time frame from the database.
// It receives in input a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsByTime(db *Handler, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE event_timestamp BETWEEN $1 AND $2`, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySeverityAndTime retrieves all the events of a specific severity that occurred in a specific time frame from the database.
// It receives in input an event severity, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySeverityAndTime(db *Handler, eventSeverity, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE event_severity = $1 AND event_timestamp BETWEEN $2 AND $3`, eventSeverity, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsByTypeAndTime retrieves all the events of a specific type that occurred in a specific time frame from the database.
// It receives in input an event type, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsByTypeAndTime(db *Handler, eventType, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE event_type = $1 AND event_timestamp BETWEEN $2 AND $3`, eventType, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsByTypeSeverityAndTime retrieves all the events of a specific type and severity that occurred in a specific time frame from the database.
// It receives in input an event type, severity, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsByTypeSeverityAndTime(db *Handler, eventType, eventSeverity, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE event_type = $1 AND event_severity = $2 AND event_timestamp BETWEEN $3 AND $4`, eventType, eventSeverity, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySourceAndTime retrieves all the events related to a specific source that occurred in a specific time frame from the database.
// It receives in input a Source ID, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySourceAndTime(db *Handler, sourceID uint64, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE source_id = $1 AND event_timestamp BETWEEN $2 AND $3`, sourceID, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySourceSeverityAndTime retrieves all the events related to a specific source and severity that occurred in a specific time frame from the database.
// It receives in input a Source ID, an event severity, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySourceSeverityAndTime(db *Handler, sourceID uint64, eventSeverity, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE source_id = $1 AND event_severity = $2 AND event_timestamp BETWEEN $3 AND $4`, sourceID, eventSeverity, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySourceTypeAndTime retrieves all the events related to a specific source and type that occurred in a specific time frame from the database.
// It receives in input a Source ID, an event type, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySourceTypeAndTime(db *Handler, sourceID uint64, eventType, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE source_id = $1 AND event_type = $2 AND event_timestamp BETWEEN $3 AND $4`, sourceID, eventType, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// GetEventsBySourceTypeSeverityAndTime retrieves all the events related to a specific source, type and severity that occurred in a specific time frame from the database.
// It receives in input a Source ID, an event type, severity, a start and end time and returns a slice of Event structs and an error if the operation fails.
func GetEventsBySourceTypeSeverityAndTime(db *Handler, sourceID uint64, eventType, eventSeverity, startTime, endTime string) ([]Event, error) {
	var events []Event

	// Execute the query
	rows, err := (*db).ExecuteQuery(`SELECT * FROM Events WHERE source_id = $1 AND event_type = $2 AND event_severity = $3 AND event_timestamp BETWEEN $4 AND $5`, sourceID, eventType, eventSeverity, startTime, endTime)
	if err != nil {
		return events, err
	}
	defer rows.Close() //nolint:errcheck // We can't check the error here

	// Iterate over the rows
	for rows.Next() {
		var e Event
		err = rows.Scan(&e.ID, &e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &e.Details)
		if err != nil {
			return events, err
		}

		events = append(events, e)
	}

	return events, nil
}

// StartEventBus starts an event bus that listens for new events in the database and publishes them to subscribers.
func StartEventBus(db *Handler) (*EventBus, func()) {
	bus := NewEventBus()

	stop := make(chan struct{})
	go func() {
		// Reuse the exact same fetch/marshal logic you already have,
		// but instead of forwarding JSON to one handler, publish to the bus.
		listenForEventsLoop(db, stop, func(fullJSON string) {
			var e Event
			if err := json.Unmarshal([]byte(fullJSON), &e); err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to decode full event JSON for bus: %v", err)
				return
			}
			bus.Publish(e)
		})
	}()

	stopFn := func() { close(stop) }
	return bus, stopFn
}

// ListenForEvents listens for new events in the database and calls the handleNotification function when a new event is received.
func ListenForEvents(db *Handler, handleNotification func(string)) {
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, os.Interrupt, syscall.SIGTERM)

	stop := make(chan struct{})

	go func() {
		<-stopSig
		close(stop)
	}()

	go listenForEventsLoop(db, stop, handleNotification)

	// Block until stop is closed
	<-stop
}

// listenForEventsLoop is the internal loop that handles listening for events and processing notifications.
func listenForEventsLoop(db *Handler, stop <-chan struct{}, handleNotification func(string)) {
	listener := (*db).NewListener()
	if listener == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to create a new listener")
		return
	}

	defer func() {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Closing listener...")
		err := listener.Close()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to close the listener: %v", err)
		}
	}()

	err := listener.Connect(cfg.Config{}, 10, 90, func(_ ListenerEventType, err error) {
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Listener error: %v", err)
		}
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to connect to the database listener: %v", err)
		return
	}

	err = listener.Listen("new_event")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to listen on 'new_event': %v", err)
		return
	}

	for {
		select {
		case n, ok := <-listener.Notify():
			if !ok {
				cmn.DebugMsg(cmn.DbgLvlInfo, "Notification channel closed")
				return
			}
			if n != nil {
				// Step 1: Decode metadata
				var meta struct {
					EventSHA256    string `json:"event_sha256"`
					EventType      string `json:"event_type"`
					SourceID       uint64 `json:"source_id"`
					EventTimestamp string `json:"event_timestamp"`
				}

				if err := json.Unmarshal([]byte(n.Extra()), &meta); err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to decode event metadata: %v", err)
					continue
				}

				// Define retry parameters
				// TODO: total needs to become a user parameter
				total := 15 * time.Second        // total wait time
				delay := 120 * time.Millisecond  // delay between retries
				maxRetries := int(total / delay) // maximum number of retries

				// Step 2: Fetch full event from DB
				event, err := fetchEventWithRetry(db, meta.EventSHA256, maxRetries, delay)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						et := strings.ToLower(strings.TrimSpace(meta.EventType))
						if strings.HasPrefix(et, "crowler_") ||
							strings.HasPrefix(et, "system_") {
							cmn.DebugMsg(cmn.DbgLvlError, "System event '%s' of type '%s' was removed before listener could fetch it:  %v", meta.EventSHA256, et, err)
						} else {
							cmn.DebugMsg(cmn.DbgLvlDebug2, "Event '%s' of type '%s' possibly already processed and removed from queue: %v", meta.EventSHA256, et, err)
						}
					} else {
						cmn.DebugMsg(cmn.DbgLvlError, "Failed to fetch event '%s' of type '%s': %v", meta.EventSHA256, meta.EventType, err)
					}
					continue
				}

				// Step 3: Convert to JSON
				fullJSON, err := json.Marshal(event)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to marshal full event: %v", err)
					continue
				}

				// Step 4: Pass it to existing handler (NO code change there)
				handleNotification(string(fullJSON))
			}
		case <-stop:
			cmn.DebugMsg(cmn.DbgLvlInfo, "Shutting down the events handler...")
			err = listener.UnlistenAll()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to unlisten: %v", err)
			}
			return
		}
	}
}

// GetEventBySHA256 retrieves an event from the database by its SHA256 hash.
func GetEventBySHA256(db *Handler, id string) (Event, error) {
	var (
		e       Event
		details []byte
	)

	err := (*db).QueryRow(`
        SELECT source_id, event_type, event_severity, event_timestamp, details
        FROM events
        WHERE event_sha256 = $1
    `, id).Scan(&e.SourceID, &e.Type, &e.Severity, &e.Timestamp, &details)

	if err != nil {
		return e, err
	}

	// Decode JSON details
	if len(details) > 0 {
		if err := json.Unmarshal(details, &e.Details); err != nil {
			return e, fmt.Errorf("failed to decode event details: %w", err)
		}
	} else {
		e.Details = make(map[string]any)
	}

	e.ID = id
	return e, nil
}

func fetchEventWithRetry(db *Handler, id string, attempts int, delay time.Duration) (Event, error) {
	var evt Event
	var err error
	if attempts <= 0 {
		attempts = 5
	}

	for i := 0; i < attempts; i++ {
		evt, err = GetEventBySHA256(db, id)
		if err == nil {
			return evt, nil
		}

		if errors.Is(err, sql.ErrNoRows) {
			time.Sleep(delay)
			continue
		}

		return evt, err
	}
	return evt, err
}
