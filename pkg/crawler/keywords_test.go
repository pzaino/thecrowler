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
	testData   string = "test1, test2, test3"
	testResult string = "test1 test2 test3"
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
		{"test1", args{"a"}, false},
		{"test2", args{"about"}, false},
		{"test3", args{"above"}, false},
		{"test4", args{"across"}, false},
		{"test5", args{"after"}, false},
		{"test6", args{"afterwards"}, false},
		{"test7", args{"again"}, false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKeyword(tt.args.keyword, ""); got != tt.want {
				t.Errorf("isKeyword() for test word %d = %v, want %v", i, got, tt.want)
			}
		})
	}
}

func TestIsKeyword2(t *testing.T) {
	type args struct {
		keyword string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"test1", args{"against"}, false},
		{"test2", args{"all"}, false},
		{"test3", args{"almost"}, false},
		{"test4", args{"alone"}, false},
		{"test5", args{"along"}, false},
		{"test6", args{"already"}, false},
		{"test7", args{"also"}, false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKeyword(tt.args.keyword, ""); got != tt.want {
				t.Errorf("isKeyword() for test word %d = %v, want %v", i, got, tt.want)
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
		{"test1", args{testData}, []string{"test1", "test2", "test3"}},
		{"test2", args{testData}, []string{"test1", "test2", "test3"}},
		{"test3", args{testData}, []string{"test1", "test2", "test3"}},
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

func TestNormalizeKeyword(t *testing.T) {
	type args struct {
		keyword string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"test1", args{"test1"}, "test1"},
		{"test2", args{"test2"}, "test2"},
		{"test3", args{"test3"}, "test3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeKeyword(tt.args.keyword); got != tt.want {
				t.Errorf("normalizeKeyword() = %v, want %v", got, tt.want)
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
		{
			name: "test1",
			args: args{text: "test1, test2, test3"},
			want: "test1 test2 test3", // Commas replaced with spaces
		},
		{
			name: "test2",
			args: args{text: "hello\nworld\ttest"},
			want: "hello world test", // Newlines and tabs replaced with spaces
		},
		{
			name: "test3",
			args: args{text: "normalize-this! Now."},
			want: "normalize this now", // Punctuation removed, spaces added
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeText(tt.args.text); got != tt.want {
				t.Errorf("normalizeText() = %q, want %q", got, tt.want)
			}
		})
	}
}

/*
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
		{"test1", args{testData}, testResult},
		{"test2", args{"test1. test2. test3."}, testResult},
		{"test3", args{"test1; test2; test3;"}, testResult},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := removePunctuation(tt.args.text); got != tt.want {
				t.Errorf("removePunctuation() = %v, want %v", got, tt.want)
			}
		})
	}
}
*/

func TestExtractKeywords(t *testing.T) {
	type args struct {
		pageInfo PageInfo
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		// Add test cases:
		{"test1", args{PageInfo{BodyText: testData, MetaTags: []MetaTag{}}}, []string{"test1", "test2", "test3"}},
		{"test2", args{PageInfo{BodyText: testData, MetaTags: []MetaTag{}}}, []string{"test1", "test2", "test3"}},
		{"test3", args{PageInfo{BodyText: testData, MetaTags: []MetaTag{}}}, []string{"test1", "test2", "test3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractKeywords(tt.args.pageInfo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractKeywords() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractFromMetaTag(t *testing.T) {
	type args struct {
		metaTags []MetaTag
		tagName  string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "test1",
			args: args{
				metaTags: []MetaTag{
					{Name: "keywords", Content: "test1, test2, test3"},
				},
				tagName: "keywords",
			},
			want: []string{"test1", "test2", "test3"},
		},
		{
			name: "test2",
			args: args{
				metaTags: []MetaTag{
					{Name: "description", Content: "test4 test5 test6"},
				},
				tagName: "description",
			},
			want: []string{"test4", "test5", "test6"},
		},
		{
			name: "test3",
			args: args{
				metaTags: []MetaTag{
					{Name: "keywords", Content: "example1; example2,example3"},
				},
				tagName: "keywords",
			},
			want: []string{"example1", "example2", "example3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFromMetaTag(tt.args.metaTags, tt.args.tagName)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractFromMetaTag() = %v, want %v", got, tt.want)
			}
		})
	}
}
