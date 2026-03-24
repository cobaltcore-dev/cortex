// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"fmt"
	"strings"
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

type Syncer struct {
	// Client to fetch commitments from Limes
	CommitmentsClient
	// Kubernetes client for CRD operations
	client.Client
}

func NewSyncer(k8sClient client.Client) *Syncer {
	return &Syncer{
		CommitmentsClient: NewCommitmentsClient(),
		Client:            k8sClient,
	}
}

func (s *Syncer) Init(ctx context.Context, config SyncerConfig) error {
	if err := s.CommitmentsClient.Init(ctx, s.Client, config); err != nil {
		return err
	}
	return nil
}

func (s *Syncer) getCommitmentStates(ctx context.Context, log logr.Logger, flavorGroups map[string]compute.FlavorGroupFeature) ([]*CommitmentState, error) {
	allProjects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	commitments, err := s.ListCommitmentsByID(ctx, allProjects...)
	if err != nil {
		return nil, err
	}

	// Filter for compute commitments with RAM flavor group resources
	var commitmentStates []*CommitmentState
	for id, commitment := range commitments {
		if commitment.ServiceType != "compute" {
			log.Info("skipping non-compute commitment", "id", id, "serviceType", commitment.ServiceType)
			continue
		}
		if !strings.HasPrefix(commitment.ResourceName, commitmentResourceNamePrefix) {
			log.Info("skipping non-RAM-flavor-group commitment", "id", id, "resourceName", commitment.ResourceName)
			continue
		}

		// Extract flavor group name from resource name
		flavorGroupName, err := getFlavorGroupNameFromResource(commitment.ResourceName)
		if err != nil {
			log.Info("skipping commitment with invalid resource name",
				"id", id,
				"resourceName", commitment.ResourceName,
				"error", err)
			continue
		}

		// Validate flavor group exists in Knowledge
		flavorGroup, exists := flavorGroups[flavorGroupName]
		if !exists {
			log.Info("skipping commitment with unknown flavor group",
				"id", id,
				"flavorGroup", flavorGroupName)
			continue
		}

		// Skip commitments with empty UUID
		if commitment.UUID == "" {
			log.Info("skipping commitment with empty UUID",
				"id", id)
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

		commitmentStates = append(commitmentStates, state)
	}

	return commitmentStates, nil
}

// SyncReservations fetches commitments from Limes and synchronizes Reservation CRDs.
func (s *Syncer) SyncReservations(ctx context.Context) error {
	// TODO handle concurrency with change API: consider creation time of reservations and status ready

	// Create context with request ID for this sync execution
	runID := fmt.Sprintf("sync-%d", time.Now().Unix())
	ctx = WithNewGlobalRequestID(ctx)
	logger := LoggerFromContext(ctx).WithValues("component", "syncer", "runID", runID)

	logger.Info("starting commitment sync with sync interval", "interval", DefaultSyncerConfig().SyncInterval)

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
	commitmentStates, err := s.getCommitmentStates(ctx, logger, flavorGroups)
	if err != nil {
		logger.Error(err, "failed to get compute commitments")
		return err
	}

	// Create ReservationManager to handle state application
	manager := NewReservationManager(s.Client)

	// Apply each commitment state using the manager
	for _, state := range commitmentStates {
		logger.Info("applying commitment state",
			"commitmentUUID", state.CommitmentUUID,
			"projectID", state.ProjectID,
			"flavorGroup", state.FlavorGroupName,
			"totalMemoryBytes", state.TotalMemoryBytes)

		_, _, err := manager.ApplyCommitmentState(ctx, logger, state, flavorGroups, CreatorValue)
		if err != nil {
			logger.Error(err, "failed to apply commitment state",
				"commitmentUUID", state.CommitmentUUID)
			// Continue with other commitments even if one fails
			continue
		}
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

	// Build set of commitment UUIDs we should have
	activeCommitments := make(map[string]bool)
	for _, state := range commitmentStates {
		activeCommitments[state.CommitmentUUID] = true
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
		}
	}

	logger.Info("synced reservations", "commitmentCount", len(commitmentStates))
	return nil
}
