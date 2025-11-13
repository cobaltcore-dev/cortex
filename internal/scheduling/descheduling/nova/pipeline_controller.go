// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/sapcc/go-bits/jobloop"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// The deschedulings pipeline controller is responsible for periodically running
// the descheduling pipeline and creating descheduling resources based on the
// selections made.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type DeschedulingsPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[*Pipeline]

	// Monitor to pass down to all pipelines.
	Monitor Monitor
	// Config for the scheduling operator.
	Conf conf.Config
	// Cycle detector to avoid descheduling loops.
	CycleDetector CycleDetector
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DeschedulingsPipelineController) InitPipeline(ctx context.Context, name string, steps []v1alpha1.Step) (*Pipeline, error) {
	pipeline := &Pipeline{
		Client:        c.Client,
		CycleDetector: c.CycleDetector,
		Monitor:       c.Monitor.SubPipeline(name),
	}
	err := pipeline.Init(ctx, steps, supportedSteps)
	return pipeline, err
}

func (c *DeschedulingsPipelineController) CreateDeschedulingsPeriodically(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("descheduler shutting down")
			return
		default:
			// Get the pipeline for the current configuration.
			p, ok := c.Pipelines["nova-descheduler"]
			if !ok {
				slog.Error("descheduler: pipeline not found or not ready yet")
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			if err := p.createDeschedulings(ctx); err != nil {
				slog.Error("descheduler: failed to create deschedulings", "error", err)
			}
			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}
}

func (c *DeschedulingsPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// This controller does not reconcile any resources directly.
	return ctrl.Result{}, nil
}

func (c *DeschedulingsPipelineController) SetupWithManager(mgr ctrl.Manager) error {
	c.Initializer = c
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		// Initialize the cycle detector.
		return c.CycleDetector.Init(ctx, mgr.GetClient(), c.Conf)
	})); err != nil {
		return err
	}
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-nova-deschedulings").
		For(
			&v1alpha1.Descheduling{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return false // This controller does not reconcile Descheduling resources directly.
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
				return pipeline.Spec.Type == v1alpha1.PipelineTypeDescheduler
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
					v1alpha1.StepTypeDescheduler,
				}
				return slices.Contains(supportedTypes, step.Spec.Type)
			})),
		).
		// Watch knowledge changes so that we can reconfigure pipelines as needed.
		Watches(
			&v1alpha1.Knowledge{},
			handler.Funcs{
				CreateFunc: c.HandleKnowledgeCreated,
				UpdateFunc: c.HandleKnowledgeUpdated,
				DeleteFunc: c.HandleKnowledgeDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				knowledge := obj.(*v1alpha1.Knowledge)
				// Only react to knowledge matching the operator.
				return knowledge.Spec.Operator == c.Conf.Operator
			})),
		).
		Complete(c)
}
