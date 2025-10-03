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
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Helped functions to deal with different date formats in JSON

// FlexibleDate is a type that can be used to parse dates in different formats
type FlexibleDate time.Time

var (
	dateFormats = []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z07:00",
	}
)

// UnmarshalJSON parses a date from a JSON string
func (fd *FlexibleDate) UnmarshalJSON(b []byte) error {
	str := string(b)
	// Trim the quotes
	str = str[1 : len(str)-1]
	var parsedTime time.Time
	var err error
	for _, format := range dateFormats {
		parsedTime, err = time.Parse(format, str)
		if err == nil {
			*fd = FlexibleDate(parsedTime)
			return nil
		}
	}
	return fmt.Errorf("could not parse date: %s", str)
}

// MarshalJSON returns a date as a JSON string
func (fd FlexibleDate) MarshalJSON() ([]byte, error) {
	formatted := fmt.Sprintf("\"%s\"", time.Time(fd).Format(dateFormats[0]))
	return []byte(formatted), nil
}

// String returns a date as a string
func (fd FlexibleDate) String() string {
	return time.Time(fd).Format(dateFormats[0])
}

// Time returns a date as a time.Time
func (fd FlexibleDate) Time() time.Time {
	return time.Time(fd)
}

// IsJSON checks if a string is a JSON
func IsJSON(s string) bool {
	var js map[string]interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// JSONStrToMap converts a JSON string to a map
func JSONStrToMap(s string) (map[string]interface{}, error) {
	var js map[string]interface{}
	err := json.Unmarshal([]byte(s), &js)
	return js, err
}

// MapToJSONStr converts a map to a JSON string
func MapToJSONStr(m map[string]interface{}) (string, error) {
	js, err := json.Marshal(m)
	return string(js), err
}

// MapStrToJSONStr converts a map[string]string to a JSON string
func MapStrToJSONStr(m map[string]string) (string, error) {
	js, err := json.Marshal(m)
	return string(js), err
}

// SafeEscapeJSONString escapes a string for JSON and for embedding in quoted contexts
func SafeEscapeJSONString(s any) string {
	ss := strings.ReplaceAll(fmt.Sprintf("%v", s), "\\", "\\\\")
	ss = strings.ReplaceAll(ss, "\"", "\\\"")
	ss = strings.ReplaceAll(ss, "'", "\\'")
	return ss
}

// SanitizeJSON sanitizes JSON documents, this is the Public entry point
// SanitizeJSON sanitizes JSON documents, this is the Public entry point.
// It always returns strict JSON with proper `null` values.
func SanitizeJSON(input string) string {
	p := &parser{s: input}
	v, err := p.parseValue()
	if err != nil {
		// Best effort: if we built something partially, try to marshal it.
		if v != nil {
			if out, mErr := json.Marshal(v); mErr == nil {
				return string(out)
			}
		}
		// If parsing completely failed, return original input
		return input
	}

	// Strict JSON re-encoding ensures <nil> → null
	out, err := json.Marshal(v)
	if err != nil {
		return input
	}
	return string(out)
}

// Internal parser implementation

type parser struct {
	s string
	i int
}

func (p *parser) eof() bool { return p.i >= len(p.s) }
func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}
func (p *parser) next() byte {
	if p.eof() {
		return 0
	}
	b := p.s[p.i]
	p.i++
	return b
}
func (p *parser) skipWS() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\r', '\n':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) parseValue() (interface{}, error) {
	p.skipWS()
	if p.eof() {
		return nil, errors.New("unexpected end of input")
	}
	switch c := p.peek(); c {
	case '{':
		return p.parseObject()
	case '[':
		return p.parseArray()
	case '"', '\'':
		return p.parseString()
	case 't', 'f', 'n':
		return p.parseLiteral()
	default:
		// Could be number or unquoted key used as bare string in arrays or values
		if c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9') {
			return p.parseNumber()
		}
		// Fallback: treat as bareword string until a structural char
		return p.parseBarewordAsString()
	}
}

func (p *parser) parseObject() (map[string]interface{}, error) {
	obj := make(map[string]interface{})
	if p.next() != '{' {
		return nil, errors.New("expected {")
	}
	p.skipWS()

	// Allow empty object
	if !p.eof() && p.peek() == '}' {
		p.i++
		return obj, nil
	}

	for {
		p.skipWS()
		if p.eof() {
			return obj, errors.New("unterminated object")
		}

		// Key can be quoted or identifier
		key, err := p.parseKey()
		if err != nil {
			// Try to recover by skipping spurious commas
			if p.peek() == ',' {
				for !p.eof() && p.peek() == ',' {
					p.i++
					p.skipWS()
				}
				continue
			}
			return obj, err
		}

		p.skipWS()
		if p.eof() {
			return obj, errors.New("expected : after key")
		}
		if p.peek() != ':' {
			// If a comma appears, assume missing value null and continue
			if p.peek() == ',' {
				obj[key] = nil
				for !p.eof() && p.peek() == ',' {
					p.i++
				}
				continue
			}
			// Try to be lenient: insert missing colon if obvious
			// but if not, abort key
			// As a last resort, set null and try to continue
			obj[key] = nil
		} else {
			p.i++ // skip :
		}

		val, err := p.parseValue()
		if err != nil {
			// Be lenient: set null if value cannot be parsed here
			obj[key] = nil
		} else {
			obj[key] = val
		}

		p.skipWS()
		// Accept 0 or more commas, ignore trailing commas
		for !p.eof() && p.peek() == ',' {
			p.i++
			p.skipWS()
		}
		if !p.eof() && p.peek() == '}' {
			p.i++
			return obj, nil
		}
		// If not closing brace, loop to read next pair
		if p.eof() {
			return obj, errors.New("unterminated object")
		}
	}
}

func (p *parser) parseArray() ([]interface{}, error) {
	if p.next() != '[' {
		return nil, errors.New("expected [")
	}
	arr := make([]interface{}, 0, 8)
	p.skipWS()

	// Allow empty array
	if !p.eof() && p.peek() == ']' {
		p.i++
		return arr, nil
	}

	for {
		p.skipWS()
		if p.eof() {
			return arr, errors.New("unterminated array")
		}
		// Parse value
		val, err := p.parseValue()
		if err == nil {
			arr = append(arr, val)
		} else {
			// If cannot parse value, attempt to skip until next delimiter
			// and append null to keep shape
			arr = append(arr, nil)
			p.syncToNextDelimiter()
		}

		p.skipWS()
		// Accept 0 or more commas, ignore trailing commas
		for !p.eof() && p.peek() == ',' {
			p.i++
			p.skipWS()
		}
		if !p.eof() && p.peek() == ']' {
			p.i++
			return arr, nil
		}
		if p.eof() {
			return arr, errors.New("unterminated array")
		}
	}
}

func (p *parser) parseKey() (string, error) {
	p.skipWS()
	if p.eof() {
		return "", errors.New("unexpected end while reading key")
	}
	if p.peek() == '"' || p.peek() == '\'' {
		s, err := p.parseString()
		if err != nil {
			return "", err
		}
		return s.(string), nil
	}
	// Unquoted identifier: letters, digits, underscore
	start := p.i
	for !p.eof() {
		c := p.peek()
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ':' || c == ',' || c == '}' {
			break
		}
		if c == '"' || c == '\'' || c == '{' || c == '[' || c == ']' {
			break
		}
		p.i++
	}
	if p.i == start {
		return "", errors.New("invalid key")
	}
	raw := p.s[start:p.i]
	// Trim whitespace at ends
	return trimSpaces(raw), nil
}

func (p *parser) parseString() (interface{}, error) {
	quote := p.next() // opening quote, ' or "
	var buf bytes.Buffer

	// We will repair unescaped inner double quotes when quote is "
	// Heuristic: if a " is seen and surrounding chars are alnum, escape it
	for !p.eof() {
		c := p.next()
		if c == quote {
			return buf.String(), nil
		}
		if c == '\\' {
			// Handle escapes
			if p.eof() {
				// Lone backslash, keep it
				buf.WriteByte('\\')
				break
			}
			esc := p.next()
			switch esc {
			case '"', '\\', '/', '\'':
				// Keep as the escaped char, but JSON uses " to delimit strings.
				// We store Go string value, so just write the escaped char.
				buf.WriteByte(esc)
			case 'b':
				buf.WriteByte('\b')
			case 'f':
				buf.WriteByte('\f')
			case 'n':
				buf.WriteByte('\n')
			case 'r':
				buf.WriteByte('\r')
			case 't':
				buf.WriteByte('\t')
			case 'u':
				// \uXXXX possibly surrogate pair
				r, ok := p.readHexRune()
				if !ok {
					// Invalid, keep literally "u"
					buf.WriteByte('u')
				} else {
					buf.WriteRune(r)
				}
			default:
				// Unknown escape, keep as literal char
				buf.WriteByte(esc)
			}
			continue
		}
		// Repair unescaped inner quote only for double quoted strings
		if quote == '"' && c == '"' {
			// check neighbors in original text
			prev := p.prevByte()
			next := p.peek()
			if isAlphaNum(prev) && isAlphaNum(next) {
				buf.WriteByte('"') // we will store the character itself
				continue
			}
			// Otherwise it was a terminator that we already handled above,
			// but since we are here it must be inner content. Keep it.
			buf.WriteByte('"')
			continue
		}
		// Regular byte, copy
		buf.WriteByte(c)
	}
	// Unterminated string. Return what we have.
	return buf.String(), errors.New("unterminated string")
}

// readHexRune reads 4 hex digits, and if it is a high surrogate followed by \u+low surrogate, combines them.
func (p *parser) readHexRune() (rune, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	code, ok := parseHex4(p.s[p.i : p.i+4])
	if !ok {
		return 0, false
	}
	p.i += 4
	// If high surrogate, handle pair
	if 0xD800 <= code && code <= 0xDBFF {
		// expect \uXXXX
		if p.i+6 <= len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
			p.i += 2
			if low, ok2 := parseHex4(p.s[p.i : p.i+4]); ok2 && 0xDC00 <= low && low <= 0xDFFF {
				p.i += 4
				r := utf16.DecodeRune(rune(code), rune(low))
				return r, true
			}
			// not a low surrogate, backtrack the 2 we consumed
			p.i -= 2
		}
	}
	return rune(code), true
}

func parseHex4(s string) (uint16, bool) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 2 {
		// Fallback manual
		var v uint16
		for i := 0; i < 4; i++ {
			x := fromHex(s[i])
			if x < 0 {
				return 0, false
			}
			v = v<<4 | uint16(x)
		}
		return v, true
	}
	return uint16(b[0])<<8 | uint16(b[1]), true
}

func fromHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

func (p *parser) prevByte() byte {
	if p.i <= 1 {
		return 0
	}
	// previous consumed byte is at p.i-1
	return p.s[p.i-2]
}

func isAlphaNum(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9')
}

func (p *parser) parseLiteral() (interface{}, error) {
	// Accept true, false, null
	if stringsHasPrefixFold(p.s[p.i:], "true") {
		p.i += 4
		return true, nil
	}
	if stringsHasPrefixFold(p.s[p.i:], "false") {
		p.i += 5
		return false, nil
	}
	if stringsHasPrefixFold(p.s[p.i:], "null") {
		p.i += 4
		return nil, nil
	}
	// Unknown bareword: read as string until delimiter
	return p.parseBarewordAsString()
}

func (p *parser) parseNumber() (interface{}, error) {
	start := p.i
	// sign
	if p.peek() == '+' || p.peek() == '-' {
		p.i++
	}
	dot := false
	exp := false
	digits := 0

	for !p.eof() {
		c := p.peek()
		if c >= '0' && c <= '9' {
			digits++
			p.i++
			continue
		}
		if c == '.' && !dot && !exp {
			dot = true
			p.i++
			continue
		}
		if (c == 'e' || c == 'E') && !exp {
			exp = true
			p.i++
			// optional sign
			if !p.eof() && (p.peek() == '+' || p.peek() == '-') {
				p.i++
			}
			continue
		}
		break
	}
	if p.i == start || digits == 0 {
		// Not a number, treat as bareword string
		p.i = start
		return p.parseBarewordAsString()
	}
	numStr := p.s[start:p.i]
	// Try int first
	if !dot && !exp {
		if iv, err := strconv.ParseInt(numStr, 10, 64); err == nil {
			return iv, nil
		}
		if uv, err := strconv.ParseUint(numStr, 10, 64); err == nil {
			return uv, nil
		}
	}
	// Fallback float
	if fv, err := strconv.ParseFloat(numStr, 64); err == nil {
		return fv, nil
	}
	// If parsing fails, keep as string
	return numStr, nil
}

func (p *parser) parseBarewordAsString() (interface{}, error) {
	start := p.i
	for !p.eof() {
		c := p.peek()
		// stop at structural chars or whitespace
		if c == ',' || c == ']' || c == '}' || c == ':' || c == '{' || c == '[' {
			break
		}
		if c == '"' || c == '\'' {
			break
		}
		if unicode.IsSpace(rune(c)) {
			break
		}
		p.i++
	}
	raw := trimSpaces(p.s[start:p.i])
	if raw == "" {
		return "", errors.New("empty bareword")
	}
	// If it was a quoted single string mistakenly started without opening quote,
	// we just return the raw text. This is a last resort.
	return raw, nil
}

func (p *parser) syncToNextDelimiter() {
	for !p.eof() {
		switch p.peek() {
		case ',', ']', '}':
			return
		default:
			p.i++
		}
	}
}

func stringsHasPrefixFold(s string, pref string) bool {
	if len(s) < len(pref) {
		return false
	}
	for i := 0; i < len(pref); i++ {
		a := s[i]
		b := pref[i]
		if a == b {
			continue
		}
		ra, _ := utf8.DecodeRuneInString(string(a))
		rb, _ := utf8.DecodeRuneInString(string(b))
		if unicode.ToLower(ra) != unicode.ToLower(rb) {
			return false
		}
	}
	return true
}

func trimSpaces(s string) string {
	// simple ASCII trimming
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
