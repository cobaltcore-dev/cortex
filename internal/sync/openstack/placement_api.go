// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

type PlacementAPI interface {
	ListResourceProviders(KeystoneAuth) ([]ResourceProvider, error)
	ResolveTraits(KeystoneAuth, ResourceProvider) ([]ProviderDetail, error)
	ResolveAggregates(KeystoneAuth, ResourceProvider) ([]ProviderDetail, error)
}

type placementAPI struct {
	conf    conf.SyncOpenStackConfig
	client  *http.Client
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
	// Needed, otherwise openstack won't tell us traits and aggregates.
	req.Header.Set("OpenStack-API-Version", "placement 1.29")
	if api.client == nil {
		client, err := sync.NewHTTPClient(api.conf.SSO)
		if err != nil {
			slog.Error("failed to create HTTP client", "error", err)
			return nil, err
		}
		api.client = client
	}
	return api.client.Do(req)
}

// List returns a list of resource providers from the OpenStack Placement API.
func (api *placementAPI) ListResourceProviders(auth KeystoneAuth) ([]ResourceProvider, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.
			WithLabelValues("openstack_resource_provider")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	url := api.conf.PlacementURL + "/resource_providers"
	resp, err := api.fetch(auth, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var responseJSON = struct {
		ResourceProviders []ResourceProvider `json:"resource_providers"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&responseJSON)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.
			WithLabelValues("openstack_resource_provider").Inc()
	}
	slog.Info("got openstack resource providers", "n", len(responseJSON.ResourceProviders))
	return responseJSON.ResourceProviders, nil
}

// Return a list of traits for a resource provider from the OpenStack Placement API.
func (api *placementAPI) ResolveTraits(auth KeystoneAuth, provider ResourceProvider) ([]ProviderDetail, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.
			WithLabelValues("openstack_resource_provider_trait")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	url := api.conf.PlacementURL + "/resource_providers/" + provider.UUID + "/traits"
	resp, err := api.fetch(auth, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var responseJSON = struct {
		Traits                     []string `json:"traits"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}{}

	err = json.NewDecoder(resp.Body).Decode(&responseJSON)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.
			WithLabelValues("openstack_resource_provider_trait").Inc()
	}
	slog.Info("got openstack resource provider traits", "n", len(responseJSON.Traits))
	traits := make([]ProviderDetail, len(responseJSON.Traits))
	for i, trait := range responseJSON.Traits {
		traits[i] = ResourceProviderTrait{
			ResourceProviderUUID:       provider.UUID,
			Name:                       trait,
			ResourceProviderGeneration: responseJSON.ResourceProviderGeneration,
		}
	}
	return traits, nil
}

// Return a list of aggregates for a resource provider from the OpenStack Placement API.
func (api *placementAPI) ResolveAggregates(auth KeystoneAuth, provider ResourceProvider) ([]ProviderDetail, error) {
	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.
			WithLabelValues("openstack_resource_provider_aggregate")
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	url := api.conf.PlacementURL + "/resource_providers/" + provider.UUID + "/aggregates"
	resp, err := api.fetch(auth, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var responseJSON = struct {
		Aggregates                 []string `json:"aggregates"`
		ResourceProviderGeneration int      `json:"resource_provider_generation"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&responseJSON)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.
			WithLabelValues("openstack_resource_provider_aggregate").Inc()
	}
	slog.Info("got openstack resource provider aggregates", "n", len(responseJSON.Aggregates))
	aggregates := make([]ProviderDetail, len(responseJSON.Aggregates))
	for i, uuid := range responseJSON.Aggregates {
		aggregates[i] = ResourceProviderAggregate{
			ResourceProviderUUID:       provider.UUID,
			UUID:                       uuid,
			ResourceProviderGeneration: responseJSON.ResourceProviderGeneration,
		}
	}
	return aggregates, nil
}
