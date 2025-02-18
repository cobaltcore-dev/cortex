// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

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

type KeystoneAPI interface {
	Authenticate(context.Context) error
	Client() *gophercloud.ProviderClient
}

type keystoneAPI struct {
	client       *gophercloud.ProviderClient
	keystoneConf KeystoneConf
}

func newKeystoneAPI(keystoneConf KeystoneConf) KeystoneAPI {
	return &keystoneAPI{keystoneConf: keystoneConf}
}

func (api *keystoneAPI) Authenticate(ctx context.Context) error {
	if api.client != nil {
		// Already authenticated.
		return nil
	}
	slog.Info("authenticating against openstack")
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

func (api *keystoneAPI) Client() *gophercloud.ProviderClient {
	return api.client
}
