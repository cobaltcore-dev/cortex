// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Base controller for decision pipelines.
type BasePipelineController[PipelineType any] struct {
	// Initialized pipelines by their name.
	Pipelines map[string]PipelineType
	// The configured pipelines by their name.
	PipelineConfigs map[string]v1alpha1.Pipeline
	// Delegate to create pipelines.
	Initializer PipelineInitializer[PipelineType]
	// Kubernetes client to manage/fetch resources.
	client.Client
	// The scheduling domain to scope resources to.
	SchedulingDomain v1alpha1.SchedulingDomain

	DecisionQueue chan DecisionUpdate
}

type DecisionUpdate struct {
	ResourceID       string
	PipelineName     string
	Result           FilterWeigherPipelineResult
	Reason           v1alpha1.SchedulingReason
	SchedulingDomain v1alpha1.SchedulingDomain
}

func (c *BasePipelineController[PipelineType]) StartExplainer(ctx context.Context) {
	c.DecisionQueue = make(chan DecisionUpdate, 100)
	log := ctrl.LoggerFrom(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-c.DecisionQueue:
			if err := c.updateDecision(ctx, update); err != nil {
				log.Error(err, "failed to update decision", "resourceID", update.ResourceID)
			}
		}
	}
}

func (c *BasePipelineController[PipelineType]) updateDecision(ctx context.Context, update DecisionUpdate) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Explaining decision for resource", "resourceID", update.ResourceID, "pipelineName", update.PipelineName)

	explainer, err := NewExplainer(c.Client)
	if err != nil {
		return fmt.Errorf("failed to create explainer: %w", err)
	}

	explanationText, err := explainer.Explain(ctx, update.ResourceID, update.PipelineName, update.RequestContext, update.Reason, update.Result)
	if err != nil {
		return fmt.Errorf("failed to generate explanation: %w", err)
	}

	// Update the decision with the explanation.
	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, client.ObjectKey{Name: update.ResourceID}, decision); err != nil {
		return fmt.Errorf("failed to get decision: %w", err)
	}

	if decision.Status.Result == nil {
		return errors.New("cannot update decision explanation: result is nil")
	}

	decision.Status.Explanation = explanationText
	if err := c.Status().Update(ctx, decision); err != nil {
		return fmt.Errorf("failed to update decision status: %w", err)
	}

	log.Info("Successfully updated decision explanation", "resourceID", update.ResourceID)
	return nil
}

// Handle the startup of the manager by initializing the pipeline map.
func (c *BasePipelineController[PipelineType]) InitAllPipelines(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("initializing pipeline map")
	c.Pipelines = make(map[string]PipelineType)
	c.PipelineConfigs = make(map[string]v1alpha1.Pipeline)
	// List all existing pipelines and initialize them.
	var pipelines v1alpha1.PipelineList
	if err := c.List(ctx, &pipelines); err != nil {
		return fmt.Errorf("failed to list existing pipelines: %w", err)
	}
	for _, pipelineConf := range pipelines.Items {
		if pipelineConf.Spec.SchedulingDomain != c.SchedulingDomain {
			continue
		}
		if pipelineConf.Spec.Type != c.Initializer.PipelineType() {
			continue
		}
		log.Info("initializing existing pipeline", "pipelineName", pipelineConf.Name)
		c.handlePipelineChange(ctx, &pipelineConf)
		c.PipelineConfigs[pipelineConf.Name] = pipelineConf
	}
	return nil
}

func (c *BasePipelineController[PipelineType]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconcile called for pipeline", "pipelineName", req.NamespacedName)

	pipeline := &v1alpha1.Pipeline{}
	err := c.Get(ctx, req.NamespacedName, pipeline)

	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Pipeline was deleted
			log.Info("pipeline deleted, removing from cache", "pipelineName", req.Name)
			delete(c.Pipelines, req.Name)
			delete(c.PipelineConfigs, req.Name)
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get pipeline", "pipelineName", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf("failed to get pipeline: %w", err)
	}

	c.handlePipelineChange(ctx, pipeline)

	return ctrl.Result{}, nil
}

// Handle a pipeline creation or update event from watching pipeline resources.
func (c *BasePipelineController[PipelineType]) handlePipelineChange(
	ctx context.Context,
	obj *v1alpha1.Pipeline,
) {

	if obj.Spec.SchedulingDomain != c.SchedulingDomain {
		delete(c.Pipelines, obj.Name) // Just to be sure.
		delete(c.PipelineConfigs, obj.Name)
		return
	}
	log := ctrl.LoggerFrom(ctx)
	old := obj.DeepCopy()

	initResult := c.Initializer.InitPipeline(ctx, *obj)

	// If there was a critical error, the pipeline cannot be used.
	if len(initResult.FilterErrors) > 0 {
		err := errors.New("one or more filters failed to initialize")
		log.Error(err, "failed to create pipeline", "pipelineName", obj.Name)
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "PipelineInitFailed",
			Message: err.Error(),
		})
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionAllStepsReady,
			Status:  metav1.ConditionFalse,
			Reason:  "PipelineInitFailed",
			Message: fmt.Sprintf("%d filters failed to initialize: %v", len(initResult.FilterErrors), initResult.FilterErrors),
		})
		patch := client.MergeFrom(old)
		if err := c.Status().Patch(ctx, obj, patch); err != nil {
			log.Error(err, "failed to patch pipeline status", "pipelineName", obj.Name)
		}
		delete(c.Pipelines, obj.Name)
		delete(c.PipelineConfigs, obj.Name)
		return
	}

	// If there was a non-critical error, continue running the pipeline but
	// report the error in the pipeline status.
	if len(initResult.WeigherErrors) > 0 || len(initResult.DetectorErrors) > 0 {
		var errmsg string
		if len(initResult.WeigherErrors) > 0 {
			errmsg += fmt.Sprintf("%d weighers failed to initialize: %v. ", len(initResult.WeigherErrors), initResult.WeigherErrors)
		}
		if len(initResult.DetectorErrors) > 0 {
			errmsg += fmt.Sprintf("%d detectors failed to initialize: %v. ", len(initResult.DetectorErrors), initResult.DetectorErrors)
		}
		log.Info("non-critical issue during pipeline initialization", "pipelineName", obj.Name, "issue", errmsg)
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionAllStepsReady,
			Status:  metav1.ConditionFalse,
			Reason:  "SomeStepsNotReady",
			Message: errmsg,
		})
	} else {
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionAllStepsReady,
			Status:  metav1.ConditionTrue,
			Reason:  "AllStepsReady",
			Message: "all pipeline steps are ready",
		})
	}

	c.Pipelines[obj.Name] = initResult.Pipeline
	c.PipelineConfigs[obj.Name] = *obj
	log.Info("pipeline created and ready", "pipelineName", obj.Name)
	meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.PipelineConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "PipelineReady",
		Message: "pipeline is ready",
	})
	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, obj, patch); err != nil {
		log.Error(err, "failed to patch pipeline status", "pipelineName", obj.Name)
		return
	}
}

// GetAllPipelineReconcileRequests returns reconcile requests for all pipelines
// managed by this controller. Used when Knowledge changes require pipeline re-evaluation.
func (c *BasePipelineController[PipelineType]) GetAllPipelineReconcileRequests(ctx context.Context) []reconcile.Request {
	var requests []reconcile.Request
	for name := range c.Pipelines {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{Name: name},
		})
	}
	return requests
}
