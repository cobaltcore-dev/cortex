// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/external/ironcore"
	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/external/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/machines/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/machines/plugins/weighers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The decision pipeline controller takes decision resources containing a
// machine ref and runs the scheduling pipeline to make a decision.
// This decision is then written back to the decision resource status.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type FilterWeigherPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Monitor to pass down to all pipelines.
	Monitor lib.FilterWeigherPipelineMonitor
}

// The type of pipeline this controller manages.
func (c *FilterWeigherPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

func (c *FilterWeigherPipelineController) ProcessNewMachine(ctx context.Context, machine *ironcorev1alpha1.Machine) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now()

	pipelineName := "machines-scheduler"

	pipeline, ok := c.Pipelines[pipelineName]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", pipelineName)
		return errors.New("pipeline not found or not ready")
	}

	pipelineConfig, ok := c.PipelineConfigs[pipelineName]
	if !ok {
		log.Error(nil, "pipeline not configured", "pipelineName", pipelineName)
		return fmt.Errorf("pipeline %s not configured", pipelineName)
	}

	// Find all available machine pools.
	pools := &ironcorev1alpha1.MachinePoolList{}
	if err := c.List(ctx, pools); err != nil {
		return err
	}
	if len(pools.Items) == 0 {
		return errors.New("no machine pools available for scheduling")
	}

	// Execute the scheduling pipeline.
	request := ironcore.MachinePipelineRequest{Pools: pools.Items}
	result, err := pipeline.Run(request)
	if err != nil {
		log.V(1).Error(err, "failed to run scheduler pipeline")
		return errors.New("failed to run scheduler pipeline")
	}

	log.Info("machine processed successfully", "duration", time.Since(startedAt))

	hosts := result.OrderedHosts
	if len(hosts) == 0 {
		log.Info("no suitable machine pools found by pipeline")
		return errors.New("no suitable machine pools found")
	}

	targetHost := hosts[0]

	// Set the machine pool ref on the machine.

	// Assign the first machine pool returned by the pipeline.
	old := machine.DeepCopy()
	machine.Spec.MachinePoolRef = &corev1.LocalObjectReference{Name: targetHost}
	patch := client.MergeFrom(old)
	if err := c.Patch(ctx, machine, patch); err != nil {
		log.V(1).Error(err, "failed to assign machine pool to instance")
		return err
	}
	log.V(1).Info("assigned machine pool to instance", "machinePool", targetHost)

	if pipelineConfig.Spec.CreateDecisions {
		c.DecisionQueue <- lib.DecisionUpdate{
			ResourceID:   machine.Name,
			PipelineName: pipelineName,
			Result:       result,
			// TODO: Refine the reason
			Reason: v1alpha1.SchedulingReasonUnknown,
		}
	}
	return nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *FilterWeigherPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[lib.FilterWeigherPipeline[ironcore.MachinePipelineRequest]] {

	return lib.InitNewFilterWeigherPipeline(
		ctx, c.Client, p.Name,
		filters.Index, p.Spec.Filters,
		weighers.Index, p.Spec.Weighers,
		c.Monitor,
	)
}

func (c *FilterWeigherPipelineController) handleMachine() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			machine := evt.Object.(*ironcorev1alpha1.Machine)
			if err := c.ProcessNewMachine(ctx, machine); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process new machine for scheduling", "machine", machine.Name)
			}
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			newMachine := evt.ObjectNew.(*ironcorev1alpha1.Machine)
			if newMachine.Spec.MachinePoolRef != nil {
				// Machine is already scheduled, no need to create a decision.
				return
			}
			if err := c.ProcessNewMachine(ctx, newMachine); err != nil {
				log := ctrl.LoggerFrom(ctx)
				log.Error(err, "failed to process new machine for scheduling", "machine", newMachine.Name)
			}
		},
		DeleteFunc: func(ctx context.Context, evt event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			// Delete the associated decision(s).
			log := ctrl.LoggerFrom(ctx)
			machine := evt.Object.(*ironcorev1alpha1.Machine)
			var decisions v1alpha1.DecisionList
			if err := c.List(ctx, &decisions); err != nil {
				log.Error(err, "failed to list decisions for deleted machine")
				return
			}
			for _, decision := range decisions.Items {
				if decision.Spec.ResourceID == machine.Name && decision.Spec.SchedulingDomain == v1alpha1.SchedulingDomainMachines {
					if err := c.Delete(ctx, &decision); err != nil {
						log.Error(err, "failed to delete decision for deleted machine")
					}
				}
			}
		},
	}
}

func (c *FilterWeigherPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainMachines
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		WatchesMulticluster(
			&ironcorev1alpha1.Machine{},
			c.handleMachine(),
			// Only schedule machines that have the custom scheduler set.
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				machine := obj.(*ironcorev1alpha1.Machine)
				if machine.Spec.MachinePoolRef != nil {
					// Skip machines that already have a machine pool assigned.
					return false
				}
				// The machine spec currently doesn't support this field yet.
				// Thus the resource will be deserialized to an empty string.
				// We subscribe to all machines without a scheduler set for now.
				// Otherwise when deployed the machine scheduler won't do anything.
				return machine.Spec.Scheduler == ""
			}),
		).
		For(
			&v1alpha1.Pipeline{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainMachines {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
			})),
		).
		Named("cortex-machine-scheduler").
		Complete(c)
}
