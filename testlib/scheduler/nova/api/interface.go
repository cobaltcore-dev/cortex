// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import "github.com/cobaltcore-dev/cortex/internal/scheduler/nova/api"

type MockRequest struct {
	Spec    api.NovaObject[api.NovaSpec]
	Context api.NovaRequestContext
	Rebuild bool
	Resize  bool
	Live    bool
	VMware  bool
	Hosts   []string
	Weights map[string]float64
}

func (r *MockRequest) GetSpec() api.NovaObject[api.NovaSpec] { return r.Spec }
func (r *MockRequest) GetContext() api.NovaRequestContext    { return r.Context }
func (r *MockRequest) GetRebuild() bool                      { return r.Rebuild }
func (r *MockRequest) GetResize() bool                       { return r.Resize }
func (r *MockRequest) GetLive() bool                         { return r.Live }
func (r *MockRequest) GetVMware() bool                       { return r.VMware }
func (r *MockRequest) GetHosts() []string                    { return r.Hosts }
func (r *MockRequest) GetWeights() map[string]float64        { return r.Weights }
