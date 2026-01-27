// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CycleBreaker[DetectionType Detection] interface {
	// Initialize the cycle detector with needed clients.
	Init(ctx context.Context, client client.Client, conf conf.Config) error
	// Filter descheduling decisions to avoid cycles.
	Filter(ctx context.Context, decisions []DetectionType) ([]DetectionType, error)
}
