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
	"fmt"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// DBQueryAction performs database queries or operations
type DBQueryAction struct{}

// Name returns the name of the action
func (d *DBQueryAction) Name() string {
	return "DBQuery"
}

// Execute runs a database query or operation
func (d *DBQueryAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	// Extract dbHandler from config
	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'dbHandler' in config"
		return rval, fmt.Errorf("missing 'dbHandler' in config")
	}

	// Extract query string
	inputRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Check if there is a params field called query
	if params["query"] == nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'query' parameter"
		return rval, fmt.Errorf("missing 'query' parameter")
	}
	query, _ := params["query"].(string)
	// Check if query needs to be resolved
	query = resolveResponseString(inputRaw, query)

	// Execute the query based on type
	var result interface{}
	result, err = dbHandler.ExecuteQuery(query)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("database operation failed: %v", err)
		return rval, fmt.Errorf("database operation failed: %v", err)
	}

	// Transform the result into a JSON document where the headers are they keys
	// and the values are the values
	resultMap := cmn.ConvertMapToJSON(cmn.ConvertInfToMap(result)) // This converts result into a map[string]interface{} and then into a JSON document

	// Return the result
	rval[StrResponse] = resultMap
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "database operation successful"

	return rval, nil
}
