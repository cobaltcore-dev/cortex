// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Interface for a detector as part of the (de)scheduling pipeline.
type Detector[RequestType PipelineRequest] interface {
	Step[RequestType]

	// Configure the step and initialize things like a database connection.
	Init(ctx context.Context, client client.Client, step v1alpha1.DetectorSpec) error
}
