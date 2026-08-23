package core

import (
	"context"
	"sync"

	"github.com/sorokin-vladimir/tele/internal/core/project"
)

// deltaQueue decouples state commits from the TUI without making projection
// delivery lossy. A short-lived relay drains each burst in order and exits once
// caught up.
type deltaQueue struct {
	mu       sync.Mutex
	pending  []project.Delta
	draining bool
	stopped  bool
	out      chan project.Delta
	stop     chan struct{}
}

func newDeltaQueue() *deltaQueue {
	return &deltaQueue{
		out:  make(chan project.Delta, 256),
		stop: make(chan struct{}),
	}
}

func (q *deltaQueue) enqueue(ds []project.Delta) {
	if len(ds) == 0 {
		return
	}
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return
	}
	q.pending = append(q.pending, ds...)
	if !q.draining {
		q.draining = true
		go q.drain()
	}
	q.mu.Unlock()
}

func (q *deltaQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.pending = nil
			q.draining = false
			q.mu.Unlock()
			return
		}
		d := q.pending[0]
		q.mu.Unlock()

		select {
		case q.out <- d:
			q.mu.Lock()
			if len(q.pending) > 0 {
				q.pending = q.pending[1:]
			}
			q.mu.Unlock()
		case <-q.stop:
			return
		}
	}
}

func (q *deltaQueue) stopWith(ctx context.Context) {
	context.AfterFunc(ctx, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if !q.stopped {
			q.stopped = true
			q.pending = nil
			close(q.stop)
		}
	})
}
