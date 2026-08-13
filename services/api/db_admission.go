package main

import (
	"net/http"
	"sync"
	"time"
)

// dbAdmissionGate limits concurrent database work while allowing its limit to
// be changed without interrupting work that has already been admitted.
type dbAdmissionGate struct {
	mu     sync.Mutex
	limit  int
	inUse  int
	notify chan struct{}
}

func newDBAdmissionGate(limit int) *dbAdmissionGate {
	if limit <= 0 {
		limit = 1
	}
	return &dbAdmissionGate{limit: limit, notify: make(chan struct{})}
}

func (g *dbAdmissionGate) Acquire(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		g.mu.Lock()
		if g.inUse < g.limit {
			g.inUse++
			g.mu.Unlock()
			return true
		}
		notify := g.notify
		g.mu.Unlock()

		select {
		case <-notify:
		case <-timer.C:
			return false
		}
	}
}

func (g *dbAdmissionGate) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inUse == 0 {
		return
	}
	g.inUse--
	g.broadcast()
}

func (g *dbAdmissionGate) SetLimit(limit int) {
	if limit <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.limit == limit {
		return
	}
	g.limit = limit
	g.broadcast()
}

func (g *dbAdmissionGate) Limit() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limit
}

func (g *dbAdmissionGate) InUse() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inUse
}

func (g *dbAdmissionGate) broadcast() {
	close(g.notify)
	g.notify = make(chan struct{})
}

const dbAdmissionTimeout = 5 * time.Second

func acquireDBAdmission(w http.ResponseWriter, countError bool) bool {
	if dbAdmission.Acquire(dbAdmissionTimeout) {
		return true
	}
	if countError {
		totalErrors.Add(1)
	}
	healthStatus := HealthCheck{Status: "DB is overloaded, please try again later"}
	handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	return false
}
