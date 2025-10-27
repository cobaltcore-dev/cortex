// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	knowledgev1alpha1 "github.com/cobaltcore-dev/cortex/knowledge/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/lib/db"
	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type DecisionReconciler struct {
	// Available pipelines by their name.
	pipelines map[string]lib.Pipeline[api.ExternalSchedulerRequest]
	// Database to pass down to all steps.
	DB db.DB
	// Monitor to pass down to all pipelines.
	Monitor lib.PipelineMonitor
	// Config for the scheduling operator.
	Conf conf.Config
	// Kubernetes client to manage/fetch resources.
	client.Client
	// Scheme for the Kubernetes client.
	Scheme *runtime.Scheme
}

func (s *DecisionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startedAt := time.Now() // So we can measure sync duration.
	log := ctrl.LoggerFrom(ctx)

	decision := &v1alpha1.Decision{}
	if err := s.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	pipeline, ok := s.pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return ctrl.Result{}, nil
	}
	if decision.Spec.NovaRaw == nil {
		log.Info("skipping decision, no novaRaw spec defined")
		return ctrl.Result{}, nil
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.NovaRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal novaRaw spec")
		return ctrl.Result{}, err
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline")
		return ctrl.Result{}, err
	}
	decision.Status.Result = &result
	decision.Status.Took = metav1.Duration{Duration: time.Since(startedAt)}
	if err := s.Status().Update(ctx, decision); err != nil {
		log.Error(err, "failed to update decision status")
		return ctrl.Result{}, err
	}
	log.Info("decision processed successfully", "duration", time.Since(startedAt))
	return ctrl.Result{}, nil
}

func (s *DecisionReconciler) handlePipelineChange(
	ctx context.Context,
	obj *v1alpha1.Pipeline,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	log := ctrl.LoggerFrom(ctx)
	// Get all configured steps for the pipeline.
	var steps []v1alpha1.Step
	obj.Status.TotalSteps, obj.Status.ReadySteps = len(obj.Spec.Steps), 0
	mandatoryStepNotReady := false
	for _, step := range obj.Spec.Steps {
		stepConf := &v1alpha1.Step{}
		if err := s.Get(ctx, client.ObjectKey{
			Name:      step.StepRef.Name,
			Namespace: step.StepRef.Namespace,
		}, stepConf); err != nil {
			continue
		}
		if !stepConf.Status.Ready {
			if step.Mandatory {
				mandatoryStepNotReady = true
			}
			continue
		}
		obj.Status.ReadySteps++
		steps = append(steps, *stepConf)
	}
	obj.Status.StepsReadyFrac = fmt.Sprintf("%d/%d", obj.Status.ReadySteps, obj.Status.TotalSteps)
	if mandatoryStepNotReady {
		log.Info("pipeline not ready, mandatory step not ready", "pipelineName", obj.Name)
		obj.Status.Ready = false
		obj.Status.Error = "mandatory step not ready"
		if err := s.Status().Update(ctx, obj); err != nil {
			log.Error(err, "failed to update pipeline status", "pipelineName", obj.Name)
		}
		delete(s.pipelines, obj.Name)
		return
	}
	var err error
	s.pipelines[obj.Name], err = NewPipeline(steps, s.DB, s.Monitor)
	if err != nil {
		log.Error(err, "failed to create pipeline", "pipelineName", obj.Name)
		obj.Status.Ready = false
		obj.Status.Error = err.Error()
		if err := s.Status().Update(ctx, obj); err != nil {
			log.Error(err, "failed to update pipeline status", "pipelineName", obj.Name)
		}
		delete(s.pipelines, obj.Name)
		return
	}
	log.Info("pipeline created and ready", "pipelineName", obj.Name)
	obj.Status.Ready = true
	obj.Status.Error = ""
	if err := s.Status().Update(ctx, obj); err != nil {
		log.Error(err, "failed to update pipeline status", "pipelineName", obj.Name)
		return
	}
}

func (s *DecisionReconciler) handlePipelineCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	pipelineConf := evt.Object.(*v1alpha1.Pipeline)
	s.handlePipelineChange(ctx, pipelineConf, queue)
}

func (s *DecisionReconciler) handlePipelineUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	pipelineConf := evt.ObjectNew.(*v1alpha1.Pipeline)
	s.handlePipelineChange(ctx, pipelineConf, queue)
}

func (s *DecisionReconciler) handlePipelineDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	pipelineConf := evt.Object.(*v1alpha1.Pipeline)
	delete(s.pipelines, pipelineConf.Name)
}

func (s *DecisionReconciler) handleStepChange(
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
		if err := s.Get(ctx, client.ObjectKey{
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
	if err := s.Status().Update(ctx, obj); err != nil {
		log.Error(err, "failed to update step status", "stepName", obj.Name)
		return
	}
	// Find all pipelines depending on this step and re-evaluate them.
	var pipelines v1alpha1.PipelineList
	if err := s.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for step", "stepName", obj.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		needsUpdate := false
		for _, stepRef := range pipeline.Spec.Steps {
			if stepRef.StepRef.Name == obj.Name && stepRef.StepRef.Namespace == obj.Namespace {
				needsUpdate = true
				break
			}
		}
		if needsUpdate {
			s.handlePipelineChange(ctx, &pipeline, queue)
		}
	}
}

func (s *DecisionReconciler) handleStepCreated(
	ctx context.Context,
	evt event.CreateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	stepConf := evt.Object.(*v1alpha1.Step)
	s.handleStepChange(ctx, stepConf, queue)
}

func (s *DecisionReconciler) handleStepUpdated(
	ctx context.Context,
	evt event.UpdateEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	stepConf := evt.ObjectNew.(*v1alpha1.Step)
	s.handleStepChange(ctx, stepConf, queue)
}

func (s *DecisionReconciler) handleStepDeleted(
	ctx context.Context,
	evt event.DeleteEvent,
	queue workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	stepConf := evt.Object.(*v1alpha1.Step)
	// When a step is deleted, we need to re-evaluate all pipelines depending on it.
	var pipelines v1alpha1.PipelineList
	log := ctrl.LoggerFrom(ctx)
	if err := s.List(ctx, &pipelines); err != nil {
		log.Error(err, "failed to list pipelines for deleted step", "stepName", stepConf.Name)
		return
	}
	for _, pipeline := range pipelines.Items {
		needsUpdate := false
		for _, stepRef := range pipeline.Spec.Steps {
			if stepRef.StepRef.Name == stepConf.Name && stepRef.StepRef.Namespace == stepConf.Namespace {
				needsUpdate = true
				break
			}
		}
		if needsUpdate {
			s.handlePipelineChange(ctx, &pipeline, queue)
		}
	}
}

func (s *DecisionReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-nova-decisions").
		For(
			&v1alpha1.Decision{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				decision := obj.(*v1alpha1.Decision)
				if decision.Spec.Operator != s.Conf.Operator {
					return false
				}
				// Ignore already decided schedulings.
				if decision.Status.Error != "" || decision.Status.Result != nil {
					return false
				}
				// Only handle nova decisions.
				return decision.Spec.Type == v1alpha1.DecisionTypeNovaServer
			})),
		).
		Watches(
			&v1alpha1.Pipeline{},
			handler.Funcs{
				CreateFunc: s.handlePipelineCreated,
				UpdateFunc: s.handlePipelineUpdated,
				DeleteFunc: s.handlePipelineDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				pipeline := obj.(*v1alpha1.Pipeline)
				// Only react to pipelines matching the operator.
				if pipeline.Spec.Operator != s.Conf.Operator {
					return false
				}
				return pipeline.Spec.Type == v1alpha1.PipelineTypeFilterWeigher
			})),
		).
		Watches(
			&v1alpha1.Step{},
			handler.Funcs{
				CreateFunc: s.handleStepCreated,
				UpdateFunc: s.handleStepUpdated,
				DeleteFunc: s.handleStepDeleted,
			},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				step := obj.(*v1alpha1.Step)
				// Only react to steps matching the operator.
				if step.Spec.Operator != s.Conf.Operator {
					return false
				}
				// Only react to filter and weigher steps.
				supportedTypes := []v1alpha1.StepType{
					v1alpha1.StepTypeFilter,
					v1alpha1.StepTypeWeigher,
				}
				return slices.Contains(supportedTypes, step.Spec.Type)
			})),
		).
		Complete(s)
}
