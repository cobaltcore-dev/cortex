// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"errors"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
		log := logf.FromContext(ctx)
		err = mcl.IndexField(ctx,
			&v1alpha1.Reservation{},
			&v1alpha1.ReservationList{},
			IdxReservationByHost,
			func(obj client.Object) []string {
				res, ok := obj.(*v1alpha1.Reservation)
				if !ok {
					log.Error(errors.New("unexpected type"), "expected Reservation", "object", obj)
					return nil
				}
				hosts := make(map[string]struct{})
				if res.Spec.TargetHost != "" {
					hosts[res.Spec.TargetHost] = struct{}{}
				}
				if res.Status.Host != "" {
					hosts[res.Status.Host] = struct{}{}
				}
				result := make([]string, 0, len(hosts))
				for h := range hosts {
					result = append(result, h)
				}
				return result
			},
		)
	})
	return err
}
