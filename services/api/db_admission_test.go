package main

import (
	"sync"
	"testing"
	"time"
)

func TestDBAdmissionImmediateAcquireReleaseAndTimeout(t *testing.T) {
	gate := newDBAdmissionGate(1)
	if !gate.Acquire(time.Second) {
		t.Fatal("expected immediate acquisition")
	}
	if gate.InUse() != 1 {
		t.Fatalf("InUse() = %d, want 1", gate.InUse())
	}
	start := time.Now()
	if gate.Acquire(25 * time.Millisecond) {
		t.Fatal("acquired a full gate")
	}
	if time.Since(start) < 15*time.Millisecond {
		t.Fatal("acquisition returned before its timeout")
	}
	gate.Release()
	gate.Release() // An unmatched release must not make accounting negative.
	if gate.InUse() != 0 {
		t.Fatalf("InUse() = %d, want 0", gate.InUse())
	}
}

func TestDBAdmissionLimitChanges(t *testing.T) {
	gate := newDBAdmissionGate(1)
	gate.Acquire(time.Second)
	acquired := make(chan bool, 1)
	go func() { acquired <- gate.Acquire(time.Second) }()

	gate.SetLimit(2)
	if !<-acquired {
		t.Fatal("increasing limit did not wake waiter")
	}
	gate.SetLimit(1)
	if gate.Acquire(20 * time.Millisecond) {
		t.Fatal("acquired while occupancy exceeded shrunken limit")
	}
	gate.Release()
	if gate.Acquire(20 * time.Millisecond) {
		t.Fatal("acquired while occupancy equaled shrunken limit")
	}
	gate.Release()
	if !gate.Acquire(time.Second) {
		t.Fatal("admission did not resume after enough releases")
	}
	gate.Release()
}

func TestDBAdmissionInvalidLimits(t *testing.T) {
	gate := newDBAdmissionGate(2)
	gate.SetLimit(0)
	gate.SetLimit(-10)
	if gate.Limit() != 2 {
		t.Fatalf("Limit() = %d, want 2", gate.Limit())
	}
	if newDBAdmissionGate(0).Limit() <= 0 {
		t.Fatal("constructor accepted a nonpositive limit")
	}
}

func TestDBAdmissionConcurrentUseAndResize(t *testing.T) {
	gate := newDBAdmissionGate(4)
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				if gate.Acquire(time.Second) {
					time.Sleep(time.Microsecond)
					gate.Release()
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 500; i++ {
			gate.SetLimit(i%8 + 1)
		}
	}()
	workers.Wait()
	if gate.InUse() != 0 {
		t.Fatalf("InUse() = %d after workers finished, want 0", gate.InUse())
	}
}
