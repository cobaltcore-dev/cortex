// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"fmt"
	"sync"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	corev1 "k8s.io/api/core/v1"
)

type Cache struct {
	// Mutex to serialize updates/access of the cache
	mu sync.RWMutex
	// State of the nodes available for scheduling
	Nodes []corev1.Node
	// State of the cluster's topology which contains
	// all nodes available for scheduling
	Topology *Topology

	nodeAllocated map[string]corev1.ResourceList
}

func NewCache() *Cache {
	return &Cache{
		Nodes:         make([]corev1.Node, 0),
		nodeAllocated: make(map[string]corev1.ResourceList),
	}
}

func (c *Cache) GetNodes() []corev1.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()

	nodes := make([]corev1.Node, len(c.Nodes))
	copy(nodes, c.Nodes)
	return nodes
}

func (c *Cache) GetTopology() *Topology {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.Topology
}

func (c *Cache) AddPod(pod *corev1.Pod) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if pod.Spec.NodeName == "" {
		return
	}

	podRequests := helpers.GetPodResourceRequests(pod)

	if allocated, exists := c.nodeAllocated[pod.Spec.NodeName]; exists {
		helpers.AddResourcesInto(allocated, podRequests)
		c.nodeAllocated[pod.Spec.NodeName] = allocated
	} else {
		c.nodeAllocated[pod.Spec.NodeName] = podRequests.DeepCopy()
	}

	c.updateNodeAllocatable(pod.Spec.NodeName)

	c.updateTopology()
}

func (c *Cache) RemovePod(pod *corev1.Pod) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if pod.Spec.NodeName == "" {
		fmt.Printf("@@@ pod %s", pod.Name)
		return
	}

	podRequests := helpers.GetPodResourceRequests(pod)

	if allocated, exists := c.nodeAllocated[pod.Spec.NodeName]; exists {
		helpers.SubtractResourcesInto(allocated, podRequests)
		c.nodeAllocated[pod.Spec.NodeName] = allocated
	}

	c.updateNodeAllocatable(pod.Spec.NodeName)

	c.updateTopology()
}

func (c *Cache) AddNode(node *corev1.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, existingNode := range c.Nodes {
		if existingNode.Name == node.Name {
			// Update existing node
			c.Nodes[i] = *node.DeepCopy()
			c.updateTopology()
			return
		}
	}

	c.Nodes = append(c.Nodes, *node.DeepCopy())

	if _, exists := c.nodeAllocated[node.Name]; !exists {
		c.nodeAllocated[node.Name] = make(corev1.ResourceList)
	}

	c.updateTopology()
}

func (c *Cache) RemoveNode(node *corev1.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, existingNode := range c.Nodes {
		if existingNode.Name == node.Name {
			// Remove by swapping with last element and truncating
			c.Nodes[i] = c.Nodes[len(c.Nodes)-1]
			c.Nodes = c.Nodes[:len(c.Nodes)-1]
			break
		}
	}

	delete(c.nodeAllocated, node.Name)

	c.updateTopology()
}

// updateNodeAllocatable updates the allocatable resources for a specific node
// This method assumes the cache mutex is already held for writing
func (c *Cache) updateNodeAllocatable(nodeName string) {
	for i, node := range c.Nodes {
		if node.Name == nodeName {
			// Calculate remaining resources from the original capacity
			remaining := node.Status.Capacity.DeepCopy()

			if allocated, exists := c.nodeAllocated[nodeName]; exists {
				helpers.SubtractResourcesInto(remaining, allocated)
			} else {
				fmt.Printf("@@@ %s", nodeName)
			}

			c.Nodes[i].Status.Allocatable = remaining
			break
		}
	}
}

// updateTopology rebuilds the topology with current nodes
// This method assumes the cache mutex is already held for writing
func (c *Cache) updateTopology() {
	// TODO: rebuilding the topology from scratch on each update is highly inefficient.
	// Implement behavior to only update the parts that have changed
	c.Topology = NewTopology(TopologyLevelNames, c.Nodes)
}
