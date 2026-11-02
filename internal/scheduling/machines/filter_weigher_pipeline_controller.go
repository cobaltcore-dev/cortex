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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The machine pipeline controller watches for new machines and runs the
// scheduling pipeline to assign each a machine pool. The result is written back
// directly to the machine's MachinePoolRef.
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

// Reconcile is a no-op: this controller schedules machines through its Machine
// watch handler and reconfigures pipelines through its Pipeline watch handler.
// A Reconcile method is required to satisfy the controller builder.
func (c *FilterWeigherPipelineController) Reconcile(context.Context, ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (c *FilterWeigherPipelineController) ProcessNewMachine(ctx context.Context, machine *ironcorev1alpha1.Machine) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()
	return c.process(ctx, machine)
}

func (c *FilterWeigherPipelineController) process(ctx context.Context, machine *ironcorev1alpha1.Machine) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	const pipelineName = "machines-scheduler"
	pipeline, ok := c.GetPipeline(pipelineName)
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", pipelineName)
		return fmt.Errorf("pipeline not found or not ready: %q", pipelineName)
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
	log.Info("decision processed successfully", "duration", time.Since(startedAt))

	// Assign the first machine pool returned by the pipeline.
	old := machine.DeepCopy()
	machine.Spec.MachinePoolRef = &corev1.LocalObjectReference{Name: *result.TargetHost}
	patch := client.MergeFrom(old)
	if err := c.Patch(ctx, machine, patch); err != nil {
		log.V(1).Error(err, "failed to assign machine pool to instance")
		return err
	}
	log.V(1).Info("assigned machine pool to instance", "machinePool", *result.TargetHost)
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
	}
}

func (c *FilterWeigherPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainMachines
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	bldr := multicluster.BuildController(mcl, mgr)
	bldr, err := bldr.WatchesMulticluster(
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
	)
	if err != nil {
		return err
	}
	// Watch pipeline changes so that we can reconfigure pipelines as needed.
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.Pipeline{},
		handler.Funcs{
			CreateFunc: c.HandlePipelineCreated,
			UpdateFunc: c.HandlePipelineUpdated,
			DeleteFunc: c.HandlePipelineDeleted,
		},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			pipeline := obj.(*v1alpha1.Pipeline)
			// Only react to pipelines matching the scheduling domain.
			if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainMachines {
				return false
			}
			return pipeline.Spec.Type == c.PipelineType()
		}),
	)
	if err != nil {
		return err
	}
	return bldr.Named("cortex-machine-scheduler").
		Complete(c)
}
