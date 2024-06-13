package main

import (
	"fmt"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input  string
		output []string
	}{
		{
			input:  `field1:value1 field2:value2`,
			output: []string{"field1:", "value1", "field2:", "value2"},
		},
		{
			input:  `field1:\"value 1\" field2:\"value 2\"`,
			output: []string{"field1:", "\"value", "1\"", "field2:", "\"value", "2\""},
		},
		{
			input:  `field1:value1 | field2:value2 & field3:value3`,
			output: []string{"field1:", "value1", "|", "field2:", "value2", "&", "field3:", "value3"},
		},
		{
			input:  `field1:value1 | field2:"value 2" & field3:value3`,
			output: []string{"field1:", "value1", "|", "field2:", "value 2", "&", "field3:", "value3"},
		},
	}

	for _, test := range tests {
		result := tokenize(test.input)
		if !reflect.DeepEqual(result, test.output) {
			t.Errorf("tokenize(%q) = got %v; want %v", test.input, result, test.output)
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
				combinedQuery: "SELECT * FROM table WHERE (LOWER(title) LIKE $1) AND (LOWER(summary) LIKE $2) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2))",
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
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) AND (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) AND (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4) AND (LOWER(page_url) LIKE $5 OR LOWER(title) LIKE $5 OR LOWER(summary) LIKE $5) AND (LOWER(page_url) LIKE $6 OR LOWER(title) LIKE $6 OR LOWER(summary) LIKE $6)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4 OR k.keyword LIKE $5 OR k.keyword LIKE $6))",
				queryParams:   []interface{}{"%field1:%", "%\"value%", "%1\"%", "%field2:%", "%\"value%", "%2\"%", 10, 0},
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
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) AND OR (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) AND (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4) AND OR (LOWER(page_url) LIKE $5 OR LOWER(title) LIKE $5 OR LOWER(summary) LIKE $5) AND (LOWER(page_url) LIKE $6 OR LOWER(title) LIKE $6 OR LOWER(summary) LIKE $6)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4 OR k.keyword LIKE $5 OR k.keyword LIKE $6))",
				queryParams:   []interface{}{"%field1:%", "%value1%", "%field2:%", "%value2%", "%field3:%", "%value3%", 10, 0},
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
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2) AND OR (LOWER(page_url) LIKE $3 OR LOWER(title) LIKE $3 OR LOWER(summary) LIKE $3) AND (LOWER(page_url) LIKE $4 OR LOWER(title) LIKE $4 OR LOWER(summary) LIKE $4) AND OR (LOWER(page_url) LIKE $5 OR LOWER(title) LIKE $5 OR LOWER(summary) LIKE $5) AND (LOWER(page_url) LIKE $6 OR LOWER(title) LIKE $6 OR LOWER(summary) LIKE $6)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2 OR k.keyword LIKE $3 OR k.keyword LIKE $4 OR k.keyword LIKE $5 OR k.keyword LIKE $6))",
				queryParams:   []interface{}{"%field1:%", "%value1%", "%field2:%", "%value 2%", "%field3:%", "%value3%", 10, 0},
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
				combinedQuery: "SELECT * FROM table WHERE ((LOWER(page_url) LIKE $1 OR LOWER(title) LIKE $1 OR LOWER(summary) LIKE $1) AND (LOWER(page_url) LIKE $2 OR LOWER(title) LIKE $2 OR LOWER(summary) LIKE $2)) OR ((k.keyword LIKE $1 OR k.keyword LIKE $2))",
				queryParams:   []interface{}{"%invalid:%", "%value%", 10, 0},
				err:           nil,
			},
		},
	}

	for _, test := range tests {
		SQLQuery, err := parseAdvancedQuery(test.queryBody, test.input, "")
		combinedQuery := SQLQuery.sqlQuery
		queryParams := SQLQuery.sqlParams
		if combinedQuery != test.expected.combinedQuery {
			t.Errorf("parseAdvancedQuery(%q, %q) combinedQuery = %q;\n want %q", test.queryBody, test.input, combinedQuery, test.expected.combinedQuery)
		}

		if !reflect.DeepEqual(queryParams, test.expected.queryParams) {
			t.Errorf("parseAdvancedQuery(%q, %q) queryParams = %v;\n want %v", test.queryBody, test.input, queryParams, test.expected.queryParams)
		}

		if fmt.Sprint(err) != fmt.Sprint(test.expected.err) {
			t.Errorf("parseAdvancedQuery(%q, %q) err = %v;\n want %v", test.queryBody, test.input, err, test.expected.err)
		}
	}
}
