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

// Package browser owns the context-aware lifecycle of a leased VDI browser session.
package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pzaino/thecrowler/pkg/vdi"
)

const (
	defaultNavigationTimeout = 45 * time.Second
	defaultReadinessTimeout  = 5 * time.Second
	defaultPollInterval      = 200 * time.Millisecond
)

// Lease is the ownership token returned by a VDI pool.
type Lease interface {
	Release() error
}

// LeaseAcquirer obtains an exclusively leased VDI instance.
type LeaseAcquirer interface {
	AcquireContext(context.Context, string) (Lease, vdi.SeleniumInstance, error)
}

// ConnectFunc connects a WebDriver to a leased VDI instance.
type ConnectFunc func(context.Context, vdi.SeleniumInstance, int) (vdi.WebDriver, error)

// SessionManager runs work inside a fully managed browser session.
type SessionManager interface {
	WithSession(context.Context, SessionOptions, func(*Session) error) error
}

// SessionOptions controls acquisition, browser setup, and initial navigation.
type SessionOptions struct {
	AllowedVDINames   string
	BrowserType       int
	URL               string
	NavigationTimeout time.Duration
	ReadinessTimeout  time.Duration
	PollInterval      time.Duration
	Setup             SetupOptions
}

// SetupOptions controls browser-level setup that is independent of crawler
// traversal and extraction policy.
type SetupOptions struct {
	InitialURL               string
	SetGPUPatch              bool
	ReinforceBrowserSettings bool
}

// Session is the browser and VDI instance exposed to a managed callback.
type Session struct {
	WebDriver vdi.WebDriver
	Instance  vdi.SeleniumInstance
}

// Manager implements SessionManager over a VDI lease source.
type Manager struct {
	leases  LeaseAcquirer
	connect ConnectFunc
}

// NewSessionManager creates a browser session manager.
func NewSessionManager(leases LeaseAcquirer, connect ConnectFunc) *Manager {
	return &Manager{leases: leases, connect: connect}
}

// Pool adapts vdi.Pool to LeaseAcquirer.
type Pool struct {
	Pool *vdi.Pool
}

// AcquireContext acquires a lease from the wrapped VDI pool.
func (p Pool) AcquireContext(ctx context.Context, allowedNames string) (Lease, vdi.SeleniumInstance, error) {
	if p.Pool == nil {
		return nil, vdi.SeleniumInstance{}, errors.New("browser: VDI pool is nil")
	}
	return p.Pool.AcquireContext(ctx, allowedNames)
}

// WithSession acquires, connects, sets up, navigates, waits for DOM readiness,
// invokes fn, then clears/closes the browser and releases the lease. Cleanup and
// release are attempted on every path after acquisition.
func (m *Manager) WithSession(ctx context.Context, opts SessionOptions, fn func(*Session) error) (retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil || m.leases == nil {
		return errors.New("browser: lease acquirer is nil")
	}
	if m.connect == nil {
		return errors.New("browser: connector is nil")
	}
	if fn == nil {
		return errors.New("browser: session callback is nil")
	}

	lease, instance, err := m.leases.AcquireContext(ctx, opts.AllowedVDINames)
	if err != nil {
		return fmt.Errorf("browser: acquire VDI lease: %w", err)
	}
	if lease == nil {
		return errors.New("browser: lease acquirer returned a nil lease")
	}
	defer func() {
		retErr = errors.Join(retErr, wrapError("release VDI lease", lease.Release()))
	}()

	wd, err := m.connect(ctx, instance, opts.BrowserType)
	if err != nil {
		return fmt.Errorf("browser: connect WebDriver: %w", err)
	}
	if wd == nil {
		return errors.New("browser: connector returned a nil WebDriver")
	}
	defer func() {
		retErr = errors.Join(retErr, wrapError("clean up browser", Cleanup(wd, CleanupOptions{Close: true})))
	}()

	if err := Setup(ctx, wd, opts.Setup); err != nil {
		return err
	}
	if strings.TrimSpace(opts.URL) != "" {
		if err := Navigate(ctx, wd, opts.URL, durationOr(opts.NavigationTimeout, defaultNavigationTimeout)); err != nil {
			return err
		}
		if err := WaitForDOMReady(ctx, wd, durationOr(opts.ReadinessTimeout, defaultReadinessTimeout), durationOr(opts.PollInterval, defaultPollInterval)); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(&Session{WebDriver: wd, Instance: instance})
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("browser: %s: %w", operation, err)
}
