// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import "log/slog"

type mockFilterWeigherPipelineRequest struct {
	WeightKeys   []string
	TraceLogArgs []slog.Attr
	Subjects     []string
	Weights      map[string]float64
	Pipeline     string
}

func (m mockFilterWeigherPipelineRequest) GetWeightKeys() []string        { return m.WeightKeys }
func (m mockFilterWeigherPipelineRequest) GetTraceLogArgs() []slog.Attr   { return m.TraceLogArgs }
func (m mockFilterWeigherPipelineRequest) GetSubjects() []string          { return m.Subjects }
func (m mockFilterWeigherPipelineRequest) GetWeights() map[string]float64 { return m.Weights }
func (m mockFilterWeigherPipelineRequest) GetPipeline() string            { return m.Pipeline }

func (m mockFilterWeigherPipelineRequest) FilterSubjects(subjects map[string]float64) FilterWeigherPipelineRequest {
	filteredSubjects := make([]string, 0, len(subjects))
	for subject := range subjects {
		filteredSubjects = append(filteredSubjects, subject)
	}
	m.Subjects = filteredSubjects
	return m
}
