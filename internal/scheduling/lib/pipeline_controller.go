// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
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
	// Event recorder for publishing events.
	Recorder events.EventRecorder

	DecisionQueue chan DecisionUpdate
}

type DecisionUpdate struct {
	ResourceID   string
	PipelineName string
	Result       FilterWeigherPipelineResult
	Intent       v1alpha1.SchedulingIntent
}

func (c *BasePipelineController[PipelineType]) StartExplainer(ctx context.Context) {
	log := ctrl.LoggerFrom(ctx)
	for {
		select {
		case <-ctx.Done():
			log.Info("Context cancelled, draining decision queue before shutdown")
			// Drain the queue with a background context to ensure we process all pending decisions
			close(c.DecisionQueue)
			drainCtx := context.Background()
			for update := range c.DecisionQueue {
				if err := c.updateDecision(drainCtx, update); err != nil {
					log.Error(err, "failed to update decision during shutdown", "resourceID", update.ResourceID, "domain", c.SchedulingDomain)
				}
			}
			log.Info("Decision queue drained, explainer shutting down")
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

	explanationText, err := explainer.Explain(ctx, update)
	if err != nil {
		return fmt.Errorf("failed to generate explanation: %w", err)
	}

	// Try to get existing decision
	decision := &v1alpha1.Decision{}
	if err = c.Get(ctx, client.ObjectKey{Name: update.ResourceID}, decision); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get decision: %w", err)
		}

		// Decision doesn't exist - create new one
		decision = &v1alpha1.Decision{
			ObjectMeta: metav1.ObjectMeta{
				Name: update.ResourceID,
			},
			Spec: v1alpha1.DecisionSpec{
				SchedulingDomain: c.SchedulingDomain,
				ResourceID:       update.ResourceID,
			},
		}

		if err := c.Create(ctx, decision); err != nil {
			return fmt.Errorf("failed to create decision: %w", err)
		}
		log.Info("Created new decision", "resourceID", update.ResourceID)
	}

	// Get the pipeline config to check max history entries
	pipelineConfig, ok := c.PipelineConfigs[update.PipelineName]
	if !ok {
		return fmt.Errorf("pipeline config %s not found", update.PipelineName)
	}

	// Prepare the scheduling history entry
	historyEntry := v1alpha1.SchedulingHistoryEntry{
		OrderedHosts: update.Result.OrderedHosts,
		Timestamp:    metav1.Now(),
		PipelineRef: corev1.ObjectReference{
			APIVersion: "cortex.cloud/v1alpha1",
			Kind:       "Pipeline",
			Name:       update.PipelineName,
		},
		Intent: update.Intent,
	}

	// Check if scheduling failed (no hosts available)
	schedulingFailed := len(update.Result.OrderedHosts) == 0

	// Update status with retry on conflict to handle concurrent updates
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest version before each retry attempt
		if err := c.Get(ctx, client.ObjectKey{Name: update.ResourceID}, decision); err != nil {
			return err
		}

		// Apply status updates
		decision.Status.Explanation = explanationText

		if schedulingFailed {
			decision.Status.TargetHost = ""
			meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.DecisionConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "NoValidHosts",
				Message: "Cannot schedule: No valid hosts available after filtering",
			})
		} else {
			decision.Status.TargetHost = update.Result.OrderedHosts[0]
			meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.DecisionConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "Scheduled",
				Message: "Scheduling decision made successfully",
			})
		}

		decision.Status.SchedulingHistory = append(decision.Status.SchedulingHistory, historyEntry)

		// Limit history entries if configured
		maxEntries := pipelineConfig.Spec.MaxHistoryEntries
		// If maxEntries is set to 0 we want to keep all history entries, otherwise trim the history to maxEntries.
		if maxEntries > 0 && len(decision.Status.SchedulingHistory) > maxEntries {
			// Keep only the most recent entries
			decision.Status.SchedulingHistory = decision.Status.SchedulingHistory[len(decision.Status.SchedulingHistory)-maxEntries:]
		}

		return c.Status().Update(ctx, decision)
	})

	if err != nil {
		return fmt.Errorf("failed to update decision status: %w", err)
	}

	// Publish event to the decision
	if c.Recorder != nil {
		if schedulingFailed {
			// Warning event for failed scheduling
			c.Recorder.Eventf(decision, nil, corev1.EventTypeWarning, "NoValidHosts", "Scheduling", "Cannot schedule: No valid hosts available. %s", explanationText)
			log.Info("Published NoValidHosts event", "resourceID", update.ResourceID)
		} else {
			// Normal event for successful scheduling
			intentStr := string(update.Intent)
			c.Recorder.Eventf(decision, nil, corev1.EventTypeNormal, intentStr, "Scheduling", "Scheduled to %s. %s", decision.Status.TargetHost, explanationText)
			log.Info("Published scheduling event", "resourceID", update.ResourceID, "targetHost", decision.Status.TargetHost, "reason", update.Intent)
		}
	}

	log.Info("Successfully updated decision", "resourceID", update.ResourceID, "targetHost", decision.Status.TargetHost, "schedulingFailed", schedulingFailed)
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
		if client.IgnoreNotFound(err) == nil {
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

	// If there were unknown filters, weighers, or detectors, report that in
	// the status but continue running the pipeline.
	var unknownSteps []string
	unknownSteps = append(unknownSteps, initResult.UnknownFilters...)
	unknownSteps = append(unknownSteps, initResult.UnknownWeighers...)
	unknownSteps = append(unknownSteps, initResult.UnknownDetectors...)
	if len(unknownSteps) > 0 {
		errmsg := fmt.Sprintf("pipeline contains %d unknown steps: %v",
			len(unknownSteps), unknownSteps)
		log.Info("unknown steps in pipeline configuration",
			"pipelineName", obj.Name, "issue", errmsg)
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionAllStepsIndexed,
			Status:  metav1.ConditionFalse,
			Reason:  "PipelineContainsUnknownSteps",
			Message: errmsg,
		})
	} else {
		meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.PipelineConditionAllStepsIndexed,
			Status:  metav1.ConditionTrue,
			Reason:  "AllStepsIndexed",
			Message: "all pipeline steps are indexed and known by the controller",
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
