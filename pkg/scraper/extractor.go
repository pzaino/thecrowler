// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package scraper provides page extraction and scraping-rule execution
// without depending on feature packages.
package scraper

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	rs "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
	"golang.org/x/net/html"
)

// ValueMatcher resolves and matches selector.Value against an element.
type ValueMatcher func(item interface{}, selector rs.Selector) (bool, error)

// ExternalSelector handles selectors whose implementation has side effects,
// such as plugin and agent calls. Those effects remain owned by the caller.
type ExternalSelector func(selector rs.Selector) ([]interface{}, error)

// Extractor holds the explicit dependencies used by extraction operations.
type Extractor struct {
	Driver          *vdi.WebDriver
	MatchValue      ValueMatcher
	ExtractExternal ExternalSelector
}

// LookupRequest describes a selector lookup.
type LookupRequest struct {
	Selector rs.Selector
	All      bool
}

// LookupResult contains elements returned by a selector lookup.
type LookupResult struct {
	Elements []vdi.WebElement
}

// ExtractRequest describes content extraction for one selector.
type ExtractRequest struct {
	Selector rs.Selector
	All      bool
}

// ExtractResult contains values extracted for one selector.
type ExtractResult struct {
	Values []interface{}
}

// FindElement finds the first element matching the request.
func (e Extractor) FindElement(req LookupRequest) (LookupResult, error) {
	req.All = false
	return e.lookup(req)
}

// FindElements finds all elements matching the request.
func (e Extractor) FindElements(req LookupRequest) (LookupResult, error) {
	req.All = true
	return e.lookup(req)
}

func (e Extractor) lookup(req LookupRequest) (LookupResult, error) {
	if e.Driver == nil || *e.Driver == nil {
		return LookupResult{}, errors.New("web driver is required")
	}

	selector := req.Selector
	selectorType := strings.ToLower(strings.TrimSpace(selector.SelectorType))
	var elements []vdi.WebElement
	var err error
	switch selectorType {
	case "css":
		elements, err = (*e.Driver).FindElements(vdi.ByCSSSelector, selector.Selector)
	case "id":
		elements, err = (*e.Driver).FindElements(vdi.ByID, selector.Selector)
	case "name":
		elements, err = (*e.Driver).FindElements(vdi.ByName, selector.Selector)
	case "link_text", "linktext":
		elements, err = (*e.Driver).FindElements(vdi.ByLinkText, selector.Selector)
	case "partial_link_text", "partiallinktext":
		elements, err = (*e.Driver).FindElements(vdi.ByPartialLinkText, selector.Selector)
	case "tag_name", "tagname", "tag", "element":
		elements, err = (*e.Driver).FindElements(vdi.ByTagName, selector.Selector)
	case "class_name", "classname", "class":
		elements, err = (*e.Driver).FindElements(vdi.ByClassName, selector.Selector)
	case "js_path":
		result, executeErr := (*e.Driver).ExecuteScript(fmt.Sprintf("return document.querySelector(%q);", selector.Selector), nil)
		if executeErr != nil {
			return LookupResult{}, fmt.Errorf("execute JavaScript selector: %w", executeErr)
		}
		element, ok := result.(vdi.WebElement)
		if !ok {
			return LookupResult{}, fmt.Errorf("no element found for JS Path: %s", selector.Selector)
		}
		elements = append(elements, element)
	case "xpath":
		elements, err = (*e.Driver).FindElements(vdi.ByXPATH, selector.Selector)
	default:
		return LookupResult{}, fmt.Errorf("unsupported selector type: %s", selector.SelectorType)
	}
	if err != nil {
		return LookupResult{}, fmt.Errorf("find element: %w", err)
	}

	filtered := make([]vdi.WebElement, 0, len(elements))
	for _, element := range elements {
		matches, matchErr := e.matches(element, selector, req.All)
		if matchErr != nil {
			return LookupResult{}, matchErr
		}
		if matches {
			filtered = append(filtered, element)
			if !req.All {
				break
			}
		}
	}
	if len(filtered) == 0 {
		return LookupResult{Elements: filtered}, fmt.Errorf("element '%s' Not found", selector.Selector)
	}
	return LookupResult{Elements: filtered}, nil
}

func (e Extractor) matches(item interface{}, selector rs.Selector, all bool) (bool, error) {
	if name := strings.TrimSpace(selector.Attribute.Name); name != "" {
		var value string
		var found bool
		switch element := item.(type) {
		case vdi.WebElement:
			attribute, err := element.GetAttribute(name)
			if err != nil {
				return false, nil
			}
			value, found = attribute, true
		case *goquery.Selection:
			value, found = element.Attr(name)
		case *html.Node:
			value, found = htmlquery.SelectAttr(element, name), true
		}
		if !found {
			return false, nil
		}
		want := strings.TrimSpace(selector.Attribute.Value)
		if want != "" && want != "*" && want != ".*" {
			if all {
				if !strings.EqualFold(strings.TrimSpace(value), want) {
					return false, nil
				}
			} else {
				re, err := regexp.Compile(want)
				if err != nil {
					return false, fmt.Errorf("compile attribute pattern: %w", err)
				}
				if !re.MatchString(value) {
					return false, nil
				}
			}
		}
	}
	if strings.TrimSpace(selector.Value) == "" {
		return true, nil
	}
	if e.MatchValue == nil {
		return defaultMatchValue(item, selector)
	}
	return e.MatchValue(item, selector)
}

func defaultMatchValue(item interface{}, selector rs.Selector) (bool, error) {
	var text string
	switch element := item.(type) {
	case vdi.WebElement:
		value, err := element.Text()
		if err != nil {
			return false, err
		}
		text = value
	case *goquery.Selection:
		text = element.Text()
	case *html.Node:
		text = element.Data
	default:
		return false, fmt.Errorf("unsupported value match item %T", item)
	}
	re, err := regexp.Compile(strings.TrimSpace(selector.Value))
	if err != nil {
		return false, fmt.Errorf("compile selector value: %w", err)
	}
	return re.MatchString(text), nil
}

// Extract extracts content using browser lookup first and HTML fallbacks when needed.
func (e Extractor) Extract(req ExtractRequest) (ExtractResult, error) {
	selectorType := strings.ToLower(strings.TrimSpace(req.Selector.SelectorType))
	if selectorType == "plugin_call" || selectorType == "agent_call" {
		if e.ExtractExternal == nil {
			return ExtractResult{}, fmt.Errorf("external selector handler is required for %s", selectorType)
		}
		values, err := e.ExtractExternal(req.Selector)
		return ExtractResult{Values: values}, err
	}

	var elements []vdi.WebElement
	var lookupErr error
	if selectorType != "regex" && selectorType != "xpath" {
		lookup, err := e.lookup(LookupRequest{Selector: req.Selector, All: req.All})
		lookupErr = err
		elements = lookup.Elements
	}
	if len(elements) > 0 && lookupErr == nil {
		values := make([]interface{}, 0, len(elements))
		for _, element := range elements {
			extracted, err := ExtractElement(ElementRequest{Element: element, Selector: req.Selector})
			if err != nil {
				return ExtractResult{}, err
			}
			for _, value := range extracted.Values {
				values = append(values, value)
			}
		}
		return ExtractResult{Values: values}, nil
	}

	if e.Driver == nil || *e.Driver == nil {
		return ExtractResult{}, errors.New("web driver is required")
	}
	source, err := (*e.Driver).PageSource()
	if err != nil {
		return ExtractResult{}, fmt.Errorf("read page source: %w", err)
	}
	values, err := e.extractFallback(source, req)
	if err != nil {
		if lookupErr != nil {
			return ExtractResult{}, errors.Join(lookupErr, err)
		}
		return ExtractResult{}, err
	}
	return ExtractResult{Values: values}, nil
}

func (e Extractor) extractFallback(source string, req ExtractRequest) ([]interface{}, error) {
	selectorType := strings.ToLower(strings.TrimSpace(req.Selector.SelectorType))
	if selectorType == "regex" {
		matches, err := ExtractRegex(RegexRequest{Content: source, Pattern: req.Selector.Selector, All: req.All})
		if err != nil {
			return nil, err
		}
		return stringsToInterfaces(matches.Values), nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("parse page source: %w", err)
	}
	var values []string
	switch selectorType {
	case "css":
		values, err = e.extractCSS(doc, req)
	case "xpath":
		values, err = e.extractXPath(doc, req)
	default:
		if err == nil {
			err = fmt.Errorf("no fallback for selector type: %s", req.Selector.SelectorType)
		}
	}
	return stringsToInterfaces(values), err
}

func (e Extractor) extractCSS(doc *goquery.Document, req ExtractRequest) ([]string, error) {
	var elements []*goquery.Selection
	if req.All {
		doc.Find(req.Selector.Selector).Each(func(_ int, selection *goquery.Selection) { elements = append(elements, selection) })
	} else if selection := doc.Find(req.Selector.Selector).First(); selection.Length() > 0 {
		elements = append(elements, selection)
	}
	var values []string
	for _, element := range elements {
		matches, err := e.matches(element, req.Selector, false)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		extracted, err := ExtractElement(ElementRequest{Element: element, Selector: req.Selector})
		if err != nil {
			return nil, err
		}
		values = append(values, extracted.Values...)
		if !req.All {
			break
		}
	}
	return values, nil
}

func (e Extractor) extractXPath(doc *goquery.Document, req ExtractRequest) ([]string, error) {
	items, err := htmlquery.QueryAll(doc.Nodes[0], req.Selector.Selector)
	if err != nil {
		return nil, fmt.Errorf("query XPath: %w", err)
	}
	var values []string
	for _, item := range items {
		matches, err := e.matches(item, req.Selector, false)
		if err != nil {
			return nil, err
		}
		if matches {
			values = append(values, htmlquery.InnerText(item))
			if !req.All {
				break
			}
		}
	}
	return values, nil
}

func stringsToInterfaces(values []string) []interface{} {
	result := make([]interface{}, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

// ElementRequest describes data extraction from a browser or goquery element.
type ElementRequest struct {
	Element  interface{}
	Selector rs.Selector
}

// ElementResult contains strings extracted from one element.
type ElementResult struct {
	Values []string
}

// ExtractElement extracts text or an attribute and optionally applies a pattern.
func ExtractElement(req ElementRequest) (ElementResult, error) {
	var data string
	var err error
	extractType := strings.ToLower(strings.TrimSpace(req.Selector.Extract.Type))
	pattern := req.Selector.Extract.Pattern
	switch element := req.Element.(type) {
	case vdi.WebElement:
		switch extractType {
		case "text", "inner_text", "innertext", "content":
			data, err = element.Text()
		case "attribute":
			data, err = element.GetAttribute(pattern)
			pattern = ""
		default:
			data, err = element.Text()
		}
	case *goquery.Selection:
		switch extractType {
		case "attribute":
			var exists bool
			data, exists = element.Attr(pattern)
			if !exists {
				err = errors.New("attribute not found")
			}
			pattern = ""
		default:
			data = element.Text()
		}
	default:
		return ElementResult{}, fmt.Errorf("unsupported element type %T", req.Element)
	}
	if err != nil {
		return ElementResult{}, fmt.Errorf("extract element data: %w", err)
	}
	if pattern == "" || pattern == ".*" {
		return ElementResult{Values: []string{data}}, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ElementResult{}, fmt.Errorf("compile extraction pattern: %w", err)
	}
	return ElementResult{Values: re.FindAllString(data, -1)}, nil
}

// RegexRequest describes a pure regular-expression extraction.
type RegexRequest struct {
	Content string
	Pattern string
	All     bool
}

// RegexResult contains regular-expression matches.
type RegexResult struct{ Values []string }

// ExtractRegex extracts the first capture group, or the whole match when absent.
func ExtractRegex(req RegexRequest) (RegexResult, error) {
	re, err := regexp.Compile(req.Pattern)
	if err != nil {
		return RegexResult{}, fmt.Errorf("compile extraction regex: %w", err)
	}
	matches := re.FindAllStringSubmatch(req.Content, -1)
	if !req.All && len(matches) > 1 {
		matches = matches[:1]
	}
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, match[1])
		} else if len(match) == 1 {
			values = append(values, match[0])
		}
	}
	return RegexResult{Values: values}, nil
}
