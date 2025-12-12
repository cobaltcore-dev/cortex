// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/openstack"
	"github.com/prometheus/client_golang/prometheus"
)

type NovaAPI interface {
	// Init the nova API.
	Init(ctx context.Context) error
	// Get all nova servers that are NOT deleted. (Includes ERROR, SHUTOFF etc)
	GetAllServers(ctx context.Context) ([]Server, error)
	// Get all deleted nova servers since the timestamp.
	GetDeletedServers(ctx context.Context, since time.Time) ([]DeletedServer, error)
	// Get all nova hypervisors.
	GetAllHypervisors(ctx context.Context) ([]Hypervisor, error)
	// Get all nova flavors.
	GetAllFlavors(ctx context.Context) ([]Flavor, error)
	// Get all nova migrations.
	GetAllMigrations(ctx context.Context) ([]Migration, error)
	// Get all aggregates.
	GetAllAggregates(ctx context.Context) ([]Aggregate, error)
}

// API for OpenStack Nova.
type novaAPI struct {
	// Monitor to track the api.
	mon datasources.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Nova configuration.
	conf v1alpha1.NovaDatasource
	// Authenticated OpenStack service client to fetch the data.
	client *openstack.OpenstackClient
}

func NewNovaAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.NovaDatasource) NovaAPI {
	return &novaAPI{mon: mon, keystoneAPI: k, conf: conf}
}

// Init the nova API.
func (api *novaAPI) Init(ctx context.Context) error {
	client, err := openstack.NovaClient(ctx, api.keystoneAPI)
	if err != nil {
		return err
	}
	api.client = client
	return nil
}

// Get all Nova servers that are NOT deleted. (Includes ERROR, SHUTOFF etc)
func (api *novaAPI) GetAllServers(ctx context.Context) ([]Server, error) {
	label := Server{}.TableName()
	slog.Info("fetching nova data", "label", label)

	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var allServers []Server
	err := api.client.List(ctx, "servers/detail", url.Values{"all_tenants": []string{"true"}}, "servers", &allServers)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(allServers))
	return allServers, nil
}

// Get all deleted Nova servers.
// Note on Nova terminology: Nova uses "instance" internally in its database and code,
// but exposes these as "server" objects through the public API.
// Server lifecycle and cleanup:
//   - In SAP Cloud Infrastructure's Nova fork, orphaned servers are purged after 3 weeks
//   - This means historical server data is limited to 3 weeks
func (api *novaAPI) GetDeletedServers(ctx context.Context, since time.Time) ([]DeletedServer, error) {
	label := DeletedServer{}.TableName()
	slog.Info("fetching nova data", "label", label)

	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var allDeletedServers []DeletedServer
	query := url.Values{
		"status":        []string{"DELETED"},
		"all_tenants":   []string{"true"},
		"changes-since": []string{since.Format(time.RFC3339)},
	}
	err := api.client.List(ctx, "servers/detail", query, "servers", &allDeletedServers)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(allDeletedServers))
	return allDeletedServers, nil
}

// Get all Nova hypervisors.
func (api *novaAPI) GetAllHypervisors(ctx context.Context) ([]Hypervisor, error) {
	label := Hypervisor{}.TableName()
	slog.Info("fetching nova data", "label", label)
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var hypervisors []Hypervisor
	err := api.client.List(ctx, "os-hypervisors/detail", url.Values{}, "hypervisors", &hypervisors)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(hypervisors))
	return hypervisors, nil
}

// Get all Nova flavors.
func (api *novaAPI) GetAllFlavors(ctx context.Context) ([]Flavor, error) {
	label := Flavor{}.TableName()
	slog.Info("fetching nova data", "label", label)

	var flavors []Flavor
	query := url.Values{
		"all_tenants": []string{"true"},
	}

	err := api.client.List(ctx, "flavors/detail", query, "flavors", &flavors)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(flavors))
	return flavors, nil
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
func (api *novaAPI) GetAllMigrations(ctx context.Context) ([]Migration, error) {
	label := Migration{}.TableName()
	slog.Info("fetching nova data", "label", label)
	// Note: currently we need to fetch this without gophercloud.
	// See: https://github.com/gophercloud/gophercloud/pull/3244
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var migrations []Migration
	err := api.client.List(ctx, "os-migrations", url.Values{}, "migrations", &migrations)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(migrations))
	return migrations, nil
}

// Get all Nova aggregates.
func (api *novaAPI) GetAllAggregates(ctx context.Context) ([]Aggregate, error) {
	label := Aggregate{}.TableName()
	slog.Info("fetching nova data", "label", label)

	var rawAggregates []RawAggregate
	err := api.client.List(ctx, "os-aggregates", url.Values{}, "aggregates", &rawAggregates)
	if err != nil {
		return nil, err
	}
	slog.Info("fetched", "label", label, "count", len(rawAggregates))
	aggregates := []Aggregate{}
	// Convert RawAggregate to Aggregate
	for _, rawAggregate := range rawAggregates {
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
			aggregates = append(aggregates, Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      nil,
				Metadata:         string(properties),
			})
		}
		for _, host := range rawAggregate.Hosts {
			computeHost := host // Make it safe.
			aggregates = append(aggregates, Aggregate{
				UUID:             rawAggregate.UUID,
				Name:             rawAggregate.Name,
				AvailabilityZone: rawAggregate.AvailabilityZone,
				ComputeHost:      &computeHost,
				Metadata:         string(properties),
			})
		}
	}
	slog.Info("extracted after fetch", "label", label, "count", len(aggregates))
	return aggregates, nil
}
