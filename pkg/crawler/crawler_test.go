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

func TestCheckMaxDepth(t *testing.T) {
	tests := []struct {
		name     string
		maxDepth int
		want     int
	}{
		{"ZeroDepth", 0, 1},
		{"PositiveDepth", 5, 5},
		{"NegativeDepth", -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkMaxDepth(tt.maxDepth); got != tt.want {
				t.Errorf("checkMaxDepth() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResetPageInfo(t *testing.T) {
	tests := []struct {
		name string
		page PageInfo
		want PageInfo
	}{
		{
			name: "Reset all fields",
			page: PageInfo{
				URL:          "https://example.com",
				Title:        "Example Title",
				HTML:         "<html></html>",
				BodyText:     "Example body text",
				Summary:      "Example summary",
				DetectedLang: "en",
				DetectedType: "text/html",
				PerfInfo:     PerformanceLog{},
				MetaTags:     []MetaTag{{Name: "description", Content: "Example description"}},
				ScrapedData:  []ScrapedItem{},
				Links:        []LinkItem{{Link: "https://example.com/link"}},
			},
			want: PageInfo{
				URL:          "",
				Title:        "",
				HTML:         "",
				BodyText:     "",
				Summary:      "",
				DetectedLang: "",
				DetectedType: "",
				PerfInfo:     PerformanceLog{},
				MetaTags:     []MetaTag{},
				ScrapedData:  []ScrapedItem{},
				Links:        []LinkItem{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetPageInfo(&tt.page)
			if !reflect.DeepEqual(tt.page, tt.want) {
				t.Errorf("resetPageInfo() = %v, want %v", tt.page, tt.want)
			}
		})
	}
}

func TestGenerateUniqueName(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		imageType string
		want      string
	}{
		{
			name:      "Test with valid URL and image type",
			url:       "https://example.com",
			imageType: "png",
			want:      generateUniqueName("https://example.com", "png"),
		},
		{
			name:      "Test with different URL and same image type",
			url:       "https://anotherexample.com",
			imageType: "png",
			want:      generateUniqueName("https://anotherexample.com", "png"),
		},
		{
			name:      "Test with same URL and different image type",
			url:       "https://example.com",
			imageType: "jpg",
			want:      generateUniqueName("https://example.com", "jpg"),
		},
		{
			name:      "Test with empty URL and image type",
			url:       "",
			imageType: "",
			want:      generateUniqueName("", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateUniqueName(tt.url, tt.imageType); got != tt.want {
				t.Errorf("generateUniqueName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDocTypeIsHTML(t *testing.T) {
	tests := []struct {
		name string
		mime string
		want bool
	}{
		{"HTML Text", "text/html", true},
		{"Plain Text", "text/plain", true},
		{"XHTML", "application/xhtml+xml", true},
		{"XML", "application/xml", false},
		{"JSON", "application/json", false},
		{"JavaScript", "application/javascript", false},
		{"CSS", "text/css", true},
		{"Empty String", "", false},
		{"Random String", "random/string", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := docTypeIsHTML(tt.mime); got != tt.want {
				t.Errorf("docTypeIsHTML() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"Valid URL with scheme", "https://www.google.com", true},
		{"Valid URL without scheme", "www.google.com", true},
		{"Invalid URL", "ht@tp://invalid-url", false},
		{"Empty URL", "", false},
		{"URL with only scheme", "http://", false},
		{"URL with path", "https://www.google.com/search", true},
		{"URL with query", "https://www.google.com/search?q=golang", true},
		{"URL with fragment", "https://www.google.com/search#top", true},
		{"URL with port", "https://www.google.com:8080", true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidURL(tt.url); got != tt.want {
				t.Errorf("IsValidURL() = %v for URL n.%d, want %v", got, i, tt.want)
			}
		})
	}
}

func TestIsValidURIProtocol(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"Valid HTTP URL", "http://example.com", true},
		{"Valid HTTPS URL", "https://example.com", true},
		{"Valid FTP URL", "ftp://example.com", true},
		{"Valid FTPS URL", "ftps://example.com", true},
		{"Invalid URL with no scheme", "example.com", false},
		{"Invalid URL with unsupported scheme", "mailto:example@example.com", false},
		{"Empty URL", "", false},
		{"URL with only scheme", "http://", true},
		{"URL with unsupported scheme", "sftp://example.com", false},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidURIProtocol(tt.url); got != tt.want {
				t.Errorf("IsValidURIProtocol() = %v, for test n.%d want %v", got, i, tt.want)
			}
		})
	}
}
