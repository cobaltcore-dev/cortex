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

func CinderClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()
	serviceType := "volumev3"
	sameAsKeystone := keystoneAPI.Availability()
	url, err := keystoneAPI.FindEndpoint(sameAsKeystone, serviceType)
	if err != nil {
		return nil, fmt.Errorf("failed to find cinder endpoint: %w", err)
	}

	microversion := "3.70"
	slog.Info("using cinder endpoint", "url", url)
	serviceClient := &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Microversion:   microversion,
	}

	apiVersionHeaderKey := "OpenStack-API-Version"
	apiVersionHeader := "volume " + microversion
	return &OpenstackClient{
		keystoneAPI:         keystoneAPI,
		serviceClient:       serviceClient,
		apiVersionHeaderKey: &apiVersionHeaderKey,
		apiVersionHeader:    &apiVersionHeader,
	}, nil
}
