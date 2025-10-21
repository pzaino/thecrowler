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

// ConvertInfToMap converts interface{} values to map[string]interface{},
// handling both map[string]interface{} and map[interface{}]interface{} cases recursively.
func ConvertInfToMap(input interface{}) map[string]interface{} {
	output := make(map[string]interface{})

	switch val := input.(type) {
	case map[string]interface{}:
		for k, v := range val {
			switch inner := v.(type) {
			case map[string]interface{}, map[interface{}]interface{}:
				output[k] = ConvertInfToMap(inner)
			default:
				output[k] = inner
			}
		}

	case map[interface{}]interface{}:
		for k, v := range val {
			strKey, ok := k.(string)
			if !ok {
				strKey = fmt.Sprintf("%v", k)
			}
			switch inner := v.(type) {
			case map[string]interface{}, map[interface{}]interface{}:
				output[strKey] = ConvertInfToMap(inner)
			default:
				output[strKey] = inner
			}
		}

	default:
		// not a map at all, wrap it so you can inspect it later
		output["_value"] = val
	}

	return output
}

// ConvertInfToMapInf converts an interface{} to a map[string]interface{} and
// recursively converts nested maps as an interface{}
func ConvertInfToMapInf(input interface{}) interface{} {
	switch data := input.(type) {
	case map[interface{}]interface{}:
		output := make(map[string]interface{})
		for key, value := range data {
			strKey, ok := key.(string)
			if !ok {
				panic(fmt.Sprintf("Key %v is not a string", key))
			}
			output[strKey] = ConvertInfToMap(value) // Recursively convert
		}
		return output
	case []interface{}: // Handle arrays of maps
		newSlice := make([]interface{}, len(data))
		for i, v := range data {
			newSlice[i] = ConvertInfToMap(v) // Recursively convert elements
		}
		return newSlice
	default:
		return input // Return as is if not a map or slice
	}
}

// ConvertInfMapToStrMap converts a map[interface{}]interface{} to a map[string]interface{}
func ConvertInfMapToStrMap(input map[interface{}]interface{}) map[string]interface{} {
	output := make(map[string]interface{})
	for key, value := range input {
		strKey, ok := key.(string)
		if !ok {
			continue // Skip keys that are not strings
		}
		output[strKey] = value
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

// ConvertMapToString converts a map[string]interface{} to a string
func ConvertMapToString(input map[string]interface{}) string {
	return string(ConvertMapToJSON(input))
}

// ConvertStringToMap converts a string to a map[string]interface{}
func ConvertStringToMap(input string) map[string]interface{} {
	return ConvertJSONToMap([]byte(input))
}

// ConvertMap converts a map[interface{}]interface{} to a map[string]interface{}
func ConvertMapIIToSI(m interface{}) interface{} {
	switch v := m.(type) {
	case map[interface{}]interface{}:
		converted := make(map[string]interface{})
		for key, value := range v {
			strKey := fmt.Sprintf("%v", key) // Convert key to string
			converted[strKey] = ConvertMapIIToSI(value)
		}
		return converted
	case []interface{}:
		for i, value := range v {
			v[i] = ConvertMapIIToSI(value)
		}
	}
	return m
}

// ConvertSliceInfToString converts a slice of interfaces into a string
func ConvertSliceInfToString(input []interface{}) string {
	strSlice := make([]string, len(input))
	for i, v := range input {
		strSlice[i] = fmt.Sprintf("%v", v) // Convert each element to string
	}
	// Join strSlice into a single JSON string
	strBuffer, err := json.Marshal(strSlice)
	if err != nil {
		DebugMsg(DbgLvlError, fmt.Sprintf("Error marshaling slice to JSON: %s", err.Error()))
		return "[]"
	}
	return fmt.Sprintf("[%s]", strBuffer)
}
