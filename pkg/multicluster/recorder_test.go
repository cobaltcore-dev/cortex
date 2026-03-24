// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"fmt"
	"sync"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// fakeEventRecorder captures Eventf calls for assertions.
type fakeEventRecorder struct {
	mu    sync.Mutex
	calls []eventfCall
}

type eventfCall struct {
	regarding runtime.Object
	eventtype string
	reason    string
	action    string
	note      string
}

func (f *fakeEventRecorder) Eventf(regarding, _ runtime.Object, eventtype, reason, action, note string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, eventfCall{
		regarding: regarding,
		eventtype: eventtype,
		reason:    reason,
		action:    action,
		note:      fmt.Sprintf(note, args...),
	})
}

func (f *fakeEventRecorder) getCalls() []eventfCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]eventfCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestMultiClusterRecorder_HomeGVK(t *testing.T) {
	scheme := newTestScheme(t)
	homeRecorder := &fakeEventRecorder{}
	homeCluster := &fakeCluster{
		fakeClient:   nil,
		fakeCache:    &fakeCache{},
		fakeRecorder: homeRecorder,
	}

	historyGVK := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "History"}
	mcl := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs:    map[schema.GroupVersionKind]bool{historyGVK: true},
	}

	recorder := mcl.GetEventRecorder("test-recorder")

	history := &v1alpha1.History{
		ObjectMeta: metav1.ObjectMeta{Name: "nova-uuid-1"},
	}
	recorder.Eventf(history, nil, corev1.EventTypeNormal, "SchedulingSucceeded", "Scheduled", "selected host: %s", "compute-1")

	calls := homeRecorder.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].eventtype != corev1.EventTypeNormal {
		t.Errorf("expected event type %q, got %q", corev1.EventTypeNormal, calls[0].eventtype)
	}
	if calls[0].reason != "SchedulingSucceeded" {
		t.Errorf("expected reason %q, got %q", "SchedulingSucceeded", calls[0].reason)
	}
	if calls[0].note != "selected host: compute-1" {
		t.Errorf("expected note %q, got %q", "selected host: compute-1", calls[0].note)
	}
}

func TestMultiClusterRecorder_RemoteGVK(t *testing.T) {
	scheme := newTestScheme(t)
	homeRecorder := &fakeEventRecorder{}
	remoteRecorder := &fakeEventRecorder{}

	homeCluster := &fakeCluster{
		fakeClient:   nil,
		fakeCache:    &fakeCache{},
		fakeRecorder: homeRecorder,
	}
	remote := &fakeCluster{
		fakeClient:   nil,
		fakeCache:    &fakeCache{},
		fakeRecorder: remoteRecorder,
	}

	reservationGVK := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	mcl := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		ResourceRouters: map[schema.GroupVersionKind]ResourceRouter{
			reservationGVK: ReservationsResourceRouter{},
		},
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			reservationGVK: {{cluster: remote, labels: map[string]string{"availabilityZone": "az-a"}}},
		},
	}

	recorder := mcl.GetEventRecorder("test-recorder")

	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-1"},
		Spec:       v1alpha1.ReservationSpec{AvailabilityZone: "az-a"},
	}
	recorder.Eventf(res, nil, corev1.EventTypeNormal, "ValidationPassed", "Validated", "reservation validated")

	// Event should go to the remote recorder, not home.
	homeCalls := homeRecorder.getCalls()
	if len(homeCalls) != 0 {
		t.Errorf("expected 0 home calls, got %d", len(homeCalls))
	}
	remoteCalls := remoteRecorder.getCalls()
	if len(remoteCalls) != 1 {
		t.Fatalf("expected 1 remote call, got %d", len(remoteCalls))
	}
	if remoteCalls[0].action != "Validated" {
		t.Errorf("expected action %q, got %q", "Validated", remoteCalls[0].action)
	}
}

func TestMultiClusterRecorder_MultipleRemotes(t *testing.T) {
	scheme := newTestScheme(t)
	homeRecorder := &fakeEventRecorder{}
	remoteARecorder := &fakeEventRecorder{}
	remoteBRecorder := &fakeEventRecorder{}

	homeCluster := &fakeCluster{fakeRecorder: homeRecorder, fakeCache: &fakeCache{}}
	remoteA := &fakeCluster{fakeRecorder: remoteARecorder, fakeCache: &fakeCache{}}
	remoteB := &fakeCluster{fakeRecorder: remoteBRecorder, fakeCache: &fakeCache{}}

	reservationGVK := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "Reservation"}
	mcl := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		ResourceRouters: map[schema.GroupVersionKind]ResourceRouter{
			reservationGVK: ReservationsResourceRouter{},
		},
		remoteClusters: map[schema.GroupVersionKind][]remoteCluster{
			reservationGVK: {
				{cluster: remoteA, labels: map[string]string{"availabilityZone": "az-a"}},
				{cluster: remoteB, labels: map[string]string{"availabilityZone": "az-b"}},
			},
		},
	}

	recorder := mcl.GetEventRecorder("test-recorder")

	// Event for az-b should go to remoteB.
	res := &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "res-b"},
		Spec:       v1alpha1.ReservationSpec{AvailabilityZone: "az-b"},
	}
	recorder.Eventf(res, nil, corev1.EventTypeWarning, "SchedulingFailed", "FailedScheduling", "no host found")

	if len(remoteARecorder.getCalls()) != 0 {
		t.Errorf("expected 0 calls to remote-a, got %d", len(remoteARecorder.getCalls()))
	}
	if len(remoteBRecorder.getCalls()) != 1 {
		t.Fatalf("expected 1 call to remote-b, got %d", len(remoteBRecorder.getCalls()))
	}
	if remoteBRecorder.getCalls()[0].reason != "SchedulingFailed" {
		t.Errorf("expected reason %q, got %q", "SchedulingFailed", remoteBRecorder.getCalls()[0].reason)
	}
}

func TestMultiClusterRecorder_FallbackOnUnknownGVK(t *testing.T) {
	scheme := newTestScheme(t)
	homeRecorder := &fakeEventRecorder{}
	homeCluster := &fakeCluster{fakeRecorder: homeRecorder, fakeCache: &fakeCache{}}

	mcl := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs:    map[schema.GroupVersionKind]bool{},
	}

	recorder := mcl.GetEventRecorder("test-recorder")

	// unknownType is not in the scheme — should fall back to home recorder.
	obj := &unknownType{ObjectMeta: metav1.ObjectMeta{Name: "unknown-1"}}
	recorder.Eventf(obj, nil, corev1.EventTypeNormal, "Test", "Test", "test message")

	if len(homeRecorder.getCalls()) != 1 {
		t.Fatalf("expected 1 home call on fallback, got %d", len(homeRecorder.getCalls()))
	}
}

func TestMultiClusterRecorder_FallbackOnNilRegarding(t *testing.T) {
	scheme := newTestScheme(t)
	homeRecorder := &fakeEventRecorder{}
	homeCluster := &fakeCluster{fakeRecorder: homeRecorder, fakeCache: &fakeCache{}}

	mcl := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
	}

	recorder := mcl.GetEventRecorder("test-recorder")
	recorder.Eventf(nil, nil, corev1.EventTypeNormal, "Test", "Test", "nil regarding")

	if len(homeRecorder.getCalls()) != 1 {
		t.Fatalf("expected 1 home call for nil regarding, got %d", len(homeRecorder.getCalls()))
	}
}

func TestMultiClusterRecorder_ConcurrentEventf(t *testing.T) {
	scheme := newTestScheme(t)
	homeRecorder := &fakeEventRecorder{}
	homeCluster := &fakeCluster{fakeRecorder: homeRecorder, fakeCache: &fakeCache{}}

	historyGVK := schema.GroupVersionKind{Group: "cortex.cloud", Version: "v1alpha1", Kind: "History"}
	mcl := &Client{
		HomeCluster: homeCluster,
		HomeScheme:  scheme,
		homeGVKs:    map[schema.GroupVersionKind]bool{historyGVK: true},
	}

	recorder := mcl.GetEventRecorder("test-recorder")

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			history := &v1alpha1.History{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("history-%d", n)},
			}
			recorder.Eventf(history, nil, corev1.EventTypeNormal, "Test", "Test", "event %d", n)
		}(i)
	}
	wg.Wait()

	if len(homeRecorder.getCalls()) != goroutines {
		t.Errorf("expected %d calls, got %d", goroutines, len(homeRecorder.getCalls()))
	}
}
