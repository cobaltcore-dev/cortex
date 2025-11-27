// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/placement/v1/resourceproviders"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/prometheus/client_golang/prometheus"
)

type PlacementAPI interface {
	// Init the placement API.
	Init(ctx context.Context) error
	// Fetch all resource providers from the placement API.
	GetAllResourceProviders(ctx context.Context) ([]ResourceProvider, error)
	// Fetch all traits for the given resource providers from the placement API.
	GetAllTraits(ctx context.Context, providers []ResourceProvider) ([]Trait, error)
	// Fetch all inventories + usages for the given resource providers from the placement API.
	GetAllInventoryUsages(ctx context.Context, providers []ResourceProvider) ([]InventoryUsage, error)
}

// API for OpenStack
type placementAPI struct {
	// Monitor to track the api.
	mon datasources.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Placement configuration.
	conf v1alpha1.PlacementDatasource
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
	// Sleep interval to avoid overloading the API.
	sleepInterval time.Duration
}

// Create a new OpenStack placement api.
func NewPlacementAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.PlacementDatasource) PlacementAPI {
	return &placementAPI{mon: mon, keystoneAPI: k, conf: conf, sleepInterval: 50 * time.Millisecond}
}

// Init the placement API.
func (api *placementAPI) Init(ctx context.Context) error {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		return err
	}
	// Automatically fetch the placement endpoint from the keystone service catalog.
	provider := api.keystoneAPI.Client()
	serviceType := "placement"
	sameAsKeystone := api.keystoneAPI.Availability()
	url, err := api.keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return err
	}
	slog.Info("using placement endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		// Needed, otherwise openstack will return 404s for traits.
		Microversion: "1.29",
	}
	return nil
}

// Fetch all resource providers from the placement API.
func (api *placementAPI) GetAllResourceProviders(ctx context.Context) ([]ResourceProvider, error) {
	label := ResourceProvider{}.TableName()
	slog.Info("fetching placement data", "label", label)
	// Fetch all pages.
	pages, err := func() (pagination.Page, error) {
		if api.mon.RequestTimer != nil {
			hist := api.mon.RequestTimer.WithLabelValues(label)
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
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := sync.Mutex{}
	results := []Trait{}
	var wg sync.WaitGroup
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

// Resolve the resource inventories and usages for the given resource providers.
// This function fetches the data for each resource provider in parallel.
func (api *placementAPI) GetAllInventoryUsages(ctx context.Context, providers []ResourceProvider) ([]InventoryUsage, error) {
	label := InventoryUsage{}.TableName()
	slog.Info("fetching placement data", "label", label)
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := sync.Mutex{}
	results := []InventoryUsage{}
	var wg sync.WaitGroup
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
func (api *placementAPI) getInventoryUsages(ctx context.Context, provider ResourceProvider) ([]InventoryUsage, error) {
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

	results := []InventoryUsage{}
	for inventoryType, inventory := range inventory.Inventories {
		usage, ok := usagesByType[inventoryType]
		if !ok {
			return nil, fmt.Errorf("no usage found for inventory type %s", inventoryType)
		}
		results = append(results, InventoryUsage{
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
