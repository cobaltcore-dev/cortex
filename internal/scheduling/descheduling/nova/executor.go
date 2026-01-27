// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"

	"github.com/sapcc/go-bits/jobloop"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	Monitor lib.DetectorMonitor[plugins.VMDetection]
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
		old := descheduling.DeepCopy()
		meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DeschedulingConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UnsupportedRefType",
			Message: "unsupported refType: " + string(descheduling.Spec.RefType),
		})
		patch := client.MergeFrom(old)
		if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
			log.Error(err, "failed to patch descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Currently we only know how to handle deschedulings for nova compute hosts.
	if descheduling.Spec.PrevHostType != v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName {
		log.Info("skipping descheduling, unsupported prevHostType", "prevHostType", descheduling.Spec.PrevHostType)
		old := descheduling.DeepCopy()
		meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DeschedulingConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UnsupportedPrevHostType",
			Message: "unsupported prevHostType: " + string(descheduling.Spec.PrevHostType),
		})
		patch := client.MergeFrom(old)
		if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
			log.Error(err, "failed to patch descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// We need a server uuid to proceed.
	if descheduling.Spec.Ref == "" {
		log.Info("skipping descheduling, missing ref")
		old := descheduling.DeepCopy()
		meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DeschedulingConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingRef",
			Message: "missing ref",
		})
		patch := client.MergeFrom(old)
		if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
			log.Error(err, "failed to patch descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Don't touch processing deschedulings.
	if meta.IsStatusConditionTrue(descheduling.Status.Conditions, v1alpha1.DeschedulingConditionInProgress) {
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
		old := descheduling.DeepCopy()
		meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DeschedulingConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "VMNotOnExpectedHost",
			Message: "VM not on expected host, expected: " + descheduling.Spec.PrevHost + ", actual: " + server.ComputeHost,
		})
		patch := client.MergeFrom(old)
		if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
			log.Error(err, "failed to patch descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Don't touch servers that are turned off or are in an error state.
	if server.Status != "ACTIVE" {
		log.Error(errors.New("VM not active"), "skipping descheduling, VM not active", "vmId", vmId)
		old := descheduling.DeepCopy()
		meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DeschedulingConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "VMNotActive",
			Message: "VM not active, current status: " + server.Status,
		})
		patch := client.MergeFrom(old)
		if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
			log.Error(err, "failed to patch descheduling status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	log.Info(
		"descheduler: live-migrating virtual machine",
		"vmId", vmId, "host", server.ComputeHost,
	)

	if !e.Conf.DisableDeschedulerDryRun {
		log.Info("descheduler: dry-run enabled, skipping execution", "vmId", vmId)
		return ctrl.Result{}, nil
	}

	log.Info("descheduler: executing migration for VM", "vmId", vmId)
	if err := e.NovaAPI.LiveMigrate(ctx, vmId); err != nil {
		log.Error(err, "descheduler: failed to live-migrate VM", "vmId", vmId, "error", err)
		old := descheduling.DeepCopy()
		meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DeschedulingConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "LiveMigrationFailed",
			Message: "failed to live-migrate VM: " + err.Error(),
		})
		patch := client.MergeFrom(old)
		if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
			log.Error(err, "failed to patch descheduling status")
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
			old := descheduling.DeepCopy()
			meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.DeschedulingConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "GetVMStatusFailed",
				Message: "failed to get VM status: " + err.Error(),
			})
			patch := client.MergeFrom(old)
			if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
				log.Error(err, "failed to patch descheduling status")
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
			old := descheduling.DeepCopy()
			meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.DeschedulingConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "LiveMigrationFailed",
				Message: "live-migration failed for VM: " + vmId,
			})
			patch := client.MergeFrom(old)
			if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
				log.Error(err, "failed to patch descheduling status")
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

	old := descheduling.DeepCopy()
	meta.RemoveStatusCondition(&descheduling.Status.Conditions, v1alpha1.DeschedulingConditionInProgress)
	meta.SetStatusCondition(&descheduling.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.DeschedulingConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "DeschedulingSucceeded",
		Message: "descheduling succeeded",
	})
	descheduling.Status.NewHost = server.ComputeHost
	descheduling.Status.NewHostType = v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName
	patch := client.MergeFrom(old)
	if err := e.Status().Patch(ctx, descheduling, patch); err != nil {
		log.Error(err, "failed to patch descheduling status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (s *Executor) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	return multicluster.BuildController(mcl, mgr).
		Named("cortex-descheduler").
		For(
			&v1alpha1.Descheduling{},
			// Only schedule machines that have the custom scheduler set.
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				deschedulings := obj.(*v1alpha1.Descheduling)
				// We only care about deschedulings that are not completed yet.
				if meta.IsStatusConditionTrue(deschedulings.Status.Conditions, v1alpha1.DeschedulingConditionInProgress) {
					return false
				}
				// We don't care about deschedulings that failed.
				if meta.IsStatusConditionFalse(deschedulings.Status.Conditions, v1alpha1.DeschedulingConditionReady) {
					return false
				}
				return true
			})),
		).
		Complete(s)
}
