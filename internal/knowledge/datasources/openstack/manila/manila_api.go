// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"log/slog"
	"net/url"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/openstack"
	"github.com/prometheus/client_golang/prometheus"
)

type ManilaAPI interface {
	// Init the manila API.
	Init(ctx context.Context) error
	// Get all manila storage pools.
	GetAllStoragePools(ctx context.Context) ([]StoragePool, error)
}

// API for OpenStack Manila.
type manilaAPI struct {
	// Monitor to track the api.
	mon datasources.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Manila configuration.
	conf v1alpha1.ManilaDatasource
	// Authenticated OpenStack service client to fetch the data.
	client *openstack.OpenstackClient
}

// Create a new OpenStack Manila api.
func NewManilaAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.ManilaDatasource) ManilaAPI {
	return &manilaAPI{mon: mon, keystoneAPI: k, conf: conf}
}

// Init the manila API.
func (api *manilaAPI) Init(ctx context.Context) error {
	client, err := openstack.ManilaClient(ctx, api.keystoneAPI)
	if err != nil {
		return err
	}
	api.client = client
	return nil
}

// Get all Manila storage pools.
func (api *manilaAPI) GetAllStoragePools(ctx context.Context) ([]StoragePool, error) {
	label := StoragePool{}.TableName()
	slog.Info("fetching manila data", "label", label)

	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var pools []StoragePool
	err := api.client.List(ctx, "shares/detail", url.Values{"all_tenants": []string{"true"}}, "pools", &pools)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(pools))
	return pools, nil
}
