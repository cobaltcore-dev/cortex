// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"log/slog"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/placement/v1/resourceproviders"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type PlacementAPI interface {
	// Init the placement API.
	Init(ctx context.Context)
	// Fetch all resource providers from the placement API.
	GetAllResourceProviders(ctx context.Context) ([]ResourceProvider, error)
	// Fetch all traits for the given resource providers from the placement API.
	GetAllTraits(ctx context.Context, providers []ResourceProvider) ([]Trait, error)
}

// API for OpenStack placement.
type placementAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI KeystoneAPI
	// Placement configuration.
	conf PlacementConf
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
	// Sleep interval to avoid overloading the API.
	sleepInterval time.Duration
}

// Create a new OpenStack placement api.
func newPlacementAPI(mon sync.Monitor, k KeystoneAPI, conf PlacementConf) PlacementAPI {
	return &placementAPI{mon: mon, keystoneAPI: k, conf: conf, sleepInterval: 50 * time.Millisecond}
}

// Init the placement API.
func (api *placementAPI) Init(ctx context.Context) {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		panic(err)
	}
	// Automatically fetch the placement endpoint from the keystone service catalog.
	provider := api.keystoneAPI.Client()
	serviceType := "placement"
	url, err := api.keystoneAPI.FindEndpoint(api.conf.Availability, serviceType)
	if err != nil {
		panic(err)
	}
	slog.Info("using placement endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		// Needed, otherwise openstack will return 404s for traits.
		Microversion: "1.29",
	}
}

// Fetch all resource providers from the placement API.
func (api *placementAPI) GetAllResourceProviders(ctx context.Context) ([]ResourceProvider, error) {
	label := ResourceProvider{}.TableName()
	slog.Info("fetching placement data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.PipelineRequestTimer != nil {
			hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
			timer := prometheus.NewTimer(hist)
			defer timer.ObserveDuration()
		}
		return resourceproviders.List(api.sc, resourceproviders.ListOpts{}).AllPages(ctx)
	}()
	if err != nil {
		return nil, err
	}
	// Parse the json data into our custom model.
	var data = &struct {
		ResourceProviders []ResourceProvider `json:"resource_providers"`
	}{}
	if err := pages.(resourceproviders.ResourceProvidersPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched placement data", "label", label, "count", len(data.ResourceProviders))
	return data.ResourceProviders, nil
}

// Resolve the traits for the given resource providers.
// This function fetches the traits for each resource provider in parallel.
func (api *placementAPI) GetAllTraits(ctx context.Context, providers []ResourceProvider) ([]Trait, error) {
	label := Trait{}.TableName()
	slog.Info("fetching placement data", "label", label)
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := gosync.Mutex{}
	results := []Trait{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(providers))

	for _, provider := range providers {
		wg.Add(1)
		go func(provider ResourceProvider) {
			defer wg.Done()
			// Fetch traits for the provider.
			newResults, err := api.getTraits(ctx, provider)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			resultMutex.Lock()
			results = append(results, newResults...)
			resultMutex.Unlock()
		}(provider)
		time.Sleep(api.sleepInterval) // Don't overload the API.
	}

	// Wait for all goroutines to finish and close the error channel.
	go func() {
		wg.Wait()
		close(errChan)
	}()
	// Return the first error encountered, if any.
	for err := range errChan {
		if err != nil {
			slog.Error("failed to resolve traits", "error", err)
			return nil, err
		}
	}
	return results, nil
}

// Resolve the trait for the given resource provider.
func (api *placementAPI) getTraits(ctx context.Context, provider ResourceProvider) ([]Trait, error) {
	result := resourceproviders.GetTraits(ctx, api.sc, provider.UUID)
	if result.Err != nil {
		return nil, result.Err
	}
	obj, err := result.Extract()
	if err != nil {
		return nil, err
	}
	// We don't unwrap the object directly from json since it is nice
	// to have the provider UUID in the trait model.
	results := []Trait{}
	for _, trait := range obj.Traits {
		results = append(results, Trait{
			ResourceProviderUUID:       provider.UUID,
			Name:                       trait,
			ResourceProviderGeneration: provider.ResourceProviderGeneration,
		})
	}
	return results, nil
}
