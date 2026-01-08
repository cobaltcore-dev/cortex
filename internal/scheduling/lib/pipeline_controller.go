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
	InitPipeline(ctx context.Context, name string, steps []v1alpha1.Step) (PipelineType, error)
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
	// Get all configured steps for the pipeline.
	var steps []v1alpha1.Step
	obj.Status.TotalSteps, obj.Status.ReadySteps = len(obj.Spec.Steps), 0
	var err error
	for _, step := range obj.Spec.Steps {
		stepConf := &v1alpha1.Step{}
		log.Info("checking step for pipeline", "pipelineName", obj.Name, "stepName", step.Ref.Name)
		if err = c.Get(ctx, client.ObjectKey{
			Name:      step.Ref.Name,
			Namespace: step.Ref.Namespace,
		}, stepConf); err != nil {
			err = fmt.Errorf("failed to get step %s: %w", step.Ref.Name, err)
			continue
		}
		if !stepConf.Status.Ready {
			if step.Mandatory {
				err = fmt.Errorf("mandatory step %s not ready", step.Ref.Name)
			}
			log.Info("step not ready", "pipelineName", obj.Name, "stepName", step.Ref.Name)
			continue
		}
		obj.Status.ReadySteps++
		steps = append(steps, *stepConf)
	}
	obj.Status.StepsReadyFrac = fmt.Sprintf("%d/%d", obj.Status.ReadySteps, obj.Status.TotalSteps)
	if err != nil {
		log.Error(err, "pipeline not ready due to step issues", "pipelineName", obj.Name)
		obj.Status.Ready = false
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.StepConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "StepNotReady",
			Message: err.Error(),
		})
		patch := client.MergeFrom(obj.DeepCopy())
		if err := c.Status().Patch(ctx, obj, patch); err != nil {
			log.Error(err, "failed to patch pipeline status", "pipelineName", obj.Name)
		}
		delete(c.Pipelines, obj.Name)
		delete(c.PipelineConfigs, obj.Name)
		return
	}
	c.Pipelines[obj.Name], err = c.Initializer.InitPipeline(ctx, obj.Name, steps)
	c.PipelineConfigs[obj.Name] = *obj
	if err != nil {
		log.Error(err, "failed to create pipeline", "pipelineName", obj.Name)
		obj.Status.Ready = false
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "PipelineInitFailed",
			Message: err.Error(),
		})
		patch := client.MergeFrom(obj.DeepCopy())
		if err := c.Status().Patch(ctx, obj, patch); err != nil {
			log.Error(err, "failed to patch pipeline status", "pipelineName", obj.Name)
		}
		delete(c.Pipelines, obj.Name)
		delete(c.PipelineConfigs, obj.Name)
		return
	}
	log.Info("pipeline created and ready", "pipelineName", obj.Name)
	obj.Status.Ready = true
	meta.RemoveStatusCondition(&obj.Status.Conditions, v1alpha1.PipelineConditionError)
	patch := client.MergeFrom(obj.DeepCopy())
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

// Handle a step creation or update event from watching step resources.
func (c *BasePipelineController[PipelineType]) handleStepChange(
	ctx context.Context,
	obj *v1alpha1.Step,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	if obj.Spec.SchedulingDomain != c.SchedulingDomain {
		return
	}
	log := ctrl.LoggerFrom(ctx)
	// Check the status of all knowledges depending on this step.
	obj.Status.ReadyKnowledges = 0
	obj.Status.TotalKnowledges = len(obj.Spec.Knowledges)
	for _, knowledgeRef := range obj.Spec.Knowledges {
		knowledge := &v1alpha1.Knowledge{}
		if err := c.Get(ctx, client.ObjectKey{
			Name:      knowledgeRef.Name,
			Namespace: knowledgeRef.Namespace,
		}, knowledge); err != nil {
			log.Error(err, "failed to get knowledge depending on step", "knowledgeName", knowledgeRef.Name)
			continue
		}
		// Check if the knowledge status conditions indicate an error.
		if meta.IsStatusConditionTrue(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionError) {
			log.Info("knowledge not ready due to error condition", "knowledgeName", knowledgeRef.Name)
			continue
		}
		if knowledge.Status.RawLength == 0 {
			log.Info("knowledge not ready, no data available", "knowledgeName", knowledgeRef.Name)
			continue
		}
		obj.Status.ReadyKnowledges++
	}
	obj.Status.KnowledgesReadyFrac = fmt.Sprintf("%d/%d", obj.Status.ReadyKnowledges, obj.Status.TotalKnowledges)
	if obj.Status.ReadyKnowledges != obj.Status.TotalKnowledges {
		obj.Status.Ready = false
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.StepConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "KnowledgesNotReady",
			Message: "not all knowledges are ready",
		})
		log.Info("step not ready, not all knowledges are ready", "stepName", obj.Name)
	} else {
		obj.Status.Ready = true
		meta.RemoveStatusCondition(&obj.Status.Conditions, v1alpha1.StepConditionError)
		log.Info("step is ready", "stepName", obj.Name)
	}
	patch := client.MergeFrom(obj.DeepCopy())
	if err := c.Status().Patch(ctx, obj, patch); err != nil {
		log.Error(err, "failed to patch step status", "stepName", obj.Name)
		return
	}
	// Find all pipelines depending on this step and re-evaluate them.
	var pipelines v1alpha1.PipelineList
	if err := c.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for step", "stepName", obj.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		needsUpdate := false
		for _, step := range pipeline.Spec.Steps {
			if step.Ref.Name == obj.Name && step.Ref.Namespace == obj.Namespace {
				needsUpdate = true
				break
			}
		}
		if needsUpdate {
			c.handlePipelineChange(ctx, &pipeline, queue)
		}
	}
}

// Handler bound to a step watch to handle created steps.
//
// This handler will look at the underlying resources of the step and check
// if they are ready. It will then re-evaluate all pipelines depending on the step.
func (c *BasePipelineController[PipelineType]) HandleStepCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	stepConf := evt.Object.(*v1alpha1.Step)
	c.handleStepChange(ctx, stepConf, queue)
}

// Handler bound to a step watch to handle updated steps.
//
// This handler will look at the underlying resources of the step and check
// if they are ready. It will then re-evaluate all pipelines depending on the step.
func (c *BasePipelineController[PipelineType]) HandleStepUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	stepConf := evt.ObjectNew.(*v1alpha1.Step)
	c.handleStepChange(ctx, stepConf, queue)
}

// Handler bound to a step watch to handle deleted steps.
//
// This handler will re-evaluate all pipelines depending on the step.
func (c *BasePipelineController[PipelineType]) HandleStepDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	stepConf := evt.Object.(*v1alpha1.Step)
	if stepConf.Spec.SchedulingDomain != c.SchedulingDomain {
		return
	}
	// When a step is deleted, we need to re-evaluate all pipelines depending on it.
	var pipelines v1alpha1.PipelineList
	log := ctrl.LoggerFrom(ctx)
	if err := c.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for deleted step", "stepName", stepConf.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		needsUpdate := false
		for _, step := range pipeline.Spec.Steps {
			if step.Ref.Name == stepConf.Name && step.Ref.Namespace == stepConf.Namespace {
				needsUpdate = true
				break
			}
		}
		if needsUpdate {
			c.handlePipelineChange(ctx, &pipeline, queue)
		}
	}
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
	log.Info("knowledge changed, re-evaluating dependent steps", "knowledgeName", obj.Name)
	// Find all steps depending on this knowledge and re-evaluate them.
	var steps v1alpha1.StepList
	if err := c.List(ctx, &steps); err != nil {
		log.Error(err, "failed to list steps for knowledge", "knowledgeName", obj.Name)
		return
	}
	for _, step := range steps.Items {
		needsUpdate := false
		for _, knowledgeRef := range step.Spec.Knowledges {
			if knowledgeRef.Name == obj.Name && knowledgeRef.Namespace == obj.Namespace {
				needsUpdate = true
				break
			}
		}
		if needsUpdate {
			c.handleStepChange(ctx, &step, queue)
		}
	}
}

// Handler bound to a knowledge watch to handle created knowledges.
//
// This handler will re-evaluate all steps depending on the knowledge.
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
// This handler will re-evaluate all steps depending on the knowledge.
func (c *BasePipelineController[PipelineType]) HandleKnowledgeUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	before := evt.ObjectOld.(*v1alpha1.Knowledge)
	after := evt.ObjectNew.(*v1alpha1.Knowledge)
	errorBefore := meta.IsStatusConditionTrue(before.Status.Conditions, v1alpha1.KnowledgeConditionError)
	errorAfter := meta.IsStatusConditionTrue(after.Status.Conditions, v1alpha1.KnowledgeConditionError)
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
// This handler will re-evaluate all steps depending on the knowledge.
func (c *BasePipelineController[PipelineType]) HandleKnowledgeDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {

	knowledgeConf := evt.Object.(*v1alpha1.Knowledge)
	c.handleKnowledgeChange(ctx, knowledgeConf, queue)
}
