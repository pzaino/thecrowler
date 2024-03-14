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

import "fmt"

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
