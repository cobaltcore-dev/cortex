// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package podgroupsets

import (
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/delegation/podgroupsets"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type PodGroupSetPipeline struct {
}

func (p *PodGroupSetPipeline) Run(request podgroupsets.PodGroupSetPipelineRequest) (v1alpha1.DecisionResult, error) {
	// Map to store current node capacities
	nodeCapacities := make(map[string]corev1.ResourceList)
	for _, node := range request.Nodes {
		nodeCapacities[node.Name] = node.Status.Allocatable.DeepCopy()
	}

	targetPlacements := make(map[string]string)

	// Iterate over all pod groups
	for _, group := range request.PodGroupSet.Spec.PodGroups {
		for i := range int(group.Spec.Replicas) {
			// We construct a pod name to identify it in the placements.
			// The actual pod creation will need to match this or we map by index/key.
			// Using "GroupName-Index" as key relative to the set.
			podKey := fmt.Sprintf("%s-%d", group.Name, i)
			assigned := false

			// Try to find a node
			for _, node := range request.Nodes {
				nodeName := node.Name
				capacity := nodeCapacities[nodeName]

				if fits(capacity, group.Spec.PodSpec.Containers) {
					// Assign
					targetPlacements[podKey] = nodeName
					subtract(capacity, group.Spec.PodSpec.Containers)
					assigned = true
					break
				}
			}

			if !assigned {
				return v1alpha1.DecisionResult{}, fmt.Errorf("could not schedule pod %s of group %s: not enough resources", podKey, group.Name)
			}
		}
	}

	return v1alpha1.DecisionResult{
		TargetPlacements: targetPlacements,
	}, nil
}

func fits(available corev1.ResourceList, containers []corev1.Container) bool {
	reqs := getRequests(containers)
	for name, qty := range reqs {
		avail, ok := available[name]
		if !ok || avail.Cmp(qty) < 0 {
			return false
		}
	}
	return true
}

func subtract(available corev1.ResourceList, containers []corev1.Container) {
	reqs := getRequests(containers)
	for name, qty := range reqs {
		avail := available[name]
		avail.Sub(qty)
		available[name] = avail
	}
}

func getRequests(containers []corev1.Container) corev1.ResourceList {
	res := corev1.ResourceList{}
	for _, c := range containers {
		for name, qty := range c.Resources.Requests {
			if val, ok := res[name]; ok {
				val.Add(qty)
				res[name] = val
			} else {
				res[name] = qty.DeepCopy()
			}
		}
	}
	return res
}
