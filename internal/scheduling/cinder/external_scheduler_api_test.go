// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cinderapi "github.com/cobaltcore-dev/cortex/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"

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
	config := conf.Config{SchedulingDomain: "test-operator"}
	delegate := &mockHTTPAPIDelegate{}

	api := NewAPI(config, delegate)

	if api == nil {
		t.Fatal("NewAPI returned nil")
	}

	httpAPI, ok := api.(*httpAPI)
	if !ok {
		t.Fatal("NewAPI did not return httpAPI type")
	}

	if httpAPI.config.SchedulingDomain != "test-operator" {
		t.Errorf("Expected scheduling domain 'test-operator', got %s", httpAPI.config.SchedulingDomain)
	}

	if httpAPI.delegate != delegate {
		t.Error("Delegate not set correctly")
	}

	if httpAPI.monitor.ApiRequestsTimer == nil {
		t.Error("Monitor not initialized")
	}
}

func TestHTTPAPI_Init(t *testing.T) {
	config := conf.Config{SchedulingDomain: "test-operator"}
	delegate := &mockHTTPAPIDelegate{}
	api := NewAPI(config, delegate)

	mux := http.NewServeMux()
	api.Init(mux)

	// Test that the handler is registered by making a request
	req := httptest.NewRequest(http.MethodGet, "/scheduler/cinder/external", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should get method not allowed since we're using GET instead of POST
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestHTTPAPI_canRunScheduler(t *testing.T) {
	config := conf.Config{SchedulingDomain: "test-operator"}
	delegate := &mockHTTPAPIDelegate{}
	api := NewAPI(config, delegate).(*httpAPI)

	tests := []struct {
		name        string
		requestData cinderapi.ExternalSchedulerRequest
		expectedOk  bool
		expectedMsg string
	}{
		{
			name: "valid request",
			requestData: cinderapi.ExternalSchedulerRequest{
				Hosts: []cinderapi.ExternalSchedulerHost{
					{VolumeHost: "host1"},
					{VolumeHost: "host2"},
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
			requestData: cinderapi.ExternalSchedulerRequest{
				Hosts: []cinderapi.ExternalSchedulerHost{
					{VolumeHost: "host1"},
					{VolumeHost: "host2"},
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
			requestData: cinderapi.ExternalSchedulerRequest{
				Hosts: []cinderapi.ExternalSchedulerHost{
					{VolumeHost: "host1"},
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

func TestHTTPAPI_CinderExternalScheduler(t *testing.T) {
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
				req := cinderapi.ExternalSchedulerRequest{
					Hosts: []cinderapi.ExternalSchedulerHost{
						{VolumeHost: "host1"},
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
				req := cinderapi.ExternalSchedulerRequest{
					Hosts: []cinderapi.ExternalSchedulerHost{
						{VolumeHost: "host1"},
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
				req := cinderapi.ExternalSchedulerRequest{
					Hosts: []cinderapi.ExternalSchedulerHost{
						{VolumeHost: "host1"},
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
			config := conf.Config{SchedulingDomain: "test-operator"}
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

			api := NewAPI(config, delegate).(*httpAPI)

			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			} else {
				body = strings.NewReader("")
			}

			req := httptest.NewRequest(tt.method, "/scheduler/cinder/external", body)
			w := httptest.NewRecorder()

			api.CinderExternalScheduler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectedStatus == http.StatusOK && len(tt.expectedHosts) > 0 {
				var response cinderapi.ExternalSchedulerResponse
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

func TestHTTPAPI_inferPipelineName(t *testing.T) {
	config := conf.Config{SchedulingDomain: "test-operator"}
	delegate := &mockHTTPAPIDelegate{}
	api := NewAPI(config, delegate).(*httpAPI)

	tests := []struct {
		name         string
		request      cinderapi.ExternalSchedulerRequest
		expectedName string
		expectError  bool
	}{
		{
			name: "returns default pipeline name",
			request: cinderapi.ExternalSchedulerRequest{
				Hosts: []cinderapi.ExternalSchedulerHost{
					{VolumeHost: "host1"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			expectedName: "cinder-external-scheduler",
			expectError:  false,
		},
		{
			name:         "returns default pipeline name for empty request",
			request:      cinderapi.ExternalSchedulerRequest{},
			expectedName: "cinder-external-scheduler",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipelineName, err := api.inferPipelineName(tt.request)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if pipelineName != tt.expectedName {
				t.Errorf("expected pipeline name %s, got %s", tt.expectedName, pipelineName)
			}
		})
	}
}

func TestHTTPAPI_CinderExternalScheduler_DecisionCreation(t *testing.T) {
	config := conf.Config{SchedulingDomain: v1alpha1.SchedulingDomainCinder}

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

	api := NewAPI(config, delegate).(*httpAPI)

	requestData := cinderapi.ExternalSchedulerRequest{
		Hosts: []cinderapi.ExternalSchedulerHost{
			{VolumeHost: "host1"},
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
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader(body))
	w := httptest.NewRecorder()

	api.CinderExternalScheduler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if capturedDecision == nil {
		t.Fatal("Decision was not captured")
	}

	// Verify decision fields
	if capturedDecision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainCinder {
		t.Errorf("Expected scheduling domain %s, got %s", v1alpha1.SchedulingDomainCinder, capturedDecision.Spec.SchedulingDomain)
	}

	if capturedDecision.Spec.PipelineRef.Name != "test-pipeline" {
		t.Errorf("Expected pipeline 'test-pipeline', got %s", capturedDecision.Spec.PipelineRef.Name)
	}

	if capturedDecision.GenerateName != "cinder-" {
		t.Errorf("Expected generate name 'cinder-', got %s", capturedDecision.GenerateName)
	}

	if capturedDecision.Spec.CinderRaw == nil {
		t.Error("CinderRaw should not be nil")
	}
}
