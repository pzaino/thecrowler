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

// Package common package is used to store common functions and variables
package common

import (
	"encoding/json"
	"fmt"
)

// ConvertInterfaceMapToStringMap recursively converts map[interface{}]interface{} to map[string]interface{}.
func ConvertInterfaceMapToStringMap(value interface{}) interface{} {
	switch x := value.(type) {
	case map[interface{}]interface{}:
		m := map[string]interface{}{}
		for k, v := range x {
			m[fmt.Sprint(k)] = ConvertInterfaceMapToStringMap(v)
		}
		return m
	case []interface{}:
		for i, v := range x {
			x[i] = ConvertInterfaceMapToStringMap(v)
		}
	}
	return value
}

// ConvertMapInfInf recursively converts a map[interface{}]interface{} to map[string]interface{}
func ConvertMapInfInf(input map[interface{}]interface{}) map[string]interface{} {
	output := make(map[string]interface{})
	for key, value := range input {
		strKey, ok := key.(string)
		if !ok {
			panic(fmt.Sprintf("Key %v is not a string", key))
		}

		// Recursively convert nested maps
		switch value := value.(type) {
		case map[interface{}]interface{}:
			output[strKey] = ConvertMapInfInf(value)
		default:
			output[strKey] = value
		}
	}
	return output
}

// ConvertInfToMap converts an interface{} to a map[string]interface{}
func ConvertInfToMap(input interface{}) map[string]interface{} {
	output := make(map[string]interface{})
	for key, value := range input.(map[interface{}]interface{}) {
		strKey, ok := key.(string)
		if !ok {
			panic(fmt.Sprintf("Key %v is not a string", key))
		}

		// Recursively convert nested maps
		switch value := value.(type) {
		case map[interface{}]interface{}:
			output[strKey] = ConvertMapInfInf(value)
		default:
			output[strKey] = value
		}
	}
	return output
}

// ConvertMapToJSON converts a map[string]interface{} to a JSON document
func ConvertMapToJSON(input map[string]interface{}) []byte {
	output, err := json.Marshal(input)
	if err != nil {
		DebugMsg(DbgLvlError, fmt.Sprintf("Error converting map to JSON: %s", err.Error()))
	}
	return output
}

// ConvertJSONToMap converts a JSON document to a map[string]interface{}
func ConvertJSONToMap(input []byte) map[string]interface{} {
	output := make(map[string]interface{})
	err := json.Unmarshal(input, &output)
	if err != nil {
		DebugMsg(DbgLvlError, fmt.Sprintf("Error converting JSON to map: %s", err.Error()))
	}
	return output
}
