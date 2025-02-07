// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

type PlacementAPI interface {
	ListResourceProviders(KeystoneAuth) ([]ResourceProvider, error)
	ResolveTraits(KeystoneAuth, ResourceProvider) ([]ResourceProviderTrait, error)
	ResolveAggregates(KeystoneAuth, ResourceProvider) ([]ResourceProviderAggregate, error)
}

type placementAPI struct {
	conf    conf.SyncOpenStackConfig
	monitor sync.Monitor
}

func NewPlacementAPI(conf conf.SyncOpenStackConfig, monitor sync.Monitor) PlacementAPI {
	return &placementAPI{
		conf:    conf,
		monitor: monitor,
	}
}

func (api *placementAPI) fetch(auth KeystoneAuth, url string) (*http.Response, error) {
	slog.Info("getting openstack data", "url", url)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return nil, err
	}
	req.Header.Set("X-Auth-Token", auth.token)
	client, err := sync.NewHTTPClient(api.conf.SSO)
	if err != nil {
		slog.Error("failed to create HTTP client", "error", err)
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("failed to send request", "error", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Error("unexpected status code", "status", resp.StatusCode)
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return resp, nil
}

// List returns a list of resource providers from the OpenStack Placement API.
func (api *placementAPI) ListResourceProviders(auth KeystoneAuth) ([]ResourceProvider, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues("openstack_resource_provider")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	url := api.conf.PlacementURL + "/resource_providers"
	resp, err := api.fetch(auth, url)
	if err != nil {
		return nil, err
	}
	var responseJson = struct {
		ResourceProviders []ResourceProvider `json:"resource_providers"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&responseJson)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_resource_provider").Inc()
	}
	slog.Info("got openstack resource providers", "n", len(responseJson.ResourceProviders))
	return responseJson.ResourceProviders, nil
}

// Return a list of traits for a resource provider from the OpenStack Placement API.
func (api *placementAPI) ResolveTraits(auth KeystoneAuth, provider ResourceProvider) ([]ResourceProviderTrait, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues("openstack_resource_provider_trait")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	url := api.conf.PlacementURL + "/resource_providers/" + provider.UUID + "/traits"
	resp, err := api.fetch(auth, url)
	if err != nil {
		return nil, err
	}
	var responseJson = struct {
		Traits []string `json:"traits"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&responseJson)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_resource_provider_trait").Inc()
	}
	slog.Info("got openstack resource provider traits", "n", len(responseJson.Traits))
	traits := make([]ResourceProviderTrait, len(responseJson.Traits))
	for i, trait := range responseJson.Traits {
		traits[i] = ResourceProviderTrait{
			ResourceProviderUUID: provider.UUID,
			Name:                 trait,
		}
	}
	return traits, nil
}

// Return a list of aggregates for a resource provider from the OpenStack Placement API.
func (api *placementAPI) ResolveAggregates(auth KeystoneAuth, provider ResourceProvider) ([]ResourceProviderAggregate, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues("openstack_resource_provider_aggregate")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	url := api.conf.PlacementURL + "/resource_providers/" + provider.UUID + "/aggregates"
	resp, err := api.fetch(auth, url)
	if err != nil {
		return nil, err
	}
	var responseJson = struct {
		Aggregates []string `json:"aggregates"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&responseJson)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues("openstack_resource_provider_aggregate").Inc()
	}
	slog.Info("got openstack resource provider aggregates", "n", len(responseJson.Aggregates))
	aggregates := make([]ResourceProviderAggregate, len(responseJson.Aggregates))
	for i, uuid := range responseJson.Aggregates {
		aggregates[i] = ResourceProviderAggregate{
			ResourceProviderUUID: provider.UUID,
			UUID:                 uuid,
		}
	}
	return aggregates, nil
}
