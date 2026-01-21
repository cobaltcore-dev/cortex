// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import "log/slog"

type mockPipelineRequest struct {
	WeightKeys   []string
	TraceLogArgs []slog.Attr
	Subjects     []string
	Weights      map[string]float64
	Pipeline     string
}

func (m mockPipelineRequest) GetWeightKeys() []string        { return m.WeightKeys }
func (m mockPipelineRequest) GetTraceLogArgs() []slog.Attr   { return m.TraceLogArgs }
func (m mockPipelineRequest) GetSubjects() []string          { return m.Subjects }
func (m mockPipelineRequest) GetWeights() map[string]float64 { return m.Weights }
func (m mockPipelineRequest) GetPipeline() string            { return m.Pipeline }

func (m mockPipelineRequest) FilterSubjects(subjects map[string]float64) PipelineRequest {
	filteredSubjects := make([]string, 0, len(subjects))
	for subject := range subjects {
		filteredSubjects = append(filteredSubjects, subject)
	}
	m.Subjects = filteredSubjects
	return m
}
