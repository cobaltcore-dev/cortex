// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	"github.com/cobaltcore-dev/cortex/lib/scheduling"
	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type MachineStep = scheduling.Step[MachinePipelineRequest]

// Configuration of steps supported by the scheduling.
// The steps actually used by the scheduler are defined through the configuration file.
var SupportedSteps = map[string]func() MachineStep{
	// Currently no steps are implemented.
}

type MachineScheduler struct {
	// Available pipelines by their name.
	Pipelines map[string]scheduling.Pipeline[MachinePipelineRequest]

	// Kubernetes client to manage/fetch resources.
	client.Client
	// Scheme for the Kubernetes client.
	Scheme *runtime.Scheme
}

// Called by the kubernetes apiserver to handle new or updated Machine resources.
func (s *MachineScheduler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Currently we will always look for a pipeline named "default".
	// In the future we could control this through the Machine spec.
	pipelineName := "default"
	pipeline, ok := s.Pipelines[pipelineName]
	if !ok {
		log.V(1).Info("skipping scheduling, no default pipeline configured")
		return ctrl.Result{}, nil
	}

	machine := &computev1alpha1.Machine{}
	if err := s.Get(ctx, req.NamespacedName, machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If the machine pool ref is unset, find a suitable machine pool.
	if machine.Spec.MachinePoolRef != nil {
		log.V(1).Info("skipping scheduling for instance with assigned machine pool")
		return ctrl.Result{}, nil
	}

	// Find all available machine pools.
	pools := &computev1alpha1.MachinePoolList{}
	if err := s.List(ctx, pools); err != nil {
		return ctrl.Result{}, err
	}
	if len(pools.Items) == 0 {
		log.V(1).Info("skipping scheduling, no machine pools available")
		return ctrl.Result{}, nil
	}

	// Execute the scheduling pipeline.
	request := MachinePipelineRequest{
		Pools:    pools.Items,
		Pipeline: pipelineName,
	}
	names, err := pipeline.Run(request)
	if err != nil {
		log.V(1).Error(err, "failed to run scheduler pipeline")
		return ctrl.Result{}, err
	}
	if len(names) == 0 {
		log.V(1).Info("skipping scheduling, no suitable machine pool found")
		return ctrl.Result{}, nil
	}

	// Assign the first machine pool returned by the pipeline.
	machine.Spec.MachinePoolRef = &corev1.LocalObjectReference{Name: names[0]}
	if err := s.Update(ctx, machine); err != nil {
		log.V(1).Error(err, "failed to assign machine pool to instance")
		return ctrl.Result{}, err
	}
	log.V(1).Info("assigned machine pool to instance", "machinePool", names[0])
	return ctrl.Result{}, nil
}

func (s *MachineScheduler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-machine-scheduler").
		For(&computev1alpha1.Machine{}).
		Complete(s)
}
