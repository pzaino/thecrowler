package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	mail "github.com/pzaino/thecrowler/pkg/mail"
	"golang.org/x/time/rate"
)

func TestInitAPIv1RegistersControlRoutes(t *testing.T) {
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
	})

	initAPIv1()

	registeredRoutes := []string{
		"/v1/health",
		"/v1/health/",
		"/v1/ready",
		"/v1/ready/",
		"/v1/config",
	}
	for _, route := range registeredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != route {
				t.Fatalf("registered pattern for %q = %q, want %q", route, pattern, route)
			}
		})
	}

	unregisteredRoutes := []string{
		"/v1/search/general",
		"/v1/information_seed/list",
		"/v1/source/add",
	}
	for _, route := range unregisteredRoutes {
		t.Run(route, func(t *testing.T) {
			_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, route, nil))
			if pattern != "" {
				t.Fatalf("unexpected registered pattern for %q = %q", route, pattern)
			}
		})
	}
}

func TestPipelineStatusJSONIncludesEmailRunSummary(t *testing.T) {
	want := mail.RunSummary{
		Counts:      mail.RunCounts{Mailboxes: 1, Completed: 3, Warnings: 2},
		Checkpoints: mail.CheckpointOutcomes{Committed: 1, Advanced: 1},
		Timing:      mail.RunTiming{Duration: 2 * time.Second},
	}
	statuses := []crowler.Status{{EmailSummary: &want, StartTime: time.Now()}}
	statuses[0].PipelineRunning.Store(1)

	reports := pipelineStatusJSON(&statuses)
	if len(reports) != 1 || reports[0].EmailSummary == nil || *reports[0].EmailSummary != want {
		t.Fatalf("pipeline status email summary = %#v, want %#v", reports, want)
	}
}
