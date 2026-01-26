// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

// Mock PipelineInitializer for testing
type mockPipelineInitializer struct {
	pipelineType     v1alpha1.PipelineType
	initPipelineFunc func(ctx context.Context, p v1alpha1.Pipeline) PipelineInitResult[mockPipeline]
}

func (m *mockPipelineInitializer) InitPipeline(
	ctx context.Context, p v1alpha1.Pipeline,
) PipelineInitResult[mockPipeline] {

	if m.initPipelineFunc != nil {
		return m.initPipelineFunc(ctx, p)
	}
	return PipelineInitResult[mockPipeline]{Pipeline: mockPipeline{name: p.Name}}
}

func (m *mockPipelineInitializer) PipelineType() v1alpha1.PipelineType {
	return m.pipelineType
}
