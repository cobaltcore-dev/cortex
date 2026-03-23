// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"fmt"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
)

// FlavorGroupAcceptsCommitments returns true if the given flavor group can accept committed resources.
// Currently, only groups with a fixed RAM/core ratio (all flavors have the same ratio) accept CRs.
// This is the single source of truth for CR eligibility and should be used across all CR APIs.
func FlavorGroupAcceptsCommitments(fg *compute.FlavorGroupFeature) bool {
	return fg.HasFixedRamCoreRatio()
}

// FlavorGroupCommitmentRejectionReason returns the reason why the given flavor group does not accept CRs.
// Returns empty string if the group accepts commitments.
func FlavorGroupCommitmentRejectionReason(fg *compute.FlavorGroupFeature) string {
	if FlavorGroupAcceptsCommitments(fg) {
		return ""
	}
	var minRatio, maxRatio uint64
	if fg.RamCoreRatioMin != nil {
		minRatio = *fg.RamCoreRatioMin
	}
	if fg.RamCoreRatioMax != nil {
		maxRatio = *fg.RamCoreRatioMax
	}
	return fmt.Sprintf("flavor group %q has variable RAM/core ratio (min=%d, max=%d) and does not accept commitments",
		fg.Name, minRatio, maxRatio)
}
