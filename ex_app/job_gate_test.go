package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestQueueAdmissionIsAtomic(t *testing.T) {
	g := &jobGate{}
	settings := defaultAppSettings()
	settings.MaxQueuedJobsPerUser = 1
	blocker, err := g.reserve(settings, "blocker")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.release()
	var wg sync.WaitGroup
	accepted := make(chan *jobReservation, 256)
	start := make(chan struct{})
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if reservation, err := g.reserve(settings, "alice"); err == nil {
				accepted <- reservation
			} else if !errors.Is(err, errQueueFull) {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(accepted)
	count := 0
	for reservation := range accepted {
		count++
		reservation.release()
	}
	if count != 1 {
		t.Fatalf("queue limit 1 admitted %d concurrent requests", count)
	}
}

func TestZeroQueueAllowsOnlyImmediateCapacity(t *testing.T) {
	g := &jobGate{}
	settings := defaultAppSettings()
	settings.MaxQueuedJobsPerUser = 0
	first, err := g.reserve(settings, "alice")
	if err != nil {
		t.Fatalf("idle converter: %v", err)
	}
	if err := first.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.reserve(settings, "bob"); !errors.Is(err, errQueueFull) {
		t.Fatalf("busy converter admitted a job with no queue: %v", err)
	}
	first.release()
	first.release() // A second cleanup must not consume another job's slot.
	next, err := g.reserve(settings, "bob")
	if err != nil {
		t.Fatalf("released capacity was not reusable: %v", err)
	}
	next.release()
	if g.activeTotal != 0 || len(g.activeByUser) != 0 || len(g.queuedByUser) != 0 {
		t.Fatalf("capacity leaked: %+v", g)
	}
}

func TestPerUserCapacityAndCancelledQueue(t *testing.T) {
	g := &jobGate{}
	settings := defaultAppSettings()
	settings.MaxConcurrentJobs = 2
	settings.MaxQueuedJobsPerUser = 1
	alice, _ := g.reserve(settings, "alice")
	defer alice.release()
	queued, err := g.reserve(settings, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if queued.active {
		t.Fatal("second alice job exceeded per-user concurrency")
	}
	bob, err := g.reserve(settings, "bob")
	if err != nil || !bob.active {
		t.Fatalf("bob could not use remaining capacity: %v", err)
	}
	defer bob.release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := queued.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled job acquired capacity: %v", err)
	}
	queued.release()
	replacement, err := g.reserve(settings, "alice")
	if err != nil {
		t.Fatalf("cancelled job retained its queue slot: %v", err)
	}
	defer replacement.release()
	alice.release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := replacement.acquire(ctx); err != nil {
		t.Fatalf("queued job did not acquire released capacity: %v", err)
	}
}
