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
	"strings"

	"golang.org/x/net/html"
)

// StaticHTMLContent is the text and hyperlinks found by statically parsing an
// HTML document. Relative link targets are preserved rather than resolved
// against a base URL.
type StaticHTMLContent struct {
	Text  string
	Links []StaticHTMLLink
}

// StaticHTMLLink describes a hyperlink found in statically parsed HTML.
type StaticHTMLLink struct {
	Href string
	Text string
}

// ExtractStaticHTML parses source without a browser and extracts visible-ish
// text and hyperlinks. It has no HTTP client or resource loader: URLs in images,
// stylesheets, scripts, fonts, media, frames, tracking pixels, CSS, and other
// resource-bearing markup remain inert. Hyperlink targets are returned as data
// but are never followed.
//
// Text in non-rendered containers (for example script, style, template, and
// embedded-resource elements) and elements hidden by common inline markers is
// omitted. This is intentionally an approximation: static parsing cannot
// account for computed CSS or DOM changes made by JavaScript.
func ExtractStaticHTML(source string) (StaticHTMLContent, error) {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return StaticHTMLContent{}, fmt.Errorf("browser: parse static HTML: %w", err)
	}

	var content StaticHTMLContent
	var text []string
	walkStaticHTML(document, false, &text, &content.Links)
	content.Text = strings.Join(text, " ")
	return content, nil
}

func walkStaticHTML(node *html.Node, hidden bool, text *[]string, links *[]StaticHTMLLink) {
	if node == nil {
		return
	}

	if node.Type == html.ElementNode {
		hidden = hidden || isStaticallyHidden(node)
	}
	if hidden {
		return
	}

	if node.Type == html.TextNode {
		appendNormalizedText(text, node.Data)
		return
	}

	if links != nil && node.Type == html.ElementNode && (strings.EqualFold(node.Data, "a") || strings.EqualFold(node.Data, "area")) {
		if href := strings.TrimSpace(attributeValue(node, "href")); href != "" {
			var linkText []string
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walkStaticHTML(child, false, &linkText, nil)
			}
			*links = append(*links, StaticHTMLLink{
				Href: href,
				Text: strings.Join(linkText, " "),
			})
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkStaticHTML(child, false, text, links)
	}
}

func appendNormalizedText(destination *[]string, value string) {
	*destination = append(*destination, strings.Fields(value)...)
}

func attributeValue(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute.Val
		}
	}
	return ""
}

func isStaticallyHidden(node *html.Node) bool {
	switch strings.ToLower(node.Data) {
	case "head", "script", "style", "template", "noscript", "iframe", "object", "embed", "audio", "video", "svg", "canvas":
		return true
	}

	for _, attribute := range node.Attr {
		switch strings.ToLower(attribute.Key) {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(attribute.Val), "true") {
				return true
			}
		case "style":
			if inlineStyleHidesElement(attribute.Val) {
				return true
			}
		}
	}
	return false
}

func inlineStyleHidesElement(style string) bool {
	for declaration := range strings.SplitSeq(style, ";") {
		property, value, found := strings.Cut(declaration, ":")
		if !found {
			continue
		}
		property = strings.ToLower(strings.TrimSpace(property))
		value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important")))
		if property == "display" && value == "none" {
			return true
		}
		if property == "visibility" && (value == "hidden" || value == "collapse") {
			return true
		}
	}
	return false
}
