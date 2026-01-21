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
	InitPipeline(ctx context.Context, p v1alpha1.Pipeline) (PipelineType, error)
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

	// Check if all steps are ready. If not, check if the step is mandatory.
	obj.Status.TotalSteps = len(obj.Spec.Steps)
	obj.Status.ReadySteps = 0
	for _, step := range obj.Spec.Steps {
		err := c.checkStepReady(ctx, &step)
		if err == nil {
			obj.Status.ReadySteps++
			continue
		}
		if step.Mandatory {
			meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.PipelineConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "MandatoryStepNotReady",
				Message: fmt.Sprintf("mandatory step %s not ready: %s", step.Name, err.Error()),
			})
			patch := client.MergeFrom(old)
			if err := c.Status().Patch(ctx, obj, patch); err != nil {
				log.Error(err, "failed to patch pipeline status", "pipelineName", obj.Name)
			}
			delete(c.Pipelines, obj.Name)
			delete(c.PipelineConfigs, obj.Name)
			return
		}
	}
	obj.Status.StepsReadyFrac = fmt.Sprintf("%d/%d", obj.Status.ReadySteps, obj.Status.TotalSteps)

	var err error
	c.Pipelines[obj.Name], err = c.Initializer.InitPipeline(ctx, *obj)
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

// Check if a step is ready, and if not, return an error indicating why not.
func (c *BasePipelineController[PipelineType]) checkStepReady(
	ctx context.Context,
	obj *v1alpha1.StepSpec,
) error {

	log := ctrl.LoggerFrom(ctx)
	// Check the status of all knowledges depending on this step.
	readyKnowledges := 0
	totalKnowledges := len(obj.Knowledges)
	for _, knowledgeRef := range obj.Knowledges {
		knowledge := &v1alpha1.Knowledge{}
		if err := c.Get(ctx, client.ObjectKey{
			Name:      knowledgeRef.Name,
			Namespace: knowledgeRef.Namespace,
		}, knowledge); err != nil {
			log.Error(err, "failed to get knowledge depending on step", "knowledgeName", knowledgeRef.Name)
			continue
		}
		// Check if the knowledge status conditions indicate an error.
		if meta.IsStatusConditionFalse(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
			log.Info("knowledge not ready due to error condition", "knowledgeName", knowledgeRef.Name)
			continue
		}
		if knowledge.Status.RawLength == 0 {
			log.Info("knowledge not ready, no data available", "knowledgeName", knowledgeRef.Name)
			continue
		}
		readyKnowledges++
	}
	if readyKnowledges != totalKnowledges {
		return fmt.Errorf(
			"%d/%d knowledges ready",
			readyKnowledges, totalKnowledges,
		)
	}
	return nil
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
	log.Info("knowledge changed, re-evaluating dependent pipelines", "knowledgeName", obj.Name)
	// Find all pipelines depending on this knowledge and re-evaluate them.
	var pipelines v1alpha1.PipelineList
	if err := c.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for knowledge", "knowledgeName", obj.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		needsUpdate := false
		for _, step := range pipeline.Spec.Steps {
			for _, knowledgeRef := range step.Knowledges {
				if knowledgeRef.Name == obj.Name && knowledgeRef.Namespace == obj.Namespace {
					needsUpdate = true
					break
				}
			}
		}
		if needsUpdate {
			log.Info("re-evaluating pipeline due to knowledge change", "pipelineName", pipeline.Name)
			c.handlePipelineChange(ctx, &pipeline, queue)
		}
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
