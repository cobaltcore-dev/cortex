// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/cinder"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/cinder/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/cinder/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
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

// Callback executed when kubernetes asks to reconcile a decision resource.
func (c *FilterWeigherPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

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

// Process the decision from the API. Should create and return the updated decision.
func (c *FilterWeigherPipelineController) ProcessNewDecisionFromAPI(ctx context.Context, decision *v1alpha1.Decision) error {
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

func (c *FilterWeigherPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) error {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return errors.New("pipeline not found or not ready")
	}
	if decision.Spec.CinderRaw == nil {
		log.Error(nil, "skipping decision, no cinderRaw spec defined")
		return errors.New("no cinderRaw spec defined")
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.CinderRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal cinderRaw spec")
		return err
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline")
		return err
	}
	decision.Status.Result = &result
	log.Info("decision processed successfully", "duration", time.Since(startedAt))
	return nil
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
	c.SchedulingDomain = v1alpha1.SchedulingDomainCinder
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
				// Only react to pipelines matching the scheduling domain.
				if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainCinder {
					return false
				}
				return pipeline.Spec.Type == c.PipelineType()
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
				// Only react to knowledge matching the scheduling domain.
				return knowledge.Spec.SchedulingDomain == v1alpha1.SchedulingDomainCinder
			}),
		).
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainCinder {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Result != nil {
					return false
				}
				return true
			})),
		).
		Named("cortex-cinder-decisions").
		Complete(c)
}
