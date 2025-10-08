// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	libconf "github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	delegationAPI "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/lib"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/nova/api"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/liquid"
)

// Mock implementation of Pipeline
type mockExternalSchedulerPipeline struct{}

func (m *mockExternalSchedulerPipeline) Run(request api.PipelineRequest) ([]string, error) {
	return []string{"host1"}, nil
}

func (m *mockExternalSchedulerPipeline) SetConsumer(consumer lib.SchedulingDecisionConsumer[api.PipelineRequest]) {
	// Do nothing
}

func (m *mockExternalSchedulerPipeline) Consume(
	request api.PipelineRequest,
	applicationOrder []string,
	inWeights map[string]float64,
	stepWeights map[string]map[string]float64,
) {
	// Do nothing
}

func TestCanRunScheduler(t *testing.T) {
	httpAPI := &httpAPI{
		pipelines: map[string]lib.Pipeline[api.PipelineRequest]{
			"default": &mockExternalSchedulerPipeline{},
		},
	}

	tests := []struct {
		name    string
		request api.PipelineRequest
		wantOk  bool
	}{
		{
			name: "Missing weight for host",
			request: api.PipelineRequest{
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
				},
				Weights: map[string]float64{},
			},
			wantOk: false,
		},
		{
			name: "Weight assigned to unknown host",
			request: api.PipelineRequest{
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
				},
				Weights: map[string]float64{
					"unknown_host": 1.0,
				},
			},
			wantOk: false,
		},
		{
			name: "Unsupported baremetal flavor",
			request: api.PipelineRequest{
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
				},
				Weights: map[string]float64{
					"unknown_host": 1.0,
				},
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						Flavor: delegationAPI.NovaObject[delegationAPI.NovaFlavor]{
							Data: delegationAPI.NovaFlavor{
								ExtraSpecs: map[string]string{
									"capabilities:cpu_arch": "x86_64",
								},
							},
						},
					},
				},
			},
			wantOk: false,
		},
		{
			name: "Unsupported resize request",
			request: api.PipelineRequest{
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
				},
				VMware: true,
				Weights: map[string]float64{
					"host1": 1.0,
				},
				Resize: true,
			},
			wantOk: false,
		},
		{
			name: "Valid request",
			request: api.PipelineRequest{
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
				},
				VMware: true,
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotOk, _ := httpAPI.canRunScheduler(tt.request); gotOk != tt.wantOk {
				t.Errorf("canRunScheduler() gotOk = %v, want %v", gotOk, tt.wantOk)
			}
		})
	}
}

func TestHandleExternalSchedulerRequest(t *testing.T) {
	// Mock the Pipeline
	mockPipeline := &mockExternalSchedulerPipeline{}

	httpAPI := &httpAPI{
		pipelines: map[string]lib.Pipeline[api.PipelineRequest]{
			"default": mockPipeline,
		},
	}

	tests := []struct {
		name           string
		method         string
		requestBody    api.PipelineRequest
		wantStatusCode int
		wantResponse   delegationAPI.ExternalSchedulerResponse
	}{
		{
			name:   "Invalid request method",
			method: http.MethodGet,
			requestBody: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID:    "project1",
						NumInstances: 1,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
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
			requestBody: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID:    "project1",
						NumInstances: 1,
					},
				},
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
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
			requestBody: api.PipelineRequest{
				Spec: delegationAPI.NovaObject[delegationAPI.NovaSpec]{
					Data: delegationAPI.NovaSpec{
						ProjectID:    "project1",
						NumInstances: 1,
					},
				},
				VMware: true,
				Hosts: []delegationAPI.ExternalSchedulerHost{
					{ComputeHost: "host1", HypervisorHostname: "hypervisor1"},
				},
				Weights: map[string]float64{
					"host1": 1.0,
				},
			},
			wantStatusCode: http.StatusOK,
			wantResponse: delegationAPI.ExternalSchedulerResponse{
				Hosts: []string{"host1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestBody, err := json.Marshal(tt.requestBody)
			if err != nil {
				t.Fatalf("failed to marshal request body: %v", err)
			}
			req, err := http.NewRequestWithContext(
				t.Context(), tt.method,
				"/scheduler/nova/external", bytes.NewBuffer(requestBody),
			)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(httpAPI.NovaExternalScheduler)
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.wantStatusCode {
				t.Errorf("Handler() status code = %v, want %v", status, tt.wantStatusCode)
			}

			if tt.wantStatusCode == http.StatusOK {
				var gotResponse delegationAPI.ExternalSchedulerResponse
				if err := json.NewDecoder(rr.Body).Decode(&gotResponse); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if len(gotResponse.Hosts) != len(tt.wantResponse.Hosts) {
					t.Fatalf(
						"Handler() response length = %v, want %v",
						len(gotResponse.Hosts), len(tt.wantResponse.Hosts),
					)
				}
				for i := range gotResponse.Hosts {
					if gotResponse.Hosts[i] != tt.wantResponse.Hosts[i] {
						t.Fatalf(
							"Handler() response[%d] = %v, want %v",
							i, gotResponse.Hosts[i], tt.wantResponse.Hosts[i],
						)
					}
				}
			}
		})
	}
}

type mockCommitmentsPipeline struct {
	shouldReturnHosts bool
	shouldError       bool
}

func (p *mockCommitmentsPipeline) SetConsumer(consumer lib.SchedulingDecisionConsumer[api.PipelineRequest]) {

}

func (p *mockCommitmentsPipeline) Consume(
	request api.PipelineRequest,
	applicationOrder []string,
	inWeights map[string]float64,
	stepWeights map[string]map[string]float64,
) {
}

func (p *mockCommitmentsPipeline) Run(request api.PipelineRequest) ([]string, error) {
	if p.shouldError {
		return nil, errors.New("mock error")
	}
	if p.shouldReturnHosts {
		return []string{"host1"}, nil
	}
	return []string{}, nil
}

func setupCommitmentsTestAPI(t *testing.T) (*httpAPI, db.DB) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}

	// Create flavor table
	err := testDB.CreateTable(testDB.AddTable(nova.Flavor{}))
	if err != nil {
		t.Fatalf("failed to create flavor table: %v", err)
	}

	config := conf.SchedulerConfig{
		Nova: conf.NovaSchedulerConfig{
			LiquidAPI: conf.NovaSchedulerLiquidAPIConfig{
				Hypervisors: []string{"qemu", "vmware"},
			},
			Pipelines: []conf.NovaSchedulerPipelineConfig{
				{Name: "reservations"},
			},
		},
		API: conf.SchedulerAPIConfig{},
	}

	registry := monitoring.NewRegistry(libconf.MonitoringConfig{})

	// Create mock pipeline
	mockPipeline := &mockCommitmentsPipeline{}
	httpAPI := &httpAPI{
		pipelines: map[string]lib.Pipeline[api.PipelineRequest]{
			"reservations": mockPipeline,
		},
		config:  config,
		monitor: lib.NewSchedulerMonitor(registry),
		DB:      testDB,
	}

	return httpAPI, testDB
}

func TestHandleCommitmentChangeRequest(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		requestBody    liquid.CommitmentChangeRequest
		setupFlavors   []nova.Flavor
		setupPipeline  func(*mockCommitmentsPipeline)
		expectedStatus int
		expectedReason string
	}{
		{
			name:           "non-POST method",
			method:         "GET",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:   "no confirmation required",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_test": {
								TotalConfirmedBefore: 5,
								TotalConfirmedAfter:  5, // No change
							},
						},
					},
				},
			},
			expectedStatus: http.StatusOK,
			expectedReason: "",
		},
		{
			name:   "no reservations pipeline",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_test": {
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  1, // Requires confirmation
							},
						},
					},
				},
			},
			setupPipeline: func(p *mockCommitmentsPipeline) {
				// Remove reservations pipeline by setting up API without it
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:   "no flavors available",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_test": {
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  1,
							},
						},
					},
				},
			},
			setupFlavors:   []nova.Flavor{}, // Empty flavors
			expectedStatus: http.StatusOK,
			expectedReason: "cortex has no flavor information yet, please retry later",
		},
		{
			name:   "flavor not found",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_missing": {
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  1,
							},
						},
					},
				},
			},
			setupFlavors: []nova.Flavor{
				{Name: "test", ExtraSpecs: `{"capabilities:hypervisor_type": "qemu"}`},
			},
			expectedStatus: http.StatusOK,
			expectedReason: "possible inconsistency between nova and limes, flavor not found: missing",
		},
		{
			name:   "non-instance commitment ignored",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"gpus": { // Not instances_*
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  10,
							},
						},
					},
				},
			},
			setupFlavors: []nova.Flavor{
				{Name: "dummy", ExtraSpecs: `{"capabilities:hypervisor_type": "qemu"}`}, // Add at least one flavor
			},
			expectedStatus: http.StatusOK,
			expectedReason: "",
		},
		{
			name:   "pipeline execution fails",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ProjectMetadata: option.Some(liquid.ProjectMetadata{
							UUID: "project1",
							Domain: liquid.DomainMetadata{
								UUID: "domain1",
							},
						}),
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_test": {
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  1,
							},
						},
					},
				},
			},
			setupFlavors: []nova.Flavor{
				{Name: "test", ExtraSpecs: `{"capabilities:hypervisor_type": "qemu"}`},
			},
			setupPipeline: func(p *mockCommitmentsPipeline) {
				p.shouldError = true
			},
			expectedStatus: http.StatusOK,
			expectedReason: "cortex pipeline failed to execute, please try again",
		},
		{
			name:   "no space for commitment",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ProjectMetadata: option.Some(liquid.ProjectMetadata{
							UUID: "project1",
							Domain: liquid.DomainMetadata{
								UUID: "domain1",
							},
						}),
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_test": {
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  1,
							},
						},
					},
				},
			},
			setupFlavors: []nova.Flavor{
				{Name: "test", ExtraSpecs: `{"capabilities:hypervisor_type": "qemu"}`},
			},
			setupPipeline: func(p *mockCommitmentsPipeline) {
				p.shouldReturnHosts = false // No hosts available
			},
			expectedStatus: http.StatusOK,
			expectedReason: "no space for this commitment",
		},
		{
			name:   "successful commitment approval",
			method: "POST",
			requestBody: liquid.CommitmentChangeRequest{
				AZ: "eu-de-1a",
				ByProject: map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset{
					"project1": {
						ProjectMetadata: option.Some(liquid.ProjectMetadata{
							UUID: "project1",
							Domain: liquid.DomainMetadata{
								UUID: "domain1",
							},
						}),
						ByResource: map[liquid.ResourceName]liquid.ResourceCommitmentChangeset{
							"instances_test": {
								TotalConfirmedBefore: 0,
								TotalConfirmedAfter:  1,
							},
						},
					},
				},
			},
			setupFlavors: []nova.Flavor{
				{Name: "test", ExtraSpecs: `{"capabilities:hypervisor_type": "qemu"}`},
			},
			setupPipeline: func(p *mockCommitmentsPipeline) {
				p.shouldReturnHosts = true
			},
			expectedStatus: http.StatusOK,
			expectedReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpAPI, testDB := setupCommitmentsTestAPI(t)
			defer testDB.Close()

			// Setup flavors if provided
			if tt.setupFlavors != nil {
				for _, flavor := range tt.setupFlavors {
					if err := testDB.Insert(&flavor); err != nil {
						t.Fatalf("failed to insert flavor: %v", err)
					}
				}
			}

			// Setup pipeline if needed
			if tt.setupPipeline != nil {
				if tt.name == "no reservations pipeline" {
					// Remove reservations pipeline
					delete(httpAPI.pipelines, "reservations")
				} else {
					tt.setupPipeline(httpAPI.pipelines["reservations"].(*mockCommitmentsPipeline))
				}
			}

			// Prepare request
			var body bytes.Buffer
			if tt.method == "POST" {
				if err := json.NewEncoder(&body).Encode(tt.requestBody); err != nil {
					t.Fatalf("failed to encode request body: %v", err)
				}
			}

			req := httptest.NewRequest(tt.method, "/scheduler/nova/commitments/change", &body)
			w := httptest.NewRecorder()

			// Call handler
			httpAPI.HandleCommitmentChangeRequest(w, req)

			// Check status code
			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			// Check response body for successful responses
			if tt.expectedStatus == http.StatusOK {
				var response liquid.CommitmentChangeResponse
				if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}

				if response.RejectionReason != tt.expectedReason {
					t.Errorf("expected rejection reason %q, got %q", tt.expectedReason, response.RejectionReason)
				}

				// Check RetryAt field for rejections
				retriableReasons := []string{
					"cortex has no flavor information yet, please retry later",
					"cortex pipeline failed to execute, please try again",
				}
				isRetriable := false
				for _, reason := range retriableReasons {
					if tt.expectedReason == reason {
						isRetriable = true
						break
					}
				}

				if tt.expectedReason != "" && isRetriable {
					if !response.RetryAt.IsSome() {
						t.Error("expected RetryAt to be set for retriable rejection")
					} else {
						retryTime, err := response.RetryAt.Value()
						if err != nil {
							t.Errorf("failed to get RetryAt value: %v", err)
						} else if retryTime.(time.Time).Before(time.Now()) {
							t.Error("expected RetryAt to be in the future")
						}
					}
				}
				if tt.expectedReason == "no space for this commitment" || (tt.expectedReason != "" && !isRetriable) {
					if response.RetryAt.IsSome() {
						t.Error("expected RetryAt to be empty for non-retriable rejection")
					}
				}
			}
		})
	}
}
