package pods

import (
	"container/heap"
	"sync"
	"time"
)

type SchedulingQueue interface {
	Add(item SchedulingItem)
	Get() (SchedulingItem, bool)
	Done(item SchedulingItem)

	AddBackoff(item SchedulingItem)
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
	GetSize() int
}

type queueItem struct {
	item SchedulingItem

	index int

	enqueueTime time.Time

	backoffDuration time.Duration
	readyAt         time.Time
}

type priorityHeap []*queueItem

func (h priorityHeap) Len() int { return len(h) }

func (h priorityHeap) Less(i, j int) bool {
	// Priority ordering:
	// 1. PodGroupSets before Pods
	// 2. For PodGroupSets: larger pod count first
	// 3. For Pods: earlier enqueue time first (FIFO)

	itemI, itemJ := h[i].item, h[j].item
	kindI, kindJ := itemI.Kind(), itemJ.Kind()

	// PodGroupSets always come before Pods
	if kindI == KindPodGroupSet && kindJ == KindPod {
		return true
	}
	if kindI == KindPod && kindJ == KindPodGroupSet {
		return false
	}

	// Both are PodGroupSets - order by pod count (larger first)
	if kindI == KindPodGroupSet && kindJ == KindPodGroupSet {
		sizeI, sizeJ := itemI.GetSize(), itemJ.GetSize()
		if sizeI != sizeJ {
			return sizeI > sizeJ // Larger PodGroupSets first
		}
		// Same size - use enqueue time (earlier first)
		return h[i].enqueueTime.Before(h[j].enqueueTime)
	}

	// Both are Pods - order by enqueue time (earlier first)
	if kindI == KindPod && kindJ == KindPod {
		return h[i].enqueueTime.Before(h[j].enqueueTime)
	}

	return h[i].enqueueTime.Before(h[j].enqueueTime)
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

// PrioritySchedulingQueue implements SchedulingQueue
type PrioritySchedulingQueue struct {
	lock sync.Mutex
	cond *sync.Cond

	// Contains items that should be scheduled immeadietly
	activeQ priorityHeap
	// Contains items that failed to schedule but are expected to get scheduled eventually (e.g. pipeline not ready)
	backoffQ []*queueItem
	// Contains items that are waiting for events to happen (e.g. new capacity)
	unschedQ map[string]*queueItem

	// items is used to retain the state of each queueing item (e.g. enqueue time) when moving between queues
	items map[string]*queueItem

	shuttingDown bool
}

func NewPrioritySchedulingQueue() *PrioritySchedulingQueue {
	q := &PrioritySchedulingQueue{
		activeQ:  priorityHeap{},
		backoffQ: []*queueItem{},
		unschedQ: make(map[string]*queueItem),
		items:    make(map[string]*queueItem),
	}
	q.cond = sync.NewCond(&q.lock)
	heap.Init(&q.activeQ)

	go q.flushBackoffPeriodically()

	return q
}

func (q *PrioritySchedulingQueue) Add(item SchedulingItem) {
	q.lock.Lock()
	defer q.lock.Unlock()

	if q.shuttingDown {
		return
	}

	key := item.Key()
	if _, exists := q.items[key]; exists {
		return
	}

	qi := &queueItem{
		item:        item,
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
	q.lock.Lock()
	defer q.lock.Unlock()

	key := item.Key()
	delete(q.items, key)
}

func (q *PrioritySchedulingQueue) AddUnschedulable(item SchedulingItem) {
	q.lock.Lock()
	defer q.lock.Unlock()

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

	// TODO: this should not move all pods to the active queue per default.
	// Instead, only pods that would benefit from reason should be
	// reconsidered for scheduling (see kube-scheduler queueing hints)

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

func (q *PrioritySchedulingQueue) AddBackoff(item SchedulingItem) {
	q.lock.Lock()
	defer q.lock.Unlock()

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

// flushBackoffPeriodically runs in a background goroutine to periodically
// move items from backoff queue to active queue when they're ready
func (q *PrioritySchedulingQueue) flushBackoffPeriodically() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		q.lock.Lock()
		if q.shuttingDown {
			q.lock.Unlock()
			return
		}

		before := len(q.backoffQ)
		q.flushBackoffLocked()
		after := len(q.backoffQ)

		// Signal waiters if we moved items to active queue
		if before > after && q.activeQ.Len() > 0 {
			q.cond.Signal()
		}
		q.lock.Unlock()
	}
}

// TODO: these definitions are duplicates apart from `Kind()`

// PodSchedulingItem implements SchedulingItem
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

func (p *PodSchedulingItem) GetSize() int {
	return 1
}

// PodGroupSetSchedulingItem implements SchedulingItem
type PodGroupSetSchedulingItem struct {
	Namespace string
	Name      string
	PodCount  int // Number of pods in this PodGroupSet
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

func (pgs *PodGroupSetSchedulingItem) GetSize() int {
	return pgs.PodCount
}
