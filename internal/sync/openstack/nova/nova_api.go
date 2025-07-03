// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/aggregates"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type NovaAPI interface {
	// Init the nova API.
	Init(ctx context.Context)
	// Get all changed nova servers since the timestamp.
	GetChangedServers(ctx context.Context, changedSince *time.Time) ([]Server, error)
	// Get all nova hypervisors since the timestamp.
	GetAllHypervisors(ctx context.Context) ([]Hypervisor, error)
	// Get all changed nova flavors since the timestamp.
	// Note: This should only include the public flavors.
	GetChangedFlavors(ctx context.Context, changedSince *time.Time) ([]Flavor, error)
	// Get all changed nova migrations since the timestamp.
	GetChangedMigrations(ctx context.Context, changedSince *time.Time) ([]Migration, error)
	// Get all changed aggregates since the timestamp.
	GetAllAggregates(ctx context.Context) ([]Aggregate, error)
}

// API for OpenStack Nova.
type novaAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Nova configuration.
	conf NovaConf
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

// Create a new OpenStack server syncer.
func NewNovaAPI(mon sync.Monitor, k keystone.KeystoneAPI, conf NovaConf) NovaAPI {
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
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		// We need that to find placement resource providers for hypervisors.
		Microversion: "2.53",
	}
}

// Get all changed Nova servers since the timestamp.
func (api *novaAPI) GetChangedServers(ctx context.Context, changedSince *time.Time) ([]Server, error) {
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
			lo.ChangesSince = changedSince.Format(time.RFC3339)
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
func (api *novaAPI) GetAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Note: currently we need to fetch this without gophercloud.
	// Gophercloud will just assume the request is a single page even when
	// the response is paginated, returning only the first page.
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	initialURL := api.sc.Endpoint + "os-hypervisors/detail"
	var nextURL = &initialURL
	var hypervisors []Hypervisor
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", api.sc.Token())
		req.Header.Set("X-OpenStack-Nova-API-Version", api.sc.Microversion)
		resp, err := api.sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Hypervisors []Hypervisor `json:"hypervisors"`
			Links       []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"hypervisors_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		hypervisors = append(hypervisors, list.Hypervisors...)
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
	}
	slog.Info("fetched", "label", label, "count", len(hypervisors))
	return hypervisors, nil
}

// Get all Nova flavors since the timestamp.
func (api *novaAPI) GetChangedFlavors(ctx context.Context, changedSince *time.Time) ([]Flavor, error) {
	label := Flavor{}.TableName()
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
		lo := flavors.ListOpts{}
		if changedSince != nil {
			lo.ChangesSince = changedSince.Format(time.RFC3339)
		}
		return flavors.ListDetail(api.sc, lo).AllPages(ctx)
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

// Get all Nova migrations.
func (api *novaAPI) GetChangedMigrations(ctx context.Context, changedSince *time.Time) ([]Migration, error) {
	label := Migration{}.TableName()
	slog.Info("fetching nova data", "label", label, "changedSince", changedSince)
	// Note: currently we need to fetch this without gophercloud.
	// See: https://github.com/gophercloud/gophercloud/pull/3244
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	initialURL := api.sc.Endpoint + "os-migrations"
	// It is important to omit the changes-since parameter if it is nil.
	// Otherwise Nova may return huge amounts of data since the beginning of time.
	if changedSince != nil {
		initialURL += "?changes-since=" + changedSince.Format(time.RFC3339)
	}
	var nextURL = &initialURL
	var migrations []Migration
	for nextURL != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *nextURL, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Auth-Token", api.sc.Token())
		// Needed for changes-since, user_id, and project_id.
		req.Header.Set("X-OpenStack-Nova-API-Version", "2.80")
		resp, err := api.sc.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		var list struct {
			Migrations []Migration `json:"migrations"`
			Links      []struct {
				Rel  string `json:"rel"`
				Href string `json:"href"`
			} `json:"migrations_links"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		if err != nil {
			return nil, err
		}
		nextURL = nil
		for _, link := range list.Links {
			if link.Rel == "next" {
				nextURL = &link.Href
				break
			}
		}
		migrations = append(migrations, list.Migrations...)
	}
	slog.Info("fetched", "label", label, "count", len(migrations))
	return migrations, nil
}

func (api *novaAPI) GetAllAggregates(ctx context.Context) ([]Aggregate, error) {
	label := Aggregate{}.TableName()
	slog.Info("fetching nova data", "label", label)

	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return aggregates.List(api.sc).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}

	type RawAggregate struct {
		UUID             string            `json:"uuid"`
		Name             string            `json:"name"`
		AvailabilityZone *string           `json:"availability_zone"`
		Hosts            []string          `json:"hosts"`
		Metadata         map[string]string `json:"metadata"`
	}

	// Parse the json data into our custom model.
	type AggregatesPage struct {
		Aggregate []RawAggregate `json:"aggregates"`
	}

	data := &AggregatesPage{}
	if err := pages.(aggregates.AggregatesPage).ExtractInto(data); err != nil {
		return nil, err
	}

	slog.Info("fetched", "label", label, "count", len(data.Aggregate))

	aggregates := []Aggregate{}

	// Convert RawAggregate to Aggregate
	for _, rawAggregate := range data.Aggregate {
		for _, host := range rawAggregate.Hosts {
			properties, err := json.Marshal(rawAggregate.Metadata)
			if err != nil {
				properties = []byte{}
			}
			aggregates = append(aggregates, Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      host,
				Properties:       string(properties),
			})
		}
	}
	return aggregates, nil
}
