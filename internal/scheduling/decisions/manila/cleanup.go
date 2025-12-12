// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"errors"
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

// Delete all decisions for manila shares that have been deleted.
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

	manilaClient, err := openstack.ManilaClient(ctx, authenticatedKeystone)
	if err != nil {
		return err
	}

	var shares []struct {
		ID string `json:"id"`
	}
	query := url.Values{
		"all_tenants": []string{"true"},
	}
	if err := manilaClient.List(ctx, "shares/detail", query, "shares", &shares); err != nil {
		return err
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
