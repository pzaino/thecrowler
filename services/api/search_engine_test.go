package main

import (
	"fmt"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input  string
		output tokens
	}{
		{
			input:  `field1:value1 field2:value2`,
			output: tokens{{tValue: "field1:value1", tType: "0"}, {tValue: "field2:value2", tType: "0"}},
		},
		{
			input:  `field1:\"value 1\" field2:\"value 2\"`,
			output: tokens{{tValue: "field1:\"value", tType: "0"}, {tValue: "1\"", tType: "0"}, {tValue: "field2:\"value", tType: "0"}, {tValue: "2\"", tType: "0"}},
		},
		{
			input:  `field1:value1 | field2:value2 & field3:value3`,
			output: tokens{{tValue: "field1:value1", tType: "0"}, {tValue: "|", tType: "0"}, {tValue: "field2:value2", tType: "0"}, {tValue: "&", tType: "0"}, {tValue: "field3:value3", tType: "0"}},
		},
		{
			input:  `field1:value1 | field2:"value 2" & field3:value3`,
			output: tokens{{tValue: "field1:value1", tType: "0"}, {tValue: "|", tType: "0"}, {tValue: "field2:value 2", tType: "0"}, {tValue: "&", tType: "0"}, {tValue: "field3:value3", tType: "0"}},
		},
		{
			input:  `title:value1 | title:"value 2" & title:value3`,
			output: tokens{{tValue: "title:", tType: "2"}, {tValue: "value1", tType: "0"}, {tValue: "|", tType: "0"}, {tValue: "title:", tType: "2"}, {tValue: "value 2", tType: "0"}, {tValue: "&", tType: "0"}, {tValue: "title:", tType: "2"}, {tValue: "value3", tType: "0"}},
		},
	}

	for i, test := range tests {
		result := tokenize(test.input)
		if len(result) != len(test.output) {
			t.Errorf("%d: tokenize(%q) = got length %d; want length %d\n%v", i, test.input, len(result), len(test.output), result)
			continue
		}
		for i := range result {
			got, want := result[i], test.output[i]
			if got.tType != want.tType || got.tValue != want.tValue {
				t.Errorf("tokenize(%q) = got [%d] = {tType: %q, tValue: %q}; want [%d] = {tType: %q, tValue: %q}",
					test.input, i, got.tType, got.tValue, i, want.tType, want.tValue)
			}
		}
	}
}

func TestIsFieldSpecifier(t *testing.T) {
	tests := []struct {
		input  string
		output bool
	}{
		{
			input:  "title:value",
			output: true,
		},
		{
			input:  "summary:value",
			output: true,
		},
		{
			input:  "content:value",
			output: true,
		},
		{
			input:  "invalid:value",
			output: false,
		},
		{
			input:  "title",
			output: false,
		},
		{
			input:  "summary",
			output: false,
		},
		{
			input:  "content",
			output: false,
		},
		{
			input:  "invalid",
			output: false,
		},
	}

	for _, test := range tests {
		result := isFieldSpecifier(test.input)
		if result != test.output {
			t.Errorf("isFieldSpecifier(%q) = got %v; want %v", test.input, result, test.output)
		}
	}
}

/*
func TestIsQuotedString(t *testing.T) {
	tests := []struct {
		input  string
		output bool
	}{
		{
			input:  `"value"`,
			output: true,
		},
		{
			input:  `"value 1"`,
			output: true,
		},
		{
			input:  `"value 2"`,
			output: true,
		},
		{
			input:  `"value`,
			output: false,
		},
		{
			input:  `value"`,
			output: false,
		},
		{
			input:  `value`,
			output: false,
		},
	}

	for _, test := range tests {
		result := isQuotedString(test.input)
		if result != test.output {
			t.Errorf("isQuotedString(%q) = got %v; want %v", test.input, result, test.output)
		}
	}
}
*/

func TestGetDefaultFields(t *testing.T) {
	config.API.ContentSearch = true
	expectedFields := []string{"page_url", "title", "summary", "content"}
	result := getDefaultFields()
	if !reflect.DeepEqual(result, expectedFields) {
		t.Errorf("getDefaultFields() = got %v; want %v", result, expectedFields)
	}

	config.API.ContentSearch = false
	expectedFields = []string{"page_url", "title", "summary"}
	result = getDefaultFields()
	if !reflect.DeepEqual(result, expectedFields) {
		t.Errorf("getDefaultFields() = got %v; want %v", result, expectedFields)
	}
}

func TestParseAdvancedQuery(t *testing.T) {
	//defaultFields := []string{"page_url", "title", "summary"}

	tests := []struct {
		queryBody string
		input     string
		expected  struct {
			combinedQuery string
			queryParams   []interface{}
			err           error
		}
	}{
		{
			queryBody: "SELECT * FROM table WHERE ",
			input:     `title:value1 summary:value2`,
			expected: struct {
				combinedQuery string
				queryParams   []interface{}
				err           error
			}{
				//combinedQuery: "SELECT * FROM table WHERE (LOWER(title) LIKE $1) AND (LOWER(summary) LIKE $2) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2))",
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1)) AND ((LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2))",
				queryParams:   []interface{}{"%value1%", "%value2%", 10, 0},
				err:           nil,
			},
		},
		{
			queryBody: "SELECT * FROM table WHERE ",
			input:     `field1:\"value 1\" field2:\"value 2\"`,
			expected: struct {
				combinedQuery string
				queryParams   []interface{}
				err           error
			}{
				//combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) AND (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) AND (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4) AND (LOWER(page_url) LIKE $5 OR LOWER(title) LIKE $5 OR LOWER(summary) LIKE $5) AND (LOWER(page_url) LIKE $6 OR LOWER(title) LIKE $6 OR LOWER(summary) LIKE $6)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4 OR k.keyword LIKE $5 OR k.keyword LIKE $6))",
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4))",
				queryParams:   []interface{}{"%field1:\"value%", "%1\"%", "%field2:\"value%", "%2\"%", 10, 0},
				err:           nil,
			},
		},
		{
			queryBody: "SELECT * FROM table WHERE ",
			input:     `field1:value1 | field2:value2 & field3:value3`,
			expected: struct {
				combinedQuery string
				queryParams   []interface{}
				err           error
			}{
				//combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) AND OR (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) AND (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4) AND OR (LOWER(page_url) LIKE $5 OR LOWER(title) LIKE $5 OR LOWER(summary) LIKE $5) AND (LOWER(page_url) LIKE $6 OR LOWER(title) LIKE $6 OR LOWER(summary) LIKE $6)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4 OR k.keyword LIKE $5 OR k.keyword LIKE $6))",
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) OR (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) OR (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3))",
				queryParams:   []interface{}{"%field1:value1%", "%field2:value2%", "%field3:value3%", 10, 0},
				err:           nil,
			},
		},
		{
			queryBody: "SELECT * FROM table WHERE ",
			input:     `field1:value1 | field2:"value 2" & field3:value3`,
			expected: struct {
				combinedQuery string
				queryParams   []interface{}
				err           error
			}{
				//combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) AND OR (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) AND (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4) AND OR (LOWER(page_url) LIKE $5 OR LOWER(title) LIKE $5 OR LOWER(summary) LIKE $5) AND (LOWER(page_url) LIKE $6 OR LOWER(title) LIKE $6 OR LOWER(summary) LIKE $6)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4 OR k.keyword LIKE $5 OR k.keyword LIKE $6))",
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) OR (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) OR (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3))",
				queryParams:   []interface{}{"%field1:value1%", "%field2:value 2%", "%field3:value3%", 10, 0},
				err:           nil,
			},
		},
		{
			queryBody: "SELECT * FROM table WHERE ",
			input:     `invalid:value`,
			expected: struct {
				combinedQuery string
				queryParams   []interface{}
				err           error
			}{
				//combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2))",
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1)) OR ((k.keyword LIKE $1))",
				queryParams:   []interface{}{"%invalid:value%", 10, 0},
				err:           nil,
			},
		},
	}

	for i, test := range tests {
		SQLQuery, err := parseAdvancedQuery(test.queryBody, test.input, "")
		combinedQuery := SQLQuery.sqlQuery
		queryParams := SQLQuery.sqlParams
		if combinedQuery != test.expected.combinedQuery {
			t.Errorf("%d: parseAdvancedQuery(%q, %q)\n got combinedQuery = %q;\n want %q", i, test.queryBody, test.input, combinedQuery, test.expected.combinedQuery)
		}

		if !reflect.DeepEqual(queryParams, test.expected.queryParams) {
			t.Errorf("%d: parseAdvancedQuery(%q, %q)\n got queryParams = %v;\n want %v", i, test.queryBody, test.input, queryParams, test.expected.queryParams)
		}

		if fmt.Sprint(err) != fmt.Sprint(test.expected.err) {
			t.Errorf("%d: parseAdvancedQuery(%q, %q)\n got err = %v;\n want %v", i, test.queryBody, test.input, err, test.expected.err)
		}
	}
}
