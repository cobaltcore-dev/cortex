// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"fmt"

	knowledgev1alpha1 "github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The base pipeline controller will delegate some methods to the parent
// controller struct. The parent controller only needs to conform to this
// interface and set the delegate field accordingly.
type BasePipelineControllerDelegate[RequestType PipelineRequest] interface {
	// Initialize a new pipeline with the given steps.
	//
	// This method is delegated to the parent controller, when a pipeline needs
	// to be newly initialized or re-initialized to update it in the pipeline
	// map.
	InitPipeline(steps []v1alpha1.Step) (Pipeline[RequestType], error)
}

// Base controller for decision pipelines.
type BasePipelineController[RequestType PipelineRequest] struct {
	// Available pipelines by their name.
	Pipelines map[string]Pipeline[RequestType]
	// Delegate to create pipelines.
	Delegate BasePipelineControllerDelegate[RequestType]
	// Kubernetes client to manage/fetch resources.
	client.Client
}

// Handle the startup of the manager by initializing the pipeline map.
func (c *BasePipelineController[RequestType]) InitAllPipelines(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("initializing pipeline map")
	c.Pipelines = make(map[string]Pipeline[RequestType])
	// List all existing pipelines and initialize them.
	var pipelines v1alpha1.PipelineList
	if err := c.List(ctx, &pipelines); err != nil {
		return fmt.Errorf("failed to list existing pipelines: %w", err)
	}
	for _, pipelineConf := range pipelines.Items {
		log.Info("initializing existing pipeline", "pipelineName", pipelineConf.Name)
		c.handlePipelineChange(ctx, &pipelineConf, nil)
	}
	return nil
}

// Handle a pipeline creation or update event from watching pipeline resources.
func (c *BasePipelineController[RequestType]) handlePipelineChange(
	ctx context.Context,
	obj *v1alpha1.Pipeline,
	_ workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	log := ctrl.LoggerFrom(ctx)
	// Get all configured steps for the pipeline.
	var steps []v1alpha1.Step
	obj.Status.TotalSteps, obj.Status.ReadySteps = len(obj.Spec.Steps), 0
	var err error
	for _, step := range obj.Spec.Steps {
		stepConf := &v1alpha1.Step{}
		log.Info("checking step for pipeline", "pipelineName", obj.Name, "stepName", step.Ref.Name)
		if err := c.Get(ctx, client.ObjectKey{
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
		obj.Status.Error = err.Error()
		if err := c.Status().Update(ctx, obj); err != nil {
			log.Error(err, "failed to update pipeline status", "pipelineName", obj.Name)
		}
		delete(c.Pipelines, obj.Name)
		return
	}
	// Delegate to the parent controller which knows how to create the pipeline.
	c.Pipelines[obj.Name], err = c.Delegate.InitPipeline(steps)
	if err != nil {
		log.Error(err, "failed to create pipeline", "pipelineName", obj.Name)
		obj.Status.Ready = false
		obj.Status.Error = err.Error()
		if err := c.Status().Update(ctx, obj); err != nil {
			log.Error(err, "failed to update pipeline status", "pipelineName", obj.Name)
		}
		delete(c.Pipelines, obj.Name)
		return
	}
	log.Info("pipeline created and ready", "pipelineName", obj.Name)
	obj.Status.Ready = true
	obj.Status.Error = ""
	if err := c.Status().Update(ctx, obj); err != nil {
		log.Error(err, "failed to update pipeline status", "pipelineName", obj.Name)
		return
	}
}

// Handler bound to a pipeline watch to handle created pipelines.
//
// This handler will initialize new pipelines as needed and put them into the
// pipeline map.
func (c *BasePipelineController[RequestType]) HandlePipelineCreated(
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
func (c *BasePipelineController[RequestType]) HandlePipelineUpdated(
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
func (c *BasePipelineController[RequestType]) HandlePipelineDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	pipelineConf := evt.Object.(*v1alpha1.Pipeline)
	delete(c.Pipelines, pipelineConf.Name)
}

// Handle a step creation or update event from watching step resources.
func (c *BasePipelineController[RequestType]) handleStepChange(
	ctx context.Context,
	obj *v1alpha1.Step,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	log := ctrl.LoggerFrom(ctx)
	// Check the status of all knowledges depending on this step.
	obj.Status.ReadyKnowledges = 0
	obj.Status.TotalKnowledges = len(obj.Spec.Knowledges)
	for _, knowledgeRef := range obj.Spec.Knowledges {
		knowledge := &knowledgev1alpha1.Knowledge{}
		if err := c.Get(ctx, client.ObjectKey{
			Name:      knowledgeRef.Name,
			Namespace: knowledgeRef.Namespace,
		}, knowledge); err != nil {
			log.Error(err, "failed to get knowledge depending on step", "knowledgeName", knowledgeRef.Name)
			continue
		}
		if knowledge.Status.Error != "" {
			continue
		}
		obj.Status.ReadyKnowledges++
	}
	obj.Status.KnowledgesReadyFrac = fmt.Sprintf("%d/%d", obj.Status.ReadyKnowledges, obj.Status.TotalKnowledges)
	if obj.Status.ReadyKnowledges != obj.Status.TotalKnowledges {
		obj.Status.Ready = false
		obj.Status.Error = "not all knowledges are ready"
		log.Info("step not ready, not all knowledges are ready", "stepName", obj.Name)
	} else {
		obj.Status.Ready = true
		obj.Status.Error = ""
		log.Info("step is ready", "stepName", obj.Name)
	}
	if err := c.Status().Update(ctx, obj); err != nil {
		log.Error(err, "failed to update step status", "stepName", obj.Name)
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
func (c *BasePipelineController[RequestType]) HandleStepCreated(
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
func (c *BasePipelineController[RequestType]) HandleStepUpdated(
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
func (c *BasePipelineController[RequestType]) HandleStepDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	stepConf := evt.Object.(*v1alpha1.Step)
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
