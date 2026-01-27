// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"

type VMDetection struct {
	// Get the VM ID for which this decision applies.
	VMID string
	// Get a human-readable reason for this decision.
	Reason string
	// Get the compute host where the vm should be migrated away from.
	Host string
}

func (d VMDetection) GetResource() string                    { return d.VMID }
func (d VMDetection) GetReason() string                      { return d.Reason }
func (d VMDetection) GetHost() string                        { return d.Host }
func (d VMDetection) WithReason(reason string) lib.Detection { d.Reason = reason; return d }
