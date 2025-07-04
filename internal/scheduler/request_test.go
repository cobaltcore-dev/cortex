// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import "log/slog"

type mockPipelineRequest struct {
	WeightKeys   []string
	TraceLogArgs []slog.Attr
	Subjects     []string
	Weights      map[string]float64
}

func (m mockPipelineRequest) GetWeightKeys() []string        { return m.WeightKeys }
func (m mockPipelineRequest) GetTraceLogArgs() []slog.Attr   { return m.TraceLogArgs }
func (m mockPipelineRequest) GetSubjects() []string          { return m.Subjects }
func (m mockPipelineRequest) GetWeights() map[string]float64 { return m.Weights }
