// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inflight

import (
	"context"
	"errors"
	"testing"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// stubVMClient is a test double for vmClient. If err is non-nil, GetCurrentVMSize
// returns it; otherwise it returns size.
type stubVMClient struct {
	size map[hv1.ResourceName]resource.Quantity
	err  error
}

func (s *stubVMClient) StartWithKubernetesSecrets(ctx context.Context, client client.Client) error {
	return nil
}

func (s *stubVMClient) GetCurrentVMSize(ctx context.Context, vmID string) (map[hv1.ResourceName]resource.Quantity, error) {
	return s.size, s.err
}

// newTestScheme returns a runtime.Scheme with all required types registered.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add hypervisor scheme: %v", err)
	}
	return s
}

// newTestClient builds a fake client with the indices the controller relies on.
func newTestClient(scheme *runtime.Scheme, objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		WithIndex(&v1alpha1.Reservation{}, idxReservationByTargetHost, idxReservationByTargetHostFn).
		Build()
}

// newInFlightReservation builds an in-flight reservation with the given name and VM ID.
func newInFlightReservation(name, vmID, targetHost string) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeInFlight,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			TargetHost:       targetHost,
			InFlightReservation: &v1alpha1.InFlightReservationSpec{
				VMID: vmID,
			},
		},
	}
}

// newHypervisor builds a Hypervisor with the given name and instance IDs.
func newHypervisor(name string, instanceIDs ...string) *hv1.Hypervisor {
	hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: name}}
	for _, id := range instanceIDs {
		hv.Status.Instances = append(hv.Status.Instances, hv1.Instance{ID: id})
	}
	return hv
}

// assertReadyCondition fetches the named reservation and asserts the Ready condition's
// status and reason. Fails fast if the condition is missing.
func assertReadyCondition(t *testing.T, k8sClient client.Client, name string, wantStatus metav1.ConditionStatus, wantReason string) {
	t.Helper()
	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &got); err != nil {
		t.Fatalf("failed to get reservation %q: %v", name, err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ReservationConditionReady)
	if cond == nil {
		t.Fatalf("Ready condition was not set on %q", name)
		return
	}
	if cond.Status != wantStatus {
		t.Errorf("%s: Ready status = %q, want %q", name, cond.Status, wantStatus)
	}
	if cond.Reason != wantReason {
		t.Errorf("%s: Ready reason = %q, want %q", name, cond.Reason, wantReason)
	}
}

func TestReconcile_NotFoundIsIgnored(t *testing.T) {
	scheme := newTestScheme(t)
	k8sClient := newTestClient(scheme)
	c := &Controller{Client: k8sClient}

	res, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "missing"},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error for missing object: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}

func TestReconcile_UnexpectedTypeSetsConditionFalse(t *testing.T) {
	scheme := newTestScheme(t)
	wrong := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "wrong-type"},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeFailover,
		},
	}
	k8sClient := newTestClient(scheme, wrong)
	c := &Controller{Client: k8sClient}

	if _, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "wrong-type"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	assertReadyCondition(t, k8sClient, "wrong-type", metav1.ConditionFalse, "UnexpectedType")
}

func TestReconcile_MissingSpecSetsConditionFalse(t *testing.T) {
	scheme := newTestScheme(t)
	noSpec := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "no-spec"},
		Spec: v1alpha1.ReservationSpec{
			Type: v1alpha1.ReservationTypeInFlight,
			// InFlightReservation deliberately nil.
		},
	}
	k8sClient := newTestClient(scheme, noSpec)
	c := &Controller{Client: k8sClient}

	if _, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "no-spec"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	assertReadyCondition(t, k8sClient, "no-spec", metav1.ConditionFalse, "MissingSpec")
}

func TestReconcile_InstanceNotSpawnedRequeues(t *testing.T) {
	scheme := newTestScheme(t)
	res := newInFlightReservation("res-1", "vm-uuid-1", "host-1")
	hv := newHypervisor("host-1", "other-vm-uuid")
	k8sClient := newTestClient(scheme, res, hv)
	c := &Controller{Client: k8sClient}

	result, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 10*time.Second {
		t.Errorf("RequeueAfter = %v, want 10s", result.RequeueAfter)
	}

	// Reservation still exists.
	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err != nil {
		t.Fatalf("reservation was unexpectedly deleted: %v", err)
	}
	assertReadyCondition(t, k8sClient, "res-1", metav1.ConditionUnknown, "InstanceNotFound")
}

func TestReconcile_InstanceOnDifferentHostAwaitsDeletion(t *testing.T) {
	// Instance landed on a *different* host than the target. The reservation
	// is now stale but the hypervisor operator (not this controller) removes
	// it, so Reconcile should be a no-op that leaves the reservation intact.
	scheme := newTestScheme(t)
	res := newInFlightReservation("res-1", "vm-uuid-1", "host-1")
	hv1Obj := newHypervisor("host-1")
	hv2Obj := newHypervisor("host-2", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv1Obj, hv2Obj)
	c := &Controller{Client: k8sClient}

	result, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}

	// Reservation still exists — it's waiting for the hypervisor operator.
	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err != nil {
		t.Fatalf("expected reservation to still exist, got error: %v", err)
	}
}

func TestReconcile_InstanceOnTargetHostDeletesReservation(t *testing.T) {
	scheme := newTestScheme(t)
	res := newInFlightReservation("res-1", "vm-uuid-1", "host-1")
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv)
	c := &Controller{Client: k8sClient}

	if _, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err == nil {
		t.Fatal("expected reservation to be deleted, but Get succeeded")
	}
}

func TestReconcile_InstanceOnTargetHostBatchDeletesReservationsForSameVM(t *testing.T) {
	// Multiple in-flight reservations pointing at the same target host for the
	// same VM (e.g. left over from earlier scheduling attempts). Once the
	// instance is confirmed on that host, all of them must be cleaned up in a
	// single reconcile.
	scheme := newTestScheme(t)
	res1 := newInFlightReservation("res-1", "vm-uuid-1", "host-1")
	res2 := newInFlightReservation("res-2", "vm-uuid-1", "host-1")
	// A reservation for a different VM on the same host must be left alone.
	other := newInFlightReservation("res-other", "vm-uuid-2", "host-1")
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res1, res2, other, hv)
	c := &Controller{Client: k8sClient}

	if _, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err == nil {
		t.Fatal("expected res-1 to be deleted, but Get succeeded")
	}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-2"}, &got); err == nil {
		t.Fatal("expected res-2 to be deleted, but Get succeeded")
	}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-other"}, &got); err != nil {
		t.Fatalf("expected res-other to survive, got error: %v", err)
	}
}

// newResizeReservation builds an in-flight reservation with the given intent
// and resource requirements. Used to exercise the resize/rebuild size-check
// branch of Reconcile.
//
//nolint:unparam
func newResizeReservation(name, vmID, targetHost string, intent v1alpha1.SchedulingIntent, resources map[hv1.ResourceName]resource.Quantity) *v1alpha1.Reservation {
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReservationSpec{
			Type:             v1alpha1.ReservationTypeInFlight,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			TargetHost:       targetHost,
			Resources:        resources,
			InFlightReservation: &v1alpha1.InFlightReservationSpec{
				VMID:   vmID,
				Intent: intent,
			},
		},
	}
}

func TestReconcile_ResizeSizeMismatchRequeues(t *testing.T) {
	// For a resize/rebuild reservation, the instance being present on the
	// target host isn't sufficient — the VM must have grown/shrunk to the
	// reserved size. Until it has, the controller must requeue and set the
	// Ready condition to VMSizeMismatch.
	scheme := newTestScheme(t)
	reserved := map[hv1.ResourceName]resource.Quantity{
		"cpu":    resource.MustParse("4"),
		"memory": resource.MustParse("8Gi"),
	}
	current := map[hv1.ResourceName]resource.Quantity{
		"cpu":    resource.MustParse("2"),
		"memory": resource.MustParse("4Gi"),
	}
	res := newResizeReservation("res-1", "vm-uuid-1", "host-1", novaapi.ResizeIntent, reserved)
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv)
	c := &Controller{Client: k8sClient, VMClient: &stubVMClient{size: current}}

	result, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 10*time.Second {
		t.Errorf("RequeueAfter = %v, want 10s", result.RequeueAfter)
	}

	// Reservation must still exist and carry the VMSizeMismatch condition.
	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err != nil {
		t.Fatalf("reservation was unexpectedly deleted: %v", err)
	}
	assertReadyCondition(t, k8sClient, "res-1", metav1.ConditionUnknown, "VMSizeMismatch")
}

func TestReconcile_ResizeSizeMatchesDeletesReservation(t *testing.T) {
	// Once the VM has been resized to the reserved dimensions, the resize
	// reservation can be freed like a normal in-flight reservation.
	scheme := newTestScheme(t)
	reserved := map[hv1.ResourceName]resource.Quantity{
		"cpu":    resource.MustParse("4"),
		"memory": resource.MustParse("8Gi"),
	}
	res := newResizeReservation("res-1", "vm-uuid-1", "host-1", novaapi.ResizeIntent, reserved)
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv)
	c := &Controller{Client: k8sClient, VMClient: &stubVMClient{size: reserved}}

	if _, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err == nil {
		t.Fatal("expected reservation to be deleted, but Get succeeded")
	}
}

func TestReconcile_RebuildSizeMismatchRequeues(t *testing.T) {
	// Same branch as resize but exercised via the rebuild intent to make sure
	// both intents actually trip the vmClient check.
	scheme := newTestScheme(t)
	reserved := map[hv1.ResourceName]resource.Quantity{
		"cpu": resource.MustParse("4"),
	}
	current := map[hv1.ResourceName]resource.Quantity{
		"cpu": resource.MustParse("2"),
	}
	res := newResizeReservation("res-1", "vm-uuid-1", "host-1", novaapi.RebuildIntent, reserved)
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv)
	c := &Controller{Client: k8sClient, VMClient: &stubVMClient{size: current}}

	result, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 10*time.Second {
		t.Errorf("RequeueAfter = %v, want 10s", result.RequeueAfter)
	}
	assertReadyCondition(t, k8sClient, "res-1", metav1.ConditionUnknown, "VMSizeMismatch")
}

func TestReconcile_ResizeVMClientErrorReturnsError(t *testing.T) {
	// If the source of truth for VMs can't be reached we must surface the
	// error so controller-runtime backs off — silently deleting the
	// reservation here would be a resource-accounting bug.
	scheme := newTestScheme(t)
	reserved := map[hv1.ResourceName]resource.Quantity{
		"cpu": resource.MustParse("4"),
	}
	res := newResizeReservation("res-1", "vm-uuid-1", "host-1", novaapi.ResizeIntent, reserved)
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv)
	vmErr := errors.New("vm client boom")
	c := &Controller{Client: k8sClient, VMClient: &stubVMClient{err: vmErr}}

	_, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	})
	if !errors.Is(err, vmErr) {
		t.Fatalf("Reconcile err = %v, want %v", err, vmErr)
	}

	// Reservation must survive the error so it can be retried.
	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err != nil {
		t.Fatalf("reservation was unexpectedly deleted: %v", err)
	}
}

func TestReconcile_NonResizeIntentSkipsVMClient(t *testing.T) {
	// For non-resize/rebuild intents the vmClient must not be consulted at
	// all — we leave it nil to make an accidental call panic loudly.
	scheme := newTestScheme(t)
	res := newResizeReservation("res-1", "vm-uuid-1", "host-1", novaapi.LiveMigrationIntent,
		map[hv1.ResourceName]resource.Quantity{"cpu": resource.MustParse("4")})
	hv := newHypervisor("host-1", "vm-uuid-1")
	k8sClient := newTestClient(scheme, res, hv)
	c := &Controller{Client: k8sClient, VMClient: nil}

	if _, err := c.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "res-1"},
	}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var got v1alpha1.Reservation
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "res-1"}, &got); err == nil {
		t.Fatal("expected reservation to be deleted, but Get succeeded")
	}
}

func TestIdxReservationByTargetHostFn(t *testing.T) {
	tests := []struct {
		name string
		obj  client.Object
		want []string
	}{
		{
			name: "wrong type",
			obj:  &hv1.Hypervisor{},
			want: nil,
		},
		{
			name: "empty target host",
			obj: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{TargetHost: ""},
			},
			want: nil,
		},
		{
			name: "target host set",
			obj: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{TargetHost: "host-1"},
			},
			want: []string{"host-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idxReservationByTargetHostFn(tt.obj)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPredicateReservations(t *testing.T) {
	c := &Controller{}
	pred := c.predicateReservations()

	tests := []struct {
		name string
		obj  client.Object
		want bool
	}{
		{
			name: "wrong type",
			obj:  &hv1.Hypervisor{},
			want: false,
		},
		{
			name: "wrong reservation type",
			obj: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:             v1alpha1.ReservationTypeFailover,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
			},
			want: false,
		},
		{
			name: "wrong scheduling domain",
			obj: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:             v1alpha1.ReservationTypeInFlight,
					SchedulingDomain: v1alpha1.SchedulingDomainPods,
				},
			},
			want: false,
		},
		{
			name: "in-flight nova reservation",
			obj: &v1alpha1.Reservation{
				Spec: v1alpha1.ReservationSpec{
					Type:             v1alpha1.ReservationTypeInFlight,
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pred.Create(event.CreateEvent{Object: tt.obj}); got != tt.want {
				t.Errorf("Create = %v, want %v", got, tt.want)
			}
			if got := pred.Update(event.UpdateEvent{ObjectNew: tt.obj, ObjectOld: tt.obj}); got != tt.want {
				t.Errorf("Update = %v, want %v", got, tt.want)
			}
			if got := pred.Delete(event.DeleteEvent{Object: tt.obj}); got != tt.want {
				t.Errorf("Delete = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPredicateHypervisors(t *testing.T) {
	c := &Controller{}
	pred := c.predicateHypervisors()

	if got := pred.Create(event.CreateEvent{Object: &hv1.Hypervisor{}}); !got {
		t.Errorf("Create(Hypervisor) = false, want true")
	}
	if got := pred.Create(event.CreateEvent{Object: &v1alpha1.Reservation{}}); got {
		t.Errorf("Create(Reservation) = true, want false")
	}

	// Update events must only pass when Status.Instances actually changes,
	// so unrelated status churn on the Hypervisor doesn't trigger a list +
	// enqueue of all reservations targeting the host.
	same := newHypervisor("host-1", "vm-1")
	if got := pred.Update(event.UpdateEvent{ObjectOld: same, ObjectNew: same.DeepCopy()}); got {
		t.Errorf("Update(no instance change) = true, want false")
	}
	changed := same.DeepCopy()
	changed.Status.Instances = append(changed.Status.Instances, hv1.Instance{ID: "vm-2"})
	if got := pred.Update(event.UpdateEvent{ObjectOld: same, ObjectNew: changed}); !got {
		t.Errorf("Update(instance added) = false, want true")
	}
}

// mockWorkQueue captures items added during handler invocations.
type mockWorkQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	items []reconcile.Request
}

func (m *mockWorkQueue) Add(item reconcile.Request) {
	m.items = append(m.items, item)
}

func TestHandleReservations(t *testing.T) {
	c := &Controller{}
	h := c.handleReservations()
	res := &v1alpha1.Reservation{ObjectMeta: metav1.ObjectMeta{Name: "res-1"}}
	ctx := context.Background()

	t.Run("Create", func(t *testing.T) {
		q := &mockWorkQueue{}
		h.Create(ctx, event.CreateEvent{Object: res}, q)
		if len(q.items) != 1 || q.items[0].Name != "res-1" {
			t.Errorf("queue = %+v, want one entry for res-1", q.items)
		}
	})
	t.Run("Update", func(t *testing.T) {
		q := &mockWorkQueue{}
		h.Update(ctx, event.UpdateEvent{ObjectOld: res, ObjectNew: res}, q)
		if len(q.items) != 1 || q.items[0].Name != "res-1" {
			t.Errorf("queue = %+v, want one entry for res-1", q.items)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		q := &mockWorkQueue{}
		h.Delete(ctx, event.DeleteEvent{Object: res}, q)
		if len(q.items) != 1 || q.items[0].Name != "res-1" {
			t.Errorf("queue = %+v, want one entry for res-1", q.items)
		}
	})
}

func TestHandleHypervisors_EnqueuesMatchingReservations(t *testing.T) {
	scheme := newTestScheme(t)
	matching := newInFlightReservation("res-1", "vm-1", "host-1")
	other := newInFlightReservation("res-2", "vm-2", "host-2")
	k8sClient := newTestClient(scheme, matching, other)
	c := &Controller{Client: k8sClient}
	h := c.handleHypervisors()

	hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
	ctx := context.Background()

	t.Run("Create", func(t *testing.T) {
		q := &mockWorkQueue{}
		h.Create(ctx, event.CreateEvent{Object: hv}, q)
		if len(q.items) != 1 || q.items[0].Name != "res-1" {
			t.Errorf("queue = %+v, want only res-1", q.items)
		}
	})
	t.Run("Update", func(t *testing.T) {
		q := &mockWorkQueue{}
		h.Update(ctx, event.UpdateEvent{ObjectOld: hv, ObjectNew: hv}, q)
		if len(q.items) != 1 || q.items[0].Name != "res-1" {
			t.Errorf("queue = %+v, want only res-1", q.items)
		}
	})
	t.Run("Delete", func(t *testing.T) {
		q := &mockWorkQueue{}
		h.Delete(ctx, event.DeleteEvent{Object: hv}, q)
		if len(q.items) != 1 || q.items[0].Name != "res-1" {
			t.Errorf("queue = %+v, want only res-1", q.items)
		}
	})
}

func TestHandleHypervisors_NoMatchingReservations(t *testing.T) {
	scheme := newTestScheme(t)
	other := newInFlightReservation("res-2", "vm-2", "host-2")
	k8sClient := newTestClient(scheme, other)
	c := &Controller{Client: k8sClient}
	h := c.handleHypervisors()

	hv := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}
	q := &mockWorkQueue{}
	h.Create(context.Background(), event.CreateEvent{Object: hv}, q)
	if len(q.items) != 0 {
		t.Errorf("queue = %+v, want empty", q.items)
	}
}

func TestSetupWithManager_RejectsNonMulticlusterClient(t *testing.T) {
	scheme := newTestScheme(t)
	c := &Controller{Client: newTestClient(scheme), VMClient: &stubVMClient{}}
	err := c.SetupWithManager(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for non-multicluster client, got nil")
	}
}
