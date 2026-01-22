// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
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

// The base pipeline controller will delegate some methods to the parent
// controller struct. The parent controller only needs to conform to this
// interface and set the delegate field accordingly.
type PipelineInitializer[PipelineType any] interface {
	// Initialize a new pipeline with the given steps.
	//
	// This method is delegated to the parent controller, when a pipeline needs
	// to be newly initialized or re-initialized to update it in the pipeline
	// map.
	//
	// Ready and total indicate how many steps are ready out of the total
	// configured steps. Sometimes, steps may be unready but this does not
	// prevent the pipeline from being created, e.g., when weighers depend
	// on knowledges that are not yet ready.
	//
	// If there was an error that blocks us from creating the pipeline, return
	// a non-nil error.
	InitPipeline(ctx context.Context, p v1alpha1.Pipeline) (
		pipeline PipelineType,
		ready int,
		total int,
		err error,
	)

	// Collect knowledge dependencies for the given pipeline.
	//
	// This is used to determine which pipelines depend on which knowledges
	// when a knowledge changes.
	CollectKnowledgeDependencies(p v1alpha1.Pipeline) []string

	// Get the accepted pipeline type for this controller.
	//
	// This is used to filter pipelines when listing existing pipelines on
	// startup or when reacting to pipeline events.
	PipelineType() v1alpha1.PipelineType
}

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

	var err error
	c.Pipelines[obj.Name], obj.Status.ReadySteps, obj.Status.TotalSteps, err = c.
		Initializer.InitPipeline(ctx, *obj)
	obj.Status.StepsReadyFrac = fmt.Sprintf("%d/%d", obj.Status.ReadySteps, obj.Status.TotalSteps)
	c.PipelineConfigs[obj.Name] = *obj
	if err != nil {
		log.Error(err, "failed to create pipeline", "pipelineName", obj.Name)
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionReady,
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

// Handle a knowledge creation, update, or delete event from watching knowledge resources.
func (c *BasePipelineController[PipelineType]) handleKnowledgeChange(
	ctx context.Context,
	obj *v1alpha1.Knowledge,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	if obj.Spec.SchedulingDomain != c.SchedulingDomain {
		return
	}
	log := ctrl.LoggerFrom(ctx)
	log.Info("knowledge changed readiness/availability, re-evaluating pipelines", "knowledgeName", obj.Name)
	var pipelines v1alpha1.PipelineList
	if err := c.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for knowledge", "knowledgeName", obj.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		if pipeline.Spec.SchedulingDomain != c.SchedulingDomain {
			continue
		}
		if pipeline.Spec.Type != c.Initializer.PipelineType() {
			continue
		}
		knowledgeDeps := c.Initializer.CollectKnowledgeDependencies(pipeline)
		found := false
		for _, knowledgeName := range knowledgeDeps {
			if knowledgeName == obj.Name {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		log.Info("re-evaluating pipeline due to knowledge change", "pipelineName", pipeline.Name, "knowledgeName", obj.Name)
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
