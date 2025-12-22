// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import (
	"context"

	"github.com/gophercloud/gophercloud/v2"
)

type MockKeystoneClient struct {
	Url             string
	EndpointLocator gophercloud.EndpointLocator
}

func (m *MockKeystoneClient) Authenticate(ctx context.Context) error {
	return nil
}

func (m *MockKeystoneClient) Client() *gophercloud.ProviderClient {
	return &gophercloud.ProviderClient{
		EndpointLocator: m.EndpointLocator,
	}
}

func (m *MockKeystoneClient) FindEndpoint(availability, serviceType string) (string, error) {
	return m.Url, nil
}

func (m *MockKeystoneClient) Availability() string {
	return "" // Mock does not have a specific availability
}
