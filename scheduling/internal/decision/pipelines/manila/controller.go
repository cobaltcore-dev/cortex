// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/manila"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// The decision pipeline controller takes decision resources containing a
// placement request spec and runs the scheduling pipeline to make a decision.
// This decision is then written back to the decision resource status.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type DecisionPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[api.ExternalSchedulerRequest]

	// Database to pass down to all steps.
	DB db.DB
	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
	// Config for the scheduling operator.
	Conf conf.Config
}

func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := ctrl.LoggerFrom(ctx)

	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return ctrl.Result{}, nil
	}
	if decision.Spec.ManilaRaw == nil {
		log.Info("skipping decision, no manilaRaw spec defined")
		return ctrl.Result{}, nil
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.ManilaRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal manilaRaw spec")
		return ctrl.Result{}, err
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline")
		return ctrl.Result{}, err
	}
	decision.Status.Result = &result
	decision.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := c.Status().Update(ctx, decision); err != nil {
		log.Error(err, "failed to update decision status")
		return ctrl.Result{}, err
	}
	log.Info("decision processed successfully", "duration", time.Since(startedAt))
	return ctrl.Result{}, nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DecisionPipelineController) InitPipeline(steps []v1alpha1.Step) (lib.Pipeline[api.ExternalSchedulerRequest], error) {
	return NewPipeline(steps, c.DB, c.Monitor)
}

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager) error {
	c.BasePipelineController.Delegate = c
	mgr.Add(manager.RunnableFunc(c.InitAllPipelines))
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-manila-decisions").
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
				// Only handle manila decisions.
				return decision.Spec.Type == v1alpha1.DecisionTypeManilaShare
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
