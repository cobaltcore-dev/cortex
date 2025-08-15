// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	reservationv1 "github.com/cobaltcore-dev/cortex/internal/reservations/api/v1"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

type Operator struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// OpenStack api.
	OSClient OpenStackClient
}

func (o *Operator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	slog.Info("reconciling reservation", "reservation", req.Name)
	// Fetch the reservation object.
	var res reservationv1.Reservation
	if err := o.Client.Get(ctx, req.NamespacedName, &res); err != nil {
		if k8serrors.IsNotFound(err) {
			slog.Info("reservation not found, skipping", "reservation", req.Name)
			return ctrl.Result{}, nil
		}
		slog.Error("failed to get reservation", "error", err)
		return ctrl.Result{}, err
	}
	// Currently only compute instances are supported.
	if !strings.HasPrefix(res.Spec.Commitment.ResourceName, "instances_") {
		return ctrl.Result{}, nil
	}
	if res.Spec.Commitment.ServiceType != "compute" {
		return ctrl.Result{}, nil
	}
	// Currently only single-instance commitments are supported.
	if res.Spec.Commitment.Amount != 1 {
		return ctrl.Result{}, nil
	}
	// TODO: This should also return the number of placeable flavors.
	flavorName := strings.TrimPrefix(res.Spec.Commitment.ResourceName, "instances_")
	externalSchedulerRequest := api.ExternalSchedulerRequest{
		Sandboxed:         true,
		PreselectAllHosts: true,
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{Name: flavorName},
				},
				NumInstances: 1,
				ProjectID:    res.Spec.Commitment.ProjectID,
			},
		},
	}
	httpClient := http.Client{}
	url := "http://cortex-nova-scheduler:8080/scheduler/nova/external"
	reqBody, err := json.Marshal(externalSchedulerRequest)
	if err != nil {
		slog.Error("failed to marshal external scheduler request", "error", err)
		return ctrl.Result{}, err
	}
	response, err := httpClient.Post(url, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		slog.Error("failed to send external scheduler request", "error", err)
		return ctrl.Result{}, err
	}
	defer response.Body.Close()
	var externalSchedulerResponse api.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		slog.Error("failed to decode external scheduler response", "error", err)
		return ctrl.Result{}, err
	}
	if len(externalSchedulerResponse.Hosts) == 0 {
		slog.Info("no hosts found for reservation", "reservation", req.Name)
		return ctrl.Result{}, nil
	}
	// Update the reservation with the found host (idx 0)
	host := externalSchedulerResponse.Hosts[0]
	slog.Info("found host for reservation", "reservation", req.Name, "host", host)
	res.Status.Reserved = true
	res.Status.Host = reservationv1.Host{
		Kind: "compute",
		Name: host,
	}
	if err := o.Client.Status().Update(ctx, &res); err != nil {
		slog.Error("failed to update reservation status", "error", err)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (o *Operator) FetchAndCreateReservations(ctx context.Context) error {
	// Fetch all commitments from OpenStack.
	commitments, err := o.OSClient.GetAllCommitments(ctx)
	if err != nil {
		return err
	}

	// Create or update reservations based on the commitments.
	for _, commitment := range commitments {
		reservation := &reservationv1.Reservation{
			ObjectMeta: ctrl.ObjectMeta{
				Name:      commitment.UUID,
				Namespace: "default",
			},
			Spec: reservationv1.ReservationSpec{
				Commitment: commitment,
			},
		}
		// Check if the reservation already exists.
		nn := types.NamespacedName{Name: reservation.Name, Namespace: reservation.Namespace}
		var existing reservationv1.Reservation
		if err := o.Client.Get(ctx, nn, &existing); err != nil {
			if !k8serrors.IsNotFound(err) {
				slog.Error("failed to get reservation", "error", err, "name", nn.Name)
				return err
			}
			// Not found -> create
			if err := o.Client.Create(ctx, reservation); err != nil {
				if k8serrors.IsAlreadyExists(err) {
					slog.Info("reservation already exists", "name", reservation.Name)
				} else {
					slog.Error("failed to create reservation", "error", err)
					return err
				}
			} else {
				slog.Info("created reservation", "name", reservation.Name)
			}
		} else {
			slog.Info("reservation already exists", "name", existing.Name)
		}
	}
	return nil
}

func Run(ctx context.Context, conf conf.KeystoneConfig) {
	// Set up the kubernetes operator.
	schemeBuilder := &scheme.Builder{GroupVersion: schema.GroupVersion{
		Group:   "cortex.sap",
		Version: "v1",
	}}
	schemeBuilder.Register(&reservationv1.Reservation{}, &reservationv1.ReservationList{})
	slog.Info("Registering scheme for reservation CRD")
	scheme, err := schemeBuilder.Build()
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
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics:       metricsserver.Options{BindAddress: ":8081"},
		Scheme:        scheme,
		WebhookServer: webhookServer,
	})
	if err != nil {
		panic("failed to create controller manager: " + err.Error())
	}
	slog.Info("Created controller manager")

	osClient := NewOpenStackClient(conf)
	osClient.Init(ctx)
	operator := Operator{
		Client:   mgr.GetClient(),
		Scheme:   scheme,
		OSClient: osClient,
	}

	// Bind the reconciliation loop.
	err = ctrl.NewControllerManagedBy(mgr).
		For(&reservationv1.Reservation{}).
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

	go func() {
		for {
			if err := operator.FetchAndCreateReservations(ctx); err != nil {
				slog.Error("failed to fetch and create reservations", "error", err)
			}
			time.Sleep(1 * time.Minute)
		}
	}()
}
