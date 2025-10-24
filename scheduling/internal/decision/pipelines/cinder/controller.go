// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"encoding/json"
	"time"

	api "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/decision/pipelines/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type DecisionReconciler struct {
	// Available pipelines by their name.
	Pipelines map[string]lib.Pipeline[api.ExternalSchedulerRequest]
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
	pipeline, ok := s.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found", "pipelineName", decision.Spec.PipelineRef.Name)
		return ctrl.Result{}, nil
	}
	if decision.Spec.CinderRaw == nil {
		log.Info("skipping decision, no cinderRaw spec defined")
		return ctrl.Result{}, nil
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.CinderRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal cinderRaw spec")
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

func (s *DecisionReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-cinder-decisions").
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
				// Only handle cinder decisions.
				return decision.Spec.Type == v1alpha1.DecisionTypeCinderVolume
			})),
		).
		Complete(s)
}
