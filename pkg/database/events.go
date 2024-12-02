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
	"strconv"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
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

// CreateEvent creates a new event in the database.
// It receives in input an Event struct and returns the Event UID and an error if the operation fails.
func CreateEvent(db *Handler, e Event) (string, error) {
	// Generate a unique identifier for the event
	uid := GenerateEventUID(e)
	e.ID = uid

	// get the current timestamp
	e.Timestamp = time.Now().Format(time.RFC3339)

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
