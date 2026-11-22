// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package config

import (
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v2"
)

func TestParseTimeSeriesRetentionWindowCalendarSemantics(t *testing.T) {
	cases := []struct {
		value   string
		now     time.Time
		want    time.Time
		forever bool
	}{
		{"0", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Time{}, true},
		{"0d", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Time{}, true},
		{"0mo", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Time{}, true},
		{"1mo", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC), false},
		{"1mo", time.Date(2024, 3, 31, 12, 0, 0, 0, time.UTC), time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC), false},
		{"1y", time.Date(2024, 2, 29, 6, 30, 0, 0, time.UTC), time.Date(2023, 2, 28, 6, 30, 0, 0, time.UTC), false},
		{"1mo", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), false},
		{"7d", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC), false},
		{"2w", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC), false},
	}
	for _, test := range cases {
		window, err := ParseTimeSeriesRetentionWindow(test.value)
		if err != nil {
			t.Fatalf("parse %q: %v", test.value, err)
		}
		cutoff, active := window.Cutoff(test.now)
		if test.forever {
			if active {
				t.Fatalf("%q should be forever, got cutoff %s", test.value, cutoff)
			}
			continue
		}
		if !active {
			t.Fatalf("%q should be active", test.value)
		}
		if !cutoff.Equal(test.want) {
			t.Fatalf("%q cutoff = %s, want %s", test.value, cutoff, test.want)
		}
	}
}

func TestTimeSeriesRetentionWindowZeroValueIsInactive(t *testing.T) {
	if _, active := (TimeSeriesRetentionWindow{}).Cutoff(time.Now()); active {
		t.Fatal("zero window must be inactive")
	}
	if _, active := (TimeSeriesRetentionWindow{Specified: true}).Cutoff(time.Now()); active {
		t.Fatal("specified window without a positive horizon must be inactive")
	}
}

func TestTimeSeriesRetentionWindowValidate(t *testing.T) {
	if err := (TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (TimeSeriesRetentionWindow{Specified: true, Forever: true}).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, window := range map[string]TimeSeriesRetentionWindow{
		"empty":                 {Specified: true},
		"negative":              {Specified: true, Fixed: -time.Hour},
		"too many months":       {Specified: true, Months: 1201},
		"mixed units":           {Specified: true, Months: 1, Fixed: time.Hour},
		"forever with duration": {Specified: true, Forever: true, Fixed: time.Hour},
	} {
		if err := window.Validate(); err == nil {
			t.Fatalf("%s window accepted", name)
		}
	}
}

func TestParseTimeSeriesRetentionWindowRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "-1d", "1D", "1", "365", "abc", "1M", "1.5mo", "1h1mo", "1y2d", "1h30"} {
		if _, err := ParseTimeSeriesRetentionWindow(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	// 100 years is the documented ceiling; one more year must fail rather than
	// silently overflowing calendar arithmetic.
	if _, err := ParseTimeSeriesRetentionWindow("101y"); err == nil {
		t.Fatal("expected 101y to be rejected")
	}
}

func TestParseTimeSeriesRetentionWindowPreservesCompositeDurations(t *testing.T) {
	cases := map[string]struct {
		months int
		fixed  time.Duration
	}{
		"1h30m": {fixed: 90 * time.Minute},
		"1.5h":  {fixed: 90 * time.Minute},
		"1.5d":  {fixed: 36 * time.Hour},
		"1d12h": {fixed: 36 * time.Hour},
		"1w2d":  {fixed: 9 * 24 * time.Hour},
		"1y6mo": {months: 18},
		"2mo":   {months: 2},
		"0h30m": {fixed: 30 * time.Minute},
	}
	for value, want := range cases {
		window, err := ParseTimeSeriesRetentionWindow(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		if window.Months != want.months || window.Fixed != want.fixed {
			t.Fatalf("%q = %#v, want months=%d fixed=%s", value, window, want.months, want.fixed)
		}
	}
}

func TestParseTimeSeriesFixedDurationRejectsCalendarUnits(t *testing.T) {
	if _, err := ParseTimeSeriesFixedDuration("1mo"); err == nil {
		t.Fatal("calendar month accepted as a fixed duration")
	}
	if _, err := ParseTimeSeriesFixedDuration("1y"); err == nil {
		t.Fatal("calendar year accepted as a fixed duration")
	}
	if got, err := ParseTimeSeriesFixedDuration("2d"); err != nil || got != 48*time.Hour {
		t.Fatalf("2d = %s, %v", got, err)
	}
}

func TestTimeSeriesRetentionConfigDefaultsAndZero(t *testing.T) {
	normalized := NormalizeTimeSeriesConfig(TimeSeriesConfig{Enabled: true, Metrics: []TimeSeriesMetricConfig{{Key: "m", SourceKind: TimeSeriesSourceCustom, Selector: map[string]interface{}{"field": "value"}}}})
	if normalized.Retention.Raw != "30d" {
		t.Fatalf("raw default = %q", normalized.Retention.Raw)
	}
	if normalized.Retention.Schedule != "1h" || normalized.Retention.BatchSize != 1000 || normalized.Retention.MaxBatches != 10 || normalized.Retention.MaxDuration != "1m" {
		t.Fatalf("retention defaults = %#v", normalized.Retention)
	}
	if normalized.Retention.Enabled {
		t.Fatal("retention must remain disabled by default")
	}

	explicit := NormalizeTimeSeriesConfig(TimeSeriesConfig{Enabled: true, Retention: TimeSeriesRetentionConfig{Raw: "0", Aggregated: "1y"}, Metrics: []TimeSeriesMetricConfig{{Key: "m", SourceKind: TimeSeriesSourceCustom, Selector: map[string]interface{}{"field": "value"}}}})
	if explicit.Retention.Raw != "0" {
		t.Fatalf("explicit zero overwritten by default: %q", explicit.Retention.Raw)
	}
	if explicit.Retention.Aggregated != "1y" {
		t.Fatalf("explicit calendar override overwritten: %q", explicit.Retention.Aggregated)
	}
}

func TestTimeSeriesRetentionMetricOverrideInheritance(t *testing.T) {
	normalized := NormalizeTimeSeriesConfig(TimeSeriesConfig{
		Enabled:   true,
		Retention: TimeSeriesRetentionConfig{Raw: "30d", Aggregated: "365d"},
		Metrics: []TimeSeriesMetricConfig{
			{Key: "inherit", SourceKind: TimeSeriesSourceCustom, Selector: map[string]interface{}{"field": "value"}},
			{Key: "forever", SourceKind: TimeSeriesSourceCustom, Selector: map[string]interface{}{"field": "value"}, Retention: &TimeSeriesRetentionOverride{Raw: "0"}},
			{Key: "partial", SourceKind: TimeSeriesSourceCustom, Selector: map[string]interface{}{"field": "value"}, Retention: &TimeSeriesRetentionOverride{Aggregated: "2mo"}},
		},
	})
	if normalized.Metrics[0].Retention != nil {
		t.Fatal("omitted metric retention should stay nil")
	}
	if normalized.Metrics[1].Retention.Raw != "0" || normalized.Metrics[1].Retention.Aggregated != "365d" {
		t.Fatalf("forever override = %#v", normalized.Metrics[1].Retention)
	}
	if normalized.Metrics[2].Retention.Raw != "30d" || normalized.Metrics[2].Retention.Aggregated != "2mo" {
		t.Fatalf("partial override = %#v", normalized.Metrics[2].Retention)
	}
}

func TestTimeSeriesRetentionConfigValidation(t *testing.T) {
	valid := NormalizeTimeSeriesConfig(TimeSeriesConfig{Enabled: true, Retention: TimeSeriesRetentionConfig{Enabled: true, Raw: "1mo", Aggregated: "0", Schedule: "1d", BatchSize: 12, MaxBatches: 3, MaxDuration: "2m"}})
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid retention rejected: %v", err)
	}
	for name, mutate := range map[string]func(*TimeSeriesConfig){
		"negative":        func(c *TimeSeriesConfig) { c.Retention.Raw = "-1d" },
		"bad schedule":    func(c *TimeSeriesConfig) { c.Retention.Schedule = "1mo" },
		"zero batches":    func(c *TimeSeriesConfig) { c.Retention.MaxBatches = 0 },
		"zero batch size": func(c *TimeSeriesConfig) { c.Retention.BatchSize = 0 },
		"bad duration":    func(c *TimeSeriesConfig) { c.Retention.MaxDuration = "not-a-duration" },
	} {
		configuration := NormalizeTimeSeriesConfig(TimeSeriesConfig{Enabled: true})
		mutate(&configuration)
		if err := configuration.Validate(); err == nil {
			t.Fatalf("%s retention accepted", name)
		}
	}
}

func TestTimeSeriesRetentionUnquotedZeroSurvivesDefaults(t *testing.T) {
	var document struct {
		TimeSeries TimeSeriesConfig `yaml:"timeseries"`
	}
	raw := []byte(`timeseries:
  enabled: true
  retention:
    raw: 0
    aggregated: 1mo
  metrics:
    - key: unquoted-zero
      source_kind: custom
      selector: {field: value}
      retention:
        raw: 0
`)
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	normalized := NormalizeTimeSeriesConfig(document.TimeSeries)
	if normalized.Retention.Raw != "0" || normalized.Retention.Aggregated != "1mo" {
		t.Fatalf("global retention = %#v", normalized.Retention)
	}
	if normalized.Metrics[0].Retention == nil || normalized.Metrics[0].Retention.Raw != "0" || normalized.Metrics[0].Retention.Aggregated != "1mo" {
		t.Fatalf("metric retention = %#v", normalized.Metrics[0].Retention)
	}
}

func TestTimeSeriesRetentionExampleLoads(t *testing.T) {
	raw, err := os.ReadFile("../../examples/timeseries/retention-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("example does not load: %v", err)
	}
	if !parsed.TimeSeries.Retention.Enabled || parsed.TimeSeries.Retention.Raw != "30d" || parsed.TimeSeries.Retention.Aggregated != "1y" {
		t.Fatalf("example retention = %#v", parsed.TimeSeries.Retention)
	}
	if len(parsed.TimeSeries.Metrics) != 1 || parsed.TimeSeries.Metrics[0].Retention == nil {
		t.Fatalf("example metrics = %#v", parsed.TimeSeries.Metrics)
	}
	if parsed.TimeSeries.Metrics[0].Retention.Raw != "0" || parsed.TimeSeries.Metrics[0].Retention.Aggregated != "5y" {
		t.Fatalf("example metric retention = %#v", parsed.TimeSeries.Metrics[0].Retention)
	}
}

func TestNormalizeTimeSeriesConfigDoesNotMutateCaller(t *testing.T) {
	input := TimeSeriesConfig{
		Enabled: true,
		Metrics: []TimeSeriesMetricConfig{{
			Key:        "immutable-input",
			SourceKind: TimeSeriesSourceCustom,
			Selector:   map[string]interface{}{"field": "value"},
			Retention:  &TimeSeriesRetentionOverride{},
			Privacy:    &TimeSeriesPrivacyConfig{RedactPatterns: []string{}},
		}},
	}
	normalized := NormalizeTimeSeriesConfig(input)
	if input.Metrics[0].ValueType != "" || input.Metrics[0].BucketInterval != "" {
		t.Fatalf("normalization mutated the caller: %#v", input.Metrics[0])
	}
	if input.Metrics[0].Retention.Raw != "" || input.Metrics[0].Privacy.RedactPatterns == nil {
		t.Fatalf("normalization mutated caller-owned pointers: %#v", input.Metrics[0])
	}
	if normalized.Metrics[0].ValueType != TimeSeriesValueInteger || normalized.Metrics[0].Retention.Raw != "30d" {
		t.Fatalf("normalized metric = %#v", normalized.Metrics[0])
	}
}
