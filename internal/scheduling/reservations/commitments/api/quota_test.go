// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/sapcc/go-api-declarations/liquid"
	"go.xyrillian.de/gg/option"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newTestScheme returns a scheme with v1alpha1 types registered.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	return scheme
}

// marshalQuotaReq marshals a ServiceQuotaRequest, failing the test on error.
func marshalQuotaReq(t *testing.T, req liquid.ServiceQuotaRequest) []byte {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	return body
}

func TestHandleQuota_ErrorCases(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		body           []byte
		metadata       *liquid.ProjectMetadata
		enableQuota    *bool // nil = default (enabled)
		expectedStatus int
	}{
		{
			name:           "MethodNotAllowed_GET",
			method:         http.MethodGet,
			path:           "/commitments/v1/projects/project-abc/quota",
			body:           nil,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "MethodNotAllowed_POST",
			method:         http.MethodPost,
			path:           "/commitments/v1/projects/project-abc/quota",
			body:           nil,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "DisabledAPI",
			method:         http.MethodPut,
			path:           "/commitments/v1/projects/project-abc/quota",
			body:           []byte(`{"resources":{}}`),
			enableQuota:    boolPtr(false),
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "InvalidBody",
			method:         http.MethodPut,
			path:           "/commitments/v1/projects/project-abc/quota",
			body:           []byte("{invalid"),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "EmptyBody",
			method:         http.MethodPut,
			path:           "/commitments/v1/projects/project-abc/quota",
			body:           []byte(""),
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "UUIDMismatch",
			method: http.MethodPut,
			path:   "/commitments/v1/projects/project-abc/quota",
			metadata: &liquid.ProjectMetadata{
				UUID:   "different-uuid",
				Name:   "my-project",
				Domain: liquid.DomainMetadata{UUID: "domain-123", Name: "my-domain"},
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			var httpAPI *HTTPAPI
			if tc.enableQuota != nil && !*tc.enableQuota {
				config := commitments.DefaultAPIConfig()
				config.EnableQuotaAPI = false
				httpAPI = NewAPIWithConfig(k8sClient, config, nil)
			} else {
				httpAPI = NewAPI(k8sClient)
			}

			// Build body: use provided bytes or construct from metadata
			var bodyReader *bytes.Reader
			switch {
			case tc.body != nil:
				bodyReader = bytes.NewReader(tc.body)
			case tc.metadata != nil:
				quotaReq := liquid.ServiceQuotaRequest{
					Resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
						"hw_version_hana_1_ram": {Quota: 100},
					},
				}
				quotaReq.ProjectMetadata = option.Some(*tc.metadata)
				bodyReader = bytes.NewReader(marshalQuotaReq(t, quotaReq))
			default:
				bodyReader = bytes.NewReader([]byte{})
			}

			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			w := httptest.NewRecorder()

			httpAPI.HandleQuota(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, resp.StatusCode)
			}
		})
	}
}

// quotaTestKnowledge1GiB creates a Knowledge CRD for the hana_1 flavor group where
// SmallestFlavor.MemoryMB = 1024 MiB (1 GiB exactly). With this flavor the slot→GiB
// conversion is a no-op (1 slot = 1 GiB), so existing expected quota values are unchanged.
func quotaTestKnowledge1GiB(t *testing.T) *v1alpha1.Knowledge {
	t.Helper()
	return createKnowledgeCRD(buildFlavorGroupsKnowledge(
		[]*TestFlavor{{Name: "hana_c4_m1", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}}, 1,
	))
}

func TestHandleQuota_CreateAndUpdate(t *testing.T) {
	tests := []struct {
		name string
		// existing is a set of pre-existing per-AZ CRDs to seed (nil = create, non-nil = update)
		existing      []*v1alpha1.ProjectQuota
		knowledge     *v1alpha1.Knowledge // nil = use quotaTestKnowledge1GiB
		projectID     string
		resources     map[liquid.ResourceName]liquid.ResourceQuotaRequest
		metadata      *liquid.ProjectMetadata
		expectPerAZ   map[string]map[string]int64 // az → resource name → expected GiB quota in CRD
		expectName    string
		expectDom     string
		expectDomName string
	}{
		{
			name:      "Create_WithPerAZ",
			projectID: "project-abc-123",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {
					PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
						"az-a": {Quota: 60},
						"az-b": {Quota: 40},
					},
				},
			},
			metadata: &liquid.ProjectMetadata{
				UUID:   "project-abc-123",
				Domain: liquid.DomainMetadata{UUID: "domain-1"},
			},
			expectPerAZ: map[string]map[string]int64{
				"az-a": {"hw_version_hana_1_ram": 60},
				"az-b": {"hw_version_hana_1_ram": 40},
			},
			expectDom: "domain-1",
		},
		{
			name:      "Create_WithMetadata",
			projectID: "project-meta-test",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {
					PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
						"az-a": {Quota: 50},
					},
				},
			},
			metadata: &liquid.ProjectMetadata{
				UUID: "project-meta-test",
				Name: "my-project-name",
				Domain: liquid.DomainMetadata{
					UUID: "domain-uuid-456",
					Name: "my-domain-name",
				},
			},
			expectPerAZ: map[string]map[string]int64{
				"az-a": {"hw_version_hana_1_ram": 50},
			},
			expectName:    "my-project-name",
			expectDom:     "domain-uuid-456",
			expectDomName: "my-domain-name",
		},
		{
			name:      "Create_EmptyResources",
			projectID: "project-empty",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{},
			metadata: &liquid.ProjectMetadata{
				UUID:   "project-empty",
				Domain: liquid.DomainMetadata{UUID: "domain-1"},
			},
			// No AZs in request means no per-AZ CRDs are created.
			// expectPerAZ is empty — we just verify no error and 204 response.
			expectPerAZ: map[string]map[string]int64{},
			expectDom:   "domain-1",
		},
		{
			name: "Update_WithNewMetadata",
			existing: []*v1alpha1.ProjectQuota{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-update-meta-az-a"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-update-meta",
						DomainID:         "old-domain",
						DomainName:       "old-domain-name",
						ProjectName:      "old-project-name",
						AvailabilityZone: "az-a",
						Quota:            map[string]int64{"hw_version_hana_1_ram": 10},
					},
				},
			},
			projectID: "project-update-meta",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {
					PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
						"az-a": {Quota: 99},
					},
				},
			},
			metadata: &liquid.ProjectMetadata{
				UUID: "project-update-meta",
				Name: "new-project-name",
				Domain: liquid.DomainMetadata{
					UUID: "new-domain",
					Name: "new-domain-name",
				},
			},
			expectPerAZ: map[string]map[string]int64{
				"az-a": {"hw_version_hana_1_ram": 99},
			},
			expectName:    "new-project-name",
			expectDom:     "new-domain",
			expectDomName: "new-domain-name",
		},
		{
			name:      "Create_PartialAZ_OnlyOneAZ",
			projectID: "project-partial",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {
					PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
						"az-a": {Quota: 100},
						// az-b intentionally missing
					},
				},
			},
			metadata: &liquid.ProjectMetadata{
				UUID:   "project-partial",
				Domain: liquid.DomainMetadata{UUID: "domain-1"},
			},
			// Only az-a should get a CRD
			expectPerAZ: map[string]map[string]int64{
				"az-a": {"hw_version_hana_1_ram": 100},
			},
			expectDom: "domain-1",
		},
		{
			name: "Update_QuotaValues",
			existing: []*v1alpha1.ProjectQuota{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "quota-project-xyz-az-a"},
					Spec: v1alpha1.ProjectQuotaSpec{
						ProjectID:        "project-xyz",
						DomainID:         "original-domain",
						DomainName:       "original-domain-name",
						ProjectName:      "original-project-name",
						AvailabilityZone: "az-a",
						Quota:            map[string]int64{"hw_version_hana_1_ram": 50},
					},
				},
			},
			projectID: "project-xyz",
			metadata: &liquid.ProjectMetadata{
				UUID: "project-xyz",
				Name: "original-project-name",
				Domain: liquid.DomainMetadata{
					UUID: "original-domain",
					Name: "original-domain-name",
				},
			},
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {
					PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
						"az-a": {Quota: 120},
						"az-b": {Quota: 80},
					},
				},
			},
			expectPerAZ: map[string]map[string]int64{
				"az-a": {"hw_version_hana_1_ram": 120},
				"az-b": {"hw_version_hana_1_ram": 80},
			},
			expectDom:  "original-domain",
			expectName: "original-project-name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			builder := fake.NewClientBuilder().WithScheme(scheme)

			if tc.existing != nil {
				objs := make([]client.Object, len(tc.existing))
				for i := range tc.existing {
					objs[i] = tc.existing[i]
				}
				builder = builder.WithObjects(objs...)
			}
			knowledge := tc.knowledge
			if knowledge == nil {
				knowledge = quotaTestKnowledge1GiB(t)
			}
			k8sClient := builder.WithObjects(knowledge).Build()
			httpAPI := NewAPI(k8sClient)

			quotaReq := liquid.ServiceQuotaRequest{
				Resources: tc.resources,
			}
			if tc.metadata != nil {
				quotaReq.ProjectMetadata = option.Some(*tc.metadata)
			}
			body := marshalQuotaReq(t, quotaReq)

			path := "/commitments/v1/projects/" + tc.projectID + "/quota"
			req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
			w := httptest.NewRecorder()

			httpAPI.HandleQuota(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("expected status %d (No Content), got %d", http.StatusNoContent, resp.StatusCode)
			}

			// Verify per-AZ ProjectQuota CRDs were created/updated
			for az, expectedQuota := range tc.expectPerAZ {
				crdName := projectQuotaCRDName(tc.projectID, az)
				var pq v1alpha1.ProjectQuota
				if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: crdName}, &pq); err != nil {
					t.Fatalf("failed to get ProjectQuota CRD %q: %v", crdName, err)
				}

				if pq.Spec.ProjectID != tc.projectID {
					t.Errorf("CRD %q: expected ProjectID %q, got %q", crdName, tc.projectID, pq.Spec.ProjectID)
				}
				if pq.Spec.AvailabilityZone != az {
					t.Errorf("CRD %q: expected AZ %q, got %q", crdName, az, pq.Spec.AvailabilityZone)
				}

				// Verify quota values
				for resName, expectedVal := range expectedQuota {
					actual, ok := pq.Spec.Quota[resName]
					if !ok {
						t.Errorf("CRD %q: expected resource %q in quota spec", crdName, resName)
						continue
					}
					if actual != expectedVal {
						t.Errorf("CRD %q resource %q: expected %d, got %d", crdName, resName, expectedVal, actual)
					}
				}

				// Verify metadata
				if tc.expectName != "" && pq.Spec.ProjectName != tc.expectName {
					t.Errorf("CRD %q: expected ProjectName %q, got %q", crdName, tc.expectName, pq.Spec.ProjectName)
				}
				if tc.expectDom != "" && pq.Spec.DomainID != tc.expectDom {
					t.Errorf("CRD %q: expected DomainID %q, got %q", crdName, tc.expectDom, pq.Spec.DomainID)
				}
				if tc.expectDomName != "" && pq.Spec.DomainName != tc.expectDomName {
					t.Errorf("CRD %q: expected DomainName %q, got %q", crdName, tc.expectDomName, pq.Spec.DomainName)
				}
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

// TestHandleQuota_KnowledgeNotReady verifies that the quota endpoint returns 503 when
// the flavor-group Knowledge CRD is absent (needed for unit conversion).
func TestHandleQuota_KnowledgeNotReady(t *testing.T) {
	scheme := newTestScheme(t)
	// No Knowledge CRD — simulates startup before the extractor has run.
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	httpAPI := NewAPI(k8sClient)

	quotaReq := liquid.ServiceQuotaRequest{
		Resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
			"hw_version_hana_1_ram": {
				PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
					"az-a": {Quota: 10},
				},
			},
		},
	}
	quotaReq.ProjectMetadata = option.Some(liquid.ProjectMetadata{
		UUID:   "project-test",
		Domain: liquid.DomainMetadata{UUID: "domain-1"},
	})
	body := marshalQuotaReq(t, quotaReq)

	req := httptest.NewRequest(http.MethodPut, "/commitments/v1/projects/project-test/quota", bytes.NewReader(body))
	w := httptest.NewRecorder()
	httpAPI.HandleQuota(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

// TestHandleQuota_UnitConversion verifies that the quota handler converts incoming declared-unit
// values (slots for fixed-ratio groups) to GiB before writing to the ProjectQuota CRD.
func TestHandleQuota_UnitConversion(t *testing.T) {
	scheme := newTestScheme(t)

	// hana_1 group: SmallestFlavor.MemoryMB = 2048 MiB (2 GiB per slot).
	// Sending 5 slots → expect 5 * 2048 / 1024 = 10 GiB stored.
	knowledge := createKnowledgeCRD(buildFlavorGroupsKnowledge(
		[]*TestFlavor{{Name: "hana_c4_m2", Group: "hana_1", MemoryMB: 2048, VCPUs: 4}}, 1,
	))
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).Build()
	httpAPI := NewAPI(k8sClient)

	quotaReq := liquid.ServiceQuotaRequest{
		Resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
			"hw_version_hana_1_ram": {
				PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
					"az-a": {Quota: 5}, // 5 slots × 2 GiB/slot = 10 GiB
				},
			},
		},
	}
	quotaReq.ProjectMetadata = option.Some(liquid.ProjectMetadata{
		UUID:   "project-conv",
		Domain: liquid.DomainMetadata{UUID: "domain-1"},
	})
	body := marshalQuotaReq(t, quotaReq)

	req := httptest.NewRequest(http.MethodPut, "/commitments/v1/projects/project-conv/quota", bytes.NewReader(body))
	w := httptest.NewRecorder()
	httpAPI.HandleQuota(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	var pq v1alpha1.ProjectQuota
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: "quota-project-conv-az-a"}, &pq); err != nil {
		t.Fatalf("failed to get ProjectQuota CRD: %v", err)
	}
	if got := pq.Spec.Quota["hw_version_hana_1_ram"]; got != 10 {
		t.Errorf("expected stored quota=10 GiB, got %d", got)
	}
}
