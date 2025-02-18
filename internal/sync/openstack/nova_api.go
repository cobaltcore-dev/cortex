// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type NovaAPI interface {
	// Init the nova API.
	Init(ctx context.Context)
	// List all servers.
	GetAllServers(ctx context.Context) ([]Server, error)
	// List all hypervisors.
	GetAllHypervisors(ctx context.Context) ([]Hypervisor, error)
}

// API for OpenStack Nova.
type novaAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI KeystoneAPI
	// Nova configuration.
	conf NovaConf
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

// Create a new OpenStack server syncer.
func newNovaAPI(mon sync.Monitor, k KeystoneAPI, conf NovaConf) NovaAPI {
	return &novaAPI{mon: mon, keystoneAPI: k, conf: conf}
}

// Init the nova API.
func (api *novaAPI) Init(ctx context.Context) {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		panic(err)
	}
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: api.keystoneAPI.Client(),
		// For some reason gophercloud expects a trailing slash.
		Endpoint: api.conf.URL + "/",
		Type:     "compute",
	}
}

// Get all Nova servers.
func (api *novaAPI) GetAllServers(ctx context.Context) ([]Server, error) {
	label := Server{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return servers.List(api.sc, servers.ListOpts{AllTenants: true}).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Servers []Server `json:"servers"`
	}{}
	if err := pages.(servers.ServerPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Servers))
	return data.Servers, nil
}

// Get all Nova hypervisors.
func (api *novaAPI) GetAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return hypervisors.List(api.sc, hypervisors.ListOpts{}).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Hypervisors []Hypervisor `json:"hypervisors"`
	}{}
	if err := pages.(hypervisors.HypervisorPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Hypervisors))
	return data.Hypervisors, nil
}
