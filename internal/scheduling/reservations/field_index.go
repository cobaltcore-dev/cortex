// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IdxReservationByHost is the field index key for looking up Reservations by host.
// Both Spec.TargetHost and Status.Host are indexed so reservations in transit
// (TargetHost != Status.Host) are found via either field.
// All reservation types are included.
const IdxReservationByHost = "reservations.host"

var onceIndexReservationByHost sync.Once

// IndexReservationByHost registers the shared host index on the multicluster client.
// Safe to call multiple times — registration happens only once.
func IndexReservationByHost(ctx context.Context, mcl *multicluster.Client) (err error) {
	onceIndexReservationByHost.Do(func() {
		err = mcl.IndexField(ctx, &v1alpha1.Reservation{}, &v1alpha1.ReservationList{},
			IdxReservationByHost, ReservationHosts)
	})
	return err
}

// ReservationHosts is the IdxReservationByHost extractor: the distinct hosts a
// Reservation targets (Spec.TargetHost) or sits on (Status.Host).
func ReservationHosts(obj client.Object) []string {
	res, ok := obj.(*v1alpha1.Reservation)
	if !ok {
		return nil
	}
	hosts := make([]string, 0, 2)
	if res.Spec.TargetHost != "" {
		hosts = append(hosts, res.Spec.TargetHost)
	}
	if res.Status.Host != "" && res.Status.Host != res.Spec.TargetHost {
		hosts = append(hosts, res.Status.Host)
	}
	return hosts
}
