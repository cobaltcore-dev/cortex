// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/reservations/api/v1alpha1"
	"github.com/sapcc/go-bits/jobloop"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	syncLog = ctrl.Log.WithName("sync")
)

type Syncer struct {
	// Client to fetch commitments.
	CommitmentsClient
	// Client for the kubernetes API.
	client.Client
}

// Create a new compute reservation syncer.
func NewSyncer(k8sClient client.Client) *Syncer {
	config := conf.NewConfig[Config]()
	return &Syncer{
		CommitmentsClient: NewCommitmentsClient(config.Keystone),
		Client:            k8sClient,
	}
}

// Initialize the syncer.
func (s *Syncer) Init(ctx context.Context) {
	// Initialize the syncer.
	s.CommitmentsClient.Init(ctx)
}

// Fetch commitments and update/create reservations for each of them.
func (s *Syncer) SyncReservations(ctx context.Context) error {
	computeCommitments, err := s.GetComputeCommitments(ctx)
	if err != nil {
		return err
	}

	// Map commitments to reservations.
	var reservationsByName = make(map[string]v1alpha1.ComputeReservation)
	for _, commitment := range computeCommitments {
		// Get only the 5 first characters from the uuid. This should be safe enough.
		if len(commitment.UUID) < 5 {
			err := errors.New("commitment UUID is too short")
			syncLog.Error(err, "uuid is less than 5 characters", "uuid", commitment.UUID)
			continue
		}
		commitmentUUIDShort := commitment.UUID[:5]

		if commitment.Flavor != nil {
			// Flavor (instance) commitment
			spec := v1alpha1.ComputeReservationSpec{
				Kind:      v1alpha1.ComputeReservationSpecKindInstance,
				ProjectID: commitment.ProjectID,
				DomainID:  commitment.DomainID,
				Instance: v1alpha1.ComputeReservationSpecInstance{
					Flavor:     commitment.Flavor.Name,
					ExtraSpecs: commitment.Flavor.ExtraSpecs,
					Requests: map[string]resource.Quantity{
						"memory": *resource.NewQuantity(int64(commitment.Flavor.RAM)*1024*1024, resource.BinarySI),
						"cpu":    *resource.NewQuantity(int64(commitment.Flavor.VCPUs), resource.DecimalSI),
						// Disk is currently not considered.
					},
				},
			}
			for n := range commitment.Amount { // N instances
				meta := ctrl.ObjectMeta{
					Name: fmt.Sprintf("commitment-%s-%d", commitmentUUIDShort, n),
				}
				reservationsByName[meta.Name] = v1alpha1.ComputeReservation{
					ObjectMeta: meta,
					Spec:       spec,
				}
			}
			continue
		}

		// Bare resource commitment
		reservation := v1alpha1.ComputeReservation{
			ObjectMeta: ctrl.ObjectMeta{
				Name: fmt.Sprintf("commitment-%s", commitmentUUIDShort),
			},
			Spec: v1alpha1.ComputeReservationSpec{
				Kind:      v1alpha1.ComputeReservationSpecKindBareResource,
				ProjectID: commitment.ProjectID,
				DomainID:  commitment.DomainID,
				BareResource: v1alpha1.ComputeReservationSpecBareResource{
					Requests: map[string]resource.Quantity{},
				},
			},
		}
		quantity, err := commitment.ParseResource()
		if err != nil {
			syncLog.Error(err, "failed to convert limes unit", "resource name", commitment.ResourceName)
			continue
		}
		switch commitment.ResourceName {
		case "cores":
			reservation.Spec.BareResource.Requests["cpu"] = quantity
		case "ram":
			reservation.Spec.BareResource.Requests["memory"] = quantity
		default:
			syncLog.Info("unsupported bare resource commitment unit", "resource name", commitment.ResourceName)
			continue
		}
		reservationsByName[reservation.Name] = reservation
	}

	// Create new reservations or update existing ones.
	for _, res := range reservationsByName {
		// Check if the reservation already exists.
		nn := types.NamespacedName{Name: res.Name, Namespace: res.Namespace}
		var existing v1alpha1.ComputeReservation
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
		existing.Spec = res.Spec
		if err := s.Update(ctx, &existing); err != nil {
			syncLog.Error(err, "failed to update reservation", "name", nn.Name)
			return err
		}
		syncLog.Info("updated reservation", "name", nn.Name)
	}

	// Delete reservations that are not in the commitments anymore.
	var existingReservations v1alpha1.ComputeReservationList
	if err := s.List(ctx, &existingReservations); err != nil {
		syncLog.Error(err, "failed to list existing reservations")
		return err
	}
	for _, existing := range existingReservations.Items {
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

// Run a sync loop for reservations.
func (s *Syncer) Run(ctx context.Context) {
	go func() {
		for {
			if err := s.SyncReservations(ctx); err != nil {
				syncLog.Error(err, "failed to sync reservations")
			}
			time.Sleep(jobloop.DefaultJitter(time.Hour))
		}
	}()
}
