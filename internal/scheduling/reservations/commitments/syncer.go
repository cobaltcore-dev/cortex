// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	syncLog = ctrl.Log.WithName("sync")
	// Identifier for the creator of reservations.
	Creator = "commitments syncer"
)

type SyncerConfig struct {
	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`
	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`
}

type Syncer struct {
	// Client to fetch commitments.
	CommitmentsClient
	// Client for the kubernetes API.
	client.Client
}

// Create a new compute reservation syncer.
func NewSyncer(k8sClient client.Client) *Syncer {
	return &Syncer{
		CommitmentsClient: NewCommitmentsClient(),
		Client:            k8sClient,
	}
}

// Initialize the syncer.
func (s *Syncer) Init(ctx context.Context, config SyncerConfig) error {
	// Initialize the syncer.
	if err := s.CommitmentsClient.Init(ctx, s.Client, config); err != nil {
		return err
	}
	return nil
}

// Helper struct to unify the commitment with metadata needed for reservation creation.
type resolvedCommitment struct {
	Commitment
	Flavor Flavor
}

// Get all compute commitments that should be converted to reservations.
func (s *Syncer) resolveUnusedCommitments(ctx context.Context) ([]resolvedCommitment, error) {
	// Get all data we need from the openstack services.
	allProjects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	flavors, err := s.ListFlavorsByName(ctx)
	if err != nil {
		return nil, err
	}
	commitments, err := s.ListCommitmentsByID(ctx, allProjects...)
	if err != nil {
		return nil, err
	}

	// Remove non-compute/non-instance commitments or commitments we can't resolve.
	var resolvedCommitments []resolvedCommitment
	for id, commitment := range commitments {
		if commitment.ServiceType != "compute" {
			delete(commitments, id)
			syncLog.Info("skipping non-compute commitment", "id", id, "serviceType", commitment.ServiceType)
			continue
		}
		if !strings.HasPrefix(commitment.ResourceName, "instances_") {
			syncLog.Info("skipping non-instance commitment", "id", id, "resourceName", commitment.ResourceName)
			delete(commitments, id)
			continue
		}
		flavorName := strings.TrimPrefix(commitment.ResourceName, "instances_")
		flavor, ok := flavors[flavorName]
		if !ok {
			syncLog.Info("skipping commitment without known flavor", "id", id, "flavorName", flavorName)
			delete(commitments, id)
			continue
		}
		// We only support cloud-hypervisor and qemu hypervisors for commitments.
		hvType, ok := flavor.ExtraSpecs["capabilities:hypervisor_type"]
		if !ok || !slices.Contains([]string{"ch", "qemu"}, strings.ToLower(hvType)) {
			syncLog.Info("skipping commitment with unsupported hv type", "commitmentID", commitment.UUID, "hypervisorType", hvType)
			delete(commitments, id)
			continue
		}
		resolvedCommitments = append(resolvedCommitments, resolvedCommitment{
			Commitment: commitment,
			Flavor:     flavor,
		})
	}

	// Remove all commitments which are currently actively in use by a vm.
	projectsWithCommitments := make([]Project, 0, len(resolvedCommitments))
	projectIDs := make(map[string]bool)
	for _, commitment := range resolvedCommitments {
		projectIDs[commitment.ProjectID] = true
	}
	for _, project := range allProjects {
		if _, exists := projectIDs[project.ID]; exists {
			projectsWithCommitments = append(projectsWithCommitments, project)
		}
	}
	// List all servers, not only the active ones, like limes when it calculates
	// subresource usage: https://github.com/sapcc/limes/blob/c146c82/internal/liquids/nova/subresources.go#L94
	servers, err := s.ListServersByProjectID(ctx, projectsWithCommitments...)
	if err != nil {
		return nil, err
	}
	sort.Slice(resolvedCommitments, func(i, j int) bool {
		return resolvedCommitments[i].ID < resolvedCommitments[j].ID
	})
	mappedServers := map[string]struct{}{} // Servers subtracted from a commitment
	var unusedCommitments []resolvedCommitment
	for _, commitment := range resolvedCommitments {
		matchingServerCount := uint64(0)

		activeServers, ok := servers[commitment.ProjectID]
		if !ok || len(activeServers) == 0 {
			// No active servers in this project, keep the commitment.
			unusedCommitments = append(unusedCommitments, commitment)
			continue
		}
		// Some active servers, subtract them from the commitment amount.
		sort.Slice(activeServers, func(i, j int) bool {
			return activeServers[i].ID < activeServers[j].ID
		})
		for _, server := range activeServers {
			if _, exists := mappedServers[server.ID]; exists {
				// This server is already subtracted from another commitment.
				continue
			}
			if server.FlavorName != commitment.Flavor.Name {
				// This server is of a different flavor, skip it.
				continue
			}
			mappedServers[server.ID] = struct{}{}
			matchingServerCount++
			syncLog.Info("subtracting server from commitment", "commitmentID", commitment.UUID, "serverID", server.ID, "remainingAmount", commitment.Amount)
		}
		if matchingServerCount >= commitment.Amount {
			syncLog.Info("skipping commitment that is fully used by active servers", "id", commitment.UUID, "project", commitment.ProjectID)
			continue
		}
		commitment.Amount -= matchingServerCount
		unusedCommitments = append(unusedCommitments, commitment)
	}

	return unusedCommitments, nil
}

// Fetch commitments and update/create reservations for each of them.
func (s *Syncer) SyncReservations(ctx context.Context) error {
	// Get all commitments that should be converted to reservations.
	commitments, err := s.resolveUnusedCommitments(ctx)
	if err != nil {
		syncLog.Error(err, "failed to get compute commitments")
		return err
	}
	// Map commitments to reservations.
	var reservationsByName = make(map[string]v1alpha1.Reservation)
	for _, commitment := range commitments {
		// Get only the 5 first characters from the uuid. This should be safe enough.
		if len(commitment.UUID) < 5 {
			err := errors.New("commitment UUID is too short")
			syncLog.Error(err, "uuid is less than 5 characters", "uuid", commitment.UUID)
			continue
		}
		commitmentUUIDShort := commitment.UUID[:5]
		spec := v1alpha1.ReservationSpec{
			Creator: Creator,
			Scheduler: v1alpha1.ReservationSchedulerSpec{
				CortexNova: &v1alpha1.ReservationSchedulerSpecCortexNova{
					ProjectID:        commitment.ProjectID,
					DomainID:         commitment.DomainID,
					FlavorName:       commitment.Flavor.Name,
					FlavorExtraSpecs: commitment.Flavor.ExtraSpecs,
				},
			},
			Requests: map[string]resource.Quantity{
				"memory": *resource.NewQuantity(int64(commitment.Flavor.RAM)*1024*1024, resource.BinarySI),
				"cpu":    *resource.NewQuantity(int64(commitment.Flavor.VCPUs), resource.DecimalSI),
				// Disk is currently not considered.
			},
		}
		for n := range commitment.Amount { // N instances
			meta := ctrl.ObjectMeta{
				Name: fmt.Sprintf("commitment-%s-%d", commitmentUUIDShort, n),
			}
			if _, exists := reservationsByName[meta.Name]; exists {
				syncLog.Error(errors.New("duplicate reservation name"),
					"reservation name already exists",
					"name", meta.Name,
					"commitmentUUID", commitment.UUID,
				)
				continue
			}
			reservationsByName[meta.Name] = v1alpha1.Reservation{
				ObjectMeta: meta,
				Spec:       spec,
			}
		}
	}

	// Create new reservations or update existing ones.
	for _, res := range reservationsByName {
		// Check if the reservation already exists.
		nn := types.NamespacedName{Name: res.Name, Namespace: res.Namespace}
		var existing v1alpha1.Reservation
		if err := s.Get(ctx, nn, &existing); err != nil {
			if !k8serrors.IsNotFound(err) {
				syncLog.Error(err, "failed to get reservation", "name", nn.Name)
				return err
			}
			// Reservation does not exist, create it.
			if err := s.Create(ctx, &res); err != nil {
				return err
			}
			syncLog.Info("created reservation", "name", nn.Name)
			continue
		}
		// Reservation exists, update it.
		old := existing.DeepCopy()
		existing.Spec = res.Spec
		patch := client.MergeFrom(old)
		if err := s.Patch(ctx, &existing, patch); err != nil {
			syncLog.Error(err, "failed to patch reservation", "name", nn.Name)
			return err
		}
		syncLog.Info("updated reservation", "name", nn.Name)
	}

	// Delete reservations that are not in the commitments anymore.
	var existingReservations v1alpha1.ReservationList
	if err := s.List(ctx, &existingReservations); err != nil {
		syncLog.Error(err, "failed to list existing reservations")
		return err
	}
	for _, existing := range existingReservations.Items {
		// Only manage reservations created by this syncer.
		if existing.Spec.Creator != Creator {
			continue
		}
		if _, found := reservationsByName[existing.Name]; !found {
			// Reservation not found in commitments, delete it.
			if err := s.Delete(ctx, &existing); err != nil {
				syncLog.Error(err, "failed to delete reservation", "name", existing.Name)
				return err
			}
			syncLog.Info("deleted reservation", "name", existing.Name)
		}
	}

	syncLog.Info("synced reservations", "count", len(reservationsByName))
	return nil
}
