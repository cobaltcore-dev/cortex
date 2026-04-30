// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

// End-to-end tests: HTTP → CommittedResource CRD → Reservation CRDs → scheduler → controllers → HTTP response.
//
// Unlike change_commitments_test.go which uses fakeControllerClient (which immediately sets
// conditions), these tests wire real CommittedResourceController and CommitmentReservationController
// against a fake k8s client. A background goroutine drives reconcile loops so the API polling
// loop can observe terminal conditions within its timeout window.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Field index paths for the fake client — must match the unexported constants in the commitments package.
const (
	e2eIdxCommittedResourceByUUID     = "spec.commitmentUUID"
	e2eIdxReservationByCommitmentUUID = "spec.committedResourceReservation.commitmentUUID"
)

// e2eEnv is a full end-to-end test environment: real controllers, fake k8s client,
// mock scheduler, and a background reconcile driver goroutine.
type e2eEnv struct {
	t            *testing.T
	k8sClient    client.Client
	httpServer   *httptest.Server
	schedulerSrv *httptest.Server
	crCtrl       *commitments.CommittedResourceController
	resCtrl      *commitments.CommitmentReservationController
	cancelBg     context.CancelFunc
	bgDone       chan struct{}
}

// newE2EEnv creates an e2eEnv with the given flavors and scheduler handler.
// The scheduler handler controls what the mock Nova scheduler returns.
func newE2EEnv(t *testing.T, flavors []*TestFlavor, infoVersion int64, schedulerHandler http.HandlerFunc) *e2eEnv {
	t.Helper()
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	// Scheme: v1alpha1 for CR/Reservation/Knowledge types; hv1 for Hypervisor.
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hv1 scheme: %v", err)
	}

	// One hypervisor so the reservation controller can build a non-empty eligible-hosts list.
	hypervisor := &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host-1"}}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(createKnowledgeCRD(buildFlavorGroupsKnowledge(flavors, infoVersion)), hypervisor).
		WithStatusSubresource(
			&v1alpha1.CommittedResource{},
			&v1alpha1.Reservation{},
			&v1alpha1.Knowledge{},
		).
		WithIndex(&v1alpha1.Reservation{}, e2eIdxReservationByCommitmentUUID, func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil || res.Spec.CommittedResourceReservation.CommitmentUUID == "" {
				return nil
			}
			return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
		}).
		WithIndex(&v1alpha1.CommittedResource{}, e2eIdxCommittedResourceByUUID, func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok || cr.Spec.CommitmentUUID == "" {
				return nil
			}
			return []string{cr.Spec.CommitmentUUID}
		}).
		Build()

	schedulerSrv := httptest.NewServer(schedulerHandler)

	crCtrl := &commitments.CommittedResourceController{
		Client: k8sClient,
		Scheme: scheme,
		Conf:   commitments.CommittedResourceControllerConfig{RequeueIntervalRetry: metav1.Duration{Duration: 100 * time.Millisecond}},
	}

	resCtrl := &commitments.CommitmentReservationController{
		Client: k8sClient,
		Scheme: scheme,
		Conf: commitments.ReservationControllerConfig{
			SchedulerURL:          schedulerSrv.URL,
			AllocationGracePeriod: metav1.Duration{Duration: 15 * time.Minute},
			RequeueIntervalActive: metav1.Duration{Duration: 5 * time.Minute},
			RequeueIntervalRetry:  metav1.Duration{Duration: 100 * time.Millisecond},
		},
	}
	if err := resCtrl.Init(context.Background(), resCtrl.Conf); err != nil {
		t.Fatalf("resCtrl.Init: %v", err)
	}

	// HTTPAPI wired directly to the real k8s client (no fakeControllerClient wrapper).
	cfg := commitments.DefaultAPIConfig()
	cfg.WatchTimeout = metav1.Duration{Duration: 5 * time.Second}
	cfg.WatchPollInterval = metav1.Duration{Duration: 100 * time.Millisecond}
	api := NewAPIWithConfig(k8sClient, cfg, nil)
	mux := http.NewServeMux()
	api.Init(mux, prometheus.NewRegistry(), log.Log)
	httpServer := httptest.NewServer(mux)

	ctx, cancel := context.WithCancel(context.Background())
	env := &e2eEnv{
		t:            t,
		k8sClient:    k8sClient,
		httpServer:   httpServer,
		schedulerSrv: schedulerSrv,
		crCtrl:       crCtrl,
		resCtrl:      resCtrl,
		cancelBg:     cancel,
		bgDone:       make(chan struct{}),
	}
	go env.driveReconciles(ctx)
	return env
}

func (e *e2eEnv) close() {
	e.cancelBg()
	<-e.bgDone
	e.httpServer.Close()
	e.schedulerSrv.Close()
}

// asCRTestEnv wraps e2eEnv as a CRTestEnv to reuse its HTTP-call and assertion helpers.
func (e *e2eEnv) asCRTestEnv() *CRTestEnv {
	return &CRTestEnv{T: e.t, K8sClient: e.k8sClient, HTTPServer: e.httpServer}
}

// driveReconciles runs in the background, reconciling pending CRs and Reservations until ctx is cancelled.
func (e *e2eEnv) driveReconciles(ctx context.Context) {
	defer close(e.bgDone)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reconcileAll(ctx)
		}
	}
}

// reconcileAll drives one round of reconciles:
//  1. CR pass 1  — adds finalizer and creates Reservation CRDs.
//  2. Reservation pass — calls the scheduler, sets TargetHost (first reconcile) then Ready=True (second).
//  3. CR pass 2  — re-fetches each CR and picks up Reservation outcomes (placed or rejected).
//
// CRs and Reservations that have already reached a terminal state are skipped to avoid
// overwriting the rejection signal the API polling loop needs to read.
func (e *e2eEnv) reconcileAll(ctx context.Context) {
	var crList v1alpha1.CommittedResourceList
	if err := e.k8sClient.List(ctx, &crList); err != nil {
		return
	}

	// CR pass 1.
	for _, cr := range crList.Items {
		if e2eIsTerminalCR(cr) {
			continue
		}
		e.crCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}) //nolint:errcheck
	}

	// Reservation pass (two reconciles per slot: first sets TargetHost, second sets Ready=True).
	var resList v1alpha1.ReservationList
	if err := e.k8sClient.List(ctx, &resList, client.MatchingLabels{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}); err != nil {
		return
	}
	for _, res := range resList.Items {
		if e2eIsTerminalReservation(res) {
			continue
		}
		req := ctrl.Request{NamespacedName: types.NamespacedName{Name: res.Name}}
		e.resCtrl.Reconcile(ctx, req) //nolint:errcheck
		e.resCtrl.Reconcile(ctx, req) //nolint:errcheck
	}

	// CR pass 2: re-fetch so we see any condition changes made during the Reservation pass.
	for _, cr := range crList.Items {
		var latest v1alpha1.CommittedResource
		if err := e.k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name}, &latest); err != nil {
			continue // deleted or transient
		}
		if e2eIsTerminalCR(latest) {
			continue
		}
		e.crCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: latest.Name}}) //nolint:errcheck
	}
}

// e2eIsTerminalCR returns true for states the API polling loop treats as final:
// Accepted (Ready=True), Rejected, or Planned.
// CRs with DeletionTimestamp are never terminal here: they need one more reconcile to remove
// their finalizer (set by the controller on first reconcile) so the fake client can delete them.
func e2eIsTerminalCR(cr v1alpha1.CommittedResource) bool {
	if !cr.DeletionTimestamp.IsZero() {
		return false
	}
	cond := apimeta.FindStatusCondition(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady)
	if cond == nil {
		return false
	}
	if cond.Status == metav1.ConditionTrue {
		return true
	}
	return cond.Reason == v1alpha1.CommittedResourceReasonRejected ||
		cond.Reason == v1alpha1.CommittedResourceReasonPlanned
}

// waitForCRAbsent polls until the named CommittedResource no longer exists or the 1s deadline passes.
// Used after rollback calls because the finalizer removal happens asynchronously in the background reconcile loop.
func (e *e2eEnv) waitForCRAbsent(t *testing.T, crName string) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for {
		cr := &v1alpha1.CommittedResource{}
		err := e.k8sClient.Get(context.Background(), types.NamespacedName{Name: crName}, cr)
		if apierrors.IsNotFound(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("expected CommittedResource %q to be absent after rollback, but it still exists", crName)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// e2eIsTerminalReservation returns true when a Reservation is fully placed (Ready=True).
func e2eIsTerminalReservation(res v1alpha1.Reservation) bool {
	cond := apimeta.FindStatusCondition(res.Status.Conditions, v1alpha1.ReservationConditionReady)
	return cond != nil && cond.Status == metav1.ConditionTrue
}

// ============================================================================
// Scheduler handlers
// ============================================================================

func e2eAcceptScheduler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		resp := &schedulerdelegationapi.ExternalSchedulerResponse{Hosts: []string{"host-1"}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("scheduler encode: %v", err)
		}
	}
}

func e2eRejectScheduler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		// Return an empty hosts list — the reservation controller treats this as NoHostsFound.
		resp := &schedulerdelegationapi.ExternalSchedulerResponse{Hosts: []string{}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("scheduler encode: %v", err)
		}
	}
}

// ============================================================================
// E2E test cases
// ============================================================================

const e2eInfoVersion = int64(1234)

var e2eFlavor = &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}

// TestE2EChangeCommitments is the full end-to-end suite: HTTP → CRD → controller → scheduler → HTTP response.
func TestE2EChangeCommitments(t *testing.T) {
	testCases := []struct {
		Name       string
		Scheduler  func(*testing.T) http.HandlerFunc
		ReqJSON    string
		WantResp   APIResponseExpectation
		WantAbsent []string
		Verify     func(*testing.T, *e2eEnv)
	}{
		{
			Name:      "scheduler accepts: CR placed, Reservation on host-1",
			Scheduler: e2eAcceptScheduler,
			ReqJSON: buildRequestJSON(newCommitmentRequest("az-a", false, e2eInfoVersion,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-e2e-ok", "confirmed", 1))),
			WantResp: newAPIResponse(),
			Verify: func(t *testing.T, env *e2eEnv) {
				t.Helper()
				env.asCRTestEnv().VerifyCRsExist([]string{"commitment-uuid-e2e-ok"})

				var cr v1alpha1.CommittedResource
				if err := env.k8sClient.Get(context.Background(), types.NamespacedName{Name: "commitment-uuid-e2e-ok"}, &cr); err != nil {
					t.Fatalf("get CR: %v", err)
				}
				if !apimeta.IsStatusConditionTrue(cr.Status.Conditions, v1alpha1.CommittedResourceConditionReady) {
					t.Errorf("expected CR Ready=True")
				}

				var resList v1alpha1.ReservationList
				if err := env.k8sClient.List(context.Background(), &resList, client.MatchingLabels{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				}); err != nil {
					t.Fatalf("list reservations: %v", err)
				}
				if len(resList.Items) != 1 {
					t.Fatalf("expected 1 Reservation, got %d", len(resList.Items))
				}
				res := resList.Items[0]
				if !apimeta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ReservationConditionReady) {
					t.Errorf("expected Reservation Ready=True")
				}
				if res.Status.Host != "host-1" {
					t.Errorf("Reservation Status.Host: want host-1, got %q", res.Status.Host)
				}
			},
		},
		{
			Name:      "scheduler rejects: rejection propagates to API response, CR rolled back",
			Scheduler: e2eRejectScheduler,
			ReqJSON: buildRequestJSON(newCommitmentRequest("az-a", false, e2eInfoVersion,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-e2e-rej", "confirmed", 2))),
			WantResp:   newAPIResponse("no hosts found"),
			WantAbsent: []string{"commitment-uuid-e2e-rej"},
		},
		{
			Name:      "batch with one rejection: entire batch rolled back",
			Scheduler: e2eRejectScheduler,
			ReqJSON: buildRequestJSON(newCommitmentRequest("az-a", false, e2eInfoVersion,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-e2e-batch-a", "confirmed", 2),
				createCommitment("hw_version_hana_1_ram", "project-B", "uuid-e2e-batch-b", "confirmed", 2),
			)),
			WantResp:   newAPIResponse("no hosts found"),
			WantAbsent: []string{"commitment-uuid-e2e-batch-a", "commitment-uuid-e2e-batch-b"},
		},
		{
			Name:      "lifecycle: create then delete, CR and child Reservations cleaned up",
			Scheduler: e2eAcceptScheduler,
			ReqJSON: buildRequestJSON(newCommitmentRequest("az-a", false, e2eInfoVersion,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-e2e-lifecycle", "confirmed", 1))),
			WantResp: newAPIResponse(),
			Verify: func(t *testing.T, env *e2eEnv) {
				t.Helper()
				env.asCRTestEnv().VerifyCRsExist([]string{"commitment-uuid-e2e-lifecycle"})

				te := env.asCRTestEnv()
				deleteJSON := buildRequestJSON(newCommitmentRequest("az-a", false, e2eInfoVersion,
					deleteCommitment("hw_version_hana_1_ram", "project-A", "uuid-e2e-lifecycle", "confirmed", 1)))
				resp, _, statusCode := te.CallChangeCommitmentsAPI(deleteJSON)
				te.VerifyAPIResponse(newAPIResponse(), resp, statusCode)

				env.waitForCRAbsent(t, "commitment-uuid-e2e-lifecycle")

				var resList v1alpha1.ReservationList
				if err := env.k8sClient.List(context.Background(), &resList, client.MatchingLabels{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				}); err != nil {
					t.Fatalf("list reservations: %v", err)
				}
				if len(resList.Items) != 0 {
					t.Errorf("expected 0 Reservations after delete, got %d", len(resList.Items))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			env := newE2EEnv(t, []*TestFlavor{e2eFlavor}, e2eInfoVersion, tc.Scheduler(t))
			defer env.close()

			te := env.asCRTestEnv()
			resp, _, statusCode := te.CallChangeCommitmentsAPI(tc.ReqJSON)
			te.VerifyAPIResponse(tc.WantResp, resp, statusCode)
			for _, name := range tc.WantAbsent {
				env.waitForCRAbsent(t, name)
			}
			if tc.Verify != nil {
				tc.Verify(t, env)
			}
		})
	}
}
