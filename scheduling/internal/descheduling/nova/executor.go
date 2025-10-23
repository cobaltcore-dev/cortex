// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"time"

	"github.com/cobaltcore-dev/cortex/descheduler/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/descheduler/internal/conf"
	"github.com/sapcc/go-bits/jobloop"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type Executor struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the deschedulings.
	Scheme *runtime.Scheme

	// Nova API to execute the descheduling operations.
	NovaAPI NovaAPI
	// Configuration for the descheduler.
	Conf conf.Config
	// Monitor for tracking the descheduler execution.
	Monitor Monitor
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (e *Executor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	descheduling := &v1alpha1.Descheduling{}
	if err := e.Get(ctx, req.NamespacedName, descheduling); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Currently we only know how to handle deschedulings for nova VMs.
	if descheduling.Spec.RefType != v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID {
		log.Info("skipping descheduling, unsupported refType", "refType", descheduling.Spec.RefType)
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
		descheduling.Status.Error = "unsupported refType: " + string(descheduling.Spec.RefType)
		if err := e.Status().Update(ctx, descheduling); err != nil {
			log.Error(err, "failed to update descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Currently we only know how to handle deschedulings for nova compute hosts.
	if descheduling.Spec.PrevHostType != v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName {
		log.Info("skipping descheduling, unsupported prevHostType", "prevHostType", descheduling.Spec.PrevHostType)
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
		descheduling.Status.Error = "unsupported prevHostType: " + string(descheduling.Spec.PrevHostType)
		if err := e.Status().Update(ctx, descheduling); err != nil {
			log.Error(err, "failed to update descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// We need a server uuid to proceed.
	if descheduling.Spec.Ref == "" {
		log.Info("skipping descheduling, missing ref")
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
		descheduling.Status.Error = "missing ref"
		if err := e.Status().Update(ctx, descheduling); err != nil {
			log.Error(err, "failed to update descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Don't touch processing deschedulings.
	if descheduling.Status.Phase == v1alpha1.DeschedulingStatusPhaseInProgress {
		log.Info("skipping descheduling, already in progress")
		return ctrl.Result{}, nil
	}

	vmId := descheduling.Spec.Ref
	server, err := e.NovaAPI.Get(ctx, vmId)
	if err != nil {
		// Delete the descheduling if the VM was not found.
		log.Info("VM not found, deleting descheduling", "vmId", vmId)
		if err := e.Delete(ctx, descheduling); err != nil {
			log.Error(err, "failed to delete descheduling for missing VM", "vmId", vmId)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Don't touch servers which don't match the provided previous host.
	if descheduling.Spec.PrevHost != "" && server.ComputeHost != descheduling.Spec.PrevHost {
		log.Error(errors.New("VM not on expected host"), "skipping descheduling, VM not on expected host", "vmId", vmId, "expectedHost", descheduling.Spec.PrevHost, "actualHost", server.ComputeHost)
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
		descheduling.Status.Error = "VM not on expected host, expected: " + descheduling.Spec.PrevHost + ", actual: " + server.ComputeHost
		if err := e.Status().Update(ctx, descheduling); err != nil {
			log.Error(err, "failed to update descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Don't touch servers that are turned off or are in an error state.
	if server.Status != "ACTIVE" {
		log.Error(errors.New("VM not active"), "skipping descheduling, VM not active", "vmId", vmId)
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
		descheduling.Status.Error = "VM not active, current status: " + server.Status
		if err := e.Status().Update(ctx, descheduling); err != nil {
			log.Error(err, "failed to update descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	log.Info(
		"descheduler: live-migrating virtual machine",
		"vmId", vmId, "host", server.ComputeHost,
	)

	if !e.Conf.Nova.DisableDryRun {
		log.Info("descheduler: dry-run enabled, skipping execution", "vmId", vmId)
		return ctrl.Result{}, nil
	}

	log.Info("descheduler: executing migration for VM", "vmId", vmId)
	if err := e.NovaAPI.LiveMigrate(ctx, vmId); err != nil {
		log.Error(err, "descheduler: failed to live-migrate VM", "vmId", vmId, "error", err)
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
		descheduling.Status.Error = "failed to live-migrate VM: " + err.Error()
		if err := e.Status().Update(ctx, descheduling); err != nil {
			log.Error(err, "failed to update descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	// Wait for the migration to complete.
	log.Info("descheduler: live-migration started", "vmId", vmId)
	for {
		server, err = e.NovaAPI.Get(ctx, vmId)
		if err != nil {
			log.Error(err, "descheduler: failed to get VM status", "vmId", vmId)
			// Consider migration as failed
			descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
			descheduling.Status.Error = "failed to get VM status: " + err.Error()
			if err := e.Status().Update(ctx, descheduling); err != nil {
				log.Error(err, "failed to update descheduling status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if server.Status == "ACTIVE" {
			log.Info(
				"descheduler: live-migration completed, VM now runs on new host",
				"vmId", vmId, "host", server.ComputeHost,
			)
			break
		}
		if server.Status == "ERROR" {
			log.Error(errors.New("live-migration failed for VM "+vmId), "descheduler: live-migration failed", "vmId", vmId)
			descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseFailed
			descheduling.Status.Error = "live-migration failed for VM " + vmId
			if err := e.Status().Update(ctx, descheduling); err != nil {
				log.Error(err, "failed to update descheduling status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		log.Info(
			"descheduler: waiting for live-migration to complete",
			"vmId", vmId, "status", server.Status,
		)
		time.Sleep(jobloop.DefaultJitter(time.Second))
	}

	descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseCompleted
	descheduling.Status.Error = ""
	descheduling.Status.NewHost = server.ComputeHost
	descheduling.Status.NewHostType = v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName
	if err := e.Status().Update(ctx, descheduling); err != nil {
		log.Error(err, "failed to update descheduling status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (s *Executor) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cortex-descheduler").
		For(
			&v1alpha1.Descheduling{},
			// Only schedule machines that have the custom scheduler set.
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				deschedulings := obj.(*v1alpha1.Descheduling)
				// We only care about deschedulings that are not completed yet.
				if deschedulings.Status.Phase == v1alpha1.DeschedulingStatusPhaseCompleted {
					return false
				}
				// We don't care about deschedulings that failed.
				if deschedulings.Status.Phase == v1alpha1.DeschedulingStatusPhaseFailed {
					return false
				}
				return true
			})),
		).
		Complete(s)
}
