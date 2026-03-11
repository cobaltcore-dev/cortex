// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func GetMaxSlotIndex(reservations []v1alpha1.Reservation) int {
	maxIndex := -1
	for _, res := range reservations {
		// Parse slot index from name: "commitment-<uuid>-<index>"
		parts := strings.Split(res.Name, "-")
		if len(parts) >= 3 {
			if index, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				if index > maxIndex {
					maxIndex = index
				}
			}
		}
	}
	return maxIndex
}

// Always continue counting slots from max, instead of filling gaps.
func GetNextSlotIndex(reservations []v1alpha1.Reservation) int {
	maxIndex := GetMaxSlotIndex(reservations)
	return maxIndex + 1
}

// extractCommitmentUUID parses UUID from reservation name (commitment-<uuid>-<slot>).
func extractCommitmentUUID(name string) string {
	// Remove "commitment-" prefix
	withoutPrefix := strings.TrimPrefix(name, "commitment-")
	// Split by "-" and take all but the last part (which is the slot index)
	parts := strings.Split(withoutPrefix, "-")
	if len(parts) > 1 {
		// Rejoin all parts except the last one (slot index)
		return strings.Join(parts[:len(parts)-1], "-")
	}
	return withoutPrefix
}
