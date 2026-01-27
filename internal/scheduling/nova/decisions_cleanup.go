// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete all decisions for nova servers that have been deleted.
func DecisionsCleanup(ctx context.Context, client client.Client, conf conf.Config) error {
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
	pc := authenticatedKeystone.Client()
	novaURL, err := pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "compute",
		Availability: gophercloud.Availability(authenticatedKeystone.Availability()),
	})
	if err != nil {
		return err
	}
	novaSC := &gophercloud.ServiceClient{
		ProviderClient: pc,
		Endpoint:       novaURL,
		// Since 2.53, the hypervisor id and service id is a UUID.
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: "2.61",
	}

	initialURL := novaSC.Endpoint + "servers/detail?all_tenants=true"
	var nextURL = &initialURL
	var servers []struct {
		ID string `json:"id"`
	}

	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", novaSC.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", novaSC.Microversion)
		resp, err := novaSC.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Servers []struct {
				ID string `json:"id"`
			} `json:"servers"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"servers_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return err
		}
		servers = append(servers, list.Servers...)
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
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
		if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
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
