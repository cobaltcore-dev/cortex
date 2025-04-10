// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type NovaAPI interface {
	// Init the nova API.
	Init(ctx context.Context)
	// List new servers.
	GetNewServers(ctx context.Context, changedSince *string) ([]Server, error)
	// List all hypervisors.
	GetAllHypervisors(ctx context.Context) ([]Hypervisor, error)
	// List all flavors.
	// Note: This should only include the public flavors.
	GetAllFlavors(ctx context.Context) ([]Flavor, error)
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
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := api.keystoneAPI.Client()
	serviceType := "compute"
	url, err := api.keystoneAPI.FindEndpoint(api.conf.Availability, serviceType)
	if err != nil {
		panic(err)
	}
	slog.Info("using nova endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
	}
}

// Get all Nova servers.
func (api *novaAPI) GetNewServers(ctx context.Context, changedSince *string) ([]Server, error) {
	label := Server{}.TableName()
	slog.Info("fetching nova data", "label", label, "changedSince", changedSince)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		// It is important to omit the changes-since parameter if it is nil.
		// Otherwise Nova will return huge amounts of data since the beginning of time.
		lo := servers.ListOpts{AllTenants: true}
		if changedSince != nil {
			lo.ChangesSince = *changedSince
		}
		return servers.List(api.sc, lo).AllPages(ctx)
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
// Note: changedSince has no effect here since the Nova api does not support it.
// We will fetch all hypervisors all the time.
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

// Get all Nova flavors.
func (api *novaAPI) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	label := Flavor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return flavors.ListDetail(api.sc, flavors.ListOpts{}).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Flavors []Flavor `json:"flavors"`
	}{}
	if err := pages.(flavors.FlavorPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Flavors))
	return data.Flavors, nil
}
