// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/delegation/ironcore"
	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/api/delegation/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
type DecisionPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.Pipeline[ironcore.MachinePipelineRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Config for the scheduling operator.
	Conf conf.Config
	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
}

// The type of pipeline this controller manages.
func (c *DecisionPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	// Determine if this is a decision or machine reconciliation.
	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	old := decision.DeepCopy()
	if err := c.process(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, decision, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (c *DecisionPipelineController) ProcessNewMachine(ctx context.Context, machine *ironcorev1alpha1.Machine) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	// Create a decision resource to schedule the machine.
	decision := &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainMachines,
			ResourceID:       machine.Name,
			PipelineRef: corev1.ObjectReference{
				Name: "machines-scheduler",
			},
			MachineRef: &corev1.ObjectReference{
				Name:      machine.Name,
				Namespace: machine.Namespace,
			},
		},
	}

	pipelineConf, ok := c.PipelineConfigs[decision.Spec.PipelineRef.Name]
	if !ok {
		return fmt.Errorf("pipeline %s not configured", decision.Spec.PipelineRef.Name)
	}
	if pipelineConf.Spec.CreateDecisions {
		if err := c.Create(ctx, decision); err != nil {
			return err
		}
	}
	old := decision.DeepCopy()
	err := c.process(ctx, decision)
	if err != nil {
		meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DecisionConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "PipelineRunFailed",
			Message: "pipeline run failed: " + err.Error(),
		})
	} else {
		meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DecisionConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "PipelineRunSucceeded",
			Message: "pipeline run succeeded",
		})
	}
	if pipelineConf.Spec.CreateDecisions {
		patch := client.MergeFrom(old)
		if err := c.Status().Patch(ctx, decision, patch); err != nil {
			return err
		}
	}
	return err
}

func (c *DecisionPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return errors.New("pipeline not found or not ready")
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
	decision.Status.Result = &result
	log.Info("decision processed successfully", "duration", time.Since(startedAt))

	// Set the machine pool ref on the machine.
	machine := &ironcorev1alpha1.Machine{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      decision.Spec.MachineRef.Name,
		Namespace: decision.Spec.MachineRef.Namespace,
	}, machine); err != nil {
		log.Error(err, "failed to fetch machine for decision")
		return err
	}
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
func (c *DecisionPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) (lib.Pipeline[ironcore.MachinePipelineRequest], error) {

	return lib.NewFilterWeigherPipeline(
		ctx, c.Client, p.Name,
		supportedFilters, p.Spec.Filters,
		supportedWeighers, p.Spec.Weighers,
		c.Monitor,
	)
}

func (c *DecisionPipelineController) handleMachine() handler.EventHandler {
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
				if decision.Spec.MachineRef.Name == machine.Name && decision.Spec.MachineRef.Namespace == machine.Namespace {
					if err := c.Delete(ctx, &decision); err != nil {
						log.Error(err, "failed to delete decision for deleted machine")
					}
				}
			}
		},
	}
}

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
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
		// Watch pipeline changes so that we can reconfigure pipelines as needed.
		WatchesMulticluster(
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
		).
		Named("cortex-machine-scheduler").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainMachines {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Result != nil {
					return false
				}
				return true
			})),
		).
		Complete(c)
}
