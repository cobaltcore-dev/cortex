// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import "github.com/cobaltcore-dev/cortex/internal/scheduler/manila/api"

type MockRequest struct {
	Spec    any
	Context api.ManilaRequestContext
	Hosts   []string
	Weights map[string]float64
}

func (r *MockRequest) GetSpec() any                         { return r.Spec }
func (r *MockRequest) GetContext() api.ManilaRequestContext { return r.Context }
func (r *MockRequest) GetHosts() []string                   { return r.Hosts }
func (r *MockRequest) GetWeights() map[string]float64       { return r.Weights }
