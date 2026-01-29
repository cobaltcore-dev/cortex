// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"container/heap"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	corev1 "k8s.io/api/core/v1"
)

// SchedulableItem represents an item that can be scheduled (Pod or PodGroupSet)
type SchedulableItem interface {
	// GetResourceRequests returns the total resource requirements for this item
	GetResourceRequests() corev1.ResourceList
	// GetKey returns a unique identifier for this item
	GetKey() string
	// IsPod returns true if this item is a Pod
	IsPod() bool
	// GetObject returns the underlying object
	GetObject() interface{}
}

// PodItem wraps a Pod for scheduling
type PodItem struct {
	Pod *corev1.Pod
}

func (p *PodItem) GetResourceRequests() corev1.ResourceList {
	return helpers.GetPodResourceRequests(*p.Pod)
}

func (p *PodItem) GetKey() string {
	return p.Pod.Namespace + "/" + p.Pod.Name
}

func (p *PodItem) IsPod() bool {
	return true
}

func (p *PodItem) GetObject() interface{} {
	return p.Pod
}

// PodGroupSetItem wraps a PodGroupSet for scheduling
type PodGroupSetItem struct {
	PodGroupSet *v1alpha1.PodGroupSet
}

func (pgs *PodGroupSetItem) GetResourceRequests() corev1.ResourceList {
	totalRequests := make(corev1.ResourceList)

	for _, group := range pgs.PodGroupSet.Spec.PodGroups {
		// Calculate resources for one pod in this group
		podResources := helpers.GetPodResourceRequests(corev1.Pod{Spec: group.Spec.PodSpec})

		// Multiply by replicas and add to total
		for resource, quantity := range podResources {
			totalQuantity := quantity.DeepCopy()
			totalQuantity.Set(totalQuantity.Value() * int64(group.Spec.Replicas))

			if existing, ok := totalRequests[resource]; ok {
				existing.Add(totalQuantity)
				totalRequests[resource] = existing
			} else {
				totalRequests[resource] = totalQuantity
			}
		}
	}

	return totalRequests
}

func (pgs *PodGroupSetItem) GetKey() string {
	return pgs.PodGroupSet.Namespace + "/" + pgs.PodGroupSet.Name
}

func (pgs *PodGroupSetItem) IsPod() bool {
	return false
}

func (pgs *PodGroupSetItem) GetObject() interface{} {
	return pgs.PodGroupSet
}

// queueItem is used internally by the priority queue
type queueItem struct {
	item     SchedulableItem
	priority int64 // Higher values = higher priority (larger resources first)
	index    int   // Index in the heap
}

// priorityQueue implements a max-heap for scheduling items
type priorityQueue []*queueItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// Max heap - larger priority values come first
	return pq[i].priority > pq[j].priority
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*queueItem)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// SchedulingQueue holds pods and PodGroupSets that need to be scheduled
// Items are sorted by resource requests (largest first)
type SchedulingQueue struct {
	mu    sync.RWMutex
	queue priorityQueue
	items map[string]*queueItem // key -> queueItem for fast lookup
}

// NewSchedulingQueue creates a new scheduling queue
func NewSchedulingQueue() *SchedulingQueue {
	sq := &SchedulingQueue{
		items: make(map[string]*queueItem),
	}
	heap.Init(&sq.queue)
	return sq
}

// calculatePriority calculates priority based on total resource requests
// CPU and Memory are weighted, with larger values getting higher priority
func (sq *SchedulingQueue) calculatePriority(resources corev1.ResourceList) int64 {
	var priority int64

	// Weight CPU highly (convert to millicores)
	if cpu, ok := resources[corev1.ResourceCPU]; ok {
		priority += cpu.MilliValue() * 1000 // CPU weight = 1000
	}

	// Weight Memory (convert to bytes)
	if memory, ok := resources[corev1.ResourceMemory]; ok {
		priority += memory.Value() / 1024 / 1024 // Memory weight = 1 per MB
	}

	// Add other resources with lower weight
	for resourceName, quantity := range resources {
		if resourceName != corev1.ResourceCPU && resourceName != corev1.ResourceMemory {
			priority += quantity.Value() / 100 // Other resources weight = 0.01
		}
	}

	return priority
}

// Add adds a schedulable item to the queue
func (sq *SchedulingQueue) Add(item SchedulableItem) {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	key := item.GetKey()

	// Remove existing item if present
	if existingItem, exists := sq.items[key]; exists {
		heap.Remove(&sq.queue, existingItem.index)
		delete(sq.items, key)
	}

	// Calculate priority based on resource requests
	resources := item.GetResourceRequests()
	priority := sq.calculatePriority(resources)

	// Create queue item and add to heap
	qItem := &queueItem{
		item:     item,
		priority: priority,
	}

	heap.Push(&sq.queue, qItem)
	sq.items[key] = qItem
}

// AddPod adds a pod to the scheduling queue
func (sq *SchedulingQueue) AddPod(pod *corev1.Pod) {
	item := &PodItem{Pod: pod}
	sq.Add(item)
}

// AddPodGroupSet adds a PodGroupSet to the scheduling queue
func (sq *SchedulingQueue) AddPodGroupSet(pgs *v1alpha1.PodGroupSet) {
	item := &PodGroupSetItem{PodGroupSet: pgs}
	sq.Add(item)
}

// Remove removes an item from the queue by key
func (sq *SchedulingQueue) Remove(key string) {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if qItem, exists := sq.items[key]; exists {
		heap.Remove(&sq.queue, qItem.index)
		delete(sq.items, key)
	}
}

// RemovePod removes a pod from the queue
func (sq *SchedulingQueue) RemovePod(pod *corev1.Pod) {
	key := pod.Namespace + "/" + pod.Name
	sq.Remove(key)
}

// RemovePodGroupSet removes a PodGroupSet from the queue
func (sq *SchedulingQueue) RemovePodGroupSet(pgs *v1alpha1.PodGroupSet) {
	key := pgs.Namespace + "/" + pgs.Name
	sq.Remove(key)
}

// Pop returns the highest priority item from the queue
// Returns nil if the queue is empty
func (sq *SchedulingQueue) Pop() SchedulableItem {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if sq.queue.Len() == 0 {
		return nil
	}

	qItem := heap.Pop(&sq.queue).(*queueItem)
	delete(sq.items, qItem.item.GetKey())

	return qItem.item
}

// Peek returns the highest priority item without removing it
// Returns nil if the queue is empty
func (sq *SchedulingQueue) Peek() SchedulableItem {
	sq.mu.RLock()
	defer sq.mu.RUnlock()

	if sq.queue.Len() == 0 {
		return nil
	}

	return sq.queue[0].item
}

// Len returns the number of items in the queue
func (sq *SchedulingQueue) Len() int {
	sq.mu.RLock()
	defer sq.mu.RUnlock()
	return sq.queue.Len()
}

// IsEmpty returns true if the queue has no items
func (sq *SchedulingQueue) IsEmpty() bool {
	return sq.Len() == 0
}

// Contains checks if an item with the given key exists in the queue
func (sq *SchedulingQueue) Contains(key string) bool {
	sq.mu.RLock()
	defer sq.mu.RUnlock()
	_, exists := sq.items[key]
	return exists
}

// ContainsPod checks if a pod exists in the queue
func (sq *SchedulingQueue) ContainsPod(pod *corev1.Pod) bool {
	key := pod.Namespace + "/" + pod.Name
	return sq.Contains(key)
}

// ContainsPodGroupSet checks if a PodGroupSet exists in the queue
func (sq *SchedulingQueue) ContainsPodGroupSet(pgs *v1alpha1.PodGroupSet) bool {
	key := pgs.Namespace + "/" + pgs.Name
	return sq.Contains(key)
}

// List returns all items in the queue (sorted by priority, highest first)
func (sq *SchedulingQueue) List() []SchedulableItem {
	sq.mu.RLock()
	defer sq.mu.RUnlock()

	items := make([]SchedulableItem, 0, len(sq.queue))
	for _, qItem := range sq.queue {
		items = append(items, qItem.item)
	}

	return items
}

// TriggerRescheduling processes items from the queue by calling the provided processors
// This method should be called when nodes are added or pods are deleted
func (sq *SchedulingQueue) TriggerRescheduling(podProcessor func(*corev1.Pod) error, pgsProcessor func(*v1alpha1.PodGroupSet) error) {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	// Process all items in the queue, starting with highest priority
	itemsToProcess := make([]SchedulableItem, 0, len(sq.queue))
	for len(sq.queue) > 0 {
		qItem := heap.Pop(&sq.queue).(*queueItem)
		delete(sq.items, qItem.item.GetKey())
		itemsToProcess = append(itemsToProcess, qItem.item)
	}

	// Release the mutex before processing to avoid blocking other operations
	sq.mu.Unlock()

	// Process items outside of the lock
	for _, item := range itemsToProcess {
		if item.IsPod() {
			if podProcessor != nil {
				if podItem, ok := item.(*PodItem); ok {
					if err := podProcessor(podItem.Pod); err != nil {
						// If processing fails, add the item back to the queue
						sq.mu.Lock()
						sq.addItemLocked(item)
						sq.mu.Unlock()
					}
				}
			}
		} else {
			if pgsProcessor != nil {
				if pgsItem, ok := item.(*PodGroupSetItem); ok {
					if err := pgsProcessor(pgsItem.PodGroupSet); err != nil {
						// If processing fails, add the item back to the queue
						sq.mu.Lock()
						sq.addItemLocked(item)
						sq.mu.Unlock()
					}
				}
			}
		}
	}

	// Reacquire the lock for the defer unlock
	sq.mu.Lock()
}

// addItemLocked adds an item to the queue without locking (internal use only)
func (sq *SchedulingQueue) addItemLocked(item SchedulableItem) {
	key := item.GetKey()

	// Remove existing item if present
	if existingItem, exists := sq.items[key]; exists {
		heap.Remove(&sq.queue, existingItem.index)
		delete(sq.items, key)
	}

	// Calculate priority based on resource requests
	resources := item.GetResourceRequests()
	priority := sq.calculatePriority(resources)

	// Create queue item and add to heap
	qItem := &queueItem{
		item:     item,
		priority: priority,
	}

	heap.Push(&sq.queue, qItem)
	sq.items[key] = qItem
}
