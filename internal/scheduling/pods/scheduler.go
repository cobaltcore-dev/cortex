// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package pods

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/delegation/pods"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type Scheduler struct {
	Logger   logr.Logger
	Cache    *Cache
	Queue    *SchedulingQueue
	Handlers []cache.ResourceEventHandlerRegistration

	podPipeline lib.FilterWeigherPipeline[pods.PodPipelineRequest]
}

func New(ctx context.Context, informerFactory informers.SharedInformerFactory) *Scheduler {
	podLister := informerFactory.Core().V1().Pods().Lister()

	scheduler = &Scheduler{}

	// new SchedulingQueue(podLister)
	// new Cache

	// addEventHandlers

	return scheduler
}

func (scheduler *Scheduler) Run(ctx context.Context) {
	go wait.UntilWithContext(ctx, scheduler.ScheduleOne, 0)
	<-ctx.Done()
}

func (scheduler *Scheduler) ScheduleOne(ctx context.Context) {
	// pod = queue.Get
	// run pods pipeline / PGS controller
}
