// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/pods/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

var (
	handlerRegistration cache.ResourceEventHandlerRegistration
	err                 error
	handlers            []cache.ResourceEventHandlerRegistration
)

func (s *SchedulerContext) WaitForHandlersSync(ctx context.Context) error {
	return wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (done bool, err error) {
		for _, handler := range s.Handlers {
			if !handler.HasSynced() {
				return false, nil
			}
		}
		return true, nil
	})
}

func (s *SchedulerContext) AddEventHandlers(informerFactory informers.SharedInformerFactory) error {
	if handlerRegistration, err = informerFactory.Core().V1().Pods().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    s.handleAddPod,
		UpdateFunc: s.handleUpdatePod,
		DeleteFunc: s.handleDeletePod,
	}); err != nil {
		return err
	}
	handlers = append(handlers, handlerRegistration)

	if handlerRegistration, err = informerFactory.Core().V1().Nodes().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    s.handleAddNode,
			UpdateFunc: s.handleUpdateNode,
			DeleteFunc: s.handleDeleteNode,
		},
	); err != nil {
		return err
	}
	handlers = append(handlers, handlerRegistration)

	s.Handlers = handlers
	return nil
}

func (s *SchedulerContext) handleAddPod(obj interface{}) {
	// TODO: scheduled pods are currently handled by the controller themselves.
	// In the future we need to implement assumePod/forgetPod functions which
	// temporarely edit the cache but need to be verified by the informer or
	// be deleted after some timeout if the binding fails

	/* pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.Error("Cannot convert to *corev1.Pod", "obj", obj)
		return
	}
	s.Cache.AddPod(pod) */
}

func (s *SchedulerContext) handleUpdatePod(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert oldObj to *corev1.Pod", "obj", oldObj)
		return
	}

	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert newObj to *corev1.Pod", "obj", newObj)
		return
	}

	// Log resource changes for pod updates
	if oldPod.Spec.NodeName != "" {
		oldResources := helpers.GetPodResourceRequests(*oldPod)
		s.Logger.Info("Removing old pod resources from cache", "pod", oldPod.Name, "namespace", oldPod.Namespace, "node", oldPod.Spec.NodeName, "resources", oldResources)
	}

	if newPod.Spec.NodeName != "" {
		newResources := helpers.GetPodResourceRequests(*newPod)
		s.Logger.Info("Adding new pod resources to cache", "pod", newPod.Name, "namespace", newPod.Namespace, "node", newPod.Spec.NodeName, "resources", newResources)
	}

	s.Cache.RemovePod(oldPod)
	// TODO: this condition is a workaround since the initial resource allocation is marked in
	// the pipeline and the pod binding update observed here would duplicate the cache entry.
	// Future plan: track which pods are assumed/confirmed to avoid multiple entries
	// of the same pod
	if oldPod.Spec.NodeName != "" {
		s.Cache.AddPod(newPod)
	}
}

func (s *SchedulerContext) handleDeletePod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Pod", "obj", obj)
		return
	}

	if pod.Spec.NodeName != "" {
		podResources := helpers.GetPodResourceRequests(*pod)
		s.Logger.Info("Deleting pod from cache", "pod", pod.Name, "namespace", pod.Namespace, "node", pod.Spec.NodeName, "resources", podResources)
	} else {
		s.Logger.Info("Deleting pod from cache", "pod", pod.Name, "namespace", pod.Namespace)
	}

	s.Cache.RemovePod(pod)

	// Trigger rescheduling when a pod is deleted as resources are now available
	if s.Queue != nil {
		s.Logger.Info("Triggering rescheduling due to pod deletion", "pod", pod.Name, "namespace", pod.Namespace)
		s.Queue.TriggerRescheduling()
	}
}

func (s *SchedulerContext) handleAddNode(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Node", "obj", obj)
		return
	}

	// Skip control plane nodes - they should not be used for pod scheduling
	if isControlPlaneNode(*node) {
		s.Logger.Info("Skipping control plane node", "node", node.Name)
		return
	}

	s.Logger.Info("Adding node to cache", "node", node.Name, "capacity", node.Status.Capacity, "allocatable", node.Status.Allocatable)
	s.Cache.AddNode(node)

	// Trigger rescheduling when a new node is added as new capacity is available
	if s.Queue != nil {
		s.Logger.Info("Triggering rescheduling due to node addition", "node", node.Name)
		s.Queue.TriggerRescheduling()
	}
}

func (s *SchedulerContext) handleUpdateNode(oldObj, newObj interface{}) {
	oldNode, ok := oldObj.(*corev1.Node)
	if !ok {
		s.Logger.Error(nil, "Cannot convert oldObj to *corev1.Node", "obj", oldObj)
		return
	}

	newNode, ok := newObj.(*corev1.Node)
	if !ok {
		s.Logger.Error(nil, "Cannot convert newObj to *corev1.Node", "obj", newObj)
		return
	}

	// Skip control plane nodes - they should not be used for pod scheduling
	if isControlPlaneNode(*newNode) {
		s.Logger.Info("Skipping control plane node update", "node", newNode.Name)
		return
	}

	s.Logger.Info("Updating node in cache", "node", newNode.Name,
		"old_capacity", oldNode.Status.Capacity, "old_allocatable", oldNode.Status.Allocatable,
		"new_capacity", newNode.Status.Capacity, "new_allocatable", newNode.Status.Allocatable)

	s.Cache.RemoveNode(oldNode)
	s.Cache.AddNode(newNode)
}

func (s *SchedulerContext) handleDeleteNode(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Node", "obj", obj)
		return
	}
	s.Logger.Info("Deleting node from cache", "node", node.Name, "capacity", node.Status.Capacity, "allocatable", node.Status.Allocatable)
	s.Cache.RemoveNode(node)
}
