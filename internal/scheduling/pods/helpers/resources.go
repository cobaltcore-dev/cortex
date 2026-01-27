// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package helpers

import (
	corev1 "k8s.io/api/core/v1"
)

// GetPodResourceRequests calculates the effective resource requests for a pod
// by summing container requests and taking the max of init container requests.
func GetPodResourceRequests(pod corev1.Pod) corev1.ResourceList {
	requests := make(corev1.ResourceList)

	for _, container := range pod.Spec.Containers {
		AddResourcesInto(requests, container.Resources.Requests)
	}

	// Init containers run sequentially to other containers,
	// thus the maximum of all requests is determined
	initRequests := make(corev1.ResourceList)
	for _, initContainer := range pod.Spec.InitContainers {
		MaxResourcesInto(initRequests, initContainer.Resources.Requests)
	}
	MaxResourcesInto(requests, initRequests)

	return requests
}

// AddResourcesInto modifies dst in-place by adding the quantity of each resource of src.
func AddResourcesInto(dst, src corev1.ResourceList) {
	for resource, qty := range src {
		if existing, ok := dst[resource]; ok {
			qty.Add(existing)
		}
		dst[resource] = qty
	}
}

// MaxResourcesInto modifies dst in-place by taking the maximum quantity of the resources of dst and src.
func MaxResourcesInto(dst, src corev1.ResourceList) {
	for resource, qty := range src {
		if existing, ok := dst[resource]; !ok || qty.Cmp(existing) > 0 {
			dst[resource] = qty
		}
	}
}
