// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/schedulerstats"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type CinderAPI interface {
	// Int the cinder API.
	Init(ctx context.Context) error
	// Get all cinder storage pools.
	GetAllStoragePools(ctx context.Context) ([]StoragePool, error)
}

type cinderAPI struct {
	// Monitor to track the api.
	mon datasources.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Cinder configuration.
	conf v1alpha1.CinderDatasource
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

func NewCinderAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.CinderDatasource) CinderAPI {
	return &cinderAPI{
		mon:         mon,
		keystoneAPI: k,
		conf:        conf,
	}
}

func (api *cinderAPI) Init(ctx context.Context) error {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		return err
	}

	// Automatically fetch the cinder endpoint from the keystone service catalog
	provider := api.keystoneAPI.Client()
	serviceType := "volumev3"
	sameAsKeystone := api.keystoneAPI.Availability()
	url, err := api.keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return err
	}
	slog.Info("using cinder endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		Microversion:   "3.70",
	}
	return nil
}

func (api *cinderAPI) GetAllStoragePools(ctx context.Context) ([]StoragePool, error) {
	label := StoragePool{}.TableName()
	slog.Info("fetching cinder data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.RequestTimer != nil {
			hist := api.mon.RequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return schedulerstats.List(api.sc, schedulerstats.ListOpts{Detail: true}).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Pools []StoragePool `json:"pools"`
	}{}
	if err := pages.(schedulerstats.StoragePoolPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Pools))
	return data.Pools, nil
}
