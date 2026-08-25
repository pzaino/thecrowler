package main

import (
	"reflect"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestEventsStartupSelectsExactlyOneRoleListener(t *testing.T) {
	tests := []struct {
		name     string
		master   bool
		want     eventNotificationHandler
		wantOwns bool
	}{
		{name: "master", master: true, want: handleNotification, wantOwns: true},
		{name: "replica", master: false, want: handleReplicaNotification, wantOwns: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := planEventsStartup(test.master)
			if plan.notificationHandler == nil {
				t.Fatal("notification handler is nil; want exactly one selected callback")
			}
			if reflect.ValueOf(plan.notificationHandler).Pointer() != reflect.ValueOf(test.want).Pointer() {
				t.Errorf("selected the wrong notification handler")
			}
			if plan.ownsSingletonResponsibilities != test.wantOwns {
				t.Errorf("ownsSingletonResponsibilities = %v, want %v", plan.ownsSingletonResponsibilities, test.wantOwns)
			}

			calls := 0
			var gotHandler func(string)
			var gotTimeout time.Duration
			startEventsNotificationListener(new(cdb.Handler), 37*time.Second, plan,
				func(_ *cdb.Handler, handler func(string), timeout time.Duration) {
					calls++
					gotHandler = handler
					gotTimeout = timeout
				})

			if calls != 1 {
				t.Fatalf("listener starts = %d, want exactly 1", calls)
			}
			if reflect.ValueOf(gotHandler).Pointer() != reflect.ValueOf(test.want).Pointer() {
				t.Error("listener received the wrong callback")
			}
			if gotTimeout != 37*time.Second {
				t.Errorf("listener timeout = %v, want 37s", gotTimeout)
			}
		})
	}
}

func TestReplicaStartupOwnsNoSingletonResponsibilities(t *testing.T) {
	plan := planEventsStartup(false)
	masterOnlyResponsibilities := []string{
		"normal event processing",
		"heartbeat response aggregation",
		"heartbeat request coordination",
		"event janitor",
		"provider listeners",
		"events scheduler",
		"time-series maintenance and aggregation",
	}

	for _, responsibility := range masterOnlyResponsibilities {
		t.Run(responsibility, func(t *testing.T) {
			if plan.ownsSingletonResponsibilities {
				t.Errorf("replica unexpectedly owns master-only responsibility %q", responsibility)
			}
		})
	}
}
