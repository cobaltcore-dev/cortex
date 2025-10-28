// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore"
	ironcorev1alpha1 "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/ironcore/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/lib"
	corev1 "k8s.io/api/core/v1"
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

	// Config for the scheduling operator.
	Conf conf.Config
	// Database to pass down to all steps.
	DB db.DB
	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
	// Kubernetes client to manage/fetch resources.
	client.Client
}

func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := ctrl.LoggerFrom(ctx)

	// Determine if this is a decision or machine reconciliation.
	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found", "pipelineName", decision.Spec.PipelineRef.Name)
		return ctrl.Result{}, nil
	}

	// Find all available machine pools.
	pools := &ironcorev1alpha1.MachinePoolList{}
	if err := c.List(ctx, pools); err != nil {
		return ctrl.Result{}, err
	}
	if len(pools.Items) == 0 {
		log.V(1).Info("skipping scheduling, no machine pools available")
		return ctrl.Result{}, nil
	}

	// Execute the scheduling pipeline.
	request := ironcore.MachinePipelineRequest{Pools: pools.Items}
	result, err := pipeline.Run(request)
	if err != nil {
		log.V(1).Error(err, "failed to run scheduler pipeline")
		return ctrl.Result{}, err
	}
	decision.Status.Result = &result
	decision.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := c.Status().Update(ctx, decision); err != nil {
		log.Error(err, "failed to update decision status")
		return ctrl.Result{}, err
	}
	log.Info("decision processed successfully", "duration", time.Since(startedAt))

	// Set the machine pool ref on the machine.
	machine := &ironcorev1alpha1.Machine{}
	if err := c.Get(ctx, client.ObjectKey{
		Name:      decision.Spec.MachineRef.Name,
		Namespace: decision.Spec.MachineRef.Namespace,
	}, machine); err != nil {
		log.Error(err, "failed to fetch machine for decision")
		return ctrl.Result{}, err
	}
	// Assign the first machine pool returned by the pipeline.
	machine.Spec.MachinePoolRef = &corev1.LocalObjectReference{Name: *result.TargetHost}
	if err := c.Update(ctx, machine); err != nil {
		log.V(1).Error(err, "failed to assign machine pool to instance")
		return ctrl.Result{}, err
	}
	log.V(1).Info("assigned machine pool to instance", "machinePool", *result.TargetHost)

	return ctrl.Result{}, nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DecisionPipelineController) InitPipeline(steps []v1alpha1.Step) (lib.Pipeline[ironcore.MachinePipelineRequest], error) {
	return NewPipeline(steps, c.DB, c.Monitor)
}

func (c *DecisionPipelineController) handleMachine() handler.EventHandler {
	handle := func(ctx context.Context, new *ironcorev1alpha1.Machine) {
		log := ctrl.LoggerFrom(ctx)
		// Create a decision resource to schedule the machine.
		decision := &v1alpha1.Decision{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "machine-",
			},
			Spec: v1alpha1.DecisionSpec{
				Operator:   c.Conf.Operator,
				Type:       v1alpha1.DecisionTypeIroncoreMachine,
				ResourceID: new.ObjectMeta.Name,
				PipelineRef: corev1.ObjectReference{
					Name: "machines-scheduler",
				},
				MachineRef: &corev1.ObjectReference{
					Name:      new.ObjectMeta.Name,
					Namespace: new.ObjectMeta.Namespace,
				},
			},
		}
		if err := c.Create(ctx, decision); err != nil {
			log.Error(err, "failed to create decision for machine scheduling")
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, evt event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			machine := evt.Object.(*ironcorev1alpha1.Machine)
			handle(ctx, machine)
		},
		UpdateFunc: func(ctx context.Context, evt event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			new := evt.ObjectNew.(*ironcorev1alpha1.Machine)
			if new.Spec.MachinePoolRef != nil {
				// Machine is already scheduled, no need to create a decision.
				return
			}
			handle(ctx, new)
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

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager) error {
	c.BasePipelineController.Delegate = c
	mgr.Add(manager.RunnableFunc(c.InitAllPipelines))
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-machine-scheduler").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.Operator != c.Conf.Operator {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Error != "" || decision.Status.Result != nil {
					return false
				}
				// Only handle ironcore machine decisions.
				return decision.Spec.Type == v1alpha1.DecisionTypeIroncoreMachine
			})),
		).
		Watches(
			&ironcorev1alpha1.Machine{},
			c.handleMachine(),
			// Only schedule machines that have the custom scheduler set.
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
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
			})),
		).
		// Watch pipeline changes so that we can reconfigure pipelines as needed.
		Watches(
			&v1alpha1.Pipeline{},
			handler.Funcs{
				CreateFunc: c.HandlePipelineCreated,
				UpdateFunc: c.HandlePipelineUpdated,
				DeleteFunc: c.HandlePipelineDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				// Only react to pipelines matching the operator.
				if pipeline.Spec.Operator != c.Conf.Operator {
					return false
				}
				return pipeline.Spec.Type == v1alpha1.PipelineTypeFilterWeigher
			})),
		).
		// Watch step changes so that we can turn on/off pipelines depending on
		// unready steps.
		Watches(
			&v1alpha1.Step{},
			handler.Funcs{
				CreateFunc: c.HandleStepCreated,
				UpdateFunc: c.HandleStepUpdated,
				DeleteFunc: c.HandleStepDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				step := obj.(*v1alpha1.Step)
				// Only react to steps matching the operator.
				if step.Spec.Operator != c.Conf.Operator {
					return false
				}
				// Only react to filter and weigher steps.
				supportedTypes := []v1alpha1.StepType{
					v1alpha1.StepTypeFilter,
					v1alpha1.StepTypeWeigher,
				}
				return slices.Contains(supportedTypes, step.Spec.Type)
			})),
		).
		Complete(c)
}
