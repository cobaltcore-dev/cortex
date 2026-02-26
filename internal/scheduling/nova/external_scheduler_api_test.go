// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockHTTPAPIDelegate struct {
	processDecisionFunc func(ctx context.Context, decision *v1alpha1.Decision) error
}

func (m *mockHTTPAPIDelegate) ProcessNewDecisionFromAPI(ctx context.Context, decision *v1alpha1.Decision) error {
	if m.processDecisionFunc != nil {
		return m.processDecisionFunc(ctx, decision)
	}
	return nil
}

func TestNewAPI(t *testing.T) {
	delegate := &mockHTTPAPIDelegate{}
	config := HTTPAPIConfig{}

	api := NewAPI(config, delegate)

	if api == nil {
		t.Fatal("NewAPI returned nil")
	}

	httpAPI, ok := api.(*httpAPI)
	if !ok {
		t.Fatal("NewAPI did not return httpAPI type")
	}

	if httpAPI.delegate != delegate {
		t.Error("Delegate not set correctly")
	}

	if httpAPI.monitor.ApiRequestsTimer == nil {
		t.Error("Monitor not initialized")
	}
}

func TestHTTPAPI_Init(t *testing.T) {
	delegate := &mockHTTPAPIDelegate{}
	config := HTTPAPIConfig{}
	api := NewAPI(config, delegate)

	mux := http.NewServeMux()
	api.Init(mux)

	// Test that the handler is registered by making a request
	req := httptest.NewRequest(http.MethodGet, "/scheduler/nova/external", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should get method not allowed since we're using GET instead of POST
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestHTTPAPI_canRunScheduler(t *testing.T) {
	delegate := &mockHTTPAPIDelegate{}
	config := HTTPAPIConfig{}
	api := NewAPI(config, delegate).(*httpAPI)

	tests := []struct {
		name        string
		requestData novaapi.ExternalSchedulerRequest
		expectedOk  bool
		expectedMsg string
	}{
		{
			name: "valid request",
			requestData: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
					"host2": 2.0,
				},
			},
			expectedOk: true,
		},
		{
			name: "missing weight for host",
			requestData: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			expectedOk:  false,
			expectedMsg: "missing weight for host",
		},
		{
			name: "weight for unknown host",
			requestData: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
				Weights: map[string]float64{
					"host1":   1.0,
					"unknown": 2.0,
				},
			},
			expectedOk:  false,
			expectedMsg: "weight assigned to unknown host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := api.canRunScheduler(tt.requestData)

			if ok != tt.expectedOk {
				t.Errorf("Expected ok=%v, got %v", tt.expectedOk, ok)
			}

			if !tt.expectedOk && reason != tt.expectedMsg {
				t.Errorf("Expected reason '%s', got '%s'", tt.expectedMsg, reason)
			}
		})
	}
}

func TestHTTPAPI_NovaExternalScheduler(t *testing.T) {
	tests := []struct {
		name               string
		method             string
		body               string
		processDecisionErr error
		decisionResult     *v1alpha1.Decision
		expectedStatus     int
		expectedHosts      []string
	}{
		{
			name:           "invalid method",
			method:         http.MethodGet,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "invalid JSON body",
			method:         http.MethodPost,
			body:           "invalid json",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "valid request with successful processing",
			method: http.MethodPost,
			body: func() string {
				req := novaapi.ExternalSchedulerRequest{
					Spec: novaapi.NovaObject[novaapi.NovaSpec]{
						Data: novaapi.NovaSpec{
							InstanceUUID: "test-uuid",
						},
					},
					Hosts: []novaapi.ExternalSchedulerHost{
						{ComputeHost: "host1"},
					},
					Weights: map[string]float64{
						"host1": 1.0,
					},
					Pipeline: "test-pipeline",
				}
				data, err := json.Marshal(req)
				if err != nil {
					t.Fatalf("Failed to marshal request data: %v", err)
				}
				return string(data)
			}(),
			decisionResult: &v1alpha1.Decision{
				Status: v1alpha1.DecisionStatus{
					Result: &v1alpha1.DecisionResult{
						OrderedHosts: []string{"host1"},
					},
				},
			},
			expectedStatus: http.StatusOK,
			expectedHosts:  []string{"host1"},
		},
		{
			name:   "processing error",
			method: http.MethodPost,
			body: func() string {
				req := novaapi.ExternalSchedulerRequest{
					Spec: novaapi.NovaObject[novaapi.NovaSpec]{
						Data: novaapi.NovaSpec{
							InstanceUUID: "test-uuid",
						},
					},
					Hosts: []novaapi.ExternalSchedulerHost{
						{ComputeHost: "host1"},
					},
					Weights: map[string]float64{
						"host1": 1.0,
					},
					Pipeline: "test-pipeline",
				}
				data, err := json.Marshal(req)
				if err != nil {
					t.Fatalf("Failed to marshal request data: %v", err)
				}
				return string(data)
			}(),
			processDecisionErr: errors.New("processing failed"),
			expectedStatus:     http.StatusInternalServerError,
		},
		{
			name:   "decision failed",
			method: http.MethodPost,
			body: func() string {
				req := novaapi.ExternalSchedulerRequest{
					Spec: novaapi.NovaObject[novaapi.NovaSpec]{
						Data: novaapi.NovaSpec{
							InstanceUUID: "test-uuid",
						},
					},
					Hosts: []novaapi.ExternalSchedulerHost{
						{ComputeHost: "host1"},
					},
					Weights: map[string]float64{
						"host1": 1.0,
					},
					Pipeline: "test-pipeline",
				}
				data, err := json.Marshal(req)
				if err != nil {
					t.Fatalf("Failed to marshal request data: %v", err)
				}
				return string(data)
			}(),
			decisionResult: &v1alpha1.Decision{
				Status: v1alpha1.DecisionStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.DecisionConditionReady,
							Status: metav1.ConditionFalse,
							Reason: "SchedulingError",
						},
					},
				},
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delegate := &mockHTTPAPIDelegate{
				processDecisionFunc: func(ctx context.Context, decision *v1alpha1.Decision) error {
					if tt.processDecisionErr != nil {
						return tt.processDecisionErr
					}
					if tt.decisionResult != nil {
						decision.Status = tt.decisionResult.Status
						return nil
					}
					return nil
				},
			}

			config := HTTPAPIConfig{}
			api := NewAPI(config, delegate).(*httpAPI)

			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			} else {
				body = strings.NewReader("")
			}

			req := httptest.NewRequest(tt.method, "/scheduler/nova/external", body)
			w := httptest.NewRecorder()

			api.NovaExternalScheduler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectedStatus == http.StatusOK && len(tt.expectedHosts) > 0 {
				var response novaapi.ExternalSchedulerResponse
				if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}

				if len(response.Hosts) != len(tt.expectedHosts) {
					t.Errorf("Expected %d hosts, got %d", len(tt.expectedHosts), len(response.Hosts))
				}

				for i, expectedHost := range tt.expectedHosts {
					if i < len(response.Hosts) && response.Hosts[i] != expectedHost {
						t.Errorf("Expected host[%d] = %s, got %s", i, expectedHost, response.Hosts[i])
					}
				}
			}
		})
	}
}

func TestHTTPAPI_NovaExternalScheduler_DecisionCreation(t *testing.T) {
	var capturedDecision *v1alpha1.Decision
	delegate := &mockHTTPAPIDelegate{
		processDecisionFunc: func(ctx context.Context, decision *v1alpha1.Decision) error {
			capturedDecision = decision
			// Set a successful result to avoid "decision didn't produce a result" error
			decision.Status.Result = &v1alpha1.DecisionResult{
				OrderedHosts: []string{"host1"},
			}
			return nil
		},
	}

	config := HTTPAPIConfig{}
	api := NewAPI(config, delegate).(*httpAPI)

	requestData := novaapi.ExternalSchedulerRequest{
		Spec: novaapi.NovaObject[novaapi.NovaSpec]{
			Data: novaapi.NovaSpec{
				InstanceUUID: "test-uuid-123",
			},
		},
		Hosts: []novaapi.ExternalSchedulerHost{
			{ComputeHost: "host1"},
		},
		Weights: map[string]float64{
			"host1": 1.0,
		},
		Pipeline: "test-pipeline",
	}

	body, err := json.Marshal(requestData)
	if err != nil {
		t.Fatalf("Failed to marshal request data: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/nova/external", bytes.NewReader(body))
	w := httptest.NewRecorder()

	api.NovaExternalScheduler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if capturedDecision == nil {
		t.Fatal("Decision was not captured")
	}

	// Verify decision fields
	if capturedDecision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
		t.Errorf("Expected scheduling domain %s, got %s", v1alpha1.SchedulingDomainNova, capturedDecision.Spec.SchedulingDomain)
	}

	if capturedDecision.Spec.PipelineRef.Name != "test-pipeline" {
		t.Errorf("Expected pipeline 'test-pipeline', got %s", capturedDecision.Spec.PipelineRef.Name)
	}

	if capturedDecision.Spec.ResourceID != "test-uuid-123" {
		t.Errorf("Expected resource ID 'test-uuid-123', got %s", capturedDecision.Spec.ResourceID)
	}

	if capturedDecision.GenerateName != "nova-" {
		t.Errorf("Expected generate name 'nova-', got %s", capturedDecision.GenerateName)
	}

	if capturedDecision.Spec.NovaRaw == nil {
		t.Error("NovaRaw should not be nil")
	}
}

func TestLimitHostsToRequest(t *testing.T) {
	tests := []struct {
		name          string
		request       novaapi.ExternalSchedulerRequest
		hosts         []string
		expectedHosts []string
	}{
		{
			name: "all hosts in request",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hosts:         []string{"host1", "host2", "host3"},
			expectedHosts: []string{"host1", "host2", "host3"},
		},
		{
			name: "some hosts not in request - filtered out",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host3"},
				},
			},
			hosts:         []string{"host1", "host2", "host3"},
			expectedHosts: []string{"host1", "host3"},
		},
		{
			name: "no hosts in request - all filtered out",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{},
			},
			hosts:         []string{"host1", "host2"},
			expectedHosts: nil,
		},
		{
			name: "empty hosts input",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hosts:         []string{},
			expectedHosts: nil,
		},
		{
			name: "nil hosts input",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			hosts:         nil,
			expectedHosts: nil,
		},
		{
			name: "preserves order of input hosts",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hosts:         []string{"host3", "host1", "host2"},
			expectedHosts: []string{"host3", "host1", "host2"},
		},
		{
			name: "duplicate hosts in input - all kept if in request",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hosts:         []string{"host1", "host1", "host2"},
			expectedHosts: []string{"host1", "host1", "host2"},
		},
		{
			name: "host only in response not in request - filtered out",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
				},
			},
			hosts:         []string{"host1", "unknown-host"},
			expectedHosts: []string{"host1"},
		},
		{
			name: "all hosts unknown - all filtered out",
			request: novaapi.ExternalSchedulerRequest{
				Hosts: []novaapi.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hosts:         []string{"unknown1", "unknown2"},
			expectedHosts: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := limitHostsToRequest(tt.request, tt.hosts)

			if len(result) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result))
				return
			}

			for i, expectedHost := range tt.expectedHosts {
				if result[i] != expectedHost {
					t.Errorf("expected host[%d] = %s, got %s", i, expectedHost, result[i])
				}
			}
		})
	}
}

func TestHTTPAPI_inferPipelineName(t *testing.T) {
	delegate := &mockHTTPAPIDelegate{}
	config := HTTPAPIConfig{
		ExperimentalProjectIDs: []string{"my-experimental-project-id"},
	}
	api := NewAPI(config, delegate).(*httpAPI)

	tests := []struct {
		name           string
		requestData    novaapi.ExternalSchedulerRequest
		expectedResult string
		expectErr      bool
		errContains    string
	}{
		// KVM/QEMU general purpose tests
		{
			name: "qemu hypervisor general purpose without reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-general-purpose-load-balancing",
			expectErr:      false,
		},
		{
			name: "QEMU hypervisor uppercase general purpose",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "QEMU",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-general-purpose-load-balancing",
			expectErr:      false,
		},
		{
			name: "qemu hypervisor general purpose with reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: true,
			},
			expectedResult: "kvm-general-purpose-load-balancing-all-filters-enabled",
			expectErr:      false,
		},
		{
			name: "experimental project ID requesting kvm general purpose vm",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						ProjectID: "my-experimental-project-id",
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-general-purpose-load-balancing-all-filters-enabled",
			expectErr:      false,
		},
		// KVM/QEMU HANA tests
		{
			name: "qemu hypervisor HANA without reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-hana-bin-packing",
			expectErr:      false,
		},
		{
			name: "qemu hypervisor HANA with reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
								},
							},
						},
					},
				},
				Reservation: true,
			},
			expectedResult: "kvm-hana-bin-packing-all-filters-enabled",
			expectErr:      false,
		},
		{
			name: "experimental project ID requesting kvm HANA vm",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						ProjectID: "my-experimental-project-id",
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-hana-bin-packing-all-filters-enabled",
			expectErr:      false,
		},
		// CH hypervisor tests
		{
			name: "ch hypervisor general purpose without reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "ch",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-general-purpose-load-balancing",
			expectErr:      false,
		},
		{
			name: "ch hypervisor general purpose with reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "ch",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: true,
			},
			expectedResult: "kvm-general-purpose-load-balancing-all-filters-enabled",
			expectErr:      false,
		},
		{
			name: "ch hypervisor HANA without reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "ch",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "kvm-hana-bin-packing",
			expectErr:      false,
		},
		{
			name: "ch hypervisor HANA with reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "ch",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
								},
							},
						},
					},
				},
				Reservation: true,
			},
			expectedResult: "kvm-hana-bin-packing-all-filters-enabled",
			expectErr:      false,
		},
		// VMware tests
		{
			name: "vmware hypervisor general purpose without reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "VMware vCenter Server",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "vmware-general-purpose-load-balancing",
			expectErr:      false,
		},
		{
			name: "vmware hypervisor HANA without reservation",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "VMware vCenter Server",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectedResult: "vmware-hana-bin-packing",
			expectErr:      false,
		},
		{
			name: "vmware hypervisor with reservation - error",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "VMware vCenter Server",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: true,
			},
			expectErr:   true,
			errContains: "reservations are not supported on vmware hypervisors",
		},
		// Error cases
		{
			name: "missing hypervisor_type",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectErr:   true,
			errContains: "failed to determine hypervisor type from request data",
		},
		{
			name: "unsupported hypervisor_type",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "unknown-hypervisor",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "forbidden",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectErr:   true,
			errContains: "failed to determine hypervisor type from request data",
		},
		{
			name: "missing flavor type trait",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type": "qemu",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectErr:      false, // should infer general purpose.
			expectedResult: "kvm-general-purpose-load-balancing",
			errContains:    "",
		},
		{
			name: "unsupported flavor type trait value",
			requestData: novaapi.ExternalSchedulerRequest{
				Spec: novaapi.NovaObject[novaapi.NovaSpec]{
					Data: novaapi.NovaSpec{
						Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
							Data: novaapi.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:hypervisor_type":     "qemu",
									"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "invalid",
								},
							},
						},
					},
				},
				Reservation: false,
			},
			expectErr:   true,
			errContains: "failed to determine flavor type from request data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := api.inferPipelineName(tt.requestData)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error but got none")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain '%s', got '%s'", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expectedResult {
					t.Errorf("expected pipeline name '%s', got '%s'", tt.expectedResult, result)
				}
			}
		})
	}
}
