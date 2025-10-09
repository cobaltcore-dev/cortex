// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/descheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/lib/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

type server struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ComputeHost string `json:"OS-EXT-SRV-ATTR:host"`
}

type migration struct {
	InstanceUUID  string `json:"instance_uuid"`
	SourceCompute string `json:"source_compute"`
	DestCompute   string `json:"dest_compute"`
}

type NovaAPI interface {
	// Initialize the Nova API with the Keystone authentication.
	Init(ctx context.Context)
	// Get a server by ID.
	Get(ctx context.Context, id string) (server, error)
	// Live migrate a server to a new host (doesnt wait for it to complete).
	LiveMigrate(ctx context.Context, id string) error
	// Get migrations for a server by ID.
	GetServerMigrations(ctx context.Context, id string) ([]migration, error)
}

type novaAPI struct {
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Nova configuration.
	conf conf.NovaDeschedulerConfig
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
}

func NewNovaAPI(keystoneAPI keystone.KeystoneAPI, conf conf.NovaDeschedulerConfig) NovaAPI {
	return &novaAPI{
		keystoneAPI: keystoneAPI,
		conf:        conf,
	}
}

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

// Get a server by ID.
func (api *novaAPI) Get(ctx context.Context, id string) (server, error) {
	var s server
	if err := servers.Get(ctx, api.sc, id).ExtractInto(&s); err != nil {
		return server{}, err
	}
	return s, nil
}

// Live migrate a server to a new host (doesn't wait for it to complete).
func (api *novaAPI) LiveMigrate(ctx context.Context, id string) error {
	blockMigration := false
	lmo := servers.LiveMigrateOpts{
		Host:           nil,
		BlockMigration: &blockMigration, // required
	}
	result := servers.LiveMigrate(ctx, api.sc, id, lmo)
	return result.Err
}

// Get migrations for a server by ID.
func (api *novaAPI) GetServerMigrations(ctx context.Context, id string) ([]migration, error) {
	// Note: currently we need to fetch this without gophercloud.
	// See: https://github.com/gophercloud/gophercloud/pull/3244
	initialURL := api.sc.Endpoint + "os-migrations" + "?instance_uuid=" + id
	var nextURL = &initialURL
	var migrations []migration
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
			Migrations []migration `json:"migrations"`
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
	slog.Info("fetched migrations for vm", "id", id, "count", len(migrations))
	return migrations, nil
}
