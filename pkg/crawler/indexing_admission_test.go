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
