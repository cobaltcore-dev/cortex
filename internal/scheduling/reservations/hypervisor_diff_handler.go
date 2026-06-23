// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HypervisorDiffHandler is a typed event handler that calls a callback on HV Update events.
// Create and Delete events are no-ops; a periodic full reconcile is expected to correct any drift.
type HypervisorDiffHandler struct {
	// OnUpdate is called with (ctx, oldHV, newHV) on every Hypervisor Update event.
	OnUpdate func(ctx context.Context, oldHV, newHV *hv1.Hypervisor) error
}

func (h *HypervisorDiffHandler) Create(_ context.Context, _ event.TypedCreateEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *HypervisorDiffHandler) Update(ctx context.Context, e event.TypedUpdateEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if err := h.OnUpdate(ctx, e.ObjectOld, e.ObjectNew); err != nil {
		ctrl.Log.WithName("hypervisor-diff-handler").Error(err, "failed to process HV diff", "hypervisor", e.ObjectNew.Name)
	}
}

func (h *HypervisorDiffHandler) Delete(_ context.Context, _ event.TypedDeleteEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}

func (h *HypervisorDiffHandler) Generic(_ context.Context, _ event.TypedGenericEvent[*hv1.Hypervisor], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}
