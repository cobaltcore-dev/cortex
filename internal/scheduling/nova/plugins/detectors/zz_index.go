// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package detectors

import (
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins"
)

// Configuration of steps supported by the descheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var Index = map[string]lib.Detector[plugins.VMDetection]{}
