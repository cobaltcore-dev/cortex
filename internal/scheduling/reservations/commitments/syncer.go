// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	// CreatorValue identifies reservations created by this syncer.
	CreatorValue = "commitments-syncer"
)

type SyncerConfig struct {
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
	// SyncInterval defines how often the syncer reconciles Limes commitments to Reservation CRDs.
	SyncInterval time.Duration `json:"committedResourceSyncInterval"`
}

func DefaultSyncerConfig() SyncerConfig {
	return SyncerConfig{
		SyncInterval: time.Hour,
	}
}

// ApplyDefaults fills in any unset values with defaults.
func (c *SyncerConfig) ApplyDefaults() {
	defaults := DefaultSyncerConfig()
	if c.SyncInterval == 0 {
		c.SyncInterval = defaults.SyncInterval
	}
	// Note: KeystoneSecretRef and SSOSecretRef are not defaulted as they require explicit configuration
}

type Syncer struct {
	// Client to fetch commitments from Limes
	CommitmentsClient
	// Kubernetes client for CRD operations
	client.Client
	// Monitor for metrics
	monitor *SyncerMonitor
	// SyncInterval is stored for logging purposes (actual interval managed by task.Runner)
	syncInterval time.Duration
}

func NewSyncer(k8sClient client.Client, monitor *SyncerMonitor) *Syncer {
	return &Syncer{
		CommitmentsClient: NewCommitmentsClient(),
		Client:            k8sClient,
		monitor:           monitor,
	}
}

func (s *Syncer) Init(ctx context.Context, config SyncerConfig) error {
	s.syncInterval = config.SyncInterval
	if err := s.CommitmentsClient.Init(ctx, s.Client, config); err != nil {
		return err
	}
	return nil
}

// getCommitmentStatesResult holds both processed and skipped commitment UUIDs
type getCommitmentStatesResult struct {
	// states are the commitments that were successfully processed
	states []*CommitmentState
	// skippedUUIDs are commitment UUIDs that were skipped (e.g., due to unit mismatch)
	// but should NOT have their existing CRDs deleted
	skippedUUIDs map[string]bool
}

func (s *Syncer) getCommitmentStates(ctx context.Context, log logr.Logger, flavorGroups map[string]compute.FlavorGroupFeature) (*getCommitmentStatesResult, error) {
	allProjects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	commitments, err := s.ListCommitmentsByID(ctx, allProjects...)
	if err != nil {
		return nil, err
	}

	// Filter for compute commitments with RAM flavor group resources
	result := &getCommitmentStatesResult{
		states:       []*CommitmentState{},
		skippedUUIDs: make(map[string]bool),
	}
	for id, commitment := range commitments {
		// Record each commitment seen from Limes
		if s.monitor != nil {
			s.monitor.RecordCommitmentSeen()
		}

		if commitment.ServiceType != "compute" {
			log.Info("skipping non-compute commitment", "id", id, "serviceType", commitment.ServiceType)
			if s.monitor != nil {
				s.monitor.RecordCommitmentSkipped(SkipReasonNonCompute)
			}
			continue
		}

		// Validate that the commitment state is a known enum value.
		switch v1alpha1.CommitmentStatus(commitment.Status) {
		case v1alpha1.CommitmentStatusPlanned,
			v1alpha1.CommitmentStatusPending,
			v1alpha1.CommitmentStatusGuaranteed,
			v1alpha1.CommitmentStatusConfirmed,
			v1alpha1.CommitmentStatusSuperseded,
			v1alpha1.CommitmentStatusExpired:
			// valid, continue processing
		default:
			log.Info("skipping commitment with unknown status", "id", id, "status", commitment.Status)
			continue
		}

		// Extract flavor group name from resource name (validates format: hw_version_<group>_ram)
		flavorGroupName, err := GetFlavorGroupNameFromResource(commitment.ResourceName)
		if err != nil {
			log.Info("skipping commitment with invalid resource name",
				"id", id,
				"resourceName", commitment.ResourceName,
				"error", err)
			if s.monitor != nil {
				s.monitor.RecordCommitmentSkipped(SkipReasonInvalidResource)
			}
			continue
		}

		// Validate flavor group exists in Knowledge
		flavorGroup, exists := flavorGroups[flavorGroupName]
		if !exists {
			log.Info("skipping commitment with unknown flavor group",
				"id", id,
				"flavorGroup", flavorGroupName)
			if s.monitor != nil {
				s.monitor.RecordCommitmentSkipped(SkipReasonUnknownFlavorGroup)
			}
			continue
		}

		// Validate unit matches between Limes commitment and Cortex flavor group
		// Expected format: "<memoryMB> MiB" e.g. "131072 MiB" for 128 GiB
		expectedUnit := fmt.Sprintf("%d MiB", flavorGroup.SmallestFlavor.MemoryMB)
		if commitment.Unit != "" && commitment.Unit != expectedUnit {
			// Unit mismatch: Limes has not yet updated this commitment to the new unit.
			// Skip this commitment - trust what Cortex already has stored in CRDs.
			// On the next sync cycle after Limes updates, this will be processed.
			log.V(0).Info("WARNING: skipping commitment due to unit mismatch - Limes unit differs from Cortex flavor group, waiting for Limes to update",
				"commitmentUUID", commitment.UUID,
				"flavorGroup", flavorGroupName,
				"limesUnit", commitment.Unit,
				"expectedUnit", expectedUnit,
				"smallestFlavorMemoryMB", flavorGroup.SmallestFlavor.MemoryMB)
			if s.monitor != nil {
				s.monitor.RecordCommitmentSkipped(SkipReasonUnitMismatch)
			}
			// Track skipped commitment so its existing CRDs won't be deleted
			if commitment.UUID != "" {
				result.skippedUUIDs[commitment.UUID] = true
			}
			continue
		}

		// Skip commitments with empty UUID
		if commitment.UUID == "" {
			log.Info("skipping commitment with empty UUID",
				"id", id)
			if s.monitor != nil {
				s.monitor.RecordCommitmentSkipped(SkipReasonEmptyUUID)
			}
			continue
		}

		// Convert commitment to state using FromCommitment
		state, err := FromCommitment(commitment, flavorGroup)
		if err != nil {
			log.Error(err, "failed to convert commitment to state",
				"id", id,
				"uuid", commitment.UUID)
			continue
		}

		log.Info("resolved commitment to state",
			"commitmentID", commitment.UUID,
			"flavorGroup", flavorGroupName,
			"amount", commitment.Amount,
			"totalMemoryBytes", state.TotalMemoryBytes)

		result.states = append(result.states, state)

		// Record successfully processed commitment
		if s.monitor != nil {
			s.monitor.RecordCommitmentProcessed()
		}
	}

	return result, nil
}

// SyncReservations fetches commitments from Limes and synchronizes Reservation CRDs.
func (s *Syncer) SyncReservations(ctx context.Context) error {
	// TODO handle concurrency with change API: consider creation time of reservations and status ready

	// Create context with request ID for this sync execution
	runID := fmt.Sprintf("sync-%d", time.Now().Unix())
	ctx = WithNewGlobalRequestID(ctx)
	logger := LoggerFromContext(ctx).WithValues("component", "syncer", "runID", runID)

	logger.Info("starting commitment sync")

	// Record sync run
	if s.monitor != nil {
		s.monitor.RecordSyncRun()
	}

	// Check if flavor group knowledge is ready
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: s.Client}
	knowledgeCRD, err := knowledge.Get(ctx)
	if err != nil {
		logger.Error(err, "failed to check flavor group knowledge readiness")
		return err
	}
	if knowledgeCRD == nil {
		logger.Info("skipping commitment sync - flavor group knowledge not ready yet")
		return nil
	}

	// Get flavor groups using the CRD we already fetched
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, knowledgeCRD)
	if err != nil {
		logger.Error(err, "failed to get flavor groups from knowledge")
		return err
	}

	// Get all commitments as states
	commitmentResult, err := s.getCommitmentStates(ctx, logger, flavorGroups)
	if err != nil {
		logger.Error(err, "failed to get compute commitments")
		return err
	}

	// Upsert CommittedResource CRDs for each commitment
	var totalCreated, totalUpdated int
	for _, state := range commitmentResult.states {
		logger.Info("upserting committed resource CRD",
			"commitmentUUID", state.CommitmentUUID,
			"projectID", state.ProjectID,
			"flavorGroup", state.FlavorGroupName,
			"state", state.State)

		var (
			op  controllerutil.OperationResult
			err error
		)
		if isTerminalCommitment(state) {
			// Terminal commitments (superseded/expired state, or EndTime in the past): update
			// existing CRD so the controller can clean up Reservations, but do not create a
			// new one — if no CRD exists locally there are no Reservation slots to clean up.
			op, err = s.updateCommittedResourceIfExists(ctx, logger, state)
		} else {
			op, err = s.upsertCommittedResource(ctx, logger, state)
		}
		if err != nil {
			logger.Error(err, "failed to upsert committed resource CRD",
				"commitmentUUID", state.CommitmentUUID)
			continue
		}
		switch op {
		case controllerutil.OperationResultCreated:
			totalCreated++
		case controllerutil.OperationResultUpdated:
			totalUpdated++
		}
	}

	// Build set of commitment UUIDs we should have (processed + skipped)
	activeCommitments := make(map[string]bool)
	for _, state := range commitmentResult.states {
		activeCommitments[state.CommitmentUUID] = true
	}
	for uuid := range commitmentResult.skippedUUIDs {
		activeCommitments[uuid] = true
	}

	// Count CommittedResource CRDs present locally but absent from Limes (do not delete — Limes
	// responses may be transient and deleting active CRDs would drop Reservation slots).
	// Also GC CRDs whose EndTime has passed: the commitment is over, the controller's finalizer
	// will clean up child Reservations on deletion.
	var existingCRs v1alpha1.CommittedResourceList
	if err := s.List(ctx, &existingCRs); err != nil {
		logger.Error(err, "failed to list existing committed resource CRDs")
		return err
	}
	staleCRCount, gcDeleted := 0, 0
	for i := range existingCRs.Items {
		cr := &existingCRs.Items[i]
		if !activeCommitments[cr.Spec.CommitmentUUID] {
			staleCRCount++
		}
		if cr.Spec.EndTime != nil && !cr.Spec.EndTime.After(time.Now()) {
			if err := s.Delete(ctx, cr); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to GC expired committed resource CRD", "name", cr.Name)
				return err
			}
			logger.Info("GC'd expired committed resource CRD",
				"name", cr.Name, "endTime", cr.Spec.EndTime)
			gcDeleted++
		}
	}

	// Delete orphaned Reservation CRDs: type=committed-resource but commitment no longer active.
	// These are left over from the pre-refactor path where the syncer wrote Reservations directly.
	var existingReservations v1alpha1.ReservationList
	if err := s.List(ctx, &existingReservations, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		logger.Error(err, "failed to list committed resource reservations")
		return err
	}
	var totalReservationDeleted int
	for i := range existingReservations.Items {
		res := &existingReservations.Items[i]
		commitmentUUID := extractCommitmentUUID(res.Name)
		if commitmentUUID == "" {
			logger.Info("skipping reservation with unparseable name", "name", res.Name)
			continue
		}
		if !activeCommitments[commitmentUUID] {
			if err := s.Delete(ctx, res); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete orphaned reservation", "name", res.Name)
				return err
			}
			logger.Info("deleted orphaned reservation", "name", res.Name, "commitmentUUID", commitmentUUID)
			totalReservationDeleted++
		}
	}

	if s.monitor != nil {
		if totalCreated > 0 {
			s.monitor.RecordReservationsCreated(totalCreated)
		}
		if totalReservationDeleted > 0 {
			s.monitor.RecordReservationsDeleted(totalReservationDeleted)
		}
		s.monitor.RecordStaleCRs(staleCRCount)
	}

	if staleCRCount > 0 {
		logger.Info("WARNING: committed resource CRDs present locally but absent from Limes — review for manual cleanup",
			"staleCRs", staleCRCount)
	}

	logger.Info("synced committed resource CRDs",
		"processedCount", len(commitmentResult.states),
		"skippedCount", len(commitmentResult.skippedUUIDs),
		"created", totalCreated,
		"updated", totalUpdated,
		"staleCRs", staleCRCount,
		"expiredCRsGCd", gcDeleted,
		"orphanReservationsDeleted", totalReservationDeleted)
	return nil
}

func (s *Syncer) applyCommittedResourceSpec(cr *v1alpha1.CommittedResource, state *CommitmentState) {
	cr.Spec.CommitmentUUID = state.CommitmentUUID
	cr.Spec.SchedulingDomain = v1alpha1.SchedulingDomainNova
	cr.Spec.FlavorGroupName = state.FlavorGroupName
	cr.Spec.ResourceType = v1alpha1.CommittedResourceTypeMemory
	cr.Spec.Amount = *resource.NewQuantity(state.TotalMemoryBytes, resource.BinarySI)
	cr.Spec.AvailabilityZone = state.AvailabilityZone
	cr.Spec.ProjectID = state.ProjectID
	cr.Spec.DomainID = state.DomainID
	cr.Spec.State = state.State
	cr.Spec.AllowRejection = false

	if state.StartTime != nil {
		t := metav1.NewTime(*state.StartTime)
		cr.Spec.StartTime = &t
	} else {
		cr.Spec.StartTime = nil
	}
	if state.EndTime != nil {
		t := metav1.NewTime(*state.EndTime)
		cr.Spec.EndTime = &t
	} else {
		cr.Spec.EndTime = nil
	}
}

func (s *Syncer) upsertCommittedResource(ctx context.Context, logger logr.Logger, state *CommitmentState) (controllerutil.OperationResult, error) {
	cr := &v1alpha1.CommittedResource{}
	cr.Name = "commitment-" + state.CommitmentUUID

	op, err := controllerutil.CreateOrUpdate(ctx, s.Client, cr, func() error {
		s.applyCommittedResourceSpec(cr, state)
		return nil
	})
	if err != nil {
		return op, err
	}
	logger.V(1).Info("upserted committed resource CRD", "name", cr.Name, "op", op)
	return op, nil
}

// updateCommittedResourceIfExists updates an existing CommittedResource CRD but does not
// create one if it is absent. Used for terminal states (superseded/expired): we want the
// controller to see the state transition and clean up child Reservations, but there is no
// point creating a CRD for a commitment Cortex has never tracked.
func (s *Syncer) updateCommittedResourceIfExists(ctx context.Context, logger logr.Logger, state *CommitmentState) (controllerutil.OperationResult, error) {
	cr := &v1alpha1.CommittedResource{}
	name := "commitment-" + state.CommitmentUUID
	if err := s.Get(ctx, client.ObjectKey{Name: name}, cr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.V(1).Info("skipping terminal state — CRD does not exist locally",
				"commitmentUUID", state.CommitmentUUID, "state", state.State)
			return controllerutil.OperationResultNone, nil
		}
		return controllerutil.OperationResultNone, err
	}
	s.applyCommittedResourceSpec(cr, state)
	if err := s.Update(ctx, cr); err != nil {
		return controllerutil.OperationResultNone, err
	}
	logger.V(1).Info("updated committed resource CRD (terminal state)", "name", name, "state", state.State)
	return controllerutil.OperationResultUpdated, nil
}

// isTerminalCommitment returns true when a commitment should not result in new Reservation
// slots: either its Limes state is already terminal, or its EndTime has passed.
func isTerminalCommitment(state *CommitmentState) bool {
	switch state.State {
	case v1alpha1.CommitmentStatusSuperseded, v1alpha1.CommitmentStatusExpired:
		return true
	}
	return state.EndTime != nil && !state.EndTime.After(time.Now())
}
