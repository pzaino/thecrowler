// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package crawler

import (
	"fmt"
	"reflect"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPreparePageKeywordsPreservesCanonicalCounts(t *testing.T) {
	page := &PageInfo{Keywords: []string{
		" Café ", "CAFE\u0301", "café", "Go", "go", "", "  ",
	}}
	ordered, occurrences := preparePageKeywords(page)

	if want := []string{"café", "go"}; !reflect.DeepEqual(ordered, want) {
		t.Fatalf("canonical keyword order = %q, want %q", ordered, want)
	}
	if occurrences["café"] != 3 || occurrences["go"] != 2 {
		t.Fatalf("occurrences = %#v, want café=3 and go=2", occurrences)
	}
	// This intentionally pins the pre-existing PageInfo mutation: unique is
	// case-sensitive before persistence canonicalization and the result is sorted.
	if want := []string{"CAFÉ", "Café", "Go", "café", "go"}; !reflect.DeepEqual(page.Keywords, want) {
		t.Fatalf("PageInfo.Keywords = %q, want %q", page.Keywords, want)
	}
}

func TestPreparePageKeywordsLargeInputIsComplete(t *testing.T) {
	page := &PageInfo{Keywords: make([]string, postgresKeywordBatchSize*2+17)}
	for i := range page.Keywords {
		page.Keywords[i] = fmt.Sprintf("Term-%04d", i)
	}
	ordered, occurrences := preparePageKeywords(page)
	if len(ordered) != len(page.Keywords) || len(occurrences) != len(page.Keywords) {
		t.Fatalf("large set lost terms: ordered=%d occurrences=%d want=%d", len(ordered), len(occurrences), len(page.Keywords))
	}
	for _, keyword := range ordered {
		if occurrences[keyword] != 1 {
			t.Fatalf("occurrences[%q] = %d, want 1", keyword, occurrences[keyword])
		}
	}
}

func TestKeywordTimeSeriesInputsKeepPersistedIdentityAndCount(t *testing.T) {
	persisted := []persistedKeyword{
		{keyword: "café", keywordID: 41, keywordIndexID: 91, occurrences: 3},
		{keyword: "go", keywordID: 42, keywordIndexID: 92, occurrences: 2},
	}
	inputs := keywordTimeSeriesInputs(17, persisted)
	for i, input := range inputs {
		row := persisted[i]
		if input.SourceKind != cfg.TimeSeriesSourceKeyword || input.IndexID != 17 ||
			input.RowID != uint64(row.keywordID) || input.LinkID != row.keywordIndexID ||
			input.SubjectKey != row.keyword || input.Occurrences != row.occurrences ||
			input.Value != row.occurrences || input.ObservedAt.IsZero() {
			t.Fatalf("time-series input %d does not preserve persisted row: %#v", i, input)
		}
		if input.Attributes["keyword"] != row.keyword || input.Attributes["occurrences"] != row.occurrences {
			t.Fatalf("time-series attributes %d = %#v", i, input.Attributes)
		}
	}
}
