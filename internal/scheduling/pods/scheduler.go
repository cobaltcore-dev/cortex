// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cobaltcore-dev/cortex/pkg/generated/informers/externalversions"
	podgroupsetlisters "github.com/cobaltcore-dev/cortex/pkg/generated/listers/api/v1alpha1"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	PodLister         corev1listers.PodLister
	PodGroupSetLister podgroupsetlisters.PodGroupSetLister

	podPipelineController *FilterWeigherPipelineController
}

// SetPipelineController sets the reference to the pipeline controller
func (scheduler *Scheduler) SetPipelineController(controller *FilterWeigherPipelineController) {
	scheduler.podPipelineController = controller
}

func New(ctx context.Context, informerFactory informers.SharedInformerFactory, customInformerFactory externalversions.SharedInformerFactory) *Scheduler {
	scheduler := &Scheduler{
		Queue:             NewPrioritySchedulingQueue(),
		Cache:             NewCache(),
		PodLister:         informerFactory.Core().V1().Pods().Lister(),
		PodGroupSetLister: customInformerFactory.Api().V1alpha1().PodGroupSets().Lister(),
	}

	// Add event handlers with both informer factories
	if err := scheduler.AddEventHandlers(informerFactory, customInformerFactory); err != nil {
		// Log error but don't fail scheduler creation - handlers can be added later
		// This is needed because the logger might not be set yet
	}

	return scheduler
}

func (scheduler *Scheduler) Run(ctx context.Context) {
	go wait.UntilWithContext(ctx, scheduler.ScheduleOne, 0)
	<-ctx.Done()
}

func (scheduler *Scheduler) ScheduleOne(ctx context.Context) {
	logger := log.FromContext(ctx)
	logger.Info("calling Queue.Get")
	item, shutdown := scheduler.Queue.Get()
	if shutdown {
		fmt.Println("schedule one shutdown")
		return
	}

	logger.Info("schedule one", "item", item.Key())

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
			if apierrors.IsNotFound(err) {
				// Pod was deleted - stop scheduling it
				scheduler.Logger.Info("pod not found, assuming deleted", "namespace", namespace, "name", name)
				scheduler.Queue.Done(item)
				return
			} else {
				// Other error (informer not synced, temporary failure, etc.)
				scheduler.Logger.Error(err, "temporary error getting pod from lister", "namespace", namespace, "name", name)
				scheduler.Queue.AddBackoff(item)
				return
			}
		}

		if err := scheduler.schedulePod(ctx, pod); err != nil {
			scheduler.Logger.Error(err, "failed to schedule pod", "pod", pod.Name, "namespace", pod.Namespace)
			if errors.Is(err, failedSchedulingError) {
				scheduler.Queue.AddUnschedulable(item)
			} else {
				scheduler.Queue.AddBackoff(item)
			}
		} else {
			// Only call Done() on successful scheduling
			scheduler.Queue.Done(item)
		}

	case KindPodGroupSet:
		// Get the PodGroupSet object using the generated lister
		pgs, err := scheduler.PodGroupSetLister.PodGroupSets(namespace).Get(name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// PodGroupSet was deleted - stop scheduling it
				scheduler.Logger.Info("podgroupset not found, assuming deleted", "namespace", namespace, "name", name)
				scheduler.Queue.Done(item)
				return
			} else {
				// Other error (informer not synced, temporary failure, etc.)
				scheduler.Logger.Error(err, "temporary error getting podgroupset from lister", "namespace", namespace, "name", name)
				scheduler.Queue.AddBackoff(item)
				return
			}
		}

		// Call schedulePodGroupSet with the actual PodGroupSet object
		if err := scheduler.schedulePodGroupSet(ctx, pgs); err != nil {
			scheduler.Logger.Error(err, "failed to schedule podgroupset", "podgroupset", pgs.Name, "namespace", pgs.Namespace)
			if errors.Is(err, failedSchedulingError) {
				scheduler.Logger.Info("failed scheduling add to unschedulable")
				scheduler.Queue.AddUnschedulable(item)
			} else {
				scheduler.Logger.Info("failed scheduling add to backoff")
				scheduler.Queue.AddBackoff(item)
			}
		} else {
			// Only call Done() on successful scheduling
			scheduler.Queue.Done(item)
		}

	default:
		scheduler.Logger.Error(nil, "unknown item kind", "kind", item.Kind(), "item", item.String())
	}
}
