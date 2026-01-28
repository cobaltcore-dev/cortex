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
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
		c.handlePipelineChange(ctx, &pipelineConf, nil)
		c.PipelineConfigs[pipelineConf.Name] = pipelineConf
	}
	return nil
}

// Handle a pipeline creation or update event from watching pipeline resources.
func (c *BasePipelineController[PipelineType]) handlePipelineChange(
	ctx context.Context,
	obj *v1alpha1.Pipeline,
	_ workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	if obj.Spec.SchedulingDomain != c.SchedulingDomain {
		delete(c.Pipelines, obj.Name) // Just to be sure.
		delete(c.PipelineConfigs, obj.Name)
		return
	}
	log := ctrl.LoggerFrom(ctx)
	old := obj.DeepCopy()

	initResult := c.Initializer.InitPipeline(ctx, *obj)

	obj.Status.Filters = []v1alpha1.FilterStatus{}
	for _, filter := range obj.Spec.Filters {
		fs := v1alpha1.FilterStatus{Name: filter.Name}
		if err, ok := initResult.FilterErrors[filter.Name]; ok {
			meta.SetStatusCondition(&fs.Conditions, metav1.Condition{
				Type:    v1alpha1.FilterConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "FilterInitFailed",
				Message: err.Error(),
			})
		} else {
			meta.SetStatusCondition(&fs.Conditions, metav1.Condition{
				Type:    v1alpha1.FilterConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "FilterReady",
				Message: "filter is ready",
			})
		}
		obj.Status.Filters = append(obj.Status.Filters, fs)
	}

	obj.Status.Weighers = []v1alpha1.WeigherStatus{}
	for _, weigher := range obj.Spec.Weighers {
		ws := v1alpha1.WeigherStatus{Name: weigher.Name}
		if err, ok := initResult.WeigherErrors[weigher.Name]; ok {
			meta.SetStatusCondition(&ws.Conditions, metav1.Condition{
				Type:    v1alpha1.WeigherConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "WeigherInitFailed",
				Message: err.Error(),
			})
		} else {
			meta.SetStatusCondition(&ws.Conditions, metav1.Condition{
				Type:    v1alpha1.WeigherConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "WeigherReady",
				Message: "weigher is ready",
			})
		}
		obj.Status.Weighers = append(obj.Status.Weighers, ws)
	}

	obj.Status.Detectors = []v1alpha1.DetectorStatus{}
	for _, detector := range obj.Spec.Detectors {
		ds := v1alpha1.DetectorStatus{Name: detector.Name}
		if err, ok := initResult.DetectorErrors[detector.Name]; ok {
			meta.SetStatusCondition(&ds.Conditions, metav1.Condition{
				Type:    v1alpha1.DetectorConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "DetectorInitFailed",
				Message: err.Error(),
			})
		} else {
			meta.SetStatusCondition(&ds.Conditions, metav1.Condition{
				Type:    v1alpha1.DetectorConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "DetectorReady",
				Message: "detector is ready",
			})
		}
		obj.Status.Detectors = append(obj.Status.Detectors, ds)
	}

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
			Message: err.Error(),
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
		err := errors.New("one or more weighers or detectors failed to initialize")
		log.Error(err, "non-critical error during pipeline initialization", "pipelineName", obj.Name)
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionAllStepsReady,
			Status:  metav1.ConditionFalse,
			Reason:  "SomeStepsNotReady",
			Message: err.Error(),
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

// Handler bound to a pipeline watch to handle created pipelines.
//
// This handler will initialize new pipelines as needed and put them into the
// pipeline map.
func (c *BasePipelineController[PipelineType]) HandlePipelineCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	pipelineConf := evt.Object.(*v1alpha1.Pipeline)
	c.handlePipelineChange(ctx, pipelineConf, queue)
}

// Handler bound to a pipeline watch to handle updated pipelines.
//
// This handler will initialize new pipelines as needed and put them into the
// pipeline map.
func (c *BasePipelineController[PipelineType]) HandlePipelineUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	pipelineConf := evt.ObjectNew.(*v1alpha1.Pipeline)
	c.handlePipelineChange(ctx, pipelineConf, queue)
}

// Handler bound to a pipeline watch to handle deleted pipelines.
//
// This handler will remove pipelines from the pipeline map.
func (c *BasePipelineController[PipelineType]) HandlePipelineDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	pipelineConf := evt.Object.(*v1alpha1.Pipeline)
	delete(c.Pipelines, pipelineConf.Name)
	delete(c.PipelineConfigs, pipelineConf.Name)
}

// Handle a knowledge creation, readiness update, or delete event from watching knowledge resources.
func (c *BasePipelineController[PipelineType]) handleKnowledgeChange(
	ctx context.Context,
	obj *v1alpha1.Knowledge,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	if obj.Spec.SchedulingDomain != c.SchedulingDomain {
		return
	}
	log := ctrl.LoggerFrom(ctx)
	log.Info("knowledge changed, re-evaluating all pipelines", "knowledgeName", obj.Name)
	// Find all pipelines depending on this knowledge and re-evaluate them.
	var pipelines v1alpha1.PipelineList
	if err := c.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for knowledge change", "knowledgeName", obj.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		// TODO: Not all pipelines may depend on this knowledge. At the moment
		// we re-evaluate all pipelines matching this controller.
		if pipeline.Spec.SchedulingDomain != c.SchedulingDomain {
			continue
		}
		if pipeline.Spec.Type != c.Initializer.PipelineType() {
			continue
		}
		c.handlePipelineChange(ctx, &pipeline, queue)
	}
}

// Handler bound to a knowledge watch to handle created knowledges.
//
// This handler will re-evaluate all pipelines depending on the knowledge.
func (c *BasePipelineController[PipelineType]) HandleKnowledgeCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	knowledgeConf := evt.Object.(*v1alpha1.Knowledge)
	c.handleKnowledgeChange(ctx, knowledgeConf, queue)
}

// Handler bound to a knowledge watch to handle updated knowledges.
//
// This handler will re-evaluate all pipelines depending on the knowledge.
func (c *BasePipelineController[PipelineType]) HandleKnowledgeUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	before := evt.ObjectOld.(*v1alpha1.Knowledge)
	after := evt.ObjectNew.(*v1alpha1.Knowledge)
	errorBefore := meta.IsStatusConditionFalse(before.Status.Conditions, v1alpha1.KnowledgeConditionReady)
	errorAfter := meta.IsStatusConditionFalse(after.Status.Conditions, v1alpha1.KnowledgeConditionReady)
	errorChanged := errorBefore != errorAfter
	dataBecameAvailable := before.Status.RawLength == 0 && after.Status.RawLength > 0
	if !errorChanged && !dataBecameAvailable {
		// No relevant change, skip re-evaluation.
		return
	}
	c.handleKnowledgeChange(ctx, after, queue)
}

// Handler bound to a knowledge watch to handle deleted knowledges.
//
// This handler will re-evaluate all pipelines depending on the knowledge.
func (c *BasePipelineController[PipelineType]) HandleKnowledgeDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	knowledgeConf := evt.Object.(*v1alpha1.Knowledge)
	c.handleKnowledgeChange(ctx, knowledgeConf, queue)
}
