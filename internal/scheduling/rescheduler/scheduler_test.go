// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheduler(t *testing.T, objs ...client.Object) *Scheduler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	if err := vmv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add vm scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.ReschedulingRequest{}).
		Build()
	return &Scheduler{
		Client:     cl,
		Queue:      NewFIFOQueue(),
		Placement:  StubPlacementBackend{},
		placements: make(chan placementCall),
	}
}

func request(name string, state v1alpha1.ReschedulingRequestState) *v1alpha1.ReschedulingRequest {
	rr := drainRequest(name, false, drainHost{"src-host", []string{"vm-1"}})
	rr.Status.State = state
	return rr
}

// drainHost names a source host and the vm entities to move off it.
type drainHost struct {
	host string
	vms  []string
}

// drainRequest builds a drain ReschedulingRequest for the given hosts/entities.
func drainRequest(name string, reserve bool, hosts ...drainHost) *v1alpha1.ReschedulingRequest {
	targets := make([]v1alpha1.DrainTargetHost, 0, len(hosts))
	for _, h := range hosts {
		entities := make([]v1alpha1.DrainTargetEntity, 0, len(h.vms))
		for _, vm := range h.vms {
			entities = append(entities, v1alpha1.DrainTargetEntity{Kind: v1alpha1.DrainEntityKindVM, Ref: vm})
		}
		targets = append(targets, v1alpha1.DrainTargetHost{Host: h.host, Entities: entities})
	}
	return &v1alpha1.ReschedulingRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReschedulingRequestSpec{
			Action: v1alpha1.ReschedulingActionDrain,
			Drain: &v1alpha1.DrainActionSpec{
				Reserve: reserve,
				Target:  v1alpha1.DrainTarget{Hosts: targets},
			},
		},
	}
}

func stateOf(t *testing.T, s *Scheduler, name string) v1alpha1.ReschedulingRequestState {
	t.Helper()
	got := &v1alpha1.ReschedulingRequest{}
	if err := s.Get(context.Background(), client.ObjectKey{Name: name}, got); err != nil {
		t.Fatalf("get %s failed: %v", name, err)
	}
	return got.Status.State
}

// reqSpec is a request name together with a state.
type reqSpec struct {
	name  string
	state v1alpha1.ReschedulingRequestState
}

// createRequest creates a request after startup, as a selector would.
func createRequest(t *testing.T, s *Scheduler, spec reqSpec) {
	t.Helper()
	ctx := context.Background()
	rr := request(spec.name, "")
	if err := s.Create(ctx, rr); err != nil {
		t.Fatalf("create %s failed: %v", spec.name, err)
	}
	// Create ignores the status stanza; persist the requested start state.
	if spec.state != "" {
		if err := s.Get(ctx, client.ObjectKey{Name: spec.name}, rr); err != nil {
			t.Fatalf("get %s failed: %v", spec.name, err)
		}
		rr.Status.State = spec.state
		if err := s.Status().Update(ctx, rr); err != nil {
			t.Fatalf("status update %s failed: %v", spec.name, err)
		}
	}
}

func wantMap(specs []reqSpec) map[string]v1alpha1.ReschedulingRequestState {
	m := make(map[string]v1alpha1.ReschedulingRequestState, len(specs))
	for _, r := range specs {
		m[r.name] = r.state
	}
	return m
}

// statesOf returns the current state of every request in the cluster.
func statesOf(t *testing.T, s *Scheduler) map[string]v1alpha1.ReschedulingRequestState {
	t.Helper()
	var list v1alpha1.ReschedulingRequestList
	if err := s.List(context.Background(), &list); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	m := make(map[string]v1alpha1.ReschedulingRequestState, len(list.Items))
	for i := range list.Items {
		m[list.Items[i].Name] = currentState(&list.Items[i])
	}
	return m
}

// reconcilePass delivers one Reconcile to every request, simulating the watch.
// It fails if any Reconcile errors or asks for a requeue: our handlers must
// never requeue, so a non-zero result signals a potential permanent reconcile
// cycle.
func reconcilePass(t *testing.T, s *Scheduler) {
	t.Helper()
	ctx := context.Background()
	var list v1alpha1.ReschedulingRequestList
	if err := s.List(ctx, &list); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for i := range list.Items {
		key := client.ObjectKey{Name: list.Items[i].Name}
		res, err := s.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatalf("reconcile %s failed: %v", key.Name, err)
		}
		if res != (ctrl.Result{}) {
			t.Fatalf("reconcile %s requested a requeue (%+v); indicates a possible permanent reconcile cycle", key.Name, res)
		}
	}
}

// driveToWant delivers reconcile passes (the producer side) while the scheduler
// worker loop runs in the background (the consumer side), until every request
// reaches the desired state. It is iteration-bounded: not converging within the
// budget fails the test, which catches non-convergence / permanent cycles.
func driveToWant(t *testing.T, s *Scheduler, want map[string]v1alpha1.ReschedulingRequestState) {
	t.Helper()
	const maxIterations = 100
	for range maxIterations {
		reconcilePass(t, s)
		time.Sleep(time.Millisecond) // let the background worker make progress
		if reflect.DeepEqual(statesOf(t, s), want) {
			return
		}
	}
	t.Fatalf("requests did not reach desired states within %d iterations: got %v, want %v",
		maxIterations, statesOf(t, s), want)
}

// assertStable keeps reconciling for a few more iterations and asserts the
// states no longer change (and no requeue is requested), guarding against a
// controller that keeps re-reconciling after settling.
func assertStable(t *testing.T, s *Scheduler, want map[string]v1alpha1.ReschedulingRequestState) {
	t.Helper()
	const iterations = 10
	for range iterations {
		reconcilePass(t, s)
		time.Sleep(time.Millisecond)
		if got := statesOf(t, s); !reflect.DeepEqual(got, want) {
			t.Fatalf("state changed after settling: got %v, want %v", got, want)
		}
	}
}

func TestScheduler(t *testing.T) {
	tests := []struct {
		name    string
		startup []reqSpec // requests present before startup
		added   []reqSpec // requests created after startup
		want    []reqSpec // expected end state per request
	}{
		{
			name:  "new request walks to Succeeded",
			added: []reqSpec{{"new", v1alpha1.ReschedulingRequestStatePending}},
			want:  []reqSpec{{"new", v1alpha1.ReschedulingRequestStateSucceeded}},
		},
		{
			name:    "terminal request is left untouched",
			startup: []reqSpec{{"done", v1alpha1.ReschedulingRequestStateSucceeded}},
			want:    []reqSpec{{"done", v1alpha1.ReschedulingRequestStateSucceeded}},
		},
		{
			name:    "failed request stays failed",
			startup: []reqSpec{{"failed", v1alpha1.ReschedulingRequestStateFailed}},
			want:    []reqSpec{{"failed", v1alpha1.ReschedulingRequestStateFailed}},
		},
		{
			name: "restart: acknowledged scheduled, processing failed, succeeded kept",
			startup: []reqSpec{
				{"ack", v1alpha1.ReschedulingRequestStateAcknowledged},
				{"proc", v1alpha1.ReschedulingRequestStateProcessing},
				{"ok", v1alpha1.ReschedulingRequestStateSucceeded},
			},
			want: []reqSpec{
				{"ack", v1alpha1.ReschedulingRequestStateSucceeded},
				{"proc", v1alpha1.ReschedulingRequestStateFailed},
				{"ok", v1alpha1.ReschedulingRequestStateSucceeded},
			},
		},
		{
			name:    "mixed startup and added requests all complete",
			startup: []reqSpec{{"ack", v1alpha1.ReschedulingRequestStateAcknowledged}},
			added: []reqSpec{
				{"p1", v1alpha1.ReschedulingRequestStatePending},
				{"p2", v1alpha1.ReschedulingRequestStatePending},
			},
			want: []reqSpec{
				{"ack", v1alpha1.ReschedulingRequestStateSucceeded},
				{"p1", v1alpha1.ReschedulingRequestStateSucceeded},
				{"p2", v1alpha1.ReschedulingRequestStateSucceeded},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			for _, r := range tt.startup {
				objs = append(objs, request(r.name, r.state))
			}
			s := newScheduler(t, objs...)

			// Startup reconciliation rebuilds the queue / fails interrupted work.
			if err := s.reconcileOnRestart(context.Background()); err != nil {
				t.Fatalf("startup reconciliation failed: %v", err)
			}
			for _, r := range tt.added {
				createRequest(t, s, r)
			}

			// Start the real scheduler worker loop; the test never calls
			// scheduleOne itself.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go s.Run(ctx)

			want := wantMap(tt.want)
			driveToWant(t, s, want)
			assertStable(t, s, want)
		})
	}
}

// fakeBackend is a placement backend returning a fixed result.
type fakeBackend struct{ result PlacementResult }

func (b fakeBackend) Schedule(context.Context, PlacementRequest) PlacementResult { return b.result }

// startBroker runs the placement broker for a scheduler and returns a cancel
// func to stop it.
func startBroker(s *Scheduler) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go s.runPlacement(ctx)
	return cancel
}

func TestScheduleNext_SkipsStaleEntry(t *testing.T) {
	// A queued key whose request is no longer Acknowledged must not be advanced.
	rr := request("req-pending", v1alpha1.ReschedulingRequestStatePending)
	s := newScheduler(t, rr)
	defer startBroker(s)()
	s.Queue.Add(client.ObjectKeyFromObject(rr))

	s.scheduleNext(context.Background())

	if got := stateOf(t, s, "req-pending"); got != v1alpha1.ReschedulingRequestStatePending {
		t.Errorf("expected stale entry untouched (Pending), got %s", got)
	}
}

// perEntityBackend returns candidate hosts keyed by the requested instance uuid,
// so different entities of a request can get different placement results.
type perEntityBackend struct{ byUUID map[string][]string }

func (b perEntityBackend) Schedule(_ context.Context, req PlacementRequest) PlacementResult {
	return PlacementResult{Candidates: b.byUUID[req.InstanceUUID]}
}

func getRequest(t *testing.T, s *Scheduler, name string) *v1alpha1.ReschedulingRequest {
	t.Helper()
	got := &v1alpha1.ReschedulingRequest{}
	if err := s.Get(context.Background(), client.ObjectKey{Name: name}, got); err != nil {
		t.Fatalf("get %s failed: %v", name, err)
	}
	return got
}

func TestUpdateStatus_ConflictOnStaleResourceVersion(t *testing.T) {
	rr := request("rr", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	ctx := context.Background()

	// Two in-memory copies read at the same resourceVersion.
	stale := getRequest(t, s, "rr")
	fresh := getRequest(t, s, "rr")

	// The first write succeeds and bumps the resourceVersion.
	if err := s.updateStatus(ctx, fresh, func() {
		fresh.Status.State = v1alpha1.ReschedulingRequestStateProcessing
	}); err != nil {
		t.Fatalf("first update failed: %v", err)
	}
	// The stale copy now carries an outdated resourceVersion; the optimistic
	// lock must reject the write rather than silently overwriting.
	err := s.updateStatus(ctx, stale, func() {
		stale.Status.State = v1alpha1.ReschedulingRequestStateProcessing
	})
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected a conflict error from the optimistic lock, got %v", err)
	}
}

func TestHandleSucceeded_HostBlockTTL(t *testing.T) {
	s := newScheduler(t)
	rr := request("done", v1alpha1.ReschedulingRequestStateSucceeded)
	rr.Status.BlockedHosts = []string{"src-host"}
	expiry := metav1.NewTime(time.Now().Add(time.Hour))
	rr.Status.BlockExpiresAt = &expiry
	if err := s.Create(context.Background(), rr); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Within the grace period the block is retained and a requeue is requested.
	res, err := s.handleSucceeded(context.Background(), rr)
	if err != nil {
		t.Fatalf("handleSucceeded failed: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected a requeue while block is active, got %+v", res)
	}

	// Once expired the block is released.
	past := metav1.NewTime(time.Now().Add(-time.Minute))
	rr.Status.BlockExpiresAt = &past
	if _, err := s.handleSucceeded(context.Background(), rr); err != nil {
		t.Fatalf("handleSucceeded failed: %v", err)
	}
	if got := getRequest(t, s, "done"); len(got.Status.BlockedHosts) != 0 {
		t.Errorf("expected block released after TTL, got %v", got.Status.BlockedHosts)
	}
}
