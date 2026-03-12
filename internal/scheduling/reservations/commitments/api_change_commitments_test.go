// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/sapcc/go-api-declarations/liquid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TODO refactor with proper integration tests

func TestHandleChangeCommitments_VersionMismatch(t *testing.T) {
	// Create a fake Kubernetes client with a Knowledge CRD
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create a Knowledge CRD with a specific version timestamp and flavor groups
	knowledgeTimestamp := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	flavorGroup := createTestFlavorGroup()

	// Box the features using the Knowledge API
	rawExt, err := v1alpha1.BoxFeatureList([]compute.FlavorGroupFeature{flavorGroup})
	if err != nil {
		t.Fatalf("failed to box feature list: %v", err)
	}

	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: "flavor-groups",
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: "flavor-groups",
			},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastContentChange: metav1.Time{Time: knowledgeTimestamp},
			Raw:               rawExt,
			RawLength:         1,
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.KnowledgeConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "Ready",
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(knowledge).
		WithStatusSubresource(knowledge).
		Build()

	api := &HTTPAPI{
		client: k8sClient,
	}

	// Create request JSON with mismatched version
	requestJSON := `{
		"az": "az-a",
		"dryRun": false,
		"infoVersion": 12345,
		"byProject": {}
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/change-commitments", bytes.NewReader([]byte(requestJSON)))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	// Call the handler
	api.HandleChangeCommitments(w, req)

	// Check response
	resp := w.Result()
	defer resp.Body.Close()

	// Verify HTTP 409 Conflict status
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected status code %d (Conflict), got %d", http.StatusConflict, resp.StatusCode)
	}

	// Verify Content-Type is text/plain (set by http.Error)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/plain; charset=utf-8', got %q", contentType)
	}

	// Verify error message contains version information
	var responseBody bytes.Buffer
	if _, err = responseBody.ReadFrom(resp.Body); err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	bodyStr := responseBody.String()
	if !bytes.Contains([]byte(bodyStr), []byte("Version mismatch")) {
		t.Errorf("expected response to contain 'Version mismatch', got: %s", bodyStr)
	}
	if !bytes.Contains([]byte(bodyStr), []byte("12345")) {
		t.Errorf("expected response to contain request version '12345', got: %s", bodyStr)
	}
}
func TestHandleChangeCommitments_DryRun(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	api := &HTTPAPI{
		client: k8sClient,
	}

	// Create dry run request JSON
	requestJSON := `{
		"az": "az-a",
		"dryRun": true,
		"infoVersion": 12345,
		"byProject": {}
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/change-commitments", bytes.NewReader([]byte(requestJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleChangeCommitments(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Dry run should return 200 OK with rejection reason
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d (OK), got %d", http.StatusOK, resp.StatusCode)
	}

	// Verify response is JSON
	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", contentType)
	}

	// Parse response
	var response liquid.CommitmentChangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.RejectionReason != "Dry run not supported yet" {
		t.Errorf("expected rejection reason 'Dry run not supported yet', got %q", response.RejectionReason)
	}
}

func TestProcessCommitmentChanges_KnowledgeNotReady(t *testing.T) {
	// Test when flavor groups knowledge is not available
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// No Knowledge CRD created - simulates knowledge not ready
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	api := &HTTPAPI{
		client: k8sClient,
	}

	requestJSON := `{
		"az": "az-a",
		"dryRun": false,
		"infoVersion": 12345,
		"byProject": {}
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/change-commitments", bytes.NewReader([]byte(requestJSON)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	api.HandleChangeCommitments(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should return 200 OK with rejection reason
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d (OK), got %d", http.StatusOK, resp.StatusCode)
	}

	var response liquid.CommitmentChangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.RejectionReason != "caches not ready" {
		t.Errorf("expected rejection reason 'caches not ready', got %q", response.RejectionReason)
	}

	if response.RetryAt.IsNone() {
		t.Error("expected RetryAt to be set")
	}
}

// Helper function to create a minimal flavor group for testing
func createTestFlavorGroup() compute.FlavorGroupFeature {
	return compute.FlavorGroupFeature{
		Name: "test_group",
		Flavors: []compute.FlavorInGroup{
			{
				Name:     "test.small",
				MemoryMB: 8192,
				VCPUs:    2,
				DiskGB:   40,
				ExtraSpecs: map[string]string{
					"quota:separate": "true",
				},
			},
		},
		SmallestFlavor: compute.FlavorInGroup{
			Name:     "test.small",
			MemoryMB: 8192,
			VCPUs:    2,
			DiskGB:   40,
		},
	}
}
