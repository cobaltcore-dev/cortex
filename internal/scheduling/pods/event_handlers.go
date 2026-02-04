// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/generated/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

func (s *Scheduler) WaitForHandlersSync(ctx context.Context) error {
	return wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (done bool, err error) {
		for _, handler := range s.Handlers {
			if !handler.HasSynced() {
				return false, nil
			}
		}
		return true, nil
	})
}

func (s *Scheduler) AddEventHandlers(informerFactory informers.SharedInformerFactory, customInformerFactory externalversions.SharedInformerFactory) error {
	var (
		handlerRegistration cache.ResourceEventHandlerRegistration
		err                 error
		handlers            []cache.ResourceEventHandlerRegistration
	)
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

	if handlerRegistration, err = customInformerFactory.Api().V1alpha1().PodGroupSets().Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    s.handleAddPodGroupSet,
			UpdateFunc: s.handleUpdatePodGroupSet,
			DeleteFunc: s.handleDeletePodGroupSet,
		},
	); err != nil {
		return err
	}
	handlers = append(handlers, handlerRegistration)

	s.Handlers = handlers
	return nil
}

func (s *Scheduler) handleAddPod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Pod", "obj", obj)
		return
	}
	if pod.Spec.NodeName != "" {
		// Skip pods that already have a node assigned.
		s.Cache.AddPod(pod)
		return
	}
	if pod.Spec.SchedulerName != string(v1alpha1.SchedulingDomainPods) {
		// Skip pods that should not be scheduled by cortex
		s.Cache.AddPod(pod)
		return
	}
	if pod.OwnerReferences != nil {
		// Skip pods that are managed by a larger entity, e.g. by a PodGroupSet
		return
	}
	s.Queue.Add(&PodSchedulingItem{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	})
}

func (s *Scheduler) handleUpdatePod(oldObj, newObj interface{}) {
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

	s.Cache.RemovePod(oldPod)
	// TODO: this condition is a workaround since the initial resource allocation for newly binded pods is marked in
	// the pipeline and the pod binding update observed here would duplicate the cache entry.
	// Future plan: track which pods are assumed/confirmed to avoid multiple entries
	// of the same pod
	if oldPod.Spec.NodeName == "" {
		return
	}

	s.Cache.AddPod(newPod)
}

func (s *Scheduler) handleDeletePod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Pod", "obj", obj)
		return
	}

	s.Cache.RemovePod(pod)

	s.Queue.MoveAllToActive("pod deletion")
}

func (s *Scheduler) handleAddNode(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Node", "obj", obj)
		return
	}

	s.Cache.AddNode(node)

	s.Queue.MoveAllToActive("node added")
}

func (s *Scheduler) handleUpdateNode(oldObj, newObj interface{}) {
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

	s.Cache.RemoveNode(oldNode)
	s.Cache.AddNode(newNode)
}

func (s *Scheduler) handleDeleteNode(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Node", "obj", obj)
		return
	}
	s.Cache.RemoveNode(node)
}

func (s *Scheduler) handleAddPodGroupSet(obj interface{}) {
	pgs, ok := obj.(*v1alpha1.PodGroupSet)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *v1alpha1.PodGroupSet", "obj", obj)
		return
	}

	// TODO: handle the case that the PGS is already scheduled

	// Calculate total pod count across all pod groups,
	// this used to determine the priority of the workload
	var totalPods int
	for _, group := range pgs.Spec.PodGroups {
		totalPods += int(group.Spec.Replicas)
	}

	s.Queue.Add(&PodGroupSetSchedulingItem{
		Namespace: pgs.Namespace,
		Name:      pgs.Name,
		PodCount:  totalPods,
	})
}

func (s *Scheduler) handleUpdatePodGroupSet(oldObj, newObj interface{}) {
	_, ok := oldObj.(*v1alpha1.PodGroupSet)
	if !ok {
		s.Logger.Error(nil, "Cannot convert oldObj to *v1alpha1.PodGroupSet", "obj", oldObj)
		return
	}

	newPgs, ok := newObj.(*v1alpha1.PodGroupSet)
	if !ok {
		s.Logger.Error(nil, "Cannot convert newObj to *v1alpha1.PodGroupSet", "obj", newObj)
		return
	}

	s.Logger.Info("PodGroupSet updated", "name", newPgs.Name, "namespace", newPgs.Namespace)

	// TODO: implement update behavior of PodGroupSets
}

func (s *Scheduler) handleDeletePodGroupSet(obj interface{}) {
	_, ok := obj.(*v1alpha1.PodGroupSet)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *v1alpha1.PodGroupSet", "obj", obj)
		return
	}

	// TODO: are there any action points regarding in the queue?
}
