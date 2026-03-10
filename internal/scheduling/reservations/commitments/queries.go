// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListReservationsForCommitment finds Reservations by commitment UUID and optional creator.
// TODO: Uses name prefix filtering; consider adding labels for efficiency.
func ListReservationsForCommitment(ctx context.Context, k8sClient client.Client, commitmentUUID, creator string) ([]v1alpha1.Reservation, error) {
	var allReservations v1alpha1.ReservationList
	if err := k8sClient.List(ctx, &allReservations); err != nil {
		return nil, err
	}

	prefix := fmt.Sprintf("commitment-%s-", commitmentUUID)
	var matching []v1alpha1.Reservation
	for _, res := range allReservations.Items {
		// Match by name prefix and creator
		if strings.HasPrefix(res.Name, prefix) &&
			res.Spec.CommittedResourceReservation != nil &&
			(creator == "" || res.Spec.CommittedResourceReservation.Creator == creator) {
			matching = append(matching, res)
		}
	}
	return matching, nil
}

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
