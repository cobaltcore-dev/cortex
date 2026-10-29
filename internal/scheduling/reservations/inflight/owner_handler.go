// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"slices"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// requeueInterval is how long Reconcile waits before re-checking an in-flight
// reservation whose owning operation has not yet reached a terminal state.
const requeueInterval = 10 * time.Second

// WatchSpec declares a secondary resource that an OwnerHandler needs the
// in-flight controller to watch, together with how an event on that resource
// maps back to the affected in-flight reservations. The controller merges the
// WatchSpecs of all registered handlers (deduplicated by object type) and wires
// them into its own watch set, so handlers stay slim and don't own any watch
// machinery themselves.
type WatchSpec struct {
	// Object is a zero value of the watched type, e.g. &hv1.Hypervisor{}.
	Object client.Object
	// Predicate optionally filters events before MapFn runs. May be nil.
	Predicate predicate.Predicate
	// MapFn translates an event on Object into the in-flight reservations that
	// should be re-reconciled as a result. It is the standard
	// controller-runtime EnqueueRequestsFromMapFunc shape: the controller's
	// work queue is keyed by Reservation name, but events arrive for the
	// secondary Object, so MapFn is the translation layer from "this secondary
	// object changed" to "these reservations must be reconciled".
	MapFn handler.MapFunc
}

// TerminalResult is the decision an OwnerHandler returns for one in-flight
// reservation.
type TerminalResult struct {
	// Done indicates the owning operation reached a terminal state; the
	// reconciler releases the whole OperationID group.
	Done bool
	// RequeueAfter is how long to wait before re-checking when Done is false.
	RequeueAfter time.Duration
	// Reason is a short, stable machine reason surfaced on the reservation's
	// Ready condition while waiting (Done false). Suitable for condition-based
	// alerting; empty falls back to a generic reason.
	Reason string
	// Message is a human-readable detail surfaced alongside Reason.
	Message string
}

// SubjectReservations returns the in-flight reservations for the given subject
// (VM uuid) that are handled by the same OwnerHandler. The controller binds it
// per reconcile, applying the same ownership selection it uses to dispatch, so a
// handler sees exactly the sibling reservations it owns and never those owned by
// another kind that merely share the subject.
type SubjectReservations func(ctx context.Context, vmID string) ([]v1alpha1.Reservation, error)

// OwnerHandler encapsulates the owner-kind-specific lifecycle logic for an
// in-flight reservation. It is intentionally slim and stateless: it only
// declares which secondary resources must be watched and decides whether a
// given reservation's owning operation is done. The generic reconciler owns
// deletion, OperationID grouping, TTL handling and watch wiring.
//
// To support a new owner kind (e.g. a future Rescheduling CRD), add an
// implementation and register it; the controller merges its watches and
// dispatches to it by owner-reference Kind automatically.
type OwnerHandler interface {
	// Kind is the owner-reference Kind this handler is responsible for, e.g.
	// "VirtualMachine".
	Kind() string
	// WatchObjects declares the secondary resources this handler needs watched
	// to observe terminal signals. May be empty.
	WatchObjects() []WatchSpec
	// TerminalState decides the fate of a single in-flight reservation given
	// the current cluster state. On a transient failure it returns an error so
	// controller-runtime applies its exponential backoff; otherwise it returns
	// a TerminalResult. The siblings lookup yields the reservations for the same
	// subject handled by this handler; a handler that does not need it may
	// ignore the argument.
	TerminalState(ctx context.Context, res *v1alpha1.Reservation, siblings SubjectReservations) (TerminalResult, error)
}

// VirtualMachineHandler ties an in-flight reservation's lifecycle to a Nova
// VM placement operation.
//
// Terminal detection currently reads the Hypervisor CRD's Status.Instances to
// learn where the VM landed (the behaviour the controller had before owner
// handlers were introduced). The exact terminal signal for initial placement
// (Hypervisor landing vs. VirtualMachine conditions vs. a hybrid) is still
// under discussion; keeping it behind this interface lets us swap the signal
// without touching the generic reconciler.
type VirtualMachineHandler struct {
	client.Client
}

// Kind implements OwnerHandler.
func (h *VirtualMachineHandler) Kind() string { return "VirtualMachine" }

// WatchObjects implements OwnerHandler. The handler watches the Hypervisor CRD
// (to observe where a VM landed) and the VirtualMachine CRD (so condition/spec
// changes re-trigger reconciliation, per the ADR 8 secondary-watch design).
func (h *VirtualMachineHandler) WatchObjects() []WatchSpec {
	return []WatchSpec{
		{
			Object:    &hv1.Hypervisor{},
			Predicate: hypervisorInstancesChangedPredicate(),
			MapFn:     h.mapHypervisorToReservations,
		},
		{
			Object: &vmv1alpha1.VirtualMachine{},
			MapFn:  h.mapVMToReservations,
		},
	}
}

// mapHypervisorToReservations enqueues every in-flight reservation targeting the
// changed hypervisor, since its instance list changing may mean a VM we are
// waiting for has now landed.
func (h *VirtualMachineHandler) mapHypervisorToReservations(ctx context.Context, obj client.Object) []reconcile.Request {
	hv, ok := obj.(*hv1.Hypervisor)
	if !ok {
		return nil
	}
	return h.reservationsForTargetHost(ctx, hv.Name)
}

// mapVMToReservations enqueues in-flight reservations for the VM that changed.
// The VirtualMachine resource is named after the Nova instance uuid (== VMID),
// so we look reservations up by that subject.
func (h *VirtualMachineHandler) mapVMToReservations(ctx context.Context, obj client.Object) []reconcile.Request {
	vm, ok := obj.(*vmv1alpha1.VirtualMachine)
	if !ok {
		return nil
	}
	return h.reservationsForVMID(ctx, vm.Name)
}

// reservationsForTargetHost lists in-flight reservations targeting the given
// host via the targetHost index and returns one request per reservation.
func (h *VirtualMachineHandler) reservationsForTargetHost(ctx context.Context, host string) []reconcile.Request {
	log := ctrl.LoggerFrom(ctx)
	reservations := &v1alpha1.ReservationList{}
	if err := h.List(ctx, reservations, client.MatchingFields{idxReservationByTargetHost: host}); err != nil {
		log.Error(err, "Failed to list reservations for hypervisor", "hypervisor", host)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(reservations.Items))
	for _, res := range reservations.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: res.Name}})
	}
	return requests
}

// reservationsForVMID lists in-flight reservations whose subject is the given
// VM uuid via the vmID index and returns one request per reservation.
func (h *VirtualMachineHandler) reservationsForVMID(ctx context.Context, vmID string) []reconcile.Request {
	log := ctrl.LoggerFrom(ctx)
	reservations := &v1alpha1.ReservationList{}
	if err := h.List(ctx, reservations, client.MatchingFields{idxReservationByVMID: vmID}); err != nil {
		log.Error(err, "Failed to list reservations for VM", "vmID", vmID)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(reservations.Items))
	for _, res := range reservations.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: res.Name}})
	}
	return requests
}

// Waiting reasons surfaced on the Ready condition while an operation is still
// in flight (TerminalResult.Reason).
const (
	reasonAwaitingPlacement = "AwaitingPlacement"
	reasonResizing          = "Resizing"
	reasonMigrating         = "Migrating"
)

// LabelInFlightSource marks the source classification of a resize in-flight
// reservation. Resize operations whose source host is not KVM-managed (e.g. a
// VMware instance) carry no source hold on a KVM hypervisor, so the VM-operator
// never writes a Resizing condition for them. The producer sets this label to
// LabelInFlightSourceUnmanaged on the holds of such a resize so the handler can
// release them on landing instead of waiting for a condition that never comes.
// The label lives only on the short-lived in-flight Reservation and disappears
// when the reservation is released; it never touches the VM or Hypervisor CRD.
const (
	LabelInFlightSource          = "reservations.cortex.cloud/inflight-source"
	LabelInFlightSourceUnmanaged = "unmanaged"
)

// Nova instance status values (Status.Nova.Status) relevant to release
// decisions. The vm-operator CRD exposes this as a free-form string mirroring
// Nova, so we compare against literals here.
const (
	novaStatusActive = "ACTIVE"
	novaStatusError  = "ERROR"
)

// TerminalState implements OwnerHandler for Nova VM placement operations. The
// decision has two stages:
//
//  1. Supersession (newest-wins): if a newer operation exists for the same VM,
//     this reservation belongs to a superseded older operation and is released.
//     Operations are ordered by (creationTimestamp, OperationID); the UUIDv7
//     OperationID breaks ties within the timestamp's one-second resolution.
//     This handles the case where a new Nova request arrives for a VM while an
//     earlier operation's reservations are still in flight.
//
//  2. Per-intent terminal condition on the VirtualMachine CRD: the reservation
//     is released once the operation reaches a terminal state for its intent
//     (see terminalForIntent). While the VM CRD or its condition is not yet
//     available, the handler falls back to the hypervisor landing signal.
//
// Nova serializes its own operations per VM (see the controller docs): when
// Cortex receives a Nova scheduling request for a VM, no other Nova operation on
// that VM is in flight, and the holds it creates are owned by that VM's CRD.
// This per-VM bound only covers these VM-owned holds; reservations owned by
// other resources (e.g. a Rescheduling CRD) are handled elsewhere. The VM CRD's
// condition may still carry a *stale* terminal reason from a previous operation
// that reconciliation has not yet overwritten, so a terminal reason is only
// trusted when the condition transitioned at or after this reservation was
// created (see terminalByReason). This prevents a leftover Completed/Failed
// condition from immediately releasing the freshly created holds of the new
// operation.
func (h *VirtualMachineHandler) TerminalState(ctx context.Context, res *v1alpha1.Reservation, siblings SubjectReservations) (TerminalResult, error) {
	log := ctrl.LoggerFrom(ctx)
	spec := res.Spec.InFlightReservation

	superseded, err := h.isSuperseded(ctx, res, siblings)
	if err != nil {
		return TerminalResult{}, err
	}
	if superseded {
		log.V(1).Info("In-flight reservation superseded by a newer operation, releasing",
			"vmID", spec.VMID, "operationID", spec.OperationID)
		return TerminalResult{Done: true}, nil
	}

	// Read the VM CRD (named after the Nova instance uuid) to evaluate the
	// per-intent terminal condition.
	vm := &vmv1alpha1.VirtualMachine{}
	err = h.Get(ctx, client.ObjectKey{Name: spec.VMID}, vm)
	switch {
	case apierrors.IsNotFound(err):
		vm = nil // Fall back to the hypervisor landing signal below.
	case err != nil:
		return TerminalResult{}, err
	}

	if vm != nil {
		if res, ok := h.terminalForIntent(res, vm); ok {
			return res, nil
		}
		// No decision from the VM CRD yet (condition absent). Fall through to
		// the hypervisor landing fallback.
	}

	return h.terminalByHypervisorLanding(ctx, res)
}

// terminalForIntent evaluates the VM-CRD terminal condition for the
// reservation's intent. It returns (result, ok): ok=false means the VM CRD does
// not yet carry a decisive signal and the caller should fall back.
func (h *VirtualMachineHandler) terminalForIntent(res *v1alpha1.Reservation, vm *vmv1alpha1.VirtualMachine) (TerminalResult, bool) {
	switch res.Spec.InFlightReservation.Intent {
	case novaapi.CreateIntent:
		// UC1 initial placement: release once the VM is confirmed placed AND
		// live (host assigned and the domain running / Nova ACTIVE), or on an
		// explicit Nova failure (Nova ERROR). While the VM is still building,
		// keep blocking all candidates.
		//
		// Scheduled=False is intentionally NOT used as a release trigger: it is
		// not written on failure today, and it reflects the latest scheduling
		// result for the VM rather than this operation, so it cannot be
		// correlated to an OperationID.
		placed := vm.Status.Host != nil && vm.Status.Host.Name != ""
		running := meta.IsStatusConditionTrue(vm.Status.Conditions, vmv1alpha1.ConditionTypeRunning)
		novaActive := vm.Status.Nova != nil && vm.Status.Nova.Status == novaStatusActive
		if placed && (running || novaActive) {
			return TerminalResult{Done: true}, true
		}
		novaError := vm.Status.Nova != nil && vm.Status.Nova.Status == novaStatusError
		if novaError {
			return TerminalResult{Done: true}, true
		}
		// We have at least some VM-CRD signal but it is not yet decisive; keep
		// waiting rather than falling back to the host-landing heuristic.
		if placed || meta.FindStatusCondition(vm.Status.Conditions, vmv1alpha1.ConditionTypeScheduled) != nil || vm.Status.Nova != nil {
			return TerminalResult{RequeueAfter: requeueInterval, Reason: reasonAwaitingPlacement,
				Message: "VM not yet confirmed running on a host"}, true
		}
		return TerminalResult{}, false

	case novaapi.ResizeIntent:
		// UC2 resize: an unmanaged-source resize (e.g. VMware → KVM) has no KVM
		// source hold, so the vm-operator never writes a Resizing condition for
		// it. Release such a group on landing plus alive, and never on failure
		// (the TTL backstops a failed resize). A KVM-source resize keeps the
		// condition-based release below.
		if res.Labels[LabelInFlightSource] == LabelInFlightSourceUnmanaged {
			placed := vm.Status.Host != nil && vm.Status.Host.Name != ""
			running := meta.IsStatusConditionTrue(vm.Status.Conditions, vmv1alpha1.ConditionTypeRunning)
			novaActive := vm.Status.Nova != nil && vm.Status.Nova.Status == novaStatusActive
			if placed && (running || novaActive) {
				return TerminalResult{Done: true}, true
			}
			return TerminalResult{RequeueAfter: requeueInterval, Reason: reasonResizing,
				Message: "unmanaged-source resize not yet confirmed running on target"}, true
		}
		// KVM-source resize: release when the resize is no longer in progress.
		return terminalByReason(res.CreationTimestamp, vm.Status.Conditions, vmv1alpha1.ConditionTypeResizing,
			reasonResizing, "resize in progress",
			vmv1alpha1.ConditionReasonResizingCompleted,
			vmv1alpha1.ConditionReasonResizingReverted,
			vmv1alpha1.ConditionReasonResizingFailed)

	case novaapi.LiveMigrationIntent:
		// UC3 live migration: release both the forward (target) and rollback
		// (source) reservations once the migration settles, including an abort.
		return terminalByReason(res.CreationTimestamp, vm.Status.Conditions, vmv1alpha1.ConditionTypeMigrating,
			reasonMigrating, "live migration in progress",
			vmv1alpha1.ConditionReasonMigratingCompleted,
			vmv1alpha1.ConditionReasonMigratingRolledBack,
			vmv1alpha1.ConditionReasonMigratingAborting,
			vmv1alpha1.ConditionReasonMigratingFailed)

	default:
		// Other intents (rebuild, evacuate, ...) have no dedicated condition
		// yet; use the hypervisor landing fallback.
		return TerminalResult{}, false
	}
}

// terminalByReason returns (result, ok) for a condition whose terminal reasons
// are listed. ok=false when the condition is absent (caller should fall back).
//
// A terminal reason is only trusted when it is *fresh*: the condition's
// LastTransitionTime must be at or after createdAt (the reservation's creation
// time). Nova serializes its own operations per VM, so when this VM-owned
// reservation was created no other Nova operation on the VM was in flight; a
// terminal condition that transitioned *before* this reservation existed
// therefore describes a previous operation and must not release the freshly
// created holds. In that stale case we keep waiting (the vm-operator will
// transition the condition for the current operation), with the TTL backstop
// guarding against a signal that never comes.
//
// When Done is false the result carries the waiting reason/message.
func terminalByReason(createdAt metav1.Time, conditions []metav1.Condition, condType, waitReason, waitMessage string, terminalReasons ...string) (TerminalResult, bool) {
	cond := meta.FindStatusCondition(conditions, condType)
	if cond == nil {
		return TerminalResult{}, false
	}
	if slices.Contains(terminalReasons, cond.Reason) && !conditionIsStale(cond, createdAt) {
		return TerminalResult{Done: true}, true
	}
	return TerminalResult{RequeueAfter: requeueInterval, Reason: waitReason, Message: waitMessage}, true
}

// conditionIsStale reports whether cond transitioned before the reservation was
// created, i.e. the condition reflects a previous operation rather than the one
// this reservation belongs to. A zero LastTransitionTime is treated as stale:
// without a trustworthy transition time we cannot attribute the terminal signal
// to the current operation, so we conservatively wait for the TTL backstop
// rather than risk releasing holds early.
func conditionIsStale(cond *metav1.Condition, createdAt metav1.Time) bool {
	if cond.LastTransitionTime.IsZero() {
		return true
	}
	return cond.LastTransitionTime.Before(&createdAt)
}

// terminalByHypervisorLanding is the fallback terminal signal used when the VM
// CRD is absent or does not yet carry a per-intent condition. It concludes the
// operation is terminal ONLY for initial placement (CreateIntent): a VM observed
// on any hypervisor means the create landed, so the whole operation group is
// released, cleaning up all (including stale, non-winning) candidates.
//
// For every other intent (resize, live migration, rebuild, evacuate, or an
// empty/unknown intent) hypervisor observation is not a completion signal: the
// VM already occupies a host throughout a resize, and a live migration holds the
// source and target while the VM still sits on the source. Without a decisive
// VM-CRD condition there is no trustworthy terminal signal for these, so the
// handler keeps waiting and relies on the TTL backstop in the reconcile loop.
func (h *VirtualMachineHandler) terminalByHypervisorLanding(ctx context.Context, res *v1alpha1.Reservation) (TerminalResult, error) {
	log := ctrl.LoggerFrom(ctx)
	spec := res.Spec.InFlightReservation

	_, found, err := h.findVMHost(ctx, spec.VMID)
	if err != nil {
		return TerminalResult{}, err
	}
	if !found {
		log.V(1).Info("Instance has not spawned on any hypervisor yet, requeuing", "vmID", spec.VMID)
		return TerminalResult{RequeueAfter: requeueInterval, Reason: reasonAwaitingPlacement,
			Message: "instance not yet observed on any hypervisor"}, nil
	}

	// Only initial placement is terminal on hypervisor landing. Any other intent
	// waits for its VM-CRD condition or the TTL backstop, never for landing.
	if spec.Intent != novaapi.CreateIntent {
		log.V(1).Info("Instance observed on a hypervisor but intent is not initial placement, waiting for terminal signal or TTL",
			"vmID", spec.VMID, "intent", spec.Intent)
		return TerminalResult{RequeueAfter: requeueInterval, Reason: reasonAwaitingPlacement,
			Message: "instance observed on a hypervisor; awaiting terminal condition or TTL"}, nil
	}

	return TerminalResult{Done: true}, nil
}

// isSuperseded reports whether this reservation belongs to an operation older
// than the newest operation for the same VM. Operations are ordered by
// (creationTimestamp, OperationID). Reservations without an OperationID cannot
// be ordered and are never treated as superseded here.
//
// The siblings lookup already restricts the candidates to reservations handled
// by this VirtualMachine handler, so ownership stays isolated: a reservation
// owned by another kind (e.g. Rescheduling) that happens to carry the same vmID
// cannot supersede, or be superseded by, a VM operation.
func (h *VirtualMachineHandler) isSuperseded(ctx context.Context, res *v1alpha1.Reservation, siblings SubjectReservations) (bool, error) {
	spec := res.Spec.InFlightReservation
	if spec.OperationID == "" {
		return false, nil
	}
	others, err := siblings(ctx, spec.VMID)
	if err != nil {
		return false, err
	}
	for i := range others {
		other := &others[i]
		otherSpec := other.Spec.InFlightReservation
		if otherSpec == nil || otherSpec.OperationID == "" || otherSpec.OperationID == spec.OperationID {
			continue
		}
		if operationLess(res, other) {
			return true, nil
		}
	}
	return false, nil
}

// operationLess reports whether reservation a's operation is older than b's,
// ordered by creationTimestamp then OperationID (UUIDv7 lexical tiebreak).
func operationLess(a, b *v1alpha1.Reservation) bool {
	at, bt := a.CreationTimestamp.Time, b.CreationTimestamp.Time
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.Spec.InFlightReservation.OperationID < b.Spec.InFlightReservation.OperationID
}

// findVMHost returns the name of the hypervisor the VM currently runs on, if
// any, by scanning the Hypervisor CRDs' instance lists.
func (h *VirtualMachineHandler) findVMHost(ctx context.Context, vmID string) (host string, found bool, err error) {
	hvs := &hv1.HypervisorList{}
	if err := h.List(ctx, hvs); err != nil {
		return "", false, err
	}
	for _, hv := range hvs.Items {
		for _, instance := range hv.Status.Instances {
			if instance.ID == vmID {
				return hv.Name, true, nil
			}
		}
	}
	return "", false, nil
}
