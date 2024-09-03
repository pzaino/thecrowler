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
	testFQDN string = "https://www.google.com"
)

func TestExtractLinks(t *testing.T) {
	testArgs := Pars{
		WG:     nil,
		RE:     nil,
		Status: nil,
	}
	ctx := NewProcessContext(testArgs)
	type args struct {
		htmlContent string
	}
	test1 := []LinkItem{{Link: testFQDN}}
	test2 := []LinkItem{{Link: testFQDN}, {Link: testFQDN}}
	test3 := []LinkItem{{Link: testFQDN}, {Link: testFQDN}, {Link: testFQDN}}
	tests := []struct {
		name string
		args args
		want []LinkItem
	}{
		{"test1", args{"<html><head><title>Test</title></head><body><a href=\"https://www.google.com\">Google</a></body></html>"}, test1},
		{"test2", args{"<html><head><title>Test</title></head><body><a href=\"https://www.google.com\">Google</a><a href=\"https://www.google.com\">Google</a></body></html>"}, test2},
		{"test3", args{"<html><head><title>Test</title></head><body><a href=\"https://www.google.com\">Google</a><a href=\"https://www.google.com\">Google</a><a href=\"https://www.google.com\">Google</a></body></html>"}, test3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLinks(ctx, tt.args.htmlContent, ""); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractLinks() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsExternalLink(t *testing.T) {
	type args struct {
		sourceURL string
		linkURL   string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		// Add test cases
		{"test1", args{testFQDN, testFQDN}, false},
		{"test2", args{testFQDN, "https://www.google.com/test2"}, false},
		{"test3", args{testFQDN, "https://www.google.com/test3/test"}, false},
		{"test4", args{testFQDN, "https://www.google.com/test4/test/test"}, false},
		{"test5", args{"https://data.example.com", "https://www.example.com"}, false},
		{"test6", args{"www.apps.com", "javascript:void(0)"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExternalLink(tt.args.sourceURL, tt.args.linkURL, 2); got != tt.want {
				t.Errorf("isExternalLink() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCombineURLs(t *testing.T) {
	type args struct {
		baseURL     string
		relativeURL string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		// Add test cases.
		{"test1", args{testFQDN, testFQDN}, testFQDN, false},
		{"test2", args{testFQDN, "https://www.google.com/test"}, "https://www.google.com/test", false},
		{"test3", args{testFQDN, "https://www.google.com/test/test"}, "https://www.google.com/test/test", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := combineURLs(tt.args.baseURL, tt.args.relativeURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("combineURLs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("combineURLs() = %v, want %v", got, tt.want)
			}
		})
	}
}
