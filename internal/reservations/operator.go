// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	"github.com/go-logr/logr"
	"github.com/sapcc/go-bits/jobloop"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	v1alpha1 "github.com/cobaltcore-dev/cortex/internal/reservations/api/v1alpha1"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// Operator that manages the reservation resource.
type Operator struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// Client to fetch commitments.
	CommitmentsClient
	// Configuration for the operator.
	Conf conf.ReservationsConfig
}

// Reconcile the requested reservation object.
func (o *Operator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	slog.Info("reconciling reservation", "reservation", req.Name)
	// Fetch the reservation object.
	var res v1alpha1.Reservation
	if err := o.Get(ctx, req.NamespacedName, &res); err != nil {
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	// If the reservation is already active, skip it.
	if res.Status.Phase == v1alpha1.ReservationStatusPhaseActive {
		slog.Info("reservation is already active, skipping", "reservation", req.Name)
		return ctrl.Result{}, nil // Don't need to requeue.
	}
	switch res.Spec.Kind {
	case v1alpha1.ReservationSpecKindInstance:
		return o.reconcileInstanceReservation(ctx, req, res)
	default:
		slog.Info("reservation kind is not supported, skipping", "reservation", req.Name, "kind", res.Spec.Kind)
		return ctrl.Result{}, nil // Don't need to requeue.
	}
}

// Reconcile an instance reservation.
func (o *Operator) reconcileInstanceReservation(
	ctx context.Context,
	req ctrl.Request,
	res v1alpha1.Reservation,
) (ctrl.Result, error) {

	res.Status.Allocation.Kind = v1alpha1.ReservationStatusAllocationKindCompute
	spec := res.Spec.Instance
	hvType, ok := spec.ExtraSpecs["capabilities:hypervisor_type"]
	if !ok || !slices.Contains(o.Conf.Hypervisors, hvType) {
		slog.Info("hypervisor type is not supported", "reservation", req.Name, "type", hvType)
		if hvType == "" {
			res.Status.Error = "hypervisor type is not specified"
		} else {
			res.Status.Error = fmt.Sprintf("hypervisor type '%s' is not supported", hvType)
		}
		res.Status.Phase = v1alpha1.ReservationStatusPhaseFailed
		if err := o.Client.Status().Update(ctx, &res); err != nil {
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

	externalSchedulerRequest := api.ExternalSchedulerRequest{
		Sandboxed:         true,
		PreselectAllHosts: true,
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				NumInstances: 1, // One for each reservation.
				ProjectID:    res.Spec.ProjectID,
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{
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
	url := o.Conf.Endpoints.NovaExternalScheduler
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
	var externalSchedulerResponse api.ExternalSchedulerResponse
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
	res.Status.Phase = v1alpha1.ReservationStatusPhaseActive
	res.Status.Allocation.Compute = v1alpha1.ReservationStatusAllocationCompute{Host: host}
	res.Status.Error = "" // Clear any previous error.
	if err := o.Status().Update(ctx, &res); err != nil {
		slog.Error("failed to update reservation status", "error", err)
		return ctrl.Result{RequeueAfter: jobloop.DefaultJitter(time.Minute)}, err
	}
	return ctrl.Result{}, nil // No need to requeue, the reservation is now active.
}

// Fetch commitments and update/create reservations for each of them.
func (o *Operator) SyncReservations(ctx context.Context) error {
	// Commitments for a specific flavor.
	flavorCommitments, err := o.GetFlavorCommitments(ctx)
	if err != nil {
		return err
	}
	var reservations []v1alpha1.Reservation
	// Instance reservations for each commitment.
	for _, commitment := range flavorCommitments {
		// Get only the 5 first characters from the uuid. This should be safe enough.
		if len(commitment.UUID) < 5 {
			slog.Error("commitment UUID is too short", "uuid", commitment.UUID)
			continue
		}
		commitmentUUIDShort := commitment.UUID[:5]
		for n := range commitment.Amount { // N instances
			reservations = append(reservations, v1alpha1.Reservation{
				ObjectMeta: ctrl.ObjectMeta{
					Name:      fmt.Sprintf("commitment-%s-%d", commitmentUUIDShort, n),
					Namespace: o.Conf.Namespace,
				},
				Spec: v1alpha1.ReservationSpec{
					Kind:      v1alpha1.ReservationSpecKindInstance,
					ProjectID: commitment.ProjectID,
					DomainID:  commitment.DomainID,
					Instance: v1alpha1.ReservationSpecInstance{
						Flavor:     commitment.Flavor.Name,
						ExtraSpecs: commitment.Flavor.ExtraSpecs,
						Memory:     *resource.NewQuantity(int64(commitment.Flavor.RAM)*1024*1024, resource.BinarySI),
						VCPUs:      *resource.NewQuantity(int64(commitment.Flavor.VCPUs), resource.DecimalSI),
						Disk:       *resource.NewQuantity(int64(commitment.Flavor.Disk)*1024*1024*1024, resource.BinarySI),
					},
				},
			})
		}
	}
	for _, res := range reservations {
		// Check if the reservation already exists.
		nn := types.NamespacedName{Name: res.Name, Namespace: res.Namespace}
		var existing v1alpha1.Reservation
		if err := o.Get(ctx, nn, &existing); err != nil {
			if !k8serrors.IsNotFound(err) {
				slog.Error("failed to get reservation", "error", err, "name", nn.Name)
				return err
			}
			// Reservation does not exist, create it.
			if err := o.Create(ctx, &res); err != nil {
				return err
			}
			slog.Info("created reservation", "name", nn.Name)
			continue
		}
		// Reservation exists, update it.
		existing.Spec = res.Spec
		if err := o.Update(ctx, &existing); err != nil {
			slog.Error("failed to update reservation", "error", err, "name", nn.Name)
			return err
		}
		slog.Info("updated reservation", "name", nn.Name)
	}
	return nil
}

func RunOperator(ctx context.Context, conf conf.Config) {
	// Controller-runtime comes with logr instead of slog.
	// So we need to use our own sink here that wraps slog.Logger.
	ctrl.SetLogger(logr.Logger{}.WithSink(SlogLogSink{log: slog.Default()}))

	slog.Info("Registering scheme for reservation CRD")
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		panic("failed to build scheme: " + err.Error())
	}
	// If the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: []func(*tls.Config){
			func(c *tls.Config) {
				slog.Info("Setting up TLS for webhook server")
				c.NextProtos = []string{"http/1.1"}
			},
		},
	})
	monitoringConf := conf.GetMonitoringConfig()
	operatorMetricsPort := ":" + strconv.Itoa(monitoringConf.OperatorPort)
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics:       metricsserver.Options{BindAddress: operatorMetricsPort},
		Scheme:        scheme,
		WebhookServer: webhookServer,
	})
	if err != nil {
		panic("failed to create controller manager: " + err.Error())
	}
	slog.Info("created controller manager")

	keystoneConfig := conf.GetKeystoneConfig()
	commitmentsClient := NewCommitmentsClient(keystoneConfig)
	commitmentsClient.Init(ctx)
	slog.Info("initialized commitments client")

	operator := Operator{
		Client:            mgr.GetClient(),
		Scheme:            scheme,
		CommitmentsClient: commitmentsClient,
		Conf:              conf.GetReservationsConfig(),
	}
	// Bind the reconciliation loop.
	err = ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Reservation{}).
		Named("reservation").
		Complete(&operator)
	if err != nil {
		panic("failed to create controller: " + err.Error())
	}

	slog.Info("starting manager")
	go func() {
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			panic("failed to start controller manager: " + err.Error())
		}
	}()
	slog.Info("running sync loop")
	go func() {
		for {
			if err := operator.SyncReservations(ctx); err != nil {
				slog.Error("failed to sync reservations", "error", err)
			}
			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}()
}
