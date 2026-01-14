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

// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// CreateEventAction creates a database event
type CreateEventAction struct{}

// Name returns the name of the action
func (e *CreateEventAction) Name() string {
	return "CreateEvent"
}

// Execute creates a database event
func (e *CreateEventAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'db_handler' parameter in config section"
		return rval, fmt.Errorf("missing 'db_handler' parameter")
	}

	eventRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	eventMap, _ := eventRaw[StrRequest].(map[string]interface{})

	// Transform eventRaw into an event struct
	event := cdb.Event{}
	event.Details = eventRaw
	if params["event_type"] != nil {
		eType, _ := params["event_type"].(string)
		eType = strings.TrimSpace(resolveResponseString(eventMap, eType))
		event.Type = eType
	}
	if params["source"] != nil {
		eSource, _ := params["source"].(string)
		eSource = resolveResponseString(eventMap, eSource)
		eSourceInt, err := strconv.ParseUint(eSource, 10, 64)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid source ID: %v", err)
			return rval, err
		}
		event.SourceID = eSourceInt
	} else {
		event.SourceID = 0
	}
	// Check if there is a timestamp params
	if params["timestamp"] != nil {
		eTimestamp, _ := params["timestamp"].(string)
		eTimestamp = resolveResponseString(eventMap, eTimestamp)
		// convert eTimestamp to a time.Time
		t, err := time.Parse("2006-01-02 15:04:05", eTimestamp)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid timestamp: %v", err)
			return rval, err
		}
		event.Timestamp = t.Format("2006-01-02 15:04:05")
	} else {
		now := time.Now().UTC()
		event.Timestamp = string(now.Format("2006-01-02 15:04:05"))
	}
	// Check if there is a Severity params
	if params["severity"] != nil {
		eSeverity, _ := params["severity"].(string)
		eSeverity = strings.ToUpper(strings.TrimSpace(resolveResponseString(eventMap, eSeverity)))
		event.Severity = eSeverity
	} else {
		event.Severity = "MEDIUM"
	}
	// Check if there is a Details params
	if params["details"] != nil {
		eDetails, _ := params["details"].(map[string]interface{})
		eDetailsProcessed := resolveValue(eventMap, eDetails)
		event.Details, _ = eDetailsProcessed.(map[string]interface{})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	result, err := cdb.CreateEvent(ctx, &dbHandler, event)
	cancel()
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("failed to create event: %v", err)
		return rval, fmt.Errorf("failed to create event: %v", err)
	}

	rval[StrResponse] = result
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "event created successfully"

	return rval, nil
}
