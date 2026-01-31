// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	corev1 "k8s.io/api/core/v1"
)

type TopologyLevelName string

// TODO: the topology levels should be configurable,
// for simplicity, these are hard-coded

// Hierarchy levels of the cluster's topology.
// Ordered from most corse-grained topology (e.g. region or zone)
// to fine-grained topolgoy (e.g. rack or blade-chassis).
var TopologyLevelNames []TopologyLevelName = []TopologyLevelName{
	TopologyLevelName("zone"),
	TopologyLevelName("rack"),
}

const TopologyRootLevel TopologyLevelName = "cluster"
const TopologyLeafLevel TopologyLevelName = "node"
const TopologyRootNodeName string = "root"
const TopologyLabelPrefix string = "cortex/topology-"

type Topology struct {
	Levels []TopologyLevelName
	Nodes  map[TopologyLevelName]map[string]*TopologyNode
}

type TopologyNode struct {
	Name        string
	Level       TopologyLevelName
	Capacity    corev1.ResourceList
	Allocatable corev1.ResourceList
	Nodes       []corev1.Node
}

func NewTopology(topologyLevels []TopologyLevelName, nodes []corev1.Node) *Topology {
	allLevels := append([]TopologyLevelName{TopologyRootLevel}, topologyLevels...)
	allLevels = append(allLevels, TopologyLeafLevel)

	topology := Topology{
		Levels: allLevels,
		Nodes: map[TopologyLevelName]map[string]*TopologyNode{
			TopologyRootLevel: make(map[string]*TopologyNode),
		},
	}

	topology.Nodes[TopologyRootLevel][TopologyRootNodeName] = &TopologyNode{
		Name:        TopologyRootNodeName,
		Level:       TopologyRootLevel,
		Capacity:    make(corev1.ResourceList),
		Allocatable: make(corev1.ResourceList),
		Nodes:       []corev1.Node{},
	}

	for _, level := range topologyLevels {
		topology.Nodes[level] = make(map[string]*TopologyNode)
	}
	topology.Nodes[TopologyLeafLevel] = make(map[string]*TopologyNode)
	for _, node := range nodes {
		// Skip control plane nodes - they should not be used for pod scheduling
		if isControlPlaneNode(node) {
			continue
		}
		topology.addNode(node)
	}
	return &topology
}

func (t *Topology) addNode(node corev1.Node) {
	topologyNodeName := ""
	for _, level := range t.Levels {
		if level == TopologyRootLevel {
			t.Nodes[TopologyRootLevel][TopologyRootNodeName].addNode(node)
			continue
		}
		if level == TopologyLeafLevel {
			t.Nodes[TopologyLeafLevel][node.Name] = &TopologyNode{
				Name:        node.Name,
				Level:       TopologyLeafLevel,
				Capacity:    node.Status.Capacity.DeepCopy(),
				Allocatable: node.Status.Allocatable.DeepCopy(),
				Nodes:       []corev1.Node{node},
			}
			continue
		}
		labelKey := fmt.Sprintf("%s%s", TopologyLabelPrefix, level)
		value, exists := node.Labels[labelKey]
		if !exists {
			break
		}
		topologyNodeName = fmt.Sprintf("%s-%s", topologyNodeName, value)
		if topologyNode, ok := t.Nodes[level][topologyNodeName]; ok {
			topologyNode.addNode(node)
		} else {
			t.Nodes[level][topologyNodeName] = &TopologyNode{
				Name:        value,
				Level:       level,
				Capacity:    node.Status.Capacity.DeepCopy(),
				Allocatable: node.Status.Allocatable.DeepCopy(),
				Nodes:       []corev1.Node{node},
			}
		}
	}
}

func (n *TopologyNode) addNode(node corev1.Node) {
	if isControlPlaneNode(node) {
		return
	}
	helpers.AddResourcesInto(n.Capacity, node.Status.Capacity)
	helpers.AddResourcesInto(n.Allocatable, node.Status.Allocatable)
	n.Nodes = append(n.Nodes, node)
}

// isControlPlaneNode checks if a node is a control plane node and should be excluded from pod scheduling
func isControlPlaneNode(node corev1.Node) bool {
	// Check for common control plane taints
	for _, taint := range node.Spec.Taints {
		switch taint.Key {
		case "node-role.kubernetes.io/master":
			return true
		case "node-role.kubernetes.io/control-plane":
			return true
		}
	}

	// Check for common control plane labels
	if node.Labels != nil {
		if _, exists := node.Labels["node-role.kubernetes.io/master"]; exists {
			return true
		}
		if _, exists := node.Labels["node-role.kubernetes.io/control-plane"]; exists {
			return true
		}
	}

	return false
}
