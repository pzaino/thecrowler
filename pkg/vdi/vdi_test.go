package vdi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func newTestPool(t *testing.T, names ...string) *Pool {
	t.Helper()
	pool := NewPool(len(names))
	for i, name := range names {
		err := pool.Add(SeleniumInstance{Config: cfg.Selenium{
			Name: name,
			Host: "localhost",
			Port: 4444 + i,
		}})
		if err != nil {
			t.Fatalf("add VDI %q: %v", name, err)
		}
	}
	return pool
}

func TestAcquireContextFiltersAllowedNames(t *testing.T) {
	pool := newTestPool(t, "alpha", "beta")

	lease, instance, err := pool.AcquireContext(context.Background(), " BETA ")
	if err != nil {
		t.Fatalf("AcquireContext: %v", err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Errorf("Release: %v", err)
		}
	}()

	if lease.Index != 1 || lease.VDIName != "beta" {
		t.Fatalf("unexpected lease: index=%d name=%q", lease.Index, lease.VDIName)
	}
	if instance.Config.Name != "beta" {
		t.Fatalf("acquired %q, want beta", instance.Config.Name)
	}
}

func TestAcquireContextCancellationReturnsPromptlyWithoutLeakingSlot(t *testing.T) {
	pool := newTestPool(t, "alpha")
	first, _, err := pool.AcquireContext(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("first AcquireContext: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		lease, _, err := pool.AcquireContext(ctx, "alpha")
		if lease != nil {
			_ = lease.Release()
		}
		result <- err
	}()

	time.Sleep(20 * time.Millisecond)
	started := time.Now()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AcquireContext error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
			t.Fatalf("cancellation took %v", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireContext did not return after cancellation")
	}

	if got := pool.Available(); got != 0 {
		t.Fatalf("available slots before release = %d, want 0", got)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first lease: %v", err)
	}
	if got := pool.Available(); got != 1 {
		t.Fatalf("available slots after release = %d, want 1", got)
	}
}

func TestLeaseReleaseAfterAcquireErrorAndDoubleRelease(t *testing.T) {
	pool := newTestPool(t, "alpha")

	lease, _, err := pool.AcquireContext(context.Background(), "missing")
	if err == nil {
		t.Fatal("AcquireContext unexpectedly succeeded")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release nil lease after acquisition error: %v", err)
	}

	lease, _, err = pool.AcquireContext(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("AcquireContext: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if got := pool.Available(); got != 1 {
		t.Fatalf("available slots = %d, want 1", got)
	}
}

func TestLeaseCannotReleaseWrongSlot(t *testing.T) {
	pool := newTestPool(t, "alpha", "beta")
	lease, _, err := pool.AcquireContext(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("AcquireContext: %v", err)
	}

	lease.Index = 1
	lease.VDIName = "beta"
	if err := lease.Release(); err == nil {
		t.Fatal("modified lease unexpectedly released a slot")
	}
	if got := pool.Available(); got != 1 {
		t.Fatalf("available slots = %d, want 1", got)
	}

	pool.Release(0, "alpha")
	if got := pool.Available(); got != 2 {
		t.Fatalf("available slots after compatibility release = %d, want 2", got)
	}
}

func TestAcquireContextConcurrentExclusiveOwnership(t *testing.T) {
	pool := newTestPool(t, "alpha", "beta")
	const workers = 32

	var active [2]int32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			lease, _, err := pool.AcquireContext(ctx, "alpha,beta")
			if err != nil {
				errs <- err
				return
			}
			if got := atomic.AddInt32(&active[lease.Index], 1); got != 1 {
				errs <- errors.New("pool slot acquired concurrently")
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active[lease.Index], -1)
			if err := lease.Release(); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent acquisition: %v", err)
	}
	if got := pool.Available(); got != 2 {
		t.Fatalf("available slots = %d, want 2", got)
	}
}

func TestAcquireCompatibilityWrapper(t *testing.T) {
	pool := newTestPool(t, "alpha", "beta")

	index, instance, err := pool.Acquire(" beta, alpha ")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if index != 0 || instance.Config.Name != "alpha" {
		t.Fatalf("Acquire returned index=%d name=%q", index, instance.Config.Name)
	}
	pool.Release(index, " beta, alpha ")
	if got := pool.Available(); got != 2 {
		t.Fatalf("available slots = %d, want 2", got)
	}
}
