// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/sapcc/go-api-declarations/liquid"
	ctrl "sigs.k8s.io/controller-runtime"
)

var stateLog = ctrl.Log.WithName("commitment_state")

// Limes LIQUID resource naming convention: ram_<flavorgroup>
const commitmentResourceNamePrefix = "ram_"

func getFlavorGroupNameFromResource(resourceName string) (string, error) {
	if !strings.HasPrefix(resourceName, commitmentResourceNamePrefix) {
		return "", fmt.Errorf("invalid resource name: %s", resourceName)
	}
	return strings.TrimPrefix(resourceName, commitmentResourceNamePrefix), nil
}

// CommitmentState represents desired or current commitment resource allocation.
type CommitmentState struct {
	// CommitmentUUID uniquely identifies this commitment
	CommitmentUUID string
	// ProjectID is the OpenStack project this commitment belongs to
	ProjectID string
	// FlavorGroupName identifies the flavor group (e.g., "hana_medium_v2")
	FlavorGroupName string
	// the total memory in bytes across all reservation slots
	TotalMemoryBytes int64
}

// FromCommitment converts Limes commitment to CommitmentState.
func FromCommitment(
	commitment Commitment,
	flavorGroup compute.FlavorGroupFeature,
) (*CommitmentState, error) {

	flavorGroupName, err := getFlavorGroupNameFromResource(commitment.ResourceName)
	if err != nil {
		return nil, err
	}

	// Calculate total memory from commitment amount (amount = multiples of smallest flavor)
	smallestFlavorMemoryBytes := int64(flavorGroup.SmallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory from specs, realistically bounded
	totalMemoryBytes := int64(commitment.Amount) * smallestFlavorMemoryBytes              //nolint:gosec // commitment amount from Limes API, bounded by quota limits

	return &CommitmentState{
		CommitmentUUID:   commitment.UUID,
		ProjectID:        commitment.ProjectID,
		FlavorGroupName:  flavorGroupName,
		TotalMemoryBytes: totalMemoryBytes,
	}, nil
}

// FromChangeCommitmentTargetState converts LIQUID API request to CommitmentState.
func FromChangeCommitmentTargetState(
	commitment liquid.Commitment,
	projectID string,
	flavorGroupName string,
	flavorGroup compute.FlavorGroupFeature,
) (*CommitmentState, error) {

	amountMultiple := uint64(0)

	switch commitment.NewStatus.UnwrapOr("none") {
	// guaranteed and confirmed commitments are honored with start time now
	case liquid.CommitmentStatusGuaranteed, liquid.CommitmentStatusConfirmed:
		amountMultiple = commitment.Amount
	}

	// ConfirmBy is ignored for now
	// TODO do more sophisticated handling of guaranteed commitments

	// check expiry time
	if commitment.ExpiresAt.Before(time.Now()) || commitment.ExpiresAt.Equal(time.Now()) {
		// commitment is already expired, ignore capacity
		amountMultiple = 0
	}

	// Flavors are sorted by size descending, so the last one is the smallest
	smallestFlavor := flavorGroup.SmallestFlavor
	smallestFlavorMemoryBytes := int64(smallestFlavor.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory from specs, realistically bounded

	// Amount represents multiples of the smallest flavor in the group
	totalMemoryBytes := int64(amountMultiple) * smallestFlavorMemoryBytes //nolint:gosec // commitment amount from Limes API, bounded by quota limits

	return &CommitmentState{
		CommitmentUUID:   string(commitment.UUID),
		ProjectID:        projectID,
		FlavorGroupName:  flavorGroupName,
		TotalMemoryBytes: totalMemoryBytes,
	}, nil
}

// FromReservations reconstructs CommitmentState from existing Reservation CRDs.
func FromReservations(reservations []v1alpha1.Reservation) (*CommitmentState, error) {
	if len(reservations) == 0 {
		return nil, errors.New("no reservations provided")
	}

	// Extract commitment metadata from first reservation
	first := reservations[0]
	if first.Spec.CommittedResourceReservation == nil {
		return nil, errors.New("not a committed resource reservation")
	}

	state := &CommitmentState{
		CommitmentUUID:   extractCommitmentUUID(first.Name),
		ProjectID:        first.Spec.CommittedResourceReservation.ProjectID,
		FlavorGroupName:  first.Spec.CommittedResourceReservation.ResourceGroup,
		TotalMemoryBytes: 0,
	}

	// Sum memory across all reservations
	for _, res := range reservations {
		if res.Spec.CommittedResourceReservation == nil {
			return nil, errors.New("unexpected reservation type of reservation " + res.Name)
		}
		// check if it belongs to the same commitment
		if extractCommitmentUUID(res.Name) != state.CommitmentUUID {
			return nil, errors.New("reservation " + res.Name + " does not belong to commitment " + state.CommitmentUUID)
		}
		// check flavor group consistency, ignore if not matching to repair corrupted state in k8s
		if res.Spec.CommittedResourceReservation.ResourceGroup != state.FlavorGroupName {
			// log message
			stateLog.Error(errors.New("inconsistent flavor group in reservation"),
				"reservation belongs to same commitment but has different flavor group - ignoring reservation for capacity calculation",
				"reservationName", res.Name,
				"expectedFlavorGroup", state.FlavorGroupName,
				"actualFlavorGroup", res.Spec.CommittedResourceReservation.ResourceGroup,
			)
			continue
		}

		memoryQuantity := res.Spec.Resources["memory"]
		memoryBytes := memoryQuantity.Value()
		state.TotalMemoryBytes += memoryBytes
	}

	return state, nil
}
