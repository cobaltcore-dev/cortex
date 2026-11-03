// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

// SchedulerResult is the well-defined output of a scheduling pipeline run:
// ordered hosts, the target host, and the input/output weights plus per-step
// activations used to rank them. It is an alias of lib.PipelineResult, the
// domain-neutral type the generic pipeline returns. The human-readable
// explanation is NOT part of the result — it is a concern of writing the
// decision onto the scheduled resource (e.g. the Nova VM's CortexSpec), so the
// relevant side effect generates it from the result via lib.GenerateExplanation.
type SchedulerResult = lib.PipelineResult
