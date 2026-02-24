// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"fmt"
	"sync"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/manila"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/manila/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/manila/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The decision pipeline controller takes decision resources containing a
// placement request spec and runs the scheduling pipeline to make a decision.
// This decision is then written back to the decision resource status.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type FilterWeigherPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Monitor to pass down to all pipelines.
	Monitor lib.FilterWeigherPipelineMonitor
}

// The type of pipeline this controller manages.
func (c *FilterWeigherPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

// Process the decision from the API. Should create and return the updated decision.
func (c *FilterWeigherPipelineController) ProcessRequest(ctx context.Context, request api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	pipelineName := request.Pipeline

	pipeline, ok := c.Pipelines[pipelineName]
	if !ok {
		return nil, fmt.Errorf("pipeline %s not found or not ready", pipelineName)
	}
	pipelineConfig, ok := c.PipelineConfigs[pipelineName]
	if !ok {
		log.Error(nil, "pipeline config not found", "pipelineName", pipelineName)
		return nil, fmt.Errorf("pipeline config for %s not found", pipelineName)
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline", "pipeline", pipelineName)
		return nil, err
	}
	log.Info("request processed successfully", "duration", time.Since(startedAt))

	if pipelineConfig.Spec.CreateDecisions {
		c.DecisionQueue <- lib.DecisionUpdate{
			// TODO model out the spec.
			ResourceID:   "",
			PipelineName: pipelineName,
			Result:       result,
			Reason:       v1alpha1.SchedulingIntentUnknown,
		}
	}
	return &result, nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *FilterWeigherPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]] {

	return lib.InitNewFilterWeigherPipeline(
		ctx, c.Client, p.Name,
		filters.Index, p.Spec.Filters,
		weighers.Index, p.Spec.Weighers,
		c.Monitor,
	)
}

func (c *FilterWeigherPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainManila
	c.Recorder = mgr.GetEventRecorder("cortex-manila-pipeline-controller")
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		// Watch knowledge changes so that we can reconfigure pipelines as needed.
		WatchesMulticluster(
			&v1alpha1.Knowledge{},
			// Get all pipelines of the controller when knowledge changes and trigger reconciliation to update the candidates in the pipelines.
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				knowledge := obj.(*v1alpha1.Knowledge)
				if knowledge.Spec.SchedulingDomain != v1alpha1.SchedulingDomainManila {
					return nil
				}
				// When Knowledge changes, reconcile all pipelines
				return c.GetAllPipelineReconcileRequests(ctx)
			}),
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				knowledge := obj.(*v1alpha1.Knowledge)
				// Only react to knowledge matching the scheduling domain.
				return knowledge.Spec.SchedulingDomain == v1alpha1.SchedulingDomainManila
			}),
		).
		Named("cortex-manila-pipelines").
		For(
			&v1alpha1.Pipeline{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainManila {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
			})),
		).
		Complete(c)
}
