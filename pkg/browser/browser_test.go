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

package browser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pzaino/thecrowler/pkg/vdi"
)

type fakeLease struct {
	mu       sync.Mutex
	releases int
}

func (l *fakeLease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releases++
	return nil
}

func (l *fakeLease) releaseCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.releases
}

type fakeAcquirer struct {
	lease *fakeLease
}

func (a *fakeAcquirer) AcquireContext(ctx context.Context, _ string) (Lease, vdi.SeleniumInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, vdi.SeleniumInstance{}, err
	}
	if a.lease == nil {
		a.lease = &fakeLease{}
	}
	return a.lease, vdi.SeleniumInstance{}, nil
}

type fakeDriver struct {
	vdi.WebDriver

	mu             sync.Mutex
	getErr         error
	getDelay       time.Duration
	readyState     string
	currentURL     string
	storageClears  int
	deleteCookies  int
	closes         int
	quits          int
	visited        []string
	blockUntilDone bool
}

func (d *fakeDriver) Get(url string) error {
	if url != "about:blank" && d.getDelay > 0 {
		time.Sleep(d.getDelay)
	}
	if url != "about:blank" && d.blockUntilDone {
		select {}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.visited = append(d.visited, url)
	d.currentURL = url
	if url == "about:blank" {
		return nil
	}
	return d.getErr
}

func (d *fakeDriver) ExecuteScript(script string, _ []interface{}) (interface{}, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.Contains(script, "document.readyState") {
		if d.readyState == "" {
			return "complete", nil
		}
		return d.readyState, nil
	}
	d.storageClears++
	return nil, nil
}

func (d *fakeDriver) CurrentURL() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.currentURL == "" {
		return "about:blank", nil
	}
	return d.currentURL, nil
}

func (d *fakeDriver) DeleteAllCookies() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleteCookies++
	return nil
}

func (d *fakeDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closes++
	return nil
}

func (d *fakeDriver) Quit() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.quits++
	return nil
}

func (d *fakeDriver) cleanupCounts() (int, int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deleteCookies, d.closes, d.quits
}

func TestWithSessionCleansUpAndReleasesAfterCallbackError(t *testing.T) {
	callbackErr := errors.New("callback failed")
	lease := &fakeLease{}
	driver := &fakeDriver{readyState: "complete"}
	manager := NewSessionManager(&fakeAcquirer{lease: lease}, func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error) {
		return driver, nil
	})

	err := manager.WithSession(context.Background(), SessionOptions{URL: "https://example.test"}, func(session *Session) error {
		if session.WebDriver != driver {
			t.Fatalf("callback received unexpected WebDriver")
		}
		return callbackErr
	})

	if !errors.Is(err, callbackErr) {
		t.Fatalf("WithSession error = %v, want callback error", err)
	}
	assertCleanupAndRelease(t, driver, lease)
}

func TestWithSessionCleansUpAndReleasesAfterNavigationError(t *testing.T) {
	lease := &fakeLease{}
	navigationErr := errors.New("boom")
	driver := &fakeDriver{getErr: navigationErr}
	manager := NewSessionManager(&fakeAcquirer{lease: lease}, func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error) {
		return driver, nil
	})

	err := manager.WithSession(context.Background(), SessionOptions{URL: "https://example.test"}, func(*Session) error {
		t.Fatal("callback should not be invoked after navigation error")
		return nil
	})

	if !errors.Is(err, navigationErr) {
		t.Fatalf("WithSession error = %v, want navigation error", err)
	}
	assertCleanupAndRelease(t, driver, lease)
}

func TestWithSessionCleansUpAndReleasesAfterNavigationTimeout(t *testing.T) {
	lease := &fakeLease{}
	driver := &fakeDriver{blockUntilDone: true}
	manager := NewSessionManager(&fakeAcquirer{lease: lease}, func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error) {
		return driver, nil
	})

	err := manager.WithSession(context.Background(), SessionOptions{
		URL:               "https://example.test",
		NavigationTimeout: 10 * time.Millisecond,
	}, func(*Session) error {
		t.Fatal("callback should not be invoked after navigation timeout")
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "navigation timeout") {
		t.Fatalf("WithSession error = %v, want navigation timeout", err)
	}
	assertCleanupAndRelease(t, driver, lease)
}

func TestWithSessionCleansUpAndReleasesAfterReadinessTimeout(t *testing.T) {
	lease := &fakeLease{}
	driver := &fakeDriver{readyState: "loading"}
	manager := NewSessionManager(&fakeAcquirer{lease: lease}, func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error) {
		return driver, nil
	})

	err := manager.WithSession(context.Background(), SessionOptions{
		URL:              "https://example.test",
		ReadinessTimeout: 10 * time.Millisecond,
		PollInterval:     time.Millisecond,
	}, func(*Session) error {
		t.Fatal("callback should not be invoked after readiness timeout")
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "readyState") {
		t.Fatalf("WithSession error = %v, want readiness timeout", err)
	}
	assertCleanupAndRelease(t, driver, lease)
}

func TestWithSessionCleansUpAndReleasesAfterCancellation(t *testing.T) {
	lease := &fakeLease{}
	driver := &fakeDriver{getDelay: 50 * time.Millisecond}
	manager := NewSessionManager(&fakeAcquirer{lease: lease}, func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error) {
		return driver, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.WithSession(ctx, SessionOptions{URL: "https://example.test"}, func(*Session) error {
		t.Fatal("callback should not be invoked after cancellation")
		return nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WithSession error = %v, want context.Canceled", err)
	}
	if lease.releaseCount() != 0 {
		t.Fatalf("release count = %d, want no acquisition", lease.releaseCount())
	}
}

func TestWithSessionCleansUpAndReleasesWhenCanceledDuringNavigation(t *testing.T) {
	lease := &fakeLease{}
	driver := &fakeDriver{getDelay: 50 * time.Millisecond}
	manager := NewSessionManager(&fakeAcquirer{lease: lease}, func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error) {
		return driver, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := manager.WithSession(ctx, SessionOptions{URL: "https://example.test"}, func(*Session) error {
		t.Fatal("callback should not be invoked after cancellation")
		return nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WithSession error = %v, want context.Canceled", err)
	}
	assertCleanupAndRelease(t, driver, lease)
}

func assertCleanupAndRelease(t *testing.T, driver *fakeDriver, lease *fakeLease) {
	t.Helper()
	cookies, closes, quits := driver.cleanupCounts()
	if cookies != 1 || closes != 1 || quits != 1 {
		t.Fatalf("cleanup counts cookies=%d closes=%d quits=%d, want 1 each", cookies, closes, quits)
	}
	if lease.releaseCount() != 1 {
		t.Fatalf("release count = %d, want 1", lease.releaseCount())
	}
}
