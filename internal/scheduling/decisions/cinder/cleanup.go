// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

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

	cinderClient, err := openstack.CinderClient(ctx, authenticatedKeystone)
	if err != nil {
		return err
	}
	var volumes []struct {
		ID string `json:"id"`
	}
	query := url.Values{
		"all_tenants": []string{"true"},
	}
	if err := cinderClient.List(ctx, "volumes/detail", query, "volumes", &volumes); err != nil {
		return err
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
