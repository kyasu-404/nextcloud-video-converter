package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errQueueFull = errors.New("too many queued jobs for user")

var conversionGate = &jobGate{}

type jobGate struct {
	mu           sync.Mutex
	activeTotal  int
	activeByUser map[string]int
	queuedByUser map[string]int
}

// A reservation belongs to exactly one accepted job, including before its
// goroutine starts. Admission and all capacity accounting share the same lock.
type jobReservation struct {
	gate     *jobGate
	settings AppSettings
	userID   string
	active   bool
	released bool
}

func (g *jobGate) canRun(settings AppSettings, userID string) bool {
	return g.activeTotal < settings.MaxConcurrentJobs && g.activeByUser[userID] < settings.MaxConcurrentJobsPerUser
}

func (g *jobGate) reserve(settings AppSettings, userID string) (*jobReservation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.activeByUser == nil {
		g.activeByUser = make(map[string]int)
	}
	if g.queuedByUser == nil {
		g.queuedByUser = make(map[string]int)
	}
	r := &jobReservation{gate: g, settings: settings, userID: userID}
	if g.canRun(settings, userID) {
		g.activeTotal++
		g.activeByUser[userID]++
		r.active = true
	} else {
		if g.queuedByUser[userID] >= settings.MaxQueuedJobsPerUser {
			return nil, errQueueFull
		}
		g.queuedByUser[userID]++
	}
	return r, nil
}

func (r *jobReservation) acquire(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g := r.gate
		g.mu.Lock()
		if r.released {
			g.mu.Unlock()
			return context.Canceled
		}
		if !r.active && g.canRun(r.settings, r.userID) {
			decrementUserCount(g.queuedByUser, r.userID)
			g.activeTotal++
			g.activeByUser[r.userID]++
			r.active = true
		}
		active := r.active
		g.mu.Unlock()
		if active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *jobReservation) release() {
	g := r.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.released {
		return
	}
	r.released = true
	if r.active {
		g.activeTotal--
		decrementUserCount(g.activeByUser, r.userID)
	} else {
		decrementUserCount(g.queuedByUser, r.userID)
	}
}

func decrementUserCount(counts map[string]int, userID string) {
	if counts[userID] <= 1 {
		delete(counts, userID)
	} else {
		counts[userID]--
	}
}
