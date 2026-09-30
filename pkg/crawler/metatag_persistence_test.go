// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package crawler

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPrepareMetaTagsCleansTruncatesAndDeduplicatesPairs(t *testing.T) {
	name255 := strings.Repeat("界", 255)
	content1024 := strings.Repeat("é", 1024)
	got := prepareMetaTags([]MetaTag{
		{Name: name255 + "末", Content: content1024 + "z"},
		{Name: name255 + "別", Content: content1024 + "y"}, // same pair after truncation
		{Name: "topic", Content: "one"},
		{Name: "topic", Content: "two"}, // same name is not the same row
		{Name: "bad\xffname", Content: "bad\xfecontent"},
	})
	if len(got) != 4 {
		t.Fatalf("prepared rows = %d, want 4: %#v", len(got), got)
	}
	if utf8.RuneCountInString(got[0].name) != 255 || got[0].name != name255 {
		t.Fatalf("name boundary was not preserved: runes=%d", utf8.RuneCountInString(got[0].name))
	}
	if utf8.RuneCountInString(got[0].content) != 1024 || got[0].content != content1024 {
		t.Fatalf("content boundary was not preserved: runes=%d", utf8.RuneCountInString(got[0].content))
	}
	if got[1].name != "topic" || got[1].content != "one" ||
		got[2].name != "topic" || got[2].content != "two" {
		t.Fatalf("same-name/different-content rows changed: %#v", got[1:3])
	}
	if got[3].name != "badname" || got[3].content != "badcontent" ||
		!utf8.ValidString(got[3].name) || !utf8.ValidString(got[3].content) {
		t.Fatalf("invalid UTF-8 cleanup changed: %#v", got[3])
	}
}

func TestMetaTagTimeSeriesInputPreservesSelectionAndProvenance(t *testing.T) {
	stored := persistedMetaTag{name: "  CAFE\u0301  ", content: "selected", metatagID: 41, metatagIndexID: 91}
	input := metaTagTimeSeriesInput(17, stored)
	if input.SourceKind != cfg.TimeSeriesSourceMetatag || input.IndexID != 17 ||
		input.RowID != 41 || input.LinkID != 91 || input.SubjectKey != "café" ||
		input.Name != stored.name || input.RawValue != "selected" || input.Value != "selected" ||
		input.ObservedAt.IsZero() || input.ObservedAt.Location() != time.UTC {
		t.Fatalf("time-series input does not preserve persisted provenance/value: %#v", input)
	}
	if input.Attributes["name"] != stored.name || input.Attributes["content"] != stored.content {
		t.Fatalf("time-series selector attributes changed: %#v", input.Attributes)
	}
}

func TestPrepareMetaTagsUsesExactPairUniqueness(t *testing.T) {
	got := prepareMetaTags([]MetaTag{
		{Name: "Description", Content: "value"},
		{Name: "Description", Content: "value"},
		{Name: "description", Content: "value"},
		{Name: "Description", Content: "Value"},
	})
	if len(got) != 3 {
		t.Fatalf("case-sensitive pair uniqueness returned %d rows, want 3", len(got))
	}
}

func TestPrepareMetaTagsLargeInputRetainsBoundedBatchRemainder(t *testing.T) {
	input := make([]MetaTag, postgresMetaTagBatchSize*2+17)
	for i := range input {
		input[i] = MetaTag{Name: "property", Content: strings.Repeat("x", i%19) + string(rune(0x1000+i))}
	}
	got := prepareMetaTags(input)
	if len(got) != len(input) {
		t.Fatalf("large input returned %d rows, want %d", len(got), len(input))
	}
}
