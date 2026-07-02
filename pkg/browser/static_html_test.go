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
	"errors"
	"net/http"
	"strings"
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

func TestExtractStaticHTMLDoesNotFetchExternalResources(t *testing.T) {
	transport := installFailOnRequestTransport(t)
	const external = "https://resources.example.invalid"

	source := strings.ReplaceAll(`
		<html>
		<head>
			<base href="EXTERNAL/base/">
			<meta http-equiv="refresh" content="0; url=EXTERNAL/redirect">
			<link rel="stylesheet" href="EXTERNAL/styles.css">
			<link rel="preload" as="font" href="EXTERNAL/font.woff2">
			<link rel="icon" href="EXTERNAL/favicon.ico">
			<style>
				@import url("EXTERNAL/imported.css");
				@font-face { font-family: remote; src: url("EXTERNAL/font.woff2"); }
				.hero { background-image: url("EXTERNAL/background.png"); }
			</style>
			<script src="EXTERNAL/script.js"></script>
		</head>
		<body background="EXTERNAL/body-background.png">
			<div class="hero" style="background: url('EXTERNAL/inline.png')">Visible text</div>
			<img src="EXTERNAL/image.png" srcset="EXTERNAL/image-2x.png 2x" alt="Remote image">
			<picture><source srcset="EXTERNAL/picture.webp"><img src="EXTERNAL/fallback.png"></picture>
			<input type="image" src="EXTERNAL/button.png">
			<iframe src="EXTERNAL/frame.html">Frame fallback</iframe>
			<object data="EXTERNAL/object.bin">Object fallback</object>
			<embed src="EXTERNAL/embed.bin">
			<audio src="EXTERNAL/audio.mp3"><source src="EXTERNAL/audio.ogg"></audio>
			<video src="EXTERNAL/video.mp4" poster="EXTERNAL/poster.jpg">
				<source src="EXTERNAL/video.webm"><track src="EXTERNAL/captions.vtt">
			</video>
			<svg><image href="EXTERNAL/vector.png"/><use href="EXTERNAL/sprite.svg#icon"/></svg>
			<form action="EXTERNAL/submit"><button>Submit</button></form>
			<img width="1" height="1" src="EXTERNAL/tracking.gif">
			<a href="EXTERNAL/destination">Ordinary link</a>
		</body>
		</html>`, "EXTERNAL", external)

	content, err := ExtractStaticHTML(source)
	if err != nil {
		t.Fatalf("ExtractStaticHTML() error = %v", err)
	}

	transport.assertUnused(t)
	if content.Text != "Visible text Submit Ordinary link" {
		t.Fatalf("Text = %q, want %q", content.Text, "Visible text Submit Ordinary link")
	}
	assertStaticHTMLLinks(t, content.Links, []StaticHTMLLink{{Href: external + "/destination", Text: "Ordinary link"}})
}

type failOnRequestTransport struct {
	requests atomic.Int32
}

func (transport *failOnRequestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	return nil, errors.New("unexpected network request: " + request.URL.String())
}

func (transport *failOnRequestTransport) assertUnused(t *testing.T) {
	t.Helper()
	if requests := transport.requests.Load(); requests != 0 {
		t.Fatalf("network requests = %d, want 0", requests)
	}
}

func installFailOnRequestTransport(t *testing.T) *failOnRequestTransport {
	t.Helper()

	transport := &failOnRequestTransport{}
	previousDefaultTransport := http.DefaultTransport
	previousClientTransport := http.DefaultClient.Transport
	http.DefaultTransport = transport
	http.DefaultClient.Transport = transport
	t.Cleanup(func() {
		http.DefaultTransport = previousDefaultTransport
		http.DefaultClient.Transport = previousClientTransport
	})
	return transport
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
