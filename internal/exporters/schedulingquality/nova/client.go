// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"fmt"
	"math"
	"os"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
)

// Client wraps the OpenStack Nova API.
type Client struct {
	compute *gophercloud.ServiceClient
}

// NewClientFromEnv creates a Nova client from standard OpenStack environment
// variables (OS_AUTH_URL, OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, etc.).
func NewClientFromEnv() (*Client, error) {
	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("read auth env: %w", err)
	}

	provider, err := openstack.AuthenticatedClient(context.Background(), opts)
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	region := os.Getenv("OS_REGION_NAME")
	compute, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("create compute client: %w", err)
	}

	return &Client{compute: compute}, nil
}

// SmallestFlavor queries the Nova flavor list and returns the smallest CPU
// count and memory size (in bytes) across all public flavors. These values
// define the minimum placeable unit for stranding computation.
//
// Returns (minCPU, minMemoryBytes, error).
func (c *Client) SmallestFlavor(ctx context.Context) (int64, int64, error) {
	listOpts := flavors.ListOpts{
		AccessType: flavors.PublicAccess,
	}

	allPages, err := flavors.ListDetail(c.compute, listOpts).AllPages(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list flavors: %w", err)
	}

	allFlavors, err := flavors.ExtractFlavors(allPages)
	if err != nil {
		return 0, 0, fmt.Errorf("extract flavors: %w", err)
	}

	if len(allFlavors) == 0 {
		return 0, 0, fmt.Errorf("no flavors found")
	}

	minCPU := int64(math.MaxInt64)
	minMemMB := int64(math.MaxInt64)

	for _, f := range allFlavors {
		if int64(f.VCPUs) < minCPU && f.VCPUs > 0 {
			minCPU = int64(f.VCPUs)
		}
		if int64(f.RAM) < minMemMB && f.RAM > 0 {
			minMemMB = int64(f.RAM)
		}
	}

	if minCPU == math.MaxInt64 || minMemMB == math.MaxInt64 {
		return 0, 0, fmt.Errorf("no valid flavors found")
	}

	// Convert MB to bytes.
	minMemBytes := minMemMB * 1024 * 1024

	return minCPU, minMemBytes, nil
}
