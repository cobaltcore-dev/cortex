// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cobaltcore-dev/cortex/testlib"
)

// Mock implementation of Pipeline
type mockPipeline struct{}

func (m *mockPipeline) Run(state *pipelineState) ([]string, error) {
	return []string{"host1"}, nil
}

func TestCanRunScheduler(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	api := NewExternalSchedulingAPI(&mockDB).(*externalSchedulingAPI)

	tests := []struct {
		name    string
		request APINovaExternalSchedulerRequest
		wantOk  bool
	}{
		{
			name: "Rebuild not supported",
			request: APINovaExternalSchedulerRequest{
				Rebuild: true,
			},
			wantOk: false,
		},
		{
			name: "Multiple instances not supported",
			request: APINovaExternalSchedulerRequest{
				Spec: APINovaExternalSchedulerRequestSpec{
					NInstances: 2,
				},
			},
			wantOk: false,
		},
		{
			name: "Missing weight for host",
			request: APINovaExternalSchedulerRequest{
				Hosts: []APINovaExternalSchedulerRequestHost{
					{Name: "host1"},
				},
				Weights: map[string]float64{},
			},
			wantOk: false,
		},
		{
			name: "Weight assigned to unknown host",
			request: APINovaExternalSchedulerRequest{
				Hosts: []APINovaExternalSchedulerRequestHost{
					{Name: "host1"},
				},
				Weights: map[string]float64{
					"unknown_host": 1.0,
				},
			},
			wantOk: false,
		},
		{
			name: "Valid request",
			request: APINovaExternalSchedulerRequest{
				Hosts: []APINovaExternalSchedulerRequestHost{
					{Name: "host1"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotOk, _ := api.canRunScheduler(tt.request); gotOk != tt.wantOk {
				t.Errorf("canRunScheduler() gotOk = %v, want %v", gotOk, tt.wantOk)
			}
		})
	}
}

func TestHandler(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	// Mock the Pipeline
	mockPipeline := &mockPipeline{}

	api := &externalSchedulingAPI{
		Pipeline: mockPipeline,
	}

	tests := []struct {
		name           string
		method         string
		requestBody    APINovaExternalSchedulerRequest
		wantStatusCode int
		wantResponse   APINovaExternalSchedulerResponse
	}{
		{
			name:   "Invalid request method",
			method: http.MethodGet,
			requestBody: APINovaExternalSchedulerRequest{
				Spec: APINovaExternalSchedulerRequestSpec{
					ProjectID:  "project1",
					NInstances: 1,
				},
				Hosts: []APINovaExternalSchedulerRequestHost{
					{Name: "host1"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			name:   "Invalid request body",
			method: http.MethodPost,
			requestBody: APINovaExternalSchedulerRequest{
				Spec: APINovaExternalSchedulerRequestSpec{
					ProjectID:  "project1",
					NInstances: 1,
				},
				Hosts: []APINovaExternalSchedulerRequestHost{
					{Name: "host1"},
				},
				Weights: map[string]float64{
					"unknown_host": 1.0,
				},
			},
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:   "Valid request",
			method: http.MethodPost,
			requestBody: APINovaExternalSchedulerRequest{
				Spec: APINovaExternalSchedulerRequestSpec{
					ProjectID:  "project1",
					NInstances: 1,
				},
				Hosts: []APINovaExternalSchedulerRequestHost{
					{Name: "host1"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			wantStatusCode: http.StatusOK,
			wantResponse: APINovaExternalSchedulerResponse{
				Hosts: []string{"host1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestBody, _ := json.Marshal(tt.requestBody)
			req, err := http.NewRequest(tt.method, APINovaExternalSchedulerURL, bytes.NewBuffer(requestBody))
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(api.Handler)
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.wantStatusCode {
				t.Errorf("Handler() status code = %v, want %v", status, tt.wantStatusCode)
			}

			if tt.wantStatusCode == http.StatusOK {
				var gotResponse APINovaExternalSchedulerResponse
				if err := json.NewDecoder(rr.Body).Decode(&gotResponse); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				if len(gotResponse.Hosts) != len(tt.wantResponse.Hosts) {
					t.Fatalf("Handler() response length = %v, want %v", len(gotResponse.Hosts), len(tt.wantResponse.Hosts))
				}
				for i := range gotResponse.Hosts {
					if gotResponse.Hosts[i] != tt.wantResponse.Hosts[i] {
						t.Fatalf("Handler() response[%d] = %v, want %v", i, gotResponse.Hosts[i], tt.wantResponse.Hosts[i])
					}
				}
			}
		})
	}
}
