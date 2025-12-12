// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
)

func NovaClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()
	serviceType := "compute"
	sameAsKeystone := keystoneAPI.Availability()
	url, err := keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return nil, fmt.Errorf("failed to find nova endpoint: %w", err)
	}

	microversion := "2.61"
	slog.Info("using nova endpoint", "url", url)
	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		// Since microversion 2.53, the hypervisor id and service id is a UUID.
		// We need that to find placement resource providers for hypervisors.
		// Since 2.61, the extra_specs are returned in the flavor details.
		Microversion: microversion,
	}

	apiVersionHeaderKey := "X-OpenStack-Nova-API-Version"
	return &OpenstackClient{
		keystoneAPI:         keystoneAPI,
		serviceClient:       serviceClient,
		apiVersionHeaderKey: &apiVersionHeaderKey,
		apiVersionHeader:    &microversion,
	}, nil
}
