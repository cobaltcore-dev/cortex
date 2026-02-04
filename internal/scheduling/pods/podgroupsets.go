// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const podPipelineRefName string = "pods-scheduler"

func (s *Scheduler) schedulePodGroupSet(ctx context.Context, pgs *v1alpha1.PodGroupSet) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	podGroupSetResourceRequests := make(corev1.ResourceList)
	for _, group := range pgs.Spec.PodGroups {
		for range group.Spec.Replicas {
			podResources := helpers.GetPodResourceRequests(&corev1.Pod{Spec: group.Spec.PodSpec})
			helpers.AddResourcesInto(podGroupSetResourceRequests, podResources)
		}
	}

	topology := s.Cache.GetTopology()

	var bestPlacements map[string]string
	var bestWeight float64

	for _, level := range slices.Backward(topology.Levels) {
		for _, topologyNode := range topology.Nodes[level] {
			canFit := true
			for resourceName, requestedQty := range podGroupSetResourceRequests {
				allocatableQty, exists := topologyNode.Allocatable[resourceName]
				if !exists || requestedQty.Cmp(allocatableQty) > 0 {
					canFit = false
					break
				}
			}
			if !canFit {
				s.Logger.Info("no topology fit", "node", topologyNode.Name)
				continue
			}

			_, ok := s.podPipelineController.PipelineConfigs[podPipelineRefName]
			if !ok {
				return fmt.Errorf("pipeline %s not configured", podPipelineRefName)
			}
			pipeline, ok := s.podPipelineController.Pipelines[podPipelineRefName]
			if !ok {
				return fmt.Errorf("pipeline %s not found", podPipelineRefName)
			}
			placements, weight, err := s.getPodGroupSetPlacement(pgs, topologyNode.Nodes, pipeline)
			if err != nil {
				log.V(1).Error(err, "failed to schedule PodGroupSet")
				continue
			}

			if len(placements) == 0 {
				s.Logger.Info("no pipeline placement fit", "node", topologyNode.Name)
				continue
			}

			if weight > bestWeight {
				bestPlacements = placements
				bestWeight = weight
			}
		}
		if len(bestPlacements) > 0 {
			break
		}
	}

	if len(bestPlacements) > 0 {
		// Update PodGroupSet status with placements
		if err := s.updatePodGroupSetStatus(ctx, pgs, bestPlacements); err != nil {
			log.Error(err, "failed to update PodGroupSet status", "PodGroupSet", pgs.Name)
			// Don't return error here as pods are already created successfully
		}

		if err := s.createPods(ctx, pgs, bestPlacements); err != nil {
			return err
		}

		s.Recorder.Eventf(pgs, nil, corev1.EventTypeNormal, "Scheduled", "SchedulePGS", "Successfully created pods of %s/%s", pgs.Namespace, pgs.Name)
	} else {
		log.Info("no suitable placement found", "PodGroupSet", pgs.Name)
		s.Recorder.Eventf(pgs, nil, corev1.EventTypeWarning, "FailedScheduling", "SchedulePGS", "No suitable placement found for PodGroupSet %s (%s)", pgs.Name, pgs.Namespace)
		return failedSchedulingError
	}

	log.Info("PodGroupSet processed", "duration", time.Since(startedAt))
	return nil
}

func (s *Scheduler) getPodGroupSetPlacement(pgs *v1alpha1.PodGroupSet, nodes []corev1.Node, podPipeline lib.FilterWeigherPipeline[pods.PodPipelineRequest]) (map[string]string, float64, error) {
	// TODO: the nodePool behavior mimics the cache which is not optimal.
	// The problem is that we are currently iterating over the topology which would
	// get modified if the cache changes
	nodePool := make([]corev1.Node, len(nodes))
	for i, node := range nodes {
		nodePool[i] = *node.DeepCopy()
	}

	targetPlacements := make(map[string]string)
	placementWeight := 0.0

	for _, group := range pgs.Spec.PodGroups {
		for i := range int(group.Spec.Replicas) {
			podName := pgs.PodName(group.Name, i)

			podRequest := pods.PodPipelineRequest{
				Nodes: nodePool,
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: pgs.Namespace,
					},
					Spec: group.Spec.PodSpec,
				},
			}

			result, err := podPipeline.Run(podRequest)
			if err != nil {
				return nil, 0, fmt.Errorf("pod pipeline failed for pod %s: %w", podName, err)
			}
			if result.TargetHost == nil {
				return nil, 0, nil
			}

			nodeName := *result.TargetHost
			targetPlacements[podName] = nodeName
			placementWeight += result.AggregatedOutWeights[nodeName]

			podResourceRequests := helpers.GetPodResourceRequests(podRequest.Pod)
			for i := range nodePool {
				if nodePool[i].Name == nodeName {
					helpers.SubtractResourcesInto(nodePool[i].Status.Allocatable, podResourceRequests)
					break
				}
			}
		}
	}

	return targetPlacements, placementWeight, nil
}

func (s *Scheduler) createPods(ctx context.Context, pgs *v1alpha1.PodGroupSet, placements map[string]string) error {
	log := ctrl.LoggerFrom(ctx)

	// TODO: this needs to happen in two steps:
	// 1. Create a PodReservation (new CR) for each pod
	// If not successfull, delete reservations and reprocess PGS
	// 2. Create pods and bind to node with respective reservation
	// and in doing so delete the reservations

	for _, group := range pgs.Spec.PodGroups {
		for i := range int(group.Spec.Replicas) {
			podName := pgs.PodName(group.Name, i)
			nodeName, ok := placements[podName]
			if !ok {
				log.Info("No placement for pod", "key", podName)
				continue
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: pgs.Namespace,
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(pgs, v1alpha1.GroupVersion.WithKind("PodGroupSet")),
					},
				},
				Spec: group.Spec.PodSpec,
			}
			pod.Spec.SchedulerName = string(v1alpha1.SchedulingDomainPods)
			if err := s.Client.Create(ctx, pod); err != nil {
				return err
			}

			// Assume that the binding succeeds and mark resources as allocated
			pod.Spec.NodeName = nodeName
			s.Cache.AddPod(pod)

			binding := &corev1.Binding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: pgs.Namespace,
				},
				Target: corev1.ObjectReference{
					Kind: "Node",
					Name: nodeName,
				},
			}
			if err := s.Client.Create(ctx, binding); err != nil {
				log.V(1).Error(err, "failed to assign node to pod via binding")
				s.Cache.RemovePod(pod)
				return err
			}
			s.Recorder.Eventf(pod, nil, corev1.EventTypeNormal, "Scheduled", "SchedulePod", "Successfully assigned %s/%s to %s", pod.Namespace, pod.Name, nodeName)
			log.Info("created pod", "pod", podName, "node", nodeName)
		}
	}
	return nil
}

func (s *Scheduler) updatePodGroupSetStatus(ctx context.Context, pgs *v1alpha1.PodGroupSet, placements map[string]string) error {
	// Create a copy of the PodGroupSet to modify its status
	pgsCopy := pgs.DeepCopy()

	// Set the phase to Scheduled
	pgsCopy.Status.Phase = v1alpha1.PodGroupSetPhaseScheduled

	// Convert placements map to PodPlacement slice
	pgsCopy.Status.Placements = make([]v1alpha1.PodPlacement, 0, len(placements))
	for podName, nodeName := range placements {
		pgsCopy.Status.Placements = append(pgsCopy.Status.Placements, v1alpha1.PodPlacement{
			PodName:  podName,
			NodeName: nodeName,
		})
	}

	// Update the status subresource
	return s.Client.Status().Update(ctx, pgsCopy)
}
