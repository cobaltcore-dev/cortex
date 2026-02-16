// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockHypervisorClient struct {
	hypervisorsToReturn []Hypervisor
	errToReturn         error
}

func (m *mockHypervisorClient) Init(ctx context.Context, client client.Client, conf Config) error {
	return nil
}

func (m *mockHypervisorClient) ListHypervisors(ctx context.Context) ([]Hypervisor, error) {
	return m.hypervisorsToReturn, m.errToReturn
}
