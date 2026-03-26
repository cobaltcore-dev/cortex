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
	"sigs.k8s.io/controller-runtime/pkg/client"
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

		// Extract flavor group name from resource name (validates format: hw_version_<group>_ram)
		flavorGroupName, err := getFlavorGroupNameFromResource(commitment.ResourceName)
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
				s.monitor.RecordUnitMismatch(flavorGroupName)
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

	logger.Info("starting commitment sync", "syncInterval", s.syncInterval)

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

	// Create ReservationManager to handle state application
	manager := NewReservationManager(s.Client)

	// Apply each commitment state using the manager
	var totalCreated, totalDeleted, totalRepaired int
	for _, state := range commitmentResult.states {
		logger.Info("applying commitment state",
			"commitmentUUID", state.CommitmentUUID,
			"projectID", state.ProjectID,
			"flavorGroup", state.FlavorGroupName,
			"totalMemoryBytes", state.TotalMemoryBytes)

		applyResult, err := manager.ApplyCommitmentState(ctx, logger, state, flavorGroups, CreatorValue)
		if err != nil {
			logger.Error(err, "failed to apply commitment state",
				"commitmentUUID", state.CommitmentUUID)
			// Continue with other commitments even if one fails
			continue
		}

		totalCreated += applyResult.Created
		totalDeleted += applyResult.Deleted
		totalRepaired += applyResult.Repaired
	}

	// Delete reservations that are no longer in commitments
	// Only query committed resource reservations using labels for efficiency
	var existingReservations v1alpha1.ReservationList
	if err := s.List(ctx, &existingReservations, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		logger.Error(err, "failed to list existing committed resource reservations")
		return err
	}

	// Build set of commitment UUIDs we should have (processed + skipped)
	activeCommitments := make(map[string]bool)
	for _, state := range commitmentResult.states {
		activeCommitments[state.CommitmentUUID] = true
	}
	// Also include skipped commitments - don't delete their CRDs
	for uuid := range commitmentResult.skippedUUIDs {
		activeCommitments[uuid] = true
	}

	// Delete reservations for commitments that no longer exist
	for _, existing := range existingReservations.Items {
		// Extract commitment UUID from reservation name
		commitmentUUID := extractCommitmentUUID(existing.Name)
		if commitmentUUID == "" {
			logger.Info("skipping reservation with unparseable name", "name", existing.Name)
			continue
		}

		if !activeCommitments[commitmentUUID] {
			// This commitment no longer exists, delete the reservation
			if err := s.Delete(ctx, &existing); err != nil {
				logger.Error(err, "failed to delete reservation", "name", existing.Name)
				return err
			}
			logger.Info("deleted reservation for expired commitment",
				"name", existing.Name,
				"commitmentUUID", commitmentUUID)
			totalDeleted++
		}
	}

	// Record reservation change metrics
	if s.monitor != nil {
		if totalCreated > 0 {
			s.monitor.RecordReservationsCreated(totalCreated)
		}
		if totalDeleted > 0 {
			s.monitor.RecordReservationsDeleted(totalDeleted)
		}
		if totalRepaired > 0 {
			s.monitor.RecordReservationsRepaired(totalRepaired)
		}
	}

	logger.Info("synced reservations",
		"processedCount", len(commitmentResult.states),
		"skippedCount", len(commitmentResult.skippedUUIDs),
		"created", totalCreated,
		"deleted", totalDeleted,
		"repaired", totalRepaired)
	return nil
}
