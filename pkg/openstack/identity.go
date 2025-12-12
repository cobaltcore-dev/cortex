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

func IdentityClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()
	serviceType := "identity"
	url, err := keystoneAPI.FindEndpoint("public", serviceType)
	if err != nil {
		return nil, fmt.Errorf("failed to find identity endpoint: %w", err)
	}

	slog.Info("using identity endpoint", "url", url)
	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
	}
	return &OpenstackClient{
		keystoneAPI:   keystoneAPI,
		serviceClient: serviceClient,
	}, nil
}
