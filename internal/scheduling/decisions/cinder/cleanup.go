// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

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

// Delete all decisions for cinder volumes that have been deleted.
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
	authenticatedKeystone, err := keystone.Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, conf.KeystoneSecretRef)
	if err != nil {
		return err
	}
	pc := authenticatedKeystone.Client()
	cinderURL, err := pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "volumev3",
		Availability: gophercloud.Availability(authenticatedKeystone.Availability()),
	})
	if err != nil {
		return err
	}
	cinderSC := &gophercloud.ServiceClient{
		ProviderClient: pc,
		Endpoint:       cinderURL,
		Microversion:   "3.70",
	}

	initialURL := cinderSC.Endpoint + "volumes/detail?all_tenants=true"
	var nextURL = &initialURL
	var volumes []struct {
		ID string `json:"id"`
	}

	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", cinderSC.Token())
		req.Header.Set("OpenStack-API-Version", "volume "+cinderSC.Microversion)
		resp, err := cinderSC.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Volumes []struct {
				ID string `json:"id"`
			} `json:"volumes"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"volumes_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return err
		}
		volumes = append(volumes, list.Volumes...)
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}

	slog.Info("found volumes", "count", len(volumes))
	volumesByID := make(map[string]struct{})
	for _, volume := range volumes {
		volumesByID[volume.ID] = struct{}{}
	}

	// List all decisions and delete those whose volume no longer exists.
	decisionList := &v1alpha1.DecisionList{}
	if err := client.List(ctx, decisionList); err != nil {
		return err
	}
	for _, decision := range decisionList.Items {
		// Skip non-cinder decisions.
		if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainCinder {
			continue
		}
		// Skip decisions for which the volume still exists.
		if _, ok := volumesByID[decision.Spec.ResourceID]; ok {
			continue
		}
		// Delete the decision since the volume no longer exists.
		slog.Info("deleting decision for deleted volume", "decision", decision.Name, "volumeID", decision.Spec.ResourceID)
		if err := client.Delete(ctx, &decision); err != nil {
			return err
		}
	}
	return nil
}
