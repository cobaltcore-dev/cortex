/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cobaltcore-dev/cortex/api/scheduler/external/nova"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	reservationsv1alpha1 "github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/sapcc/go-bits/jobloop"
)

// ComputeReservationReconciler reconciles a ComputeReservation object
type ComputeReservationReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf Config
}

// +kubebuilder:rbac:groups=reservations.cortex,resources=computereservations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=reservations.cortex,resources=computereservations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=reservations.cortex,resources=computereservations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComputeReservationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)
	// Fetch the reservation object.
	var res v1alpha1.ComputeReservation
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	// If the reservation is already active, skip it.
	if res.Status.Phase == v1alpha1.ComputeReservationStatusPhaseActive {
		slog.Info("reservation is already active, skipping", "reservation", req.Name)
		return ctrl.Result{}, nil // Don't need to requeue.
	}
	switch res.Spec.Kind {
	case v1alpha1.ComputeReservationSpecKindInstance:
		return r.reconcileInstanceReservation(ctx, req, res)
	default:
		slog.Info("reservation kind is not supported, skipping", "reservation", req.Name, "kind", res.Spec.Kind)
		return ctrl.Result{}, nil // Don't need to requeue.
	}
}

// Reconcile an instance reservation.
func (r *ComputeReservationReconciler) reconcileInstanceReservation(
	ctx context.Context,
	req ctrl.Request,
	res v1alpha1.ComputeReservation,
) (ctrl.Result, error) {
	spec := res.Spec.Instance
	hvType, ok := spec.ExtraSpecs["capabilities:hypervisor_type"]
	if !ok || !slices.Contains(r.Conf.Hypervisors, hvType) {
		slog.Info("hypervisor type is not supported", "reservation", req.Name, "type", hvType)
		if hvType == "" {
			res.Status.Error = "hypervisor type is not specified"
		} else {
			res.Status.Error = fmt.Sprintf("hypervisor type '%s' is not supported", hvType)
		}
		res.Status.Phase = v1alpha1.ComputeReservationStatusPhaseFailed
		if err := r.Client.Status().Update(ctx, &res); err != nil {
			slog.Error("failed to update reservation status", "error", err)
			return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
		}
		return ctrl.Result{}, errors.New("hypervisor type is not supported")
	}

	// Convert resource.Quantity to integers for the API
	memoryValue := spec.Memory.ScaledValue(resource.Mega)
	if memoryValue < 0 {
		return ctrl.Result{}, fmt.Errorf("invalid memory value: %d", memoryValue)
	}
	memoryMB := uint64(memoryValue)

	vCPUsValue := spec.VCPUs.Value()
	if vCPUsValue < 0 {
		return ctrl.Result{}, fmt.Errorf("invalid vCPUs value: %d", vCPUsValue)
	}
	vCPUs := uint64(vCPUsValue)

	diskValue := spec.Disk.ScaledValue(resource.Giga)
	if diskValue < 0 {
		return ctrl.Result{}, fmt.Errorf("invalid disk value: %d", diskValue)
	}
	diskGB := uint64(diskValue)

	externalSchedulerRequest := nova.ExternalSchedulerRequest{
		Sandboxed:         true,
		PreselectAllHosts: true,
		Spec: nova.NovaObject[nova.NovaSpec]{
			Data: nova.NovaSpec{
				NumInstances: 1, // One for each reservation.
				ProjectID:    res.Spec.ProjectID,
				Flavor: nova.NovaObject[nova.NovaFlavor]{
					Data: nova.NovaFlavor{
						Name:       spec.Flavor,
						ExtraSpecs: spec.ExtraSpecs,
						MemoryMB:   memoryMB,
						VCPUs:      vCPUs,
						RootGB:     diskGB,
					},
				},
			},
		},
	}
	httpClient := http.Client{}
	url := r.Conf.Endpoints.NovaExternalScheduler
	reqBody, err := json.Marshal(externalSchedulerRequest)
	if err != nil {
		slog.Error("failed to marshal external scheduler request", "error", err)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	response, err := httpClient.Post(url, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		slog.Error("failed to send external scheduler request", "error", err)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	defer response.Body.Close()
	var externalSchedulerResponse nova.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		slog.Error("failed to decode external scheduler response", "error", err)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	if len(externalSchedulerResponse.Hosts) == 0 {
		slog.Info("no hosts found for reservation", "reservation", req.Name)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, errors.New("no hosts found for reservation")
	}
	// Update the reservation with the found host (idx 0)
	host := externalSchedulerResponse.Hosts[0]
	slog.Info("found host for reservation", "reservation", req.Name, "host", host)
	res.Status.Phase = v1alpha1.ComputeReservationStatusPhaseActive
	res.Status.Host = host
	res.Status.Error = "" // Clear any previous error.
	if err := r.Status().Update(ctx, &res); err != nil {
		slog.Error("failed to update reservation status", "error", err)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	return ctrl.Result{}, nil // No need to requeue, the reservation is now active.
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComputeReservationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&reservationsv1alpha1.ComputeReservation{}).
		Named("computereservation").
		Complete(r)
}
