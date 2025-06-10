// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
)

// Type alias for the OpenStack keystone configuration.
type KeystoneConf = conf.SyncOpenStackKeystoneConfig

// KeystoneAPI for OpenStack.
type KeystoneAPI interface {
	// Authenticate against the OpenStack keystone.
	Authenticate(context.Context) error
	// Get the OpenStack provider client.
	Client() *gophercloud.ProviderClient
	// Find the endpoint for the given service type and availability.
	FindEndpoint(availability, serviceType string) (string, error)
}

// KeystoneAPI implementation.
type keystoneAPI struct {
	// OpenStack provider client.
	client *gophercloud.ProviderClient
	// OpenStack keystone configuration.
	keystoneConf KeystoneConf
}

// Create a new OpenStack keystone API.
func NewKeystoneAPI(keystoneConf KeystoneConf) KeystoneAPI {
	return &keystoneAPI{keystoneConf: keystoneConf}
}

// Authenticate against OpenStack keystone.
func (api *keystoneAPI) Authenticate(ctx context.Context) error {
	if api.client != nil {
		// Already authenticated.
		return nil
	}
	slog.Info("authenticating against openstack", "url", api.keystoneConf.URL)
	authOptions := gophercloud.AuthOptions{
		IdentityEndpoint: api.keystoneConf.URL,
		Username:         api.keystoneConf.OSUsername,
		DomainName:       api.keystoneConf.OSUserDomainName,
		Password:         api.keystoneConf.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: api.keystoneConf.OSProjectName,
			DomainName:  api.keystoneConf.OSProjectDomainName,
		},
	}
	httpClient, err := sync.NewHTTPClient(api.keystoneConf.SSO)
	if err != nil {
		panic(err)
	}
	provider, err := openstack.NewClient(authOptions.IdentityEndpoint)
	if err != nil {
		panic(err)
	}
	provider.HTTPClient = *httpClient
	if err = openstack.Authenticate(ctx, provider, authOptions); err != nil {
		panic(err)
	}
	api.client = provider
	slog.Info("authenticated against openstack")
	return nil
}

// Find the endpoint for the given service type and availability.
func (api *keystoneAPI) FindEndpoint(availability, serviceType string) (string, error) {
	return api.client.EndpointLocator(gophercloud.EndpointOpts{
		Type:         serviceType,
		Availability: gophercloud.Availability(availability),
	})
}

// Get the OpenStack provider client.
func (api *keystoneAPI) Client() *gophercloud.ProviderClient {
	return api.client
}
