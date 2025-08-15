// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"crypto/tls"
	"log/slog"
	"time"

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
