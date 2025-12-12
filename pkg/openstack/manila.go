// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
)

func ManilaClient(ctx context.Context, keystoneAPI keystone.KeystoneAPI) (*OpenstackClient, error) {
	if err := keystoneAPI.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate keystone: %w", err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := keystoneAPI.Client()

	gophercloud.ServiceTypeAliases["shared-file-system"] = []string{"sharev2"}
	manilaSC, err := openstack.NewSharedFileSystemV2(provider, gophercloud.EndpointOpts{
		Type:         "sharev2",
		Availability: gophercloud.Availability(keystoneAPI.Availability()),
	})
	if err != nil {
		return nil, err
	}

	microversion := "2.65"
	manilaSC.Microversion = microversion

	slog.Info("using manila endpoint", "url", manilaSC.Endpoint)

	apiVersionHeaderKey := "X-OpenStack-Manila-API-Version"
	return &OpenstackClient{
		keystoneAPI:         keystoneAPI,
		serviceClient:       manilaSC,
		apiVersionHeaderKey: &apiVersionHeaderKey,
		apiVersionHeader:    &microversion,
	}, nil
}
