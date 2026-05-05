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
	"github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
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
				config := commitments.DefaultConfig()
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

func TestHandleQuota_CreateAndUpdate(t *testing.T) {
	tests := []struct {
		name string
		// existing is a pre-existing CRD to seed (nil = create, non-nil = update)
		existing      *v1alpha1.ProjectQuota
		projectID     string
		resources     map[liquid.ResourceName]liquid.ResourceQuotaRequest
		metadata      *liquid.ProjectMetadata
		expectQuota   map[string]int64            // resource name → expected total quota
		expectPerAZ   map[string]map[string]int64 // resource name → az → expected quota
		expectName    string
		expectDomain  string
		expectDomName string
	}{
		{
			name:      "Create_WithPerAZ",
			projectID: "project-abc-123",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {
					Quota: 100,
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
			expectQuota: map[string]int64{"hw_version_hana_1_ram": 100},
			expectPerAZ: map[string]map[string]int64{
				"hw_version_hana_1_ram": {"az-a": 60, "az-b": 40},
			},
			expectDomain: "domain-1",
		},
		{
			name:      "Create_EmptyResources",
			projectID: "project-empty",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{},
			metadata: &liquid.ProjectMetadata{
				UUID:   "project-empty",
				Domain: liquid.DomainMetadata{UUID: "domain-1"},
			},
			expectQuota:  map[string]int64{},
			expectDomain: "domain-1",
		},
		{
			name:      "Create_WithMetadata",
			projectID: "project-meta-test",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {Quota: 50},
			},
			metadata: &liquid.ProjectMetadata{
				UUID: "project-meta-test",
				Name: "my-project-name",
				Domain: liquid.DomainMetadata{
					UUID: "domain-uuid-456",
					Name: "my-domain-name",
				},
			},
			expectQuota:   map[string]int64{"hw_version_hana_1_ram": 50},
			expectName:    "my-project-name",
			expectDomain:  "domain-uuid-456",
			expectDomName: "my-domain-name",
		},
		{
			name: "Update_QuotaValues",
			existing: &v1alpha1.ProjectQuota{
				Spec: v1alpha1.ProjectQuotaSpec{
					ProjectID:   "project-xyz",
					DomainID:    "original-domain",
					DomainName:  "original-domain-name",
					ProjectName: "original-project-name",
					Quota: map[string]v1alpha1.ResourceQuota{
						"hw_version_hana_1_ram": {Quota: 50, PerAZ: map[string]int64{"az-a": 50}},
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
					Quota: 200,
					PerAZ: map[liquid.AvailabilityZone]liquid.AZResourceQuotaRequest{
						"az-a": {Quota: 120},
						"az-b": {Quota: 80},
					},
				},
			},
			expectQuota: map[string]int64{"hw_version_hana_1_ram": 200},
			expectPerAZ: map[string]map[string]int64{
				"hw_version_hana_1_ram": {"az-a": 120, "az-b": 80},
			},
			// Metadata should be preserved when not provided in update
			expectDomain:  "original-domain",
			expectDomName: "original-domain-name",
			expectName:    "original-project-name",
		},
		{
			name: "Update_WithNewMetadata",
			existing: &v1alpha1.ProjectQuota{
				Spec: v1alpha1.ProjectQuotaSpec{
					ProjectID:   "project-update-meta",
					DomainID:    "old-domain",
					DomainName:  "old-domain-name",
					ProjectName: "old-project-name",
					Quota: map[string]v1alpha1.ResourceQuota{
						"hw_version_hana_1_ram": {Quota: 10},
					},
				},
			},
			projectID: "project-update-meta",
			resources: map[liquid.ResourceName]liquid.ResourceQuotaRequest{
				"hw_version_hana_1_ram": {Quota: 99},
			},
			metadata: &liquid.ProjectMetadata{
				UUID: "project-update-meta",
				Name: "new-project-name",
				Domain: liquid.DomainMetadata{
					UUID: "new-domain",
					Name: "new-domain-name",
				},
			},
			expectQuota:   map[string]int64{"hw_version_hana_1_ram": 99},
			expectName:    "new-project-name",
			expectDomain:  "new-domain",
			expectDomName: "new-domain-name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			builder := fake.NewClientBuilder().WithScheme(scheme)

			if tc.existing != nil {
				tc.existing.Name = projectQuotaCRDName(tc.projectID)
				builder = builder.WithObjects(tc.existing)
			}
			k8sClient := builder.Build()
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

			// Verify the ProjectQuota CRD
			var pq v1alpha1.ProjectQuota
			crdName := projectQuotaCRDName(tc.projectID)
			if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: crdName}, &pq); err != nil {
				t.Fatalf("failed to get ProjectQuota CRD %q: %v", crdName, err)
			}

			if pq.Spec.ProjectID != tc.projectID {
				t.Errorf("expected ProjectID %q, got %q", tc.projectID, pq.Spec.ProjectID)
			}

			// Verify quota totals
			for resName, expectedTotal := range tc.expectQuota {
				actual, ok := pq.Spec.Quota[resName]
				if !ok {
					t.Errorf("expected resource %q in quota spec", resName)
					continue
				}
				if actual.Quota != expectedTotal {
					t.Errorf("resource %q: expected quota %d, got %d", resName, expectedTotal, actual.Quota)
				}
			}

			// Verify per-AZ quotas
			for resName, azMap := range tc.expectPerAZ {
				actual, ok := pq.Spec.Quota[resName]
				if !ok {
					t.Errorf("expected resource %q in quota spec for per-AZ check", resName)
					continue
				}
				for az, expectedAZ := range azMap {
					if actual.PerAZ[az] != expectedAZ {
						t.Errorf("resource %q AZ %q: expected %d, got %d", resName, az, expectedAZ, actual.PerAZ[az])
					}
				}
			}

			// Verify metadata
			if tc.expectName != "" && pq.Spec.ProjectName != tc.expectName {
				t.Errorf("expected ProjectName %q, got %q", tc.expectName, pq.Spec.ProjectName)
			}
			if tc.expectDomain != "" && pq.Spec.DomainID != tc.expectDomain {
				t.Errorf("expected DomainID %q, got %q", tc.expectDomain, pq.Spec.DomainID)
			}
			if tc.expectDomName != "" && pq.Spec.DomainName != tc.expectDomName {
				t.Errorf("expected DomainName %q, got %q", tc.expectDomName, pq.Spec.DomainName)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}
