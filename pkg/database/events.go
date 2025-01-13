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
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

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
func InitializeScheduler(db *Handler) {
	eventsSchedulerInitialize = false
	go StartScheduler(db)
	eventsSchedulerInitialize = true
	cmn.DebugMsg(cmn.DbgLvlInfo, "Event scheduler started")
}

// CreateEvent creates a new event in the database.
// It receives in input an Event struct and returns the Event UID and an error if the operation fails.
func CreateEvent(db *Handler, e Event) (string, error) {
	// get the current timestamp
	e.Timestamp = time.Now().Format(time.RFC3339)

	// Generate a unique identifier for the event
	uid := GenerateEventUID(e)
	e.ID = uid

	// Execute the query
	_, err := (*db).Exec(`
		INSERT INTO Events (event_sha256, source_id, event_type, event_severity, event_timestamp, details)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		e.ID, e.SourceID, e.Type, e.Severity, e.Timestamp, cmn.ConvertMapToString(e.Details))
	if err != nil {
		return "", err
	}

	return uid, nil
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
	if !eventsSchedulerInitialize {
		InitializeScheduler(db)
	}

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

	// Insert the scheduled event into the EventSchedules table
	_, err = (*db).Exec(`
        INSERT INTO EventSchedules (event_id, next_run, recurrence_interval, active)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (event_id) DO UPDATE
        SET next_run = $2, recurrence_interval = $3, active = $4`,
		e.ID, schedTime, recurrence, true)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scheduling event in DB: %v", err)
		return schedTime, err
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

// ListenForEvents listens for new events in the database and calls the handleNotification function when a new event is received.
func ListenForEvents(db *Handler, handleNotification func(string)) {
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

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			select {
			case n, ok := <-listener.Notify():
				if !ok {
					cmn.DebugMsg(cmn.DbgLvlInfo, "Notification channel closed")
					return
				}
				if n != nil {
					handleNotification(n.Extra())
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
	}()

	<-stop // Wait for shutdown signal
}
