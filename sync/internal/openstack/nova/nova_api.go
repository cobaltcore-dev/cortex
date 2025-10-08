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

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/nova"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
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
	// Get all nova servers that are NOT deleted. (Includes ERROR, SHUTOFF etc)
	GetAllServers(ctx context.Context) ([]nova.Server, error)
	// Get all deleted nova servers since the timestamp.
	GetDeletedServers(ctx context.Context, since time.Time) ([]nova.DeletedServer, error)
	// Get all nova hypervisors.
	GetAllHypervisors(ctx context.Context) ([]nova.Hypervisor, error)
	// Get all nova flavors.
	GetAllFlavors(ctx context.Context) ([]nova.Flavor, error)
	// Get all nova migrations.
	GetAllMigrations(ctx context.Context) ([]nova.Migration, error)
	// Get all aggregates.
	GetAllAggregates(ctx context.Context) ([]nova.Aggregate, error)
}

// API for OpenStack Nova.
type novaAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Nova configuration.
	conf conf.SyncOpenStackNovaConfig
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

// Create a new OpenStack server syncer.
func NewNovaAPI(mon sync.Monitor, k keystone.KeystoneAPI, conf conf.SyncOpenStackNovaConfig) NovaAPI {
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
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: "2.61",
	}
}

// Get all Nova servers that are NOT deleted. (Includes ERROR, SHUTOFF etc)
func (api *novaAPI) GetAllServers(ctx context.Context) ([]nova.Server, error) {
	label := nova.Server{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		lo := servers.ListOpts{
			AllTenants: true,
		}
		return servers.List(api.sc, lo).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Servers []nova.Server `json:"servers"`
	}{}
	if err := pages.(servers.ServerPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Servers))
	return data.Servers, nil
}

// Get all deleted Nova servers.
// Note on Nova terminology: Nova uses "instance" internally in its database and code,
// but exposes these as "server" objects through the public API.
// Server lifecycle and cleanup:
//   - In SAP Cloud Infrastructure's Nova fork, orphaned servers are purged after 3 weeks
//   - This means historical server data is limited to 3 weeks
func (api *novaAPI) GetDeletedServers(ctx context.Context, since time.Time) ([]nova.DeletedServer, error) {
	label := nova.DeletedServer{}.TableName()

	slog.Info("fetching nova data", "label", label, "changedSince", since)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		// It is important to omit the changes-since parameter if it is nil.
		// Otherwise Nova will return huge amounts of data since the beginning of time.
		lo := servers.ListOpts{
			Status:     "DELETED",
			AllTenants: true,
		}
		lo.ChangesSince = since.Format(time.RFC3339)
		return servers.List(api.sc, lo).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Servers []nova.DeletedServer `json:"servers"`
	}{}
	if err := pages.(servers.ServerPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Servers))
	return data.Servers, nil
}

// Get all Nova hypervisors.
func (api *novaAPI) GetAllHypervisors(ctx context.Context) ([]nova.Hypervisor, error) {
	label := nova.Hypervisor{}.TableName()
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
	var hypervisors []nova.Hypervisor
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
			Hypervisors []nova.Hypervisor `json:"hypervisors"`
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

// Get all Nova flavors.
func (api *novaAPI) GetAllFlavors(ctx context.Context) ([]nova.Flavor, error) {
	label := nova.Flavor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		lo := flavors.ListOpts{AccessType: flavors.AllAccess} // Also private flavors.
		return flavors.ListDetail(api.sc, lo).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		Flavors []nova.Flavor `json:"flavors"`
	}{}
	if err := pages.(flavors.FlavorPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(data.Flavors))
	return data.Flavors, nil
}

// Get all Nova migrations from the OpenStack API.
//
// Note on Nova terminology: Nova uses "instance" internally in its database and code,
// but exposes these as "server" objects through the public API.
//
// Migration lifecycle and cleanup:
//   - Migrations are automatically deleted when their associated server is deleted
//     (see Nova source: https://github.com/openstack/nova/blob/1508cb39a2b12ef2d4f706b9c303a744ce40e707/nova/db/main/api.py#L1337-L1358)
//   - In SAP Cloud Infrastructure's Nova fork, orphaned migrations are purged after 3 weeks
//   - This means historical migration data has limited retention
func (api *novaAPI) GetAllMigrations(ctx context.Context) ([]nova.Migration, error) {
	label := nova.Migration{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Note: currently we need to fetch this without gophercloud.
	// See: https://github.com/gophercloud/gophercloud/pull/3244
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}
	initialURL := api.sc.Endpoint + "os-migrations"
	var nextURL = &initialURL
	var migrations []nova.Migration
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
			Migrations []nova.Migration `json:"migrations"`
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

func (api *novaAPI) GetAllAggregates(ctx context.Context) ([]nova.Aggregate, error) {
	label := nova.Aggregate{}.TableName()
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

	// Parse the json data into our custom model.
	type AggregatesPage struct {
		Aggregate []nova.RawAggregate `json:"aggregates"`
	}

	data := &AggregatesPage{}
	if err := pages.(aggregates.AggregatesPage).ExtractInto(data); err != nil {
		return nil, err
	}

	slog.Info("fetched", "label", label, "count", len(data.Aggregate))

	aggregates := []nova.Aggregate{}

	// Convert RawAggregate to Aggregate
	for _, rawAggregate := range data.Aggregate {
		properties, err := json.Marshal(rawAggregate.Metadata)
		if err != nil {
			slog.Warn(
				"failed to marshal aggregate properties",
				"aggregate", rawAggregate.UUID, "error", err,
			)
			properties = []byte{}
		}
		if len(rawAggregate.Hosts) == 0 {
			// If no host is assigned to the aggregate, add it as empty.
			aggregates = append(aggregates, nova.Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      nil,
				Metadata:         string(properties),
			})
		}
		for _, host := range rawAggregate.Hosts {
			computeHost := host // Make it safe.
			aggregates = append(aggregates, nova.Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      &computeHost,
				Metadata:         string(properties),
			})
		}
	}
	return aggregates, nil
}
