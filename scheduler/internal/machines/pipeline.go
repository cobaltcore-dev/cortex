// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package machines

import (
	"context"

	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/lib/monitoring"
	"github.com/cobaltcore-dev/cortex/lib/mqtt"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type MachineStep = lib.Step[MachinePipelineRequest]

// Configuration of steps supported by the lib.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = map[string]func() MachineStep{}

type MachineScheduler struct {
	// Available pipelines by their name.
	Pipelines map[string]lib.Pipeline[MachinePipelineRequest]

	// Kubernetes client to manage/fetch resources.
	client.Client
	// Scheme for the Kubernetes client.
	Scheme *runtime.Scheme

	// Configuration for the machine scheduler.
	Conf conf.Config
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
	ctx := context.Background()
	// Our custom monitoring registry can add prometheus labels to all metrics.
	// This is useful to distinguish metrics from different deployments.
	registry := monitoring.NewRegistry(s.Conf.MonitoringConfig)

	// Currently the scheduler pipeline will always need a database and mqtt client.
	// In the future we will no longer need the mqtt client since events will be
	// exchanged through kubernetes resources. The database might also be optional
	// in the future, depending on the steps used in the pipeline.
	database := db.NewPostgresDB(ctx, s.Conf.DBConfig, registry, db.NewDBMonitor(registry))
	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	// MQTT Topic on which the pipeline will publish when it has finished.
	const topicFinished = "cortex/scheduler/machines/pipeline/finished"
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}

	// The pipeline monitor is a bucket for all metrics produced during the
	// execution of individual steps (see step monitor below) and the overall
	// pipeline.
	monitor := lib.NewPipelineMonitor(registry)
	// Step wrappers can be used to perform actions before or after each step.
	wrappers := []lib.StepWrapper[MachinePipelineRequest]{
		// Monitor the step execution.
		func(s MachineStep, conf conf.SchedulerStepConfig) MachineStep {
			// This monitor calculates detailed impact metrics for each step.
			return lib.MonitorStep(s, monitor)
		},
	}
	// We can execute different pipelines, e.g. depending on the machine spec.
	pipelines := make(
		map[string]lib.Pipeline[MachinePipelineRequest],
		len(s.Conf.Machines.Pipelines),
	)
	for _, pipelineConf := range s.Conf.Machines.Pipelines {
		plugins := pipelineConf.Plugins
		pipelines[pipelineConf.Name] = lib.NewPipeline(
			supportedSteps, plugins, wrappers,
			database, monitor, mqttClient, topicFinished,
		)
	}
	s.Pipelines = pipelines

	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-machine-scheduler").
		For(&computev1alpha1.Machine{}).
		Complete(s)
}
