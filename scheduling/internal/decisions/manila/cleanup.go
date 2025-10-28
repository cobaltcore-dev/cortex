// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/sharedfilesystems/v2/shares"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete all decisions for manila shares that have been deleted.
func cleanupManila(ctx context.Context, client client.Client, conf conf.Config) error {
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

	// Workaround to find the v2 service of manila.
	// See: https://github.com/gophercloud/gophercloud/issues/3347
	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	manilaSC, err := openstack.NewSharedFileSystemV2(pc, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(keystoneConf.Availability),
	})
	if err != nil {
		return err
	}
	manilaSC.Microversion = "2.65"

	slo := shares.ListOpts{AllTenants: true}
	pages, err := shares.ListDetail(manilaSC, slo).AllPages(ctx)
	if err != nil {
		return err
	}
	dataShares := &struct {
		Shares []struct {
			ID string `json:"id"`
		} `json:"shares"`
	}{}
	if err := pages.(shares.SharePage).ExtractInto(dataShares); err != nil {
		return err
	}
	shares := dataShares.Shares
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
		if decision.Spec.Operator != conf.Operator {
			continue
		}
		if decision.Spec.Type != v1alpha1.DecisionTypeManilaShare {
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

func CleanupManilaDecisionsRegularly(ctx context.Context, client client.Client, conf conf.Config) {
	for {
		if err := cleanupManila(ctx, client, conf); err != nil {
			slog.Error("failed to cleanup manila decisions", "error", err)
		}
		// Wait for 1 hour before the next cleanup.
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Hour):
		}
	}
}
