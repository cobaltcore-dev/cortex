// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/sharedfilesystems/v2/schedulerstats"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type ManilaAPI interface {
	// Init the manila API.
	Init(ctx context.Context)
	// Get all changed manila storage pools.
	GetChangedStoragePools(ctx context.Context, changedSince *time.Time) ([]StoragePool, error)
}

// API for OpenStack Manila.
type manilaAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Manila configuration.
	conf ManilaConf
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

// Create a new OpenStack Manila api.
func NewManilaAPI(mon sync.Monitor, k keystone.KeystoneAPI, conf ManilaConf) ManilaAPI {
	return &manilaAPI{mon: mon, keystoneAPI: k, conf: conf}
}

// Init the manila API.
func (api *manilaAPI) Init(ctx context.Context) {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		panic(err)
	}
	// Automatically fetch the manila endpoint from the keystone service catalog.
	provider := api.keystoneAPI.Client()
	// Workaround to find the v2 service of manila.
	// See: https://github.com/gophercloud/gophercloud/issues/3347
	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	sc, err := openstack.NewSharedFileSystemV2(provider, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(api.conf.Availability),
	})
	if err != nil {
		panic(fmt.Errorf("failed to create OpenStack Manila service client: %w", err))
	}
	sc.Microversion = "2.65"
	api.sc = sc
}

// Get all Manila storage pools since the timestamp.
// Note: changedSince has no effect here since the Manila api does not support it.
// We will fetch all storage pools all the time.
func (api *manilaAPI) GetChangedStoragePools(ctx context.Context, changedSince *time.Time) ([]StoragePool, error) {
	label := StoragePool{}.TableName()
	slog.Info("fetching manila data", "label", label, "changedSince", changedSince)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
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
	if err := pages.(schedulerstats.PoolPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Pools))
	return data.Pools, nil
}
