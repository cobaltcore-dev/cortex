// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"errors"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var failedSchedulingError error = errors.New("FailedScheduling")

type Scheduler struct {
	Logger   logr.Logger
	Cache    *Cache
	Queue    SchedulingQueue
	Handlers []cache.ResourceEventHandlerRegistration
	// Recorder for publishing Event objects
	Recorder events.EventRecorder
	Client   client.Client

	// Listers for getting objects from informer cache
	PodLister corev1listers.PodLister
	//PodGroupSetLister podgroupsetlisters.PodGroupSetLister

	podPipelineController *FilterWeigherPipelineController
}

// SetPipelineController sets the reference to the pipeline controller
func (scheduler *Scheduler) SetPipelineController(controller *FilterWeigherPipelineController) {
	scheduler.podPipelineController = controller
}

func New(ctx context.Context, informerFactory informers.SharedInformerFactory) *Scheduler {
	scheduler := &Scheduler{
		Queue:     NewPrioritySchedulingQueue(),
		Cache:     NewCache(),
		PodLister: informerFactory.Core().V1().Pods().Lister(),
	}
	// PodGroupSetLister will be initialized when custom informer factory is available

	// Add event handlers
	scheduler.AddEventHandlers(informerFactory)

	return scheduler
}

func (scheduler *Scheduler) Run(ctx context.Context) {
	go wait.UntilWithContext(ctx, scheduler.ScheduleOne, 0)
	<-ctx.Done()
}

func (scheduler *Scheduler) ScheduleOne(ctx context.Context) {
	item, shutdown := scheduler.Queue.Get()
	if shutdown {
		return
	}
	defer scheduler.Queue.Done(item)

	// Parse namespace and name from the item key (format: "namespace/name")
	key := item.Key()
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		scheduler.Logger.Error(nil, "invalid item key format", "key", key, "item", item.String())
		return
	}
	namespace, name := parts[0], parts[1]

	switch item.Kind() {
	case KindPod:
		pod, err := scheduler.PodLister.Pods(namespace).Get(name)
		if err != nil {
			scheduler.Logger.Error(err, "failed to get pod from lister", "namespace", namespace, "name", name)
			scheduler.Queue.AddBackoff(item)
			return
		}

		if err := scheduler.schedulePod(ctx, pod); err != nil {
			scheduler.Logger.Error(err, "failed to schedule pod", "pod", pod.Name, "namespace", pod.Namespace)
			if errors.Is(err, failedSchedulingError) {
				scheduler.Queue.AddUnschedulable(item)
			} else {
				scheduler.Queue.AddBackoff(item)
			}
		}

	/*
		case KindPodGroupSet:
			// Get the PodGroupSet object using the generated lister
			pgs, err := scheduler.PodGroupSetLister.PodGroupSets(namespace).Get(name)
			if err != nil {
				scheduler.Logger.Error(err, "failed to get podgroupset from lister", "namespace", namespace, "name", name)
				// Add to unschedulable queue for retry
				scheduler.Queue.AddUnschedulable(item)
				return
			}

			// Call schedulePodGroupSet with the actual PodGroupSet object
			if err := scheduler.schedulePodGroupSet(ctx, pgs); err != nil {
				scheduler.Logger.Error(err, "failed to schedule podgroupset", "podgroupset", pgs.Name, "namespace", pgs.Namespace)
				// Add to unschedulable queue for retry
				scheduler.Queue.AddUnschedulable(item)
			}
	*/

	default:
		scheduler.Logger.Error(nil, "unknown item kind", "kind", item.Kind(), "item", item.String())
	}
}
