// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"time"

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

func (s *Scheduler) AddEventHandlers(informerFactory informers.SharedInformerFactory) error {
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

	// TODO: PGS event handler

	s.Handlers = handlers
	return nil
}

func (s *Scheduler) handleAddPod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Pod", "obj", obj)
		return
	}
	if pod.Spec.SchedulerName != string(s.podPipelineController.SchedulingDomain) {
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
	if oldPod.Spec.NodeName != "" {
		s.Cache.AddPod(newPod)
	}
}

func (s *Scheduler) handleDeletePod(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		s.Logger.Error(nil, "Cannot convert to *corev1.Pod", "obj", obj)
		return
	}

	s.Cache.RemovePod(pod)

	// TODO: remove pod from queue if in queue

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
	s.Logger.Info("Deleting node from cache", "node", node.Name, "capacity", node.Status.Capacity, "allocatable", node.Status.Allocatable)
	s.Cache.RemoveNode(node)
}

/*
DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			log := ctrl.LoggerFrom(ctx)
			podgroupset := evt.Object.(*v1alpha1.PodGroupSet)

			for _, group := range podgroupset.Spec.PodGroups {
				for i := range int(group.Spec.Replicas) {
					podName := podgroupset.PodName(group.Name, i)
					pod := &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      podName,
							Namespace: podgroupset.Namespace,
						},
					}
					if err := c.Delete(ctx, pod); err != nil {
						if client.IgnoreNotFound(err) != nil {
							log.Error(err, "failed to delete pod for deleted podgroupset", "pod", podName)
						}
					} else {
						log.Info("deleted pod for deleted podgroupset", "pod", podName)
					}
				}
			}
		},
*/
