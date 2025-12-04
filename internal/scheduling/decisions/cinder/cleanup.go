// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
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
		Type:           "volumev3",
		Microversion:   "3.70",
	}

	slo := volumes.ListOpts{AllTenants: true}
	pages, err := volumes.List(cinderSC, slo).AllPages(ctx)
	if err != nil {
		return err
	}
	dataVolumes := &struct {
		Volumes []struct {
			ID string `json:"id"`
		} `json:"volumes"`
	}{}
	if err := pages.(volumes.VolumePage).ExtractInto(dataVolumes); err != nil {
		return err
	}
	volumes := dataVolumes.Volumes
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
		if decision.Spec.Operator != conf.Operator {
			continue
		}
		if decision.Spec.Type != v1alpha1.DecisionTypeCinderVolume {
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
