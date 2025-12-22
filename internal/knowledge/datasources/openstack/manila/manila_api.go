// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/sharedfilesystems/v2/schedulerstats"
	"github.com/gophercloud/gophercloud/v2/pagination"
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
	keystoneClient keystone.KeystoneClient
	// Manila configuration.
	conf v1alpha1.ManilaDatasource
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

// Create a new OpenStack Manila api.
func NewManilaAPI(mon datasources.Monitor, k keystone.KeystoneClient, conf v1alpha1.ManilaDatasource) ManilaAPI {
	return &manilaAPI{mon: mon, keystoneClient: k, conf: conf}
}

// Init the manila API.
func (api *manilaAPI) Init(ctx context.Context) error {
	if err := api.keystoneClient.Authenticate(ctx); err != nil {
		return err
	}
	// Automatically fetch the manila endpoint from the keystone service catalog.
	provider := api.keystoneClient.Client()
	// Workaround to find the v2 service of
	// See: https://github.com/gophercloud/gophercloud/issues/3347
	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	sameAsKeystone := api.keystoneClient.Availability()
	sc, err := openstack.NewSharedFileSystemV2(provider, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(sameAsKeystone),
	})
	if err != nil {
		return fmt.Errorf("failed to create manila service client: %w", err)
	}
	sc.Microversion = "2.65"
	api.sc = sc
	return nil
}

// Get all Manila storage pools.
func (api *manilaAPI) GetAllStoragePools(ctx context.Context) ([]StoragePool, error) {
	label := StoragePool{}.TableName()
	slog.Info("fetching manila data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.RequestTimer != nil {
			hist := api.mon.RequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return schedulerstats.ListDetail(api.sc, schedulerstats.ListDetailOpts{}).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Pools []StoragePool `json:"pools"`
	}{}
	// Log the raw body for debugging purposes.
	slog.Info("raw response body", "body", pages.(schedulerstats.PoolPage).Body)
	if err := pages.(schedulerstats.PoolPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Pools))
	return data.Pools, nil
}
