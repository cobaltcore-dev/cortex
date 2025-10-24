// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/scheduling/api/delegation/nova"
	"github.com/sapcc/go-bits/jobloop"
)

// ReservationReconciler reconciles a Reservation object
type ReservationReconciler struct {
	// Client to fetch hypervisors.
	HypervisorClient
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf Config
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ReservationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// Fetch the reservation object.
	var res v1alpha1.Reservation
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		// Can happen when the resource was just deleted.
		return ctrl.Result{}, err
	}
	// If the reservation is already active, skip it.
	if res.Status.Phase == v1alpha1.ReservationStatusPhaseActive {
		log.Info("reservation is already active, skipping", "reservation", req.Name)
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	// Currently we can only reconcile cortex-nova reservations.
	if res.Spec.Scheduler.CortexNova == nil {
		log.Info("reservation is not a cortex-nova reservation, skipping", "reservation", req.Name)
		res.Status.Error = "reservation is not a cortex-nova reservation"
		res.Status.Phase = v1alpha1.ReservationStatusPhaseFailed
		if err := r.Client.Status().Update(ctx, &res); err != nil {
			log.Error(err, "failed to update reservation status")
			return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
		}
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	schedulerSpec := res.Spec.Scheduler.CortexNova
	hvType, ok := schedulerSpec.FlavorExtraSpecs["capabilities:hypervisor_type"]
	if !ok || !slices.Contains(r.Conf.Hypervisors, hvType) {
		log.Info("hypervisor type is not supported", "reservation", req.Name)
		res.Status.Error = fmt.Sprintf("hypervisor type is not supported: %s", hvType)
		res.Status.Phase = v1alpha1.ReservationStatusPhaseFailed
		if err := r.Client.Status().Update(ctx, &res); err != nil {
			log.Error(err, "failed to update reservation status")
			return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
		}
		return ctrl.Result{}, nil // No need to requeue, the reservation is now failed.
	}

	// Convert resource.Quantity to integers for the API
	var memoryMB uint64
	if memory, ok := res.Spec.Requests["memory"]; ok {
		memoryValue := memory.ScaledValue(resource.Mega)
		if memoryValue < 0 {
			return ctrl.Result{}, fmt.Errorf("invalid memory value: %d", memoryValue)
		}
		memoryMB = uint64(memoryValue)
	}

	var cpu uint64
	if cpuQuantity, ok := res.Spec.Requests["cpu"]; ok {
		cpuValue := cpuQuantity.ScaledValue(resource.Milli)
		if cpuValue < 0 {
			return ctrl.Result{}, fmt.Errorf("invalid cpu value: %d", cpuValue)
		}
		cpu = uint64(cpuValue)
	}

	// Get all supported hosts and assign zero-weights to them.
	hypervisors, err := r.HypervisorClient.ListHypervisors(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list hypervisors: %w", err)
	}
	var eligibleHosts []schedulerdelegationapi.ExternalSchedulerHost
	for _, hv := range hypervisors {
		if hv.Type != hvType {
			continue
		}
		eligibleHosts = append(eligibleHosts, schedulerdelegationapi.ExternalSchedulerHost{
			ComputeHost:        hv.Service.Host,
			HypervisorHostname: hv.Hostname,
		})
	}
	if len(eligibleHosts) == 0 {
		log.Info("no eligible hosts found for reservation", "reservation", req.Name, "hypervisor_type", hvType)
		return ctrl.Result{}, errors.New("no eligible hosts found for reservation")
	}
	weights := make(map[string]float64, len(eligibleHosts))
	for _, host := range eligibleHosts {
		weights[host.ComputeHost] = 0.0
	}

	// Call the external scheduler delegation API to get a host for the reservation.
	externalSchedulerRequest := schedulerdelegationapi.ExternalSchedulerRequest{
		// Pipeline with all filters enabled + preselects all hosts.
		Pipeline: "all-filters-enabled",
		Hosts:    eligibleHosts,
		Weights:  weights,
		Spec: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaSpec]{
			Data: schedulerdelegationapi.NovaSpec{
				InstanceUUID: res.Name,
				NumInstances: 1, // One for each reservation.
				ProjectID:    schedulerSpec.ProjectID,
				Flavor: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaFlavor]{
					Data: schedulerdelegationapi.NovaFlavor{
						Name:       schedulerSpec.FlavorName,
						ExtraSpecs: schedulerSpec.FlavorExtraSpecs,
						MemoryMB:   memoryMB,
						VCPUs:      cpu,
						// Disk is currently not considered.
					},
				},
			},
		},
	}
	httpClient := http.Client{}
	url := r.Conf.Endpoints.NovaExternalScheduler
	reqBody, err := json.Marshal(externalSchedulerRequest)
	if err != nil {
		log.Error(err, "failed to marshal external scheduler request")
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	response, err := httpClient.Post(url, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		log.Error(err, "failed to send external scheduler request")
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	defer response.Body.Close()
	var externalSchedulerResponse schedulerdelegationapi.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		log.Error(err, "failed to decode external scheduler response")
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	if len(externalSchedulerResponse.Hosts) == 0 {
		log.Info("no hosts found for reservation", "reservation", req.Name)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, errors.New("no hosts found for reservation")
	}
	// Update the reservation with the found host (idx 0)
	host := externalSchedulerResponse.Hosts[0]
	log.Info("found host for reservation", "reservation", req.Name, "host", host)
	res.Status.Phase = v1alpha1.ReservationStatusPhaseActive
	res.Status.Host = host
	res.Status.Error = "" // Clear any previous error.
	if err := r.Status().Update(ctx, &res); err != nil {
		log.Error(err, "failed to update reservation status")
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	return ctrl.Result{}, nil // No need to requeue, the reservation is now active.
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReservationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Reservation{}).
		Named("reservation").
		WithOptions(controller.Options{
			// We want to process reservations one at a time to avoid overbooking.
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
