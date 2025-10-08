// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"log/slog"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/sync/api/objects/openstack/placement"
	sync "github.com/cobaltcore-dev/cortex/sync/internal"
	"github.com/cobaltcore-dev/cortex/sync/internal/conf"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/placement/v1/resourceproviders"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type PlacementAPI interface {
	// Init the placement API.
	Init(ctx context.Context)
	// Fetch all resource providers from the placement API.
	GetAllResourceProviders(ctx context.Context) ([]placement.ResourceProvider, error)
	// Fetch all traits for the given resource providers from the placement API.
	GetAllTraits(ctx context.Context, providers []placement.ResourceProvider) ([]placement.Trait, error)
	// Fetch all inventories + usages for the given resource providers from the placement API.
	GetAllInventoryUsages(ctx context.Context, providers []placement.ResourceProvider) ([]placement.InventoryUsage, error)
}

// API for OpenStack placement.
type placementAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Placement configuration.
	conf conf.SyncOpenStackPlacementConfig
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
	// Sleep interval to avoid overloading the API.
	sleepInterval time.Duration
}

// Create a new OpenStack placement api.
func NewPlacementAPI(mon sync.Monitor, k keystone.KeystoneAPI, conf conf.SyncOpenStackPlacementConfig) PlacementAPI {
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
func (api *placementAPI) GetAllResourceProviders(ctx context.Context) ([]placement.ResourceProvider, error) {
	label := placement.ResourceProvider{}.TableName()
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
		ResourceProviders []placement.ResourceProvider `json:"resource_providers"`
	}{}
	if err := pages.(resourceproviders.ResourceProvidersPage).ExtractInto(data); err != nil {
		return nil, err
	}
	slog.Info("fetched placement data", "label", label, "count", len(data.ResourceProviders))
	return data.ResourceProviders, nil
}

// Resolve the traits for the given resource providers.
// This function fetches the traits for each resource provider in parallel.
func (api *placementAPI) GetAllTraits(ctx context.Context, providers []placement.ResourceProvider) ([]placement.Trait, error) {
	label := placement.Trait{}.TableName()
	slog.Info("fetching placement data", "label", label)
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := gosync.Mutex{}
	results := []placement.Trait{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(providers))

	for _, provider := range providers {
		wg.Go(func() {
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
		})
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
func (api *placementAPI) getTraits(ctx context.Context, provider placement.ResourceProvider) ([]placement.Trait, error) {
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
	results := []placement.Trait{}
	for _, trait := range obj.Traits {
		results = append(results, placement.Trait{
			ResourceProviderUUID:       provider.UUID,
			Name:                       trait,
			ResourceProviderGeneration: provider.ResourceProviderGeneration,
		})
	}
	return results, nil
}

// Resolve the resource inventories and usages for the given resource providers.
// This function fetches the data for each resource provider in parallel.
func (api *placementAPI) GetAllInventoryUsages(ctx context.Context, providers []placement.ResourceProvider) ([]placement.InventoryUsage, error) {
	label := placement.InventoryUsage{}.TableName()
	slog.Info("fetching placement data", "label", label)
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := gosync.Mutex{}
	results := []placement.InventoryUsage{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(providers))

	for _, provider := range providers {
		wg.Go(func() {
			// Fetch inventory usages for the provider.
			newResults, err := api.getInventoryUsages(ctx, provider)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			resultMutex.Lock()
			results = append(results, newResults...)
			resultMutex.Unlock()
		})
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
			slog.Error("failed to resolve inventory usages", "error", err)
			return nil, err
		}
	}
	return results, nil
}

// Resolve the inventory usages for the given resource provider.
func (api *placementAPI) getInventoryUsages(ctx context.Context, provider placement.ResourceProvider) ([]placement.InventoryUsage, error) {
	inventoryResult := resourceproviders.GetInventories(ctx, api.sc, provider.UUID)
	if inventoryResult.Err != nil {
		return nil, inventoryResult.Err
	}
	inventory, err := inventoryResult.Extract()
	if err != nil {
		return nil, err
	}
	usageResult := resourceproviders.GetUsages(ctx, api.sc, provider.UUID)
	if usageResult.Err != nil {
		return nil, usageResult.Err
	}
	usage, err := usageResult.Extract()
	if err != nil {
		return nil, err
	}

	usagesByType := make(map[string]int)
	for usageType, usage := range usage.Usages {
		usagesByType[usageType] = usage
	}

	results := []placement.InventoryUsage{}
	for inventoryType, inventory := range inventory.Inventories {
		usage, ok := usagesByType[inventoryType]
		if !ok {
			return nil, fmt.Errorf("no usage found for inventory type %s", inventoryType)
		}
		results = append(results, placement.InventoryUsage{
			ResourceProviderUUID:       provider.UUID,
			ResourceProviderGeneration: provider.ResourceProviderGeneration,
			InventoryClassName:         inventoryType,
			AllocationRatio:            inventory.AllocationRatio,
			MaxUnit:                    inventory.MaxUnit,
			MinUnit:                    inventory.MinUnit,
			Reserved:                   inventory.Reserved,
			StepSize:                   inventory.StepSize,
			Total:                      inventory.Total,
			Used:                       usage,
		})
	}
	return results, nil
}
