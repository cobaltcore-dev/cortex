// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import (
	"context"

	"github.com/gophercloud/gophercloud/v2"
)

type MockKeystoneAPI struct {
	Url             string
	EndpointLocator gophercloud.EndpointLocator
}

func (m *MockKeystoneAPI) Authenticate(ctx context.Context) error {
	return nil
}

func (m *MockKeystoneAPI) Client() *gophercloud.ProviderClient {
	return &gophercloud.ProviderClient{
		EndpointLocator: m.EndpointLocator,
	}
}

func (m *MockKeystoneAPI) FindEndpoint(availability, serviceType string) (string, error) {
	return m.Url, nil
}
