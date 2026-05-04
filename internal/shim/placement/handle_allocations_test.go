// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// Passthrough mode tests (unchanged behavior)
// ---------------------------------------------------------------------------

func TestHandleManageAllocations(t *testing.T) {
	var gotPath string
	s := newTestShim(t, http.StatusNoContent, "", &gotPath)
	w := serveHandler(t, "POST", "/allocations", s.HandleManageAllocations, "/allocations")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if gotPath != "/allocations" {
		t.Fatalf("upstream path = %q, want /allocations", gotPath)
	}
}

func TestHandleListAllocations(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/allocations/{consumer_uuid}",
			s.HandleListAllocations, "/allocations/"+validUUID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "GET", "/allocations/{consumer_uuid}",
			s.HandleListAllocations, "/allocations/bad")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleUpdateAllocations(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, "PUT", "/allocations/{consumer_uuid}",
			s.HandleUpdateAllocations, "/allocations/"+validUUID)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "PUT", "/allocations/{consumer_uuid}",
			s.HandleUpdateAllocations, "/allocations/bad")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestHandleDeleteAllocations(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusNoContent, "", nil)
		w := serveHandler(t, "DELETE", "/allocations/{consumer_uuid}",
			s.HandleDeleteAllocations, "/allocations/"+validUUID)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})
	t.Run("invalid uuid", func(t *testing.T) {
		s := newTestShim(t, http.StatusOK, "{}", nil)
		w := serveHandler(t, "DELETE", "/allocations/{consumer_uuid}",
			s.HandleDeleteAllocations, "/allocations/bad")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

// ---------------------------------------------------------------------------
// Helper to create a test shim with allocations feature mode set.
// ---------------------------------------------------------------------------

const (
	testConsumerUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testRPUUID       = "11111111-2222-3333-4444-555555555555"
	testRPUUID2      = "66666666-7777-8888-9999-aaaaaaaaaaaa"
)

func testHypervisorWithBooking(name, openstackID, consumerUUID string, vcpu int64, memMB int64) *hv1.Hypervisor {
	gen := int64(1)
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     hv1.HypervisorStatus{HypervisorID: openstackID},
		Spec: hv1.HypervisorSpec{
			Bookings: []hv1.Booking{
				{Consumer: &hv1.ConsumerBooking{
					UUID:               consumerUUID,
					Resources:          map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: *resource.NewQuantity(vcpu, resource.DecimalSI), hv1.ResourceMemory: *resource.NewQuantity(memMB*1024*1024, resource.BinarySI)},
					ConsumerGeneration: &gen,
					ProjectID:          "proj-1",
					UserID:             "user-1",
				}},
			},
		},
	}
}

func newAllocationsTestShim(t *testing.T, mode FeatureMode, upstreamStatus int, upstreamBody string, hvs ...client.Object) *Shim {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		w.Write([]byte(upstreamBody)) //nolint:errcheck
	}))
	t.Cleanup(upstream.Close)
	down, up := newTestTimers()
	return &Shim{
		Client: newFakeClient(t, hvs...),
		config: config{
			PlacementURL: upstream.URL,
			Features:     featuresConfig{Allocations: mode},
		},
		httpClient:             upstream.Client(),
		maxBodyLogSize:         4096,
		downstreamRequestTimer: down,
		upstreamRequestTimer:   up,
	}
}

// ---------------------------------------------------------------------------
// CRD mode: PUT
// ---------------------------------------------------------------------------

func TestUpdateAllocations_CRD_NewConsumer(t *testing.T) {
	hv := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-node-1"},
		Status:     hv1.HypervisorStatus{HypervisorID: testRPUUID},
	}
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "", hv)

	body := `{"allocations":{"` + testRPUUID + `":{"resources":{"VCPU":4,"MEMORY_MB":8192}}},"consumer_generation":null,"project_id":"proj-1","user_id":"user-1"}`
	w := serveHandlerWithBody(t, "PUT", "/allocations/{consumer_uuid}",
		s.HandleUpdateAllocations, "/allocations/"+testConsumerUUID, strings.NewReader(body))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

func TestUpdateAllocations_CRD_GenerationMismatch(t *testing.T) {
	hv := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 2, 4096)
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "", hv)

	wrongGen := int64(99)
	_ = wrongGen
	body := `{"allocations":{"` + testRPUUID + `":{"resources":{"VCPU":4,"MEMORY_MB":8192}}},"consumer_generation":99,"project_id":"proj-1","user_id":"user-1"}`
	w := serveHandlerWithBody(t, "PUT", "/allocations/{consumer_uuid}",
		s.HandleUpdateAllocations, "/allocations/"+testConsumerUUID, strings.NewReader(body))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestUpdateAllocations_CRD_UnknownRP(t *testing.T) {
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "")

	body := `{"allocations":{"` + testRPUUID + `":{"resources":{"VCPU":4}}},"consumer_generation":null,"project_id":"proj-1","user_id":"user-1"}`
	w := serveHandlerWithBody(t, "PUT", "/allocations/{consumer_uuid}",
		s.HandleUpdateAllocations, "/allocations/"+testConsumerUUID, strings.NewReader(body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// CRD mode: GET
// ---------------------------------------------------------------------------

func TestListAllocations_CRD_Found(t *testing.T) {
	hv := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 4, 8192)
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "", hv)

	w := serveHandler(t, "GET", "/allocations/{consumer_uuid}",
		s.HandleListAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp allocationsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resp.Allocations[testRPUUID]; !ok {
		t.Fatalf("expected allocation for RP %s, got %v", testRPUUID, resp.Allocations)
	}
	if resp.Allocations[testRPUUID].Resources["VCPU"] != 4 {
		t.Fatalf("VCPU = %d, want 4", resp.Allocations[testRPUUID].Resources["VCPU"])
	}
	if resp.Allocations[testRPUUID].Resources["MEMORY_MB"] != 8192 {
		t.Fatalf("MEMORY_MB = %d, want 8192", resp.Allocations[testRPUUID].Resources["MEMORY_MB"])
	}
}

func TestListAllocations_CRD_NotFound(t *testing.T) {
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "")

	w := serveHandler(t, "GET", "/allocations/{consumer_uuid}",
		s.HandleListAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp allocationsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Allocations) != 0 {
		t.Fatalf("expected empty allocations, got %v", resp.Allocations)
	}
}

func TestListAllocations_CRD_MultiCR(t *testing.T) {
	hv1obj := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 4, 8192)
	hv2obj := testHypervisorWithBooking("hv-node-2", testRPUUID2, testConsumerUUID, 2, 4096)
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "", hv1obj, hv2obj)

	w := serveHandler(t, "GET", "/allocations/{consumer_uuid}",
		s.HandleListAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp allocationsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Allocations) != 2 {
		t.Fatalf("expected 2 allocations, got %d: %v", len(resp.Allocations), resp.Allocations)
	}
}

// ---------------------------------------------------------------------------
// CRD mode: DELETE
// ---------------------------------------------------------------------------

func TestDeleteAllocations_CRD_Found(t *testing.T) {
	hv := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 4, 8192)
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "", hv)

	w := serveHandler(t, "DELETE", "/allocations/{consumer_uuid}",
		s.HandleDeleteAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

func TestDeleteAllocations_CRD_NotFound(t *testing.T) {
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "")

	w := serveHandler(t, "DELETE", "/allocations/{consumer_uuid}",
		s.HandleDeleteAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Hybrid mode: PUT (KVM-only allocation, skips upstream)
// ---------------------------------------------------------------------------

func TestUpdateAllocations_Hybrid_KVMOnly(t *testing.T) {
	hv := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-node-1"},
		Status:     hv1.HypervisorStatus{HypervisorID: testRPUUID},
	}
	s := newAllocationsTestShim(t, FeatureModeHybrid, http.StatusInternalServerError, "should not be called", hv)

	body := `{"allocations":{"` + testRPUUID + `":{"resources":{"VCPU":4,"MEMORY_MB":8192}}},"consumer_generation":null,"project_id":"proj-1","user_id":"user-1"}`
	w := serveHandlerWithBody(t, "PUT", "/allocations/{consumer_uuid}",
		s.HandleUpdateAllocations, "/allocations/"+testConsumerUUID, strings.NewReader(body))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Hybrid mode: PUT (non-KVM allocation, forwards to upstream)
// ---------------------------------------------------------------------------

func TestUpdateAllocations_Hybrid_NonKVMOnly(t *testing.T) {
	s := newAllocationsTestShim(t, FeatureModeHybrid, http.StatusNoContent, "")

	unknownRP := "99999999-9999-9999-9999-999999999999"
	body := `{"allocations":{"` + unknownRP + `":{"resources":{"VCPU":4}}},"consumer_generation":null,"project_id":"proj-1","user_id":"user-1"}`
	w := serveHandlerWithBody(t, "PUT", "/allocations/{consumer_uuid}",
		s.HandleUpdateAllocations, "/allocations/"+testConsumerUUID, strings.NewReader(body))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Hybrid mode: GET (merges upstream + CRD)
// ---------------------------------------------------------------------------

func TestListAllocations_Hybrid_Merge(t *testing.T) {
	hv := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 4, 8192)
	upstreamBody := `{"allocations":{"upstream-rp-uuid":{"resources":{"VCPU":2}}},"consumer_generation":1,"project_id":"proj-1","user_id":"user-1"}`
	s := newAllocationsTestShim(t, FeatureModeHybrid, http.StatusOK, upstreamBody, hv)

	w := serveHandler(t, "GET", "/allocations/{consumer_uuid}",
		s.HandleListAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp allocationsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Allocations) != 2 {
		t.Fatalf("expected 2 allocations (upstream + CRD), got %d: %v", len(resp.Allocations), resp.Allocations)
	}
	if _, ok := resp.Allocations["upstream-rp-uuid"]; !ok {
		t.Fatal("expected upstream allocation in merged response")
	}
	if _, ok := resp.Allocations[testRPUUID]; !ok {
		t.Fatal("expected CRD allocation in merged response")
	}
}

// ---------------------------------------------------------------------------
// Hybrid mode: DELETE (upstream first, then CRD)
// ---------------------------------------------------------------------------

func TestDeleteAllocations_Hybrid(t *testing.T) {
	hv := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 4, 8192)
	s := newAllocationsTestShim(t, FeatureModeHybrid, http.StatusNoContent, "", hv)

	w := serveHandler(t, "DELETE", "/allocations/{consumer_uuid}",
		s.HandleDeleteAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

func TestDeleteAllocations_Hybrid_UpstreamFails(t *testing.T) {
	hv := testHypervisorWithBooking("hv-node-1", testRPUUID, testConsumerUUID, 4, 8192)
	s := newAllocationsTestShim(t, FeatureModeHybrid, http.StatusInternalServerError, "upstream error", hv)

	w := serveHandler(t, "DELETE", "/allocations/{consumer_uuid}",
		s.HandleDeleteAllocations, "/allocations/"+testConsumerUUID)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Resource translation unit tests
// ---------------------------------------------------------------------------

func TestPlacementToHVResources(t *testing.T) {
	in := map[string]int64{"VCPU": 4, "MEMORY_MB": 8192}
	out := placementToHVResources(in)
	if out[hv1.ResourceCPU] != *resource.NewQuantity(4, resource.DecimalSI) {
		t.Fatalf("cpu = %v, want 4", out[hv1.ResourceCPU])
	}
	expectedMem := resource.NewQuantity(8192*1024*1024, resource.BinarySI)
	gotMem := out[hv1.ResourceMemory]
	if gotMem.Cmp(*expectedMem) != 0 {
		t.Fatalf("memory = %v, want %v", gotMem, expectedMem)
	}
}

func TestHVToPlacementResources(t *testing.T) {
	in := map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
		hv1.ResourceMemory: *resource.NewQuantity(8192*1024*1024, resource.BinarySI),
	}
	out := hvToPlacementResources(in)
	if out["VCPU"] != 4 {
		t.Fatalf("VCPU = %d, want 4", out["VCPU"])
	}
	if out["MEMORY_MB"] != 8192 {
		t.Fatalf("MEMORY_MB = %d, want 8192", out["MEMORY_MB"])
	}
}

// ---------------------------------------------------------------------------
// CRD mode: POST (batch)
// ---------------------------------------------------------------------------

func TestManageAllocations_CRD(t *testing.T) {
	hv := &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{Name: "hv-node-1"},
		Status:     hv1.HypervisorStatus{HypervisorID: testRPUUID},
	}
	s := newAllocationsTestShim(t, FeatureModeCRD, 0, "", hv)

	body := `{"` + testConsumerUUID + `":{"allocations":{"` + testRPUUID + `":{"resources":{"VCPU":2}}},"consumer_generation":null,"project_id":"proj-1","user_id":"user-1"}}`
	w := serveHandlerWithBody(t, "POST", "/allocations",
		s.HandleManageAllocations, "/allocations", strings.NewReader(body))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}
}
