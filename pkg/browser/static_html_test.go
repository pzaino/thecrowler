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

package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestExtractStaticHTMLMalformedDocument(t *testing.T) {
	content, err := ExtractStaticHTML(`<main><p>Hello <strong>world<a href="/broken">Broken`)
	if err != nil {
		t.Fatalf("ExtractStaticHTML() error = %v", err)
	}

	if content.Text != "Hello world Broken" {
		t.Fatalf("Text = %q, want %q", content.Text, "Hello world Broken")
	}
	assertStaticHTMLLinks(t, content.Links, []StaticHTMLLink{{Href: "/broken", Text: "Broken"}})
}

func TestExtractStaticHTMLOmitsScriptsAndHiddenContent(t *testing.T) {
	content, err := ExtractStaticHTML(`
		<html>
			<head><title>Hidden title</title><style>.secret { display: block }</style></head>
			<body>
				Visible text
				<script>document.write('<a href="/injected">Injected</a>')</script>
				<template><a href="/template">Template link</a></template>
				<div hidden><a href="/hidden">Hidden link</a></div>
				<div aria-hidden="true">Aria hidden</div>
				<div style="display: none !important">Display hidden</div>
				<div style="visibility: collapse">Collapsed</div>
				<a href="/visible"><span>Visible</span> link</a>
			</body>
		</html>`)
	if err != nil {
		t.Fatalf("ExtractStaticHTML() error = %v", err)
	}

	if content.Text != "Visible text Visible link" {
		t.Fatalf("Text = %q, want %q", content.Text, "Visible text Visible link")
	}
	assertStaticHTMLLinks(t, content.Links, []StaticHTMLLink{{Href: "/visible", Text: "Visible link"}})
}

func TestExtractStaticHTMLPreservesRelativeLinks(t *testing.T) {
	content, err := ExtractStaticHTML(`
		<nav>
			<a href="../guide/start.html?mode=fast&amp;lang=en">Start guide</a>
			<a href="#details">Details</a>
			<area href="/map/region" alt="Region">
		</nav>`)
	if err != nil {
		t.Fatalf("ExtractStaticHTML() error = %v", err)
	}

	assertStaticHTMLLinks(t, content.Links, []StaticHTMLLink{
		{Href: "../guide/start.html?mode=fast&lang=en", Text: "Start guide"},
		{Href: "#details", Text: "Details"},
		{Href: "/map/region", Text: ""},
	})
}

func TestExtractStaticHTMLDoesNotLoadEmbeddedResources(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	source := fmt.Sprintf(`
		<link rel="stylesheet" href="%[1]s/styles.css">
		<img src="%[1]s/image.png" alt="Remote image">
		<iframe src="%[1]s/frame.html">Frame fallback</iframe>
		<object data="%[1]s/object.bin">Object fallback</object>
		<embed src="%[1]s/embed.bin">
		<a href="%[1]s/destination">Ordinary link</a>`, server.URL)

	content, err := ExtractStaticHTML(source)
	if err != nil {
		t.Fatalf("ExtractStaticHTML() error = %v", err)
	}

	if got := requests.Load(); got != 0 {
		t.Fatalf("embedded resource requests = %d, want 0", got)
	}
	if content.Text != "Ordinary link" {
		t.Fatalf("Text = %q, want %q", content.Text, "Ordinary link")
	}
	assertStaticHTMLLinks(t, content.Links, []StaticHTMLLink{{Href: server.URL + "/destination", Text: "Ordinary link"}})
}

func assertStaticHTMLLinks(t *testing.T, got, want []StaticHTMLLink) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Links = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Links[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}
