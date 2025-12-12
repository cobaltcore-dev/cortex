// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/openstack"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete all decisions for nova servers that have been deleted.
func Cleanup(ctx context.Context, client client.Client, conf conf.Config) error {
	var authenticatedHTTP = http.DefaultClient
	if conf.SSOSecretRef != nil {
		var err error
		authenticatedHTTP, err = sso.Connector{Client: client}.
			FromSecretRef(ctx, *conf.SSOSecretRef)
		if err != nil {
			return err
		}
	}
	authenticatedKeystone, err := keystone.
		Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, conf.KeystoneSecretRef)
	if err != nil {
		return err
	}

	novaClient, err := openstack.NovaClient(ctx, authenticatedKeystone)
	if err != nil {
		return err
	}
	var servers []struct {
		ID string `json:"id"`
	}
	query := url.Values{
		"all_tenants": []string{"true"},
	}
	if err := novaClient.List(ctx, "servers/detail", query, "servers", &servers); err != nil {
		return err
	}

	slog.Info("found servers", "count", len(servers))
	serversByID := make(map[string]struct{})
	for _, server := range servers {
		serversByID[server.ID] = struct{}{}
	}

	// List all reservations.
	reservationList := &v1alpha1.ReservationList{}
	if err := client.List(ctx, reservationList); err != nil {
		return err
	}
	reservationsByName := make(map[string]v1alpha1.Reservation)
	for _, reservation := range reservationList.Items {
		reservationsByName[reservation.Name] = reservation
	}

	// List all decisions and check if the server still exists.
	decisionList := &v1alpha1.DecisionList{}
	if err := client.List(ctx, decisionList); err != nil {
		return err
	}
	for _, decision := range decisionList.Items {
		// Skip non-nova decisions.
		if decision.Spec.Operator != conf.Operator {
			continue
		}
		if decision.Spec.Type != v1alpha1.DecisionTypeNovaServer {
			continue
		}
		// Skip decisions that are linked to existing reservations.
		if _, ok := reservationsByName[decision.Spec.ResourceID]; ok {
			continue
		}
		// Skip decisions for which the server still exists.
		if _, ok := serversByID[decision.Spec.ResourceID]; ok {
			continue
		}
		// Delete the decision since the server no longer exists.
		slog.Info("deleting decision for deleted server", "decision", decision.Name, "serverID", decision.Spec.ResourceID)
		if err := client.Delete(ctx, &decision); err != nil {
			return err
		}
	}
	return nil
}
