// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import "context"

type mockHypervisorClient struct {
	hypervisorsToReturn []Hypervisor
	errToReturn         error
}

func (m *mockHypervisorClient) Init(ctx context.Context) {}

func (m *mockHypervisorClient) ListHypervisors(ctx context.Context) ([]Hypervisor, error) {
	return m.hypervisorsToReturn, m.errToReturn
}
