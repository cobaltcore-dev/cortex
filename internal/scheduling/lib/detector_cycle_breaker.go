// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
)

type DetectorCycleBreaker[DetectionType Detection] interface {
	// Filter descheduling decisions to avoid cycles.
	Filter(ctx context.Context, decisions []DetectionType) ([]DetectionType, error)
}
