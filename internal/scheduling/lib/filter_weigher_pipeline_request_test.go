// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import "log/slog"

type mockFilterWeigherPipelineRequest struct {
	WeightKeys   []string
	TraceLogArgs []slog.Attr
	Hosts        []string
	Weights      map[string]float64
	Pipeline     string
}

func (m mockFilterWeigherPipelineRequest) GetWeightKeys() []string        { return m.WeightKeys }
func (m mockFilterWeigherPipelineRequest) GetTraceLogArgs() []slog.Attr   { return m.TraceLogArgs }
func (m mockFilterWeigherPipelineRequest) GetHosts() []string             { return m.Hosts }
func (m mockFilterWeigherPipelineRequest) GetWeights() map[string]float64 { return m.Weights }
func (m mockFilterWeigherPipelineRequest) GetPipeline() string            { return m.Pipeline }

func (m mockFilterWeigherPipelineRequest) FilterHosts(hosts map[string]float64) FilterWeigherPipelineRequest {
	filteredHosts := make([]string, 0, len(hosts))
	for host := range hosts {
		filteredHosts = append(filteredHosts, host)
	}
	m.Hosts = filteredHosts
	return m
}
