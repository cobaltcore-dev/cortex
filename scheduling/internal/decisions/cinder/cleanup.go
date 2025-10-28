// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete all decisions for cinder volumes that have been deleted.
func cleanupCinder(ctx context.Context, client client.Client, conf conf.Config) error {
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

	cinderURL, err := pc.EndpointLocator(gophercloud.EndpointOpts{
		Type:         "volumev3",
		Availability: gophercloud.Availability(keystoneConf.Availability),
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
	if len(volumes) == 0 {
		return errors.New("no volumes found")
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

func CleanupCinderDecisionsRegularly(ctx context.Context, client client.Client, conf conf.Config) {
	for {
		if err := cleanupCinder(ctx, client, conf); err != nil {
			slog.Error("failed to cleanup cinder decisions", "error", err)
		}
		// Wait for 1 hour before the next cleanup.
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Hour):
		}
	}
}
