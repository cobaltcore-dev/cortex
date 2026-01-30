package pods

import (
	"container/heap"
	"fmt"
	"sync"
	"time"
)

//
// Public interfaces
//

type SchedulingQueue interface {
	Add(item SchedulingItem)
	Get() (SchedulingItem, bool)
	Done(item SchedulingItem)

	AddUnschedulable(item SchedulingItem)
	MoveAllToActive(reason string)

	ShutDown()
}

type ItemKind string

const (
	KindPod         ItemKind = "Pod"
	KindPodGroupSet ItemKind = "PodGroupSet"
)

type SchedulingItem interface {
	Key() string
	Kind() ItemKind
	String() string
}

//
// Internal queue item
//

type queueItem struct {
	item SchedulingItem

	priority int
	index    int

	enqueueTime time.Time

	// backoff handling
	backoffDuration time.Duration
	readyAt         time.Time
}

//
// Priority heap (Active Queue)
//

type priorityHeap []*queueItem

func (h priorityHeap) Len() int { return len(h) }

func (h priorityHeap) Less(i, j int) bool {
	// Higher priority first
	return h[i].priority > h[j].priority
}

func (h priorityHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *priorityHeap) Push(x interface{}) {
	qi := x.(*queueItem)
	qi.index = len(*h)
	*h = append(*h, qi)
}

func (h *priorityHeap) Pop() interface{} {
	old := *h
	n := len(old)
	qi := old[n-1]
	old[n-1] = nil
	qi.index = -1
	*h = old[:n-1]
	return qi
}

//
// Scheduling queue implementation
//

type PrioritySchedulingQueue struct {
	lock sync.Mutex
	cond *sync.Cond

	activeQ  priorityHeap
	backoffQ []*queueItem
	unschedQ map[string]*queueItem

	// global deduplication
	items map[string]*queueItem

	shuttingDown bool
}

//
// Constructor
//

func NewPrioritySchedulingQueue() *PrioritySchedulingQueue {
	q := &PrioritySchedulingQueue{
		activeQ:  priorityHeap{},
		backoffQ: []*queueItem{},
		unschedQ: make(map[string]*queueItem),
		items:    make(map[string]*queueItem),
	}
	q.cond = sync.NewCond(&q.lock)
	heap.Init(&q.activeQ)
	return q
}

//
// Public API
//

func (q *PrioritySchedulingQueue) Add(item SchedulingItem) {
	q.lock.Lock()
	defer q.lock.Unlock()

	fmt.Printf(`add to active queue %s`, item.Key())

	if q.shuttingDown {
		return
	}

	key := item.Key()
	if _, exists := q.items[key]; exists {
		return
	}

	qi := &queueItem{
		item: item,
		// TODO: implement LGFS (largest gang served first)
		// for pods maybe do largest resource request first, weighted similarly to binpack weigher
		priority:    0, // placeholder
		enqueueTime: time.Now(),
	}

	q.items[key] = qi
	heap.Push(&q.activeQ, qi)
	q.cond.Signal()
}

func (q *PrioritySchedulingQueue) Get() (SchedulingItem, bool) {
	q.lock.Lock()
	defer q.lock.Unlock()

	for {
		if q.shuttingDown {
			return nil, true
		}

		q.flushBackoffLocked()

		if q.activeQ.Len() > 0 {
			qi := heap.Pop(&q.activeQ).(*queueItem)
			return qi.item, false
		}

		q.cond.Wait()
	}
}

func (q *PrioritySchedulingQueue) Done(item SchedulingItem) {
	// Currently no-op.
	// Hook for metrics, tracing, or future state tracking.

	fmt.Printf(`queue done %s`, item.Key())
}

func (q *PrioritySchedulingQueue) AddUnschedulable(item SchedulingItem) {
	q.lock.Lock()
	defer q.lock.Unlock()

	fmt.Printf(`queue unscheduable %s`, item.Key())

	key := item.Key()
	qi, exists := q.items[key]
	if !exists {
		return
	}

	q.unschedQ[key] = qi
}

func (q *PrioritySchedulingQueue) MoveAllToActive(reason string) {
	q.lock.Lock()
	defer q.lock.Unlock()

	fmt.Printf(`queue move all to active %s`, reason)

	for key, qi := range q.unschedQ {
		delete(q.unschedQ, key)
		heap.Push(&q.activeQ, qi)
	}

	q.cond.Broadcast()
}

func (q *PrioritySchedulingQueue) ShutDown() {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.shuttingDown = true
	q.cond.Broadcast()
}

//
// Backoff handling (internal)
//

func (q *PrioritySchedulingQueue) AddBackoff(item SchedulingItem) {
	q.lock.Lock()
	defer q.lock.Unlock()

	fmt.Printf(`add backoff %s`, item.Key())

	qi, exists := q.items[item.Key()]
	if !exists {
		return
	}

	qi.backoffDuration = nextBackoff(qi.backoffDuration)
	qi.readyAt = time.Now().Add(qi.backoffDuration)

	q.backoffQ = append(q.backoffQ, qi)
}

func (q *PrioritySchedulingQueue) flushBackoffLocked() {
	now := time.Now()
	n := 0

	for _, qi := range q.backoffQ {
		if qi.readyAt.Before(now) || qi.readyAt.Equal(now) {
			heap.Push(&q.activeQ, qi)
		} else {
			q.backoffQ[n] = qi
			n++
		}
	}

	q.backoffQ = q.backoffQ[:n]
}

func nextBackoff(prev time.Duration) time.Duration {
	if prev == 0 {
		return 1 * time.Second
	}

	next := prev * 2
	max := 60 * time.Second
	if next > max {
		return max
	}
	return next
}

//
// SchedulingItem implementations
//

// PodSchedulingItem wraps a Pod with metadata for the scheduling queue
type PodSchedulingItem struct {
	Namespace string
	Name      string
}

func (p *PodSchedulingItem) Key() string {
	return p.Namespace + "/" + p.Name
}

func (p *PodSchedulingItem) Kind() ItemKind {
	return KindPod
}

func (p *PodSchedulingItem) String() string {
	return "Pod(" + p.Key() + ")"
}

// PodGroupSetSchedulingItem wraps a PodGroupSet with metadata for the scheduling queue
type PodGroupSetSchedulingItem struct {
	Namespace string
	Name      string
}

func (pgs *PodGroupSetSchedulingItem) Key() string {
	return pgs.Namespace + "/" + pgs.Name
}

func (pgs *PodGroupSetSchedulingItem) Kind() ItemKind {
	return KindPodGroupSet
}

func (pgs *PodGroupSetSchedulingItem) String() string {
	return "PodGroupSet(" + pgs.Key() + ")"
}
