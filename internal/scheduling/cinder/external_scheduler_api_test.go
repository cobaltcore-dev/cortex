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

	cinderapi "github.com/cobaltcore-dev/cortex/api/external/cinder"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
)

type mockHTTPAPIDelegate struct {
	processFunc func(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error)
}

func (m *mockHTTPAPIDelegate) ProcessRequest(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error) {
	if m.processFunc != nil {
		return m.processFunc(ctx, request)
	}
	return &lib.FilterWeigherPipelineResult{
		OrderedHosts: []string{"host1"},
	}, nil
}

func TestNewAPI(t *testing.T) {
	delegate := &mockHTTPAPIDelegate{}

	api := NewAPI(delegate)

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
	api := NewAPI(delegate)

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
	delegate := &mockHTTPAPIDelegate{}
	api := NewAPI(delegate).(*httpAPI)

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
		name           string
		method         string
		body           string
		processFunc    func(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error)
		expectedStatus int
		expectedHosts  []string
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
						{VolumeHost: "host2"},
					},
					Weights: map[string]float64{
						"host1": 1.0,
						"host2": 0.5,
					},
					Pipeline: "test-pipeline",
				}
				data, err := json.Marshal(req)
				if err != nil {
					t.Fatalf("Failed to marshal request data: %v", err)
				}
				return string(data)
			}(),
			processFunc: func(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error) {
				return &lib.FilterWeigherPipelineResult{
					OrderedHosts: []string{"host1", "host2"},
				}, nil
			},
			expectedStatus: http.StatusOK,
			expectedHosts:  []string{"host1", "host2"},
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
			processFunc: func(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error) {
				return nil, errors.New("processing failed")
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:   "empty result",
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
			processFunc: func(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error) {
				return &lib.FilterWeigherPipelineResult{
					OrderedHosts: []string{},
				}, nil
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delegate := &mockHTTPAPIDelegate{
				processFunc: tt.processFunc,
			}
			api := NewAPI(delegate).(*httpAPI)

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
	delegate := &mockHTTPAPIDelegate{}
	api := NewAPI(delegate).(*httpAPI)

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

func TestHTTPAPI_CinderExternalScheduler_PipelineParameter(t *testing.T) {
	var capturedPipeline string
	var capturedRequest cinderapi.ExternalSchedulerRequest

	delegate := &mockHTTPAPIDelegate{
		processFunc: func(ctx context.Context, request cinderapi.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineResult, error) {
			capturedPipeline = request.Pipeline
			capturedRequest = request
			return &lib.FilterWeigherPipelineResult{
				OrderedHosts: []string{"host1"},
			}, nil
		},
	}

	api := NewAPI(delegate).(*httpAPI)

	requestData := cinderapi.ExternalSchedulerRequest{
		Hosts: []cinderapi.ExternalSchedulerHost{
			{VolumeHost: "host1"},
		},
		Weights: map[string]float64{
			"host1": 1.0,
		},
		Pipeline: "test-pipeline",
		Spec: map[string]any{
			"volume_id": "test-volume",
		},
	}

	body, err := json.Marshal(requestData)
	if err != nil {
		t.Fatalf("Failed to marshal request data: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/scheduler/cinder/external", bytes.NewReader(body))
	w := httptest.NewRecorder()

	api.CinderExternalScheduler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Verify the pipeline name was passed correctly
	expectedPipeline := "cinder-external-scheduler" // Default pipeline from inferPipelineName
	if capturedPipeline != expectedPipeline {
		t.Errorf("Expected pipeline '%s', got '%s'", expectedPipeline, capturedPipeline)
	}

	// Verify the request was passed correctly
	if len(capturedRequest.Hosts) != 1 {
		t.Errorf("Expected 1 host, got %d", len(capturedRequest.Hosts))
	}
	if capturedRequest.Hosts[0].VolumeHost != "host1" {
		t.Errorf("Expected host 'host1', got '%s'", capturedRequest.Hosts[0].VolumeHost)
	}
}
