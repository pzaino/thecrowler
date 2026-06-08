// Copyright 2026 Paolo Fabio Zaino
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

package crawler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// normalizeJSON converts a value to JSON that is safe for PostgreSQL JSONB.
// PostgreSQL rejects JSON strings containing U+0000, even though encoding/json
// can produce them. Cleaning decoded values avoids corrupting escape sequences
// by editing an already-marshaled JSON document.
func normalizeJSON(v any) ([]byte, error) {
	prepared := prepareJSONValue(v)
	raw, err := json.Marshal(prepared)
	if err != nil {
		return nil, err
	}

	var decoded any
	if err := decodeStrictJSON(raw, &decoded); err != nil {
		return nil, fmt.Errorf("validating marshaled JSON: %w", err)
	}

	clean := prepareJSONValue(decoded)
	normalized, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	if !json.Valid(normalized) {
		return nil, fmt.Errorf("normalized document is not valid JSON")
	}
	return normalized, nil
}

func prepareJSONValue(v any) any {
	switch value := v.(type) {
	case nil:
		return nil
	case string:
		return cleanJSONString(value)
	case []byte:
		return cleanJSONString(string(value))
	case json.RawMessage:
		if decoded, ok := decodeJSONDocument(string(value)); ok {
			return prepareJSONValue(decoded)
		}
		return cleanJSONString(string(value))
	case *json.RawMessage:
		if value == nil {
			return nil
		}
		return prepareJSONValue(json.RawMessage(*value))
	case *string:
		if value == nil {
			return nil
		}
		return cleanJSONString(*value)
	case float32:
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil
		}
		return value
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		return value
	case map[string]any:
		clean := make(map[string]any, len(value))
		for key, item := range value {
			clean[cleanJSONString(key)] = prepareJSONValue(item)
		}
		return clean
	case []any:
		clean := make([]any, len(value))
		for i, item := range value {
			clean[i] = prepareJSONValue(item)
		}
		return clean
	default:
		return prepareReflectedJSONValue(reflect.ValueOf(v))
	}
}

func prepareReflectedJSONValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return prepareJSONValue(value.Elem().Interface())
	case reflect.String:
		return cleanJSONString(value.String())
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil
		}
		return number
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value.Interface()
		}
		clean := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			clean[cleanJSONString(iterator.Key().String())] = prepareJSONValue(iterator.Value().Interface())
		}
		return clean
	case reflect.Slice, reflect.Array:
		clean := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			clean[i] = prepareJSONValue(value.Index(i).Interface())
		}
		return clean
	default:
		return value.Interface()
	}
}

func cleanJSONString(value string) string {
	value = strings.ToValidUTF8(value, "")
	return strings.ReplaceAll(value, "\x00", "")
}

func decodeStrictJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// decodeJSONDocument parses valid JSON first, then attempts conservative
// repairs commonly seen in browser responses: BOM/invalid UTF-8, JavaScript
// non-values, missing values, repeated/trailing commas, literal control bytes,
// unescaped inner quotes, and missing closing brackets.
func decodeJSONDocument(input string) (any, bool) {
	cleaned := strings.TrimSpace(strings.TrimPrefix(strings.ToValidUTF8(input, ""), "\ufeff"))
	if cleaned == "" {
		return nil, false
	}

	candidates := []string{cleaned}
	if sanitized := cmn.SanitizeJSON(cleaned); sanitized != cleaned {
		candidates = append(candidates, sanitized)
	}
	if repaired := repairJSONSyntax(cleaned); repaired != cleaned {
		candidates = append(candidates, repaired)
	}

	for _, candidate := range candidates {
		var decoded any
		if err := decodeStrictJSON([]byte(candidate), &decoded); err == nil {
			return prepareJSONValue(decoded), true
		}
	}
	return nil, false
}

func repairJSONSyntax(input string) string {
	var out strings.Builder
	out.Grow(len(input) + 8)
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false

	for i := 0; i < len(input); i++ {
		c := input[i]
		if inString {
			if escaped {
				out.WriteByte(c)
				escaped = false
				continue
			}
			if c == '\\' {
				out.WriteByte(c)
				escaped = true
				continue
			}
			if c == '"' {
				next := nextNonSpaceByte(input, i+1)
				if next == 0 || strings.ContainsRune(",:}]", rune(next)) {
					inString = false
					out.WriteByte(c)
				} else {
					out.WriteString(`\"`)
				}
				continue
			}
			if c < 0x20 {
				switch c {
				case '\n':
					out.WriteString(`\n`)
				case '\r':
					out.WriteString(`\r`)
				case '\t':
					out.WriteString(`\t`)
				case '\b':
					out.WriteString(`\b`)
				case '\f':
					out.WriteString(`\f`)
				}
				continue
			}
			out.WriteByte(c)
			continue
		}

		switch c {
		case '"':
			inString = true
			out.WriteByte(c)
		case '{':
			stack = append(stack, '}')
			out.WriteByte(c)
		case '[':
			stack = append(stack, ']')
			out.WriteByte(c)
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
			out.WriteByte(c)
		case ',':
			next := nextNonSpaceByte(input, i+1)
			if next != ',' && next != '}' && next != ']' && next != 0 {
				out.WriteByte(c)
			}
		case ':':
			out.WriteByte(c)
			next := nextNonSpaceByte(input, i+1)
			if next == ',' || next == '}' || next == ']' || next == 0 {
				out.WriteString("null")
			}
		default:
			if replacement, consumed := replaceJavaScriptNonValue(input[i:]); consumed > 0 {
				out.WriteString(replacement)
				i += consumed - 1
				continue
			}
			if c != 0 && (c >= 0x20 || c == '\n' || c == '\r' || c == '\t') {
				out.WriteByte(c)
			}
		}
	}

	if inString {
		out.WriteByte('"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		out.WriteByte(stack[i])
	}
	return strings.TrimSpace(out.String())
}

func nextNonSpaceByte(input string, start int) byte {
	for i := start; i < len(input); i++ {
		switch input[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return input[i]
		}
	}
	return 0
}

func replaceJavaScriptNonValue(input string) (string, int) {
	for _, token := range []string{"undefined", "-Infinity", "Infinity", "NaN"} {
		if strings.HasPrefix(input, token) {
			next := len(token)
			if next == len(input) || isJSONValueBoundary(input[next]) {
				return "null", len(token)
			}
		}
	}
	return "", 0
}

func isJSONValueBoundary(c byte) bool {
	return c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
