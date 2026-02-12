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
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	corev1 "k8s.io/api/core/v1"
)

// Endpoints for the reservations operator.
type EndpointsConfig struct {
	// The nova external scheduler endpoint.
	NovaExternalScheduler string `json:"novaExternalScheduler"`
}

type Config struct {
	// The endpoint where to find the nova external scheduler endpoint.
	Endpoints EndpointsConfig `json:"endpoints"`

	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`

	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
}

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
	// If the reservation is already active (Ready=True), skip it.
	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ReservationConditionReady) {
		log.Info("reservation is already active, skipping", "reservation", req.Name)
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	// Sync Spec values to Status.Observed* fields
	// This ensures the observed state reflects the desired state from Spec
	needsStatusUpdate := false
	if res.Spec.TargetHost != "" && res.Status.ObservedHost != res.Spec.TargetHost {
		res.Status.ObservedHost = res.Spec.TargetHost
		needsStatusUpdate = true
	}
	if needsStatusUpdate {
		old := res.DeepCopy()
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			log.Error(err, "failed to sync spec to status")
			return ctrl.Result{}, err
		}
		log.Info("synced spec to status", "reservation", req.Name, "host", res.Status.ObservedHost)
	}

	// Currently we can only reconcile nova CommittedResourceReservations (those with ResourceName set).
	resourceName := ""
	if res.Spec.CommittedResourceReservation != nil {
		resourceName = res.Spec.CommittedResourceReservation.ResourceName
	}
	if resourceName == "" {
		log.Info("reservation has no resource name, skipping", "reservation", req.Name)
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingResourceName",
			Message: "reservation has no resource name",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			log.Error(err, "failed to patch reservation status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	// Convert resource.Quantity to integers for the API
	var memoryMB uint64
	if memory, ok := res.Spec.Resources["memory"]; ok {
		memoryValue := memory.ScaledValue(resource.Mega)
		if memoryValue < 0 {
			return ctrl.Result{}, fmt.Errorf("invalid memory value: %d", memoryValue)
		}
		memoryMB = uint64(memoryValue)
	}

	var cpu uint64
	if cpuQuantity, ok := res.Spec.Resources["cpu"]; ok {
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

	// Get project ID from CommittedResourceReservation spec if available.
	projectID := ""
	if res.Spec.CommittedResourceReservation != nil {
		projectID = res.Spec.CommittedResourceReservation.ProjectID
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
				ProjectID:    projectID,
				Flavor: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaFlavor]{
					Data: schedulerdelegationapi.NovaFlavor{
						Name:     resourceName,
						MemoryMB: memoryMB,
						VCPUs:    cpu,
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
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "NoHostsFound",
			Message: "no hosts found for reservation",
		})
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
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ReservationConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "ReservationActive",
		Message: "reservation is successfully scheduled",
	})
	res.Status.ObservedHost = host
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
		if err := r.Init(ctx, mgr.GetClient(), r.Conf); err != nil {
			return err
		}
		return nil
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
