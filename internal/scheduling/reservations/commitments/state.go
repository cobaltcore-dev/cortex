// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/sapcc/go-api-declarations/liquid"
)

// commitmentUUIDPattern validates commitment UUID format.
var commitmentUUIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{6,40}$`)

// Limes LIQUID resource naming convention: hw_version_<flavorgroup>_<resourcetype>
// Supported resource types: _ram, _cores, _instances
const (
	resourceNamePrefix = "hw_version_"
	// Resource type suffixes
	ResourceSuffixRAM       = "_ram"
	ResourceSuffixCores     = "_cores"
	ResourceSuffixInstances = "_instances"
)

// ResourceNameRAM creates a LIQUID resource name for RAM from a flavor group name.
// Format: hw_version_<flavorgroup>_ram
func ResourceNameRAM(flavorGroup string) string {
	return resourceNamePrefix + flavorGroup + ResourceSuffixRAM
}

// ResourceNameCores creates a LIQUID resource name for CPU cores from a flavor group name.
// Format: hw_version_<flavorgroup>_cores
func ResourceNameCores(flavorGroup string) string {
	return resourceNamePrefix + flavorGroup + ResourceSuffixCores
}

// ResourceNameInstances creates a LIQUID resource name for instance count from a flavor group name.
// Format: hw_version_<flavorgroup>_instances
func ResourceNameInstances(flavorGroup string) string {
	return resourceNamePrefix + flavorGroup + ResourceSuffixInstances
}

// GetFlavorGroupNameFromResource extracts the flavor group name from a LIQUID resource name.
// Only accepts _ram resources since CommitmentState is RAM-based.
// Callers handling _cores or _instances must use a different approach.
func GetFlavorGroupNameFromResource(resourceName string) (string, error) {
	if !strings.HasPrefix(resourceName, resourceNamePrefix) {
		return "", fmt.Errorf("invalid resource name: %s (missing prefix)", resourceName)
	}

	// Only accept _ram suffix - commitments are RAM-based and CommitmentState
	// carries TotalMemoryBytes. Accepting _cores or _instances here would
	// silently reinterpret non-RAM amounts as RAM, producing wrong state.
	if !strings.HasSuffix(resourceName, ResourceSuffixRAM) {
		return "", fmt.Errorf("invalid resource name: %s (only _ram resources are supported for commitments)", resourceName)
	}

	// Remove prefix and suffix
	name := strings.TrimPrefix(resourceName, resourceNamePrefix)
	name = strings.TrimSuffix(name, ResourceSuffixRAM)

	// Validate that the extracted group name is not empty
	if name == "" {
		return "", fmt.Errorf("invalid resource name: %s (empty group name)", resourceName)
	}

	return name, nil
}

// CommitmentState represents desired or current commitment resource allocation.
type CommitmentState struct {
	// CommitmentUUID is the UUID of the commitment this state corresponds to.
	CommitmentUUID string
	// ProjectID is the OpenStack project this commitment belongs to
	ProjectID string
	// DomainID is the OpenStack domain this commitment belongs to
	DomainID string
	// FlavorGroupName identifies the flavor group (e.g., "hana_medium_v2")
	FlavorGroupName string
	// the total memory in bytes across all reservation slots
	TotalMemoryBytes int64
	// AvailabilityZone specifies the availability zone for this commitment
	AvailabilityZone string
	// StartTime is when the commitment becomes active
	StartTime *time.Time
	// EndTime is when the commitment expires
	EndTime *time.Time
	// CreatorRequestID is the request ID that triggered this state change (for traceability)
	CreatorRequestID string
	// NamePrefix overrides the default "commitment-<uuid>-" reservation naming convention.
	// When set (e.g. "<cr-name>-"), Reservation CRDs are named "<NamePrefix><slot-index>".
	// Used by the CommittedResource controller; leave empty for the legacy syncer path.
	NamePrefix string
	// ParentGeneration is the Generation of the parent CommittedResource CRD. Written into
	// Reservation spec so the Reservation controller can echo it back in status, letting
	// the CR controller detect when all children have been processed for the current spec.
	// Zero for syncer-created reservations (no parent CR).
	ParentGeneration int64
	// State is the lifecycle state from Limes (planned/pending/guaranteed/confirmed/superseded/expired).
	State v1alpha1.CommitmentStatus
}

// FromCommitment converts Limes commitment to CommitmentState.
func FromCommitment(
	commitment Commitment,
) (*CommitmentState, error) {
	// Validate commitment UUID format
	if !commitmentUUIDPattern.MatchString(commitment.UUID) {
		return nil, errors.New("unexpected commitment format")
	}

	flavorGroupName, err := GetFlavorGroupNameFromResource(commitment.ResourceName)
	if err != nil {
		return nil, err
	}

	// Calculate total memory from commitment amount (1 GiB per unit)
	const gibInBytes = int64(1) << 30
	totalMemoryBytes := int64(commitment.Amount) * gibInBytes //nolint:gosec // commitment amount from Limes API, bounded by quota limits

	// Set start time: use ConfirmedAt if available, otherwise CreatedAt
	var startTime *time.Time
	if commitment.ConfirmedAt != nil {
		t := time.Unix(int64(*commitment.ConfirmedAt), 0) //nolint:gosec // timestamp from Limes API, realistically bounded
		startTime = &t
	} else {
		t := time.Unix(int64(commitment.CreatedAt), 0) //nolint:gosec // timestamp from Limes API, realistically bounded
		startTime = &t
	}

	// Set end time from ExpiresAt
	var endTime *time.Time
	if commitment.ExpiresAt > 0 {
		t := time.Unix(int64(commitment.ExpiresAt), 0) //nolint:gosec // timestamp from Limes API, realistically bounded
		endTime = &t
	}

	return &CommitmentState{
		CommitmentUUID:   commitment.UUID,
		ProjectID:        commitment.ProjectID,
		DomainID:         commitment.DomainID,
		FlavorGroupName:  flavorGroupName,
		TotalMemoryBytes: totalMemoryBytes,
		AvailabilityZone: commitment.AvailabilityZone,
		StartTime:        startTime,
		EndTime:          endTime,
		State:            v1alpha1.CommitmentStatus(commitment.Status),
	}, nil
}

// FromChangeCommitmentTargetState converts LIQUID API request to CommitmentState.
func FromChangeCommitmentTargetState(
	commitment liquid.Commitment,
	projectID string,
	domainID string,
	flavorGroupName string,
	az string,
) (*CommitmentState, error) {
	// Validate commitment UUID format
	commitmentUUID := string(commitment.UUID)
	if !commitmentUUIDPattern.MatchString(commitmentUUID) {
		return nil, errors.New("unexpected commitment format")
	}

	amountMultiple := uint64(0)
	var startTime *time.Time
	var endTime *time.Time

	switch commitment.NewStatus.UnwrapOr("none") {
	// pending, guaranteed, confirmed commitments are honored with Reservation slots.
	case liquid.CommitmentStatusPending, liquid.CommitmentStatusGuaranteed, liquid.CommitmentStatusConfirmed:
		amountMultiple = commitment.Amount
		// Set start time: use ConfirmBy if available (when the commitment was confirmed),
		// otherwise use time.Now() for immediate confirmation
		confirmByTime := commitment.ConfirmBy.UnwrapOr(time.Time{})
		if !confirmByTime.IsZero() {
			startTime = &confirmByTime
		} else {
			now := time.Now()
			startTime = &now
		}
	}

	// ConfirmBy is ignored for now
	// TODO do more sophisticated handling of guaranteed commitments

	// Set end time if not zero (commitments can have no expiry)
	if !commitment.ExpiresAt.IsZero() {
		endTime = &commitment.ExpiresAt
		// check expiry time
		if !commitment.ExpiresAt.After(time.Now()) {
			// commitment is already expired, ignore capacity
			amountMultiple = 0
		}
	}

	// Amount represents GiB of RAM (1 GiB per unit)
	const gibInBytes = int64(1) << 30
	totalMemoryBytes := int64(amountMultiple) * gibInBytes

	return &CommitmentState{
		CommitmentUUID:   string(commitment.UUID),
		ProjectID:        projectID,
		DomainID:         domainID,
		FlavorGroupName:  flavorGroupName,
		TotalMemoryBytes: totalMemoryBytes,
		AvailabilityZone: az,
		StartTime:        startTime,
		EndTime:          endTime,
		State:            v1alpha1.CommitmentStatus(commitment.NewStatus.UnwrapOr("")),
	}, nil
}

// FromCommittedResource reads CommitmentState from a CommittedResource CRD.
// Only memory commitments are supported; cores support is added in a follow-up.
func FromCommittedResource(cr v1alpha1.CommittedResource) (*CommitmentState, error) {
	if cr.Spec.ResourceType != v1alpha1.CommittedResourceTypeMemory {
		return nil, fmt.Errorf("unsupported resource type %q: only memory commitments are supported", cr.Spec.ResourceType)
	}

	if !commitmentUUIDPattern.MatchString(cr.Spec.CommitmentUUID) {
		return nil, errors.New("unexpected commitment format")
	}

	state := &CommitmentState{
		CommitmentUUID:   cr.Spec.CommitmentUUID,
		ProjectID:        cr.Spec.ProjectID,
		DomainID:         cr.Spec.DomainID,
		FlavorGroupName:  cr.Spec.FlavorGroupName,
		TotalMemoryBytes: cr.Spec.Amount.Value(),
		AvailabilityZone: cr.Spec.AvailabilityZone,
	}

	if cr.Spec.StartTime != nil {
		t := cr.Spec.StartTime.Time
		state.StartTime = &t
	}
	if cr.Spec.EndTime != nil && !cr.Spec.EndTime.IsZero() {
		t := cr.Spec.EndTime.Time
		state.EndTime = &t
	}

	return state, nil
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
		DomainID:         first.Spec.CommittedResourceReservation.DomainID,
		FlavorGroupName:  first.Spec.CommittedResourceReservation.ResourceGroup,
		TotalMemoryBytes: 0,
		AvailabilityZone: first.Spec.AvailabilityZone,
	}

	if first.Spec.StartTime != nil {
		state.StartTime = &first.Spec.StartTime.Time
	}
	if first.Spec.EndTime != nil {
		state.EndTime = &first.Spec.EndTime.Time
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
			// log message using baseLog since this is a static function without context
			baseLog.Error(errors.New("inconsistent flavor group in reservation"),
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
