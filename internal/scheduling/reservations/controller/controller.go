// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
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
	Conf conf.Config
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
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "UnsupportedScheduler",
			Message: "reservation is not a cortex-nova reservation",
		})
		res.Status.Phase = v1alpha1.ReservationStatusPhaseFailed
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			log.Error(err, "failed to patch reservation status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // Don't need to requeue.
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

	// Get all hosts and assign zero-weights to them.
	hypervisors, err := r.ListHypervisors(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list hypervisors: %w", err)
	}
	var eligibleHosts []schedulerdelegationapi.ExternalSchedulerHost
	for _, hv := range hypervisors {
		eligibleHosts = append(eligibleHosts, schedulerdelegationapi.ExternalSchedulerHost{
			ComputeHost:        hv.Service.Host,
			HypervisorHostname: hv.Hostname,
		})
	}
	if len(eligibleHosts) == 0 {
		log.Info("no eligible hosts found for reservation", "reservation", req.Name)
		return ctrl.Result{}, errors.New("no eligible hosts found for reservation")
	}
	weights := make(map[string]float64, len(eligibleHosts))
	for _, host := range eligibleHosts {
		weights[host.ComputeHost] = 0.0
	}

	// Call the external scheduler delegation API to get a host for the reservation.
	externalSchedulerRequest := schedulerdelegationapi.ExternalSchedulerRequest{
		Reservation: true,
		Hosts:       eligibleHosts,
		Weights:     weights,
		Spec: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaSpec]{
			Data: schedulerdelegationapi.NovaSpec{
				InstanceUUID: res.Name,
				NumInstances: 1, // One for each reservation.
				ProjectID:    res.Spec.Scheduler.CortexNova.ProjectID,
				Flavor: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaFlavor]{
					Data: schedulerdelegationapi.NovaFlavor{
						Name:       res.Spec.Scheduler.CortexNova.FlavorName,
						ExtraSpecs: res.Spec.Scheduler.CortexNova.FlavorExtraSpecs,
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
		return ctrl.Result{}, err
	}
	response, err := httpClient.Post(url, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		log.Error(err, "failed to send external scheduler request")
		return ctrl.Result{}, err
	}
	defer response.Body.Close()
	var externalSchedulerResponse schedulerdelegationapi.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		log.Error(err, "failed to decode external scheduler response")
		return ctrl.Result{}, err
	}
	if len(externalSchedulerResponse.Hosts) == 0 {
		log.Info("no hosts found for reservation", "reservation", req.Name)
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionError,
			Status:  metav1.ConditionTrue,
			Reason:  "NoHostsFound",
			Message: "no hosts found for reservation",
		})
		res.Status.Phase = v1alpha1.ReservationStatusPhaseFailed
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			log.Error(err, "failed to patch reservation status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // No need to requeue, we didn't find a host.
	}
	// Update the reservation with the found host (idx 0)
	host := externalSchedulerResponse.Hosts[0]
	log.Info("found host for reservation", "reservation", req.Name, "host", host)
	old := res.DeepCopy()
	meta.RemoveStatusCondition(&res.Status.Conditions, v1alpha1.ReservationConditionError)
	res.Status.Phase = v1alpha1.ReservationStatusPhaseActive
	res.Status.Host = host
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, &res, patch); err != nil {
		log.Error(err, "failed to patch reservation status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil // No need to requeue, the reservation is now active.
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReservationReconciler) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return r.Init(ctx, mgr.GetClient(), r.Conf)
	})); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		For(&v1alpha1.Reservation{}).
		Named("reservation").
		WithOptions(controller.Options{
			// We want to process reservations one at a time to avoid overbooking.
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
