// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Interface for a weigher as part of the scheduling pipeline.
type Weigher[RequestType PipelineRequest] interface {
	Step[RequestType]

	// Configure the step and initialize things like a database connection.
	Init(ctx context.Context, client client.Client, step v1alpha1.WeigherSpec) error
}
