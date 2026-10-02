package crawler

import (
	"context"
	"sync"
)

// indexingAdmissionRegistry keeps one process-wide semaphore for each configured
// limit. A deployment normally has one limit; keying it makes source-specific
// configurations deterministic without resizing a live semaphore.
type indexingAdmissionRegistry struct {
	mu       sync.Mutex
	limiters map[int]chan struct{}
}

func newIndexingAdmissionRegistry() *indexingAdmissionRegistry {
	return &indexingAdmissionRegistry{limiters: make(map[int]chan struct{})}
}

func (r *indexingAdmissionRegistry) acquire(ctx context.Context, limit int) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit == 0 {
		return func() {}, nil
	}
	if limit < 0 {
		limit = 1
	}
	r.mu.Lock()
	sem := r.limiters[limit]
	if sem == nil {
		sem = make(chan struct{}, limit)
		r.limiters[limit] = sem
	}
	r.mu.Unlock()

	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-sem }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
