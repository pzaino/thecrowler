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
	"fmt"
	"strings"
	"time"

	"github.com/pzaino/thecrowler/pkg/vdi"
)

const clearStorageScript = `
window.localStorage.clear();
window.sessionStorage.clear();
if (window.indexedDB) {
	indexedDB.databases().then(dbs => {
		dbs.forEach(db => indexedDB.deleteDatabase(db.name));
	});
}
if ('caches' in window) {
	caches.keys().then(keys => {
		keys.forEach(key => caches.delete(key));
	});
}
`

// CleanupOptions controls whether Cleanup also closes and quits WebDriver.
type CleanupOptions struct {
	Close bool
}

// Setup applies generic browser-session settings.
func Setup(ctx context.Context, wd vdi.WebDriver, opts SetupOptions) error {
	if wd == nil {
		return errors.New("browser: WebDriver is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	initialURL := strings.TrimSpace(opts.InitialURL)
	if initialURL == "" {
		initialURL = "about:blank"
	}
	var setupErr error
	setupErr = errors.Join(setupErr, wrapError("load initial page", Navigate(ctx, wd, initialURL, defaultNavigationTimeout)))
	if opts.SetGPUPatch {
		setupErr = errors.Join(setupErr, wrapError("apply GPU patch", vdi.GPUPatch(wd)))
	}
	if opts.ReinforceBrowserSettings {
		setupErr = errors.Join(setupErr, wrapError("reinforce browser settings", vdi.ReinforceBrowserSettings(wd)))
	}
	return setupErr
}

// Navigate loads url and returns when WebDriver finishes, the timeout expires,
// or ctx is canceled.
func Navigate(ctx context.Context, wd vdi.WebDriver, url string, timeout time.Duration) error {
	if wd == nil {
		return errors.New("browser: WebDriver is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("browser: URL is empty")
	}
	if timeout <= 0 {
		timeout = defaultNavigationTimeout
	}

	done := make(chan error, 1)
	go func() {
		done <- wd.Get(url)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("browser: navigate to %s: %w", url, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("browser: navigation timeout after %s for URL %s", timeout, url)
	}
}

// WaitForDOMReady waits until document.readyState is complete.
func WaitForDOMReady(ctx context.Context, wd vdi.WebDriver, timeout, pollInterval time.Duration) error {
	if wd == nil {
		return errors.New("browser: WebDriver is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		value, err := wd.ExecuteScript("return document.readyState", nil)
		if err == nil && strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "complete") {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("browser: document.readyState never reached complete within %s", timeout)
		case <-ticker.C:
		}
	}
}

// Cleanup clears browser storage and cookies. When Close is true, it also
// closes the current window and quits the WebDriver session. Every requested
// cleanup operation is attempted, even when an earlier operation fails.
func Cleanup(wd vdi.WebDriver, opts CleanupOptions) error {
	if wd == nil {
		return errors.New("browser: WebDriver is nil")
	}

	var cleanupErr error
	currentURL, currentURLErr := wd.CurrentURL()
	if currentURLErr == nil && !strings.HasPrefix(currentURL, "data:") {
		_, err := wd.ExecuteScript(clearStorageScript, nil)
		cleanupErr = errors.Join(cleanupErr, wrapError("clear browser storage", err))
	}
	cleanupErr = errors.Join(cleanupErr, wrapError("delete browser cookies", wd.DeleteAllCookies()))

	if opts.Close {
		cleanupErr = errors.Join(cleanupErr, wrapError("close WebDriver", wd.Close()))
		cleanupErr = errors.Join(cleanupErr, wrapError("quit WebDriver", wd.Quit()))
	}
	return cleanupErr
}
