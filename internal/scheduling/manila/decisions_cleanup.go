// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete all decisions for manila shares that have been deleted.
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
	authenticatedKeystone, err := keystone.Connector{Client: client, HTTPClient: authenticatedHTTP}.
		FromSecretRef(ctx, conf.KeystoneSecretRef)
	if err != nil {
		return err
	}
	pc := authenticatedKeystone.Client()
	// Workaround to find the v2 service of manila.
	// See: https://github.com/gophercloud/gophercloud/issues/3347
	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	manilaSC, err := openstack.NewSharedFileSystemV2(pc, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(authenticatedKeystone.Availability()),
	})
	if err != nil {
		return err
	}
	manilaSC.Microversion = "2.65"

	initialURL := manilaSC.Endpoint + "shares/detail?all_tenants=true"
	var nextURL = &initialURL
	var shares []struct {
		ID string `json:"id"`
	}

	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("X-Auth-Token", manilaSC.Token())
		req.Header.Set("X-OpenStack-Manila-API-Version", manilaSC.Microversion)
		resp, err := manilaSC.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Shares []struct {
				ID string `json:"id"`
			} `json:"shares"`
			Links []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"shares_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return err
		}
		shares = append(shares, list.Shares...)
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}

	if len(shares) == 0 {
		return errors.New("no shares found")
	}
	slog.Info("found shares", "count", len(shares))
	sharesByID := make(map[string]struct{})
	for _, share := range shares {
		sharesByID[share.ID] = struct{}{}
	}

	// List all decisions and delete those whose share no longer exists.
	decisionList := &v1alpha1.DecisionList{}
	if err := client.List(ctx, decisionList); err != nil {
		return err
	}
	for _, decision := range decisionList.Items {
		// Skip non-manila decisions.
		if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainManila {
			continue
		}
		// Skip decisions for which the share still exists.
		if _, ok := sharesByID[decision.Spec.ResourceID]; ok {
			continue
		}
		// Delete the decision since the share no longer exists.
		slog.Info("deleting decision for deleted share", "decision", decision.Name, "shareID", decision.Spec.ResourceID)
		if err := client.Delete(ctx, &decision); err != nil {
			return err
		}
	}
	return nil
}
