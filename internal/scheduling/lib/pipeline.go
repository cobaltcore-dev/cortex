// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

type Pipeline[RequestType PipelineRequest] interface {
	// Run the scheduling pipeline with the given request.
	Run(request RequestType) (v1alpha1.DecisionResult, error)
}
