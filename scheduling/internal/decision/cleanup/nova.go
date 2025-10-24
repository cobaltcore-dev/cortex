// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/knowledge/api/datasources/openstack/nova"
	reservationsv1alpha1 "github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete all decisions for nova servers that have been deleted.
func cleanup(ctx context.Context, client client.Client, conf conf.Config) error {
	keystoneConf := conf.KeystoneConfig
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: keystoneConf.URL,
		Username:         keystoneConf.OSUsername,
		DomainName:       keystoneConf.OSUserDomainName,
		Password:         keystoneConf.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: keystoneConf.OSProjectName,
			DomainName:  keystoneConf.OSProjectDomainName,
		},
	}
	pc, err := openstack.NewClient(authOptions.IdentityEndpoint)
	if err != nil {
		return err
	}
	err = openstack.Authenticate(ctx, pc, authOptions)
	if err != nil {
		return err
	}

	novaURL, err := pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: gophercloud.Availability(keystoneConf.Availability),
	})
	if err != nil {
		return err
	}
	novaSC := &gophercloud.ServiceClient{
		ProviderClient: pc,
		Endpoint:       novaURL,
		Type:           "compute",
		// Since 2.53, the hypervisor id and service id is a UUID.
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: "2.61",
	}

	slo := servers.ListOpts{AllTenants: true}
	pages, err := servers.List(novaSC, slo).AllPages(ctx)
	if err != nil {
		return err
	}
	dataServers := &struct {
		Servers []nova.Server `json:"servers"`
	}{}
	if err := pages.(servers.ServerPage).ExtractInto(dataServers); err != nil {
		return err
	}
	servers := dataServers.Servers
	if len(servers) == 0 {
		panic("no servers found")
	}
	slog.Info("found servers", "count", len(servers))
	serversByID := make(map[string]nova.Server)
	for _, server := range servers {
		serversByID[server.ID] = server
	}

	// List all reservations.
	reservationList := &reservationsv1alpha1.ReservationList{}
	if err := client.List(ctx, reservationList); err != nil {
		return err
	}
	reservationsByName := make(map[string]reservationsv1alpha1.Reservation)
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

func CleanupNovaDecisionsRegularly(ctx context.Context, client client.Client, conf conf.Config) {
	for {
		if err := cleanup(ctx, client, conf); err != nil {
			slog.Error("failed to cleanup nova decisions", "error", err)
		}
		// Wait for 1 hour before the next cleanup.
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Hour):
		}
	}
}
