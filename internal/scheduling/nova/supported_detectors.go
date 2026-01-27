// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/detectors"
)

// Configuration of steps supported by the descheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedDetectors = map[string]lib.Detector[plugins.VMDetection]{
	"avoid_high_steal_pct": &detectors.AvoidHighStealPctStep{},
}
