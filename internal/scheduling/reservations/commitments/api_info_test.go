// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleInfo_KnowledgeNotReady(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/v1/info", http.NoBody)
	w := httptest.NewRecorder()

	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should return 503 Service Unavailable when knowledge is not ready
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status code %d (Service Unavailable), got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}

	// Verify Content-Type is text/plain (set by http.Error)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/plain; charset=utf-8', got %q", contentType)
	}
}

func TestHandleInfo_MethodNotAllowed(t *testing.T) {
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

	// Use POST instead of GET
	req := httptest.NewRequest(http.MethodPost, "/v1/info", http.NoBody)
	w := httptest.NewRecorder()

	api.HandleInfo(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status code %d (Method Not Allowed), got %d", http.StatusMethodNotAllowed, resp.StatusCode)
	}
}
