// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

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
	return &v1alpha1.ReschedulingRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReschedulingRequestSpec{Action: v1alpha1.ReschedulingActionDrain},
		Status:     v1alpha1.ReschedulingRequestStatus{State: state},
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

func TestScheduleOne_SchedulesAcknowledged(t *testing.T) {
	rr := request("req-ack", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	defer startBroker(s)()
	s.Queue.Add(client.ObjectKeyFromObject(rr))

	s.scheduleOne(context.Background())

	if got := stateOf(t, s, "req-ack"); got != v1alpha1.ReschedulingRequestStateAccepted {
		t.Errorf("expected Accepted after scheduling, got %s", got)
	}
}

func TestScheduleOne_RejectsWhenNoHost(t *testing.T) {
	rr := request("req-ack", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	s.Placement = fakeBackend{result: PlacementResult{Hosts: nil}}
	defer startBroker(s)()
	s.Queue.Add(client.ObjectKeyFromObject(rr))

	s.scheduleOne(context.Background())

	if got := stateOf(t, s, "req-ack"); got != v1alpha1.ReschedulingRequestStateRejected {
		t.Errorf("expected Rejected when no host found, got %s", got)
	}
}

func TestScheduleOne_FailsOnPlacementError(t *testing.T) {
	rr := request("req-ack", v1alpha1.ReschedulingRequestStateAcknowledged)
	s := newScheduler(t, rr)
	s.Placement = fakeBackend{result: PlacementResult{Err: errors.New("boom")}}
	defer startBroker(s)()
	s.Queue.Add(client.ObjectKeyFromObject(rr))

	s.scheduleOne(context.Background())

	if got := stateOf(t, s, "req-ack"); got != v1alpha1.ReschedulingRequestStateFailed {
		t.Errorf("expected Failed on placement error, got %s", got)
	}
}

func TestScheduleOne_SkipsStaleEntry(t *testing.T) {
	// A queued key whose request is no longer Acknowledged must not be advanced.
	rr := request("req-pending", v1alpha1.ReschedulingRequestStatePending)
	s := newScheduler(t, rr)
	defer startBroker(s)()
	s.Queue.Add(client.ObjectKeyFromObject(rr))

	s.scheduleOne(context.Background())

	if got := stateOf(t, s, "req-pending"); got != v1alpha1.ReschedulingRequestStatePending {
		t.Errorf("expected stale entry untouched (Pending), got %s", got)
	}
}
