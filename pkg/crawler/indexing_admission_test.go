package crawler

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndexingAdmissionBoundedWithoutDroppedPages(t *testing.T) {
	r := newIndexingAdmissionRegistry()
	const pages, limit = 24, 3
	var active, peak, completed atomic.Int32
	var wg sync.WaitGroup
	for range pages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := r.acquire(context.Background(), limit)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer release()
			n := active.Add(1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			completed.Add(1)
		}()
	}
	wg.Wait()
	if peak.Load() > limit {
		t.Fatalf("peak concurrency = %d, limit = %d", peak.Load(), limit)
	}
	if completed.Load() != pages {
		t.Fatalf("completed = %d, want %d", completed.Load(), pages)
	}
}

func TestIndexingAdmissionCancellationAndNoPermitLeak(t *testing.T) {
	r := newIndexingAdmissionRegistry()
	release, err := r.acquire(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.acquire(waitCtx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting acquire error = %v, want context.Canceled", err)
	}
	release()

	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	release, err = r.acquire(ctx, 1)
	if err != nil {
		t.Fatalf("permit leaked after cancellation: %v", err)
	}
	release()
	release() // release is deliberately idempotent.
}

func TestIndexingAdmissionLimitOneUsesSinglePermit(t *testing.T) {
	r := newIndexingAdmissionRegistry()
	release, err := r.acquire(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	sem := r.limiters[1]
	if sem == nil {
		t.Fatal("limit 1 did not create a semaphore")
	}
	if got, want := cap(sem), 1; got != want {
		t.Fatalf("semaphore capacity = %d, want %d", got, want)
	}
	if got, want := len(sem), 1; got != want {
		t.Fatalf("admitted operations = %d, want %d", got, want)
	}
}

func TestIndexingAdmissionUnlimited(t *testing.T) {
	r := newIndexingAdmissionRegistry()
	const callers = 32
	type result struct {
		release func()
		err     error
	}
	results := make(chan result, callers)
	start := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Keep every acquisition outstanding. A bounded semaphore smaller than the
	// caller count could not admit every goroutine and fill the results channel.
	for range callers {
		go func() {
			<-start
			release, err := r.acquire(ctx, 0)
			results <- result{release: release, err: err}
		}()
	}
	close(start)

	releases := make([]func(), 0, callers)
	for range callers {
		got := <-results
		if got.err != nil {
			t.Fatalf("acquire unlimited admission: %v", got.err)
		}
		releases = append(releases, got.release)
	}
	if len(r.limiters) != 0 {
		t.Fatalf("unlimited admission created %d limiters, want none", len(r.limiters))
	}
	for _, release := range releases {
		release()
		release() // The unlimited release is a naturally idempotent no-op.
	}
}

func TestIndexingAdmissionUnlimitedHonorsCancelledContext(t *testing.T) {
	r := newIndexingAdmissionRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := r.acquire(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("unlimited acquire error = %v, want context.Canceled", err)
	}
	if len(r.limiters) != 0 {
		t.Fatalf("cancelled unlimited admission created %d limiters, want none", len(r.limiters))
	}
}

func TestIndexingAdmissionPreservesEachChangeChain(t *testing.T) {
	r := newIndexingAdmissionRegistry()
	want := map[int][]string{}
	got := map[int][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for page := range 12 {
		want[page] = []string{"search-index", "web-object", "attributes", "metrics", "commit", "keywords"}
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			release, err := r.acquire(context.Background(), 4)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer release()
			for _, change := range want[page] {
				mu.Lock()
				got[page] = append(got[page], change)
				mu.Unlock()
			}
		}(page)
	}
	wg.Wait()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("change chains differ:\ngot  %#v\nwant %#v", got, want)
	}
}

func BenchmarkIndexingAdmissionLimits(b *testing.B) {
	for _, limit := range []int{1, 2, 4, 8} {
		b.Run(string(rune('0'+limit)), func(b *testing.B) {
			r := newIndexingAdmissionRegistry()
			b.ReportMetric(float64(limit), "indexing_limit")
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					release, _ := r.acquire(context.Background(), limit)
					release()
				}
			})
			b.ReportMetric(float64(b.N)/b.Elapsed().Minutes(), "completed_pages/min")
		})
	}
}
