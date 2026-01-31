// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"errors"
	"time"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (s *Scheduler) schedulePod(ctx context.Context, pod *corev1.Pod) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	pipeline, ok := s.podPipelineController.Pipelines[podPipelineRefName]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", podPipelineRefName)
		return errors.New("pipeline not found or not ready")
	}

	nodes := s.Cache.GetNodes()

	// Execute the scheduling pipeline.
	request := pods.PodPipelineRequest{Nodes: nodes, Pod: pod}
	result, err := pipeline.Run(request)
	if err != nil {
		log.V(1).Error(err, "failed to run filter-weigher pipeline")
		return errors.New("failed to run filter-weigher pipeline")
	}
	log.Info("filter-weigher pipeline executed successfully", "duration", time.Since(startedAt))

	if result.TargetHost == nil {
		s.Recorder.Eventf(pod, nil, corev1.EventTypeWarning, "FailedScheduling", "SchedulePod", "0/%d nodes are available", len(nodes))
		s.Queue.Add(&PodSchedulingItem{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		})
		return failedSchedulingError
	}

	// Assume that the binding succeeds and mark resources as allocated
	pod.Spec.NodeName = *result.TargetHost
	s.Cache.AddPod(pod)

	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: *result.TargetHost,
		},
	}
	if err := s.Client.Create(ctx, binding); err != nil {
		log.V(1).Error(err, "failed to assign node to pod via binding")
		s.Cache.RemovePod(pod)

		s.Queue.Add(&PodSchedulingItem{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		})

		return err
	}
	s.Recorder.Eventf(pod, nil, corev1.EventTypeNormal, "Scheduled", "SchedulePod", "Successfully assigned %s/%s to %s", pod.Namespace, pod.Name, *result.TargetHost)
	log.V(1).Info("assigned node to pod", "node", *result.TargetHost)
	return nil
}
