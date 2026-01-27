// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import "github.com/cobaltcore-dev/cortex/internal/scheduling/descheduling/nova/plugins/kvm"

// Configuration of steps supported by the descheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedDetectors = map[string]Detector{
	"avoid_high_steal_pct": &kvm.AvoidHighStealPctStep{},
}
