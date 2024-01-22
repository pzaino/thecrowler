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

package crawler

import (
	"reflect"
	"testing"
)

const (
	test_data   string = "test1, test2, test3"
	test_result string = "test1 test2 test3"
)

func TestIsKeyword(t *testing.T) {
	type args struct {
		keyword string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		// TODO: Add more test cases.
		{"test1", args{"a"}, false},
		{"test2", args{"about"}, false},
		{"test3", args{"above"}, false},
		{"test4", args{"across"}, false},
		{"test5", args{"after"}, false},
		{"test6", args{"afterwards"}, false},
		{"test7", args{"again"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKeyword(tt.args.keyword); got != tt.want {
				t.Errorf("isKeyword() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractFromMetaTag(t *testing.T) {
	type args struct {
		metaTags map[string]string
		tagName  string
	}
	keywords := make(map[string]string)
	keywords["keywords"] = test_data
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"test1", args{keywords, "keywords"}, []string{"test1", "test2", "test3"}},
		{"test2", args{keywords, "description"}, []string{}},
		{"test3", args{keywords, "test"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFromMetaTag(tt.args.metaTags, tt.args.tagName)
			if (len(got) != 0 && !reflect.DeepEqual(got, tt.want)) || len(got) != len(tt.want) {
				t.Errorf("extractFromMetaTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractContentKeywords(t *testing.T) {
	type args struct {
		content string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"test1", args{test_data}, []string{"test1", "test2", "test3"}},
		{"test2", args{test_data}, []string{"test1", "test2", "test3"}},
		{"test3", args{test_data}, []string{"test1", "test2", "test3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractContentKeywords(tt.args.content); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractContentKeywords() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnique(t *testing.T) {
	type args struct {
		strSlice []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"test1", args{[]string{"test1", "test2", "test3"}}, []string{"test1", "test2", "test3"}},
		{"test2", args{[]string{"test1", "test2", "test3"}}, []string{"test1", "test2", "test3"}},
		{"test3", args{[]string{"test1", "test2", "test3"}}, []string{"test1", "test2", "test3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unique(tt.args.strSlice); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("unique() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractKeywords(t *testing.T) {
	type args struct {
		pageInfo PageInfo
	}
	keywords := make(map[string]string)
	keywords["keywords"] = test_data
	pageInfo := PageInfo{
		MetaTags: keywords,
	}

	tests := []struct {
		name string
		args args
		want []string
	}{
		{"test1", args{pageInfo}, []string{"test1", "test2", "test3"}},
		{"test2", args{pageInfo}, []string{"test1", "test2", "test3"}},
		{"test3", args{pageInfo}, []string{"test1", "test2", "test3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractKeywords(tt.args.pageInfo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractKeywords() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeText(t *testing.T) {
	type args struct {
		text string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		// Add test cases:
		{"test1", args{test_data}, test_result},
		{"test2", args{test_data}, test_result},
		{"test3", args{test_data}, test_result},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeText(tt.args.text); got != tt.want {
				t.Errorf("normalizeText() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemovePunctuation(t *testing.T) {
	type args struct {
		text string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		// Add test cases:
		{"test1", args{test_data}, test_result},
		{"test2", args{"test1. test2. test3."}, test_result},
		{"test3", args{"test1; test2; test3;"}, test_result},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := removePunctuation(tt.args.text); got != tt.want {
				t.Errorf("removePunctuation() = %v, want %v", got, tt.want)
			}
		})
	}
}
