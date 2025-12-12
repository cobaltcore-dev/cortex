// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
)

func LimesClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}

	// Automatically fetch the limes endpoint from the keystone service catalog.
	// See: https://github.com/sapcc/limes/blob/5ea068b/docs/users/api-example.md?plain=1#L23
	provider := keystoneAPI.Client()

	serviceType := "resources"
	sameAsKeystone := keystoneAPI.Availability()
	url, err := keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return nil, err
	}

	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
	}
	return &OpenstackClient{
		keystoneAPI:   keystoneAPI,
		serviceClient: serviceClient,
	}, nil
}
