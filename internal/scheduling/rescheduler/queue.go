// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SchedulingQueue holds rescheduling requests waiting to be scheduled. The
// reconciler adds Acknowledged requests to it and the scheduler loop pops them.
// It is modelled after the kube-scheduler scheduling queue: Pop blocks until an
// item is available so the scheduler goroutine hangs on the next request, and
// Close unblocks it on shutdown.
//
// The implementation is an in-memory FIFO for now; it can be replaced by a
// priority queue without changing callers.
type SchedulingQueue interface {
	// Add enqueues the request identified by key. A key may be enqueued more
	// than once; the consumer re-validates each popped request, so duplicate or
	// stale entries are harmless.
	Add(key client.ObjectKey)
	// Pop returns the next request, blocking until one is available. It returns
	// false once the queue is closed and drained.
	Pop() (client.ObjectKey, bool)
	// Run starts any background processing. It is a no-op for the FIFO queue and
	// exists so a future priority queue can flush backoff etc.
	Run(ctx context.Context)
	// Close stops the queue and unblocks any goroutine waiting in Pop.
	Close()
}

// fifoQueue is an in-memory, first-in-first-out SchedulingQueue. Pop blocks on
// a condition variable and is woken by Add or Close.
type fifoQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []client.ObjectKey
	closed bool
}

// NewFIFOQueue returns an empty, ready-to-use FIFO SchedulingQueue.
func NewFIFOQueue() SchedulingQueue {
	q := &fifoQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *fifoQueue) Add(key client.ObjectKey) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, key)
	q.cond.Signal()
}

func (q *fifoQueue) Pop() (client.ObjectKey, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return client.ObjectKey{}, false
	}
	key := q.items[0]
	q.items = q.items[1:]
	return key, true
}

func (q *fifoQueue) Run(context.Context) {}

func (q *fifoQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}
