// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
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
	lib.BasePipelineController[lib.Pipeline[api.ExternalSchedulerRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
	// Config for the scheduling operator.
	Conf conf.Config
}

// The type of pipeline this controller manages.
func (c *DecisionPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

// Callback executed when kubernetes asks to reconcile a decision resource.
func (c *DecisionPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := c.process(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	if err := c.Status().Update(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// Process the decision from the API. Should create and return the updated decision.
func (c *DecisionPipelineController) ProcessNewDecisionFromAPI(ctx context.Context, decision *v1alpha1.Decision) error {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	pipelineConf, ok := c.PipelineConfigs[decision.Spec.PipelineRef.Name]
	if !ok {
		return fmt.Errorf("pipeline %s not configured", decision.Spec.PipelineRef.Name)
	}
	if pipelineConf.Spec.CreateDecisions {
		if err := c.Create(ctx, decision); err != nil {
			return err
		}
	}
	if err := c.process(ctx, decision); err != nil {
		return err
	}
	if pipelineConf.Spec.CreateDecisions {
		if err := c.Status().Update(ctx, decision); err != nil {
			return err
		}
	}
	return nil
}

func (c *DecisionPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return errors.New("pipeline not found or not ready")
	}
	if decision.Spec.NovaRaw == nil {
		log.Error(nil, "skipping decision, no novaRaw spec defined")
		return errors.New("no novaRaw spec defined")
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.NovaRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal novaRaw spec")
		return err
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline")
		return err
	}
	decision.Status.Result = &result
	decision.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	log.Info("decision processed successfully", "duration", time.Since(startedAt))
	return nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *DecisionPipelineController) InitPipeline(
	ctx context.Context,
	name string,
	steps []v1alpha1.Step,
) (lib.Pipeline[api.ExternalSchedulerRequest], error) {

	return lib.NewPipeline(ctx, c.Client, name, supportedSteps, steps, c.Monitor)
}

func (c *DecisionPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
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
				// Only react to pipelines matching the operator.
				if pipeline.Spec.Operator != c.Conf.Operator {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
			}),
		).
		// Watch step changes so that we can turn on/off pipelines depending on
		// unready steps.
		WatchesMulticluster(
			&v1alpha1.Step{},
			handler.Funcs{
				CreateFunc: c.HandleStepCreated,
				UpdateFunc: c.HandleStepUpdated,
				DeleteFunc: c.HandleStepDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
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
			}),
		).
		// Watch knowledge changes so that we can reconfigure pipelines as needed.
		WatchesMulticluster(
			&v1alpha1.Knowledge{},
			handler.Funcs{
				CreateFunc: c.HandleKnowledgeCreated,
				UpdateFunc: c.HandleKnowledgeUpdated,
				DeleteFunc: c.HandleKnowledgeDeleted,
			},
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				knowledge := obj.(*v1alpha1.Knowledge)
				// Only react to knowledge matching the operator.
				return knowledge.Spec.Operator == c.Conf.Operator
			}),
		).
		Named("cortex-nova-decisions").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.Operator != c.Conf.Operator {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Result != nil {
					return false
				}
				// Only handle nova decisions.
				return decision.Spec.Type == v1alpha1.DecisionTypeNovaServer
			})),
		).
		Complete(c)
}
