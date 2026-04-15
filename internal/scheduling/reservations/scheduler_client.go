// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// DefaultHTTPTimeout is the default timeout for HTTP requests to the scheduler API.
const DefaultHTTPTimeout = 30 * time.Second

var log = logf.Log.WithName("scheduler-client").WithValues("module", "reservations")

// loggerFromContext returns a logger with greq and req values from the context.
func loggerFromContext(ctx context.Context) logr.Logger {
	return log.WithValues(
		"greq", GlobalRequestIDFromContext(ctx),
		"req", RequestIDFromContext(ctx),
	)
}

// NOTE+FIXME: we should not send ourselves REST API calls. This needs to be replaced by direct Go call (if possible) or communication via CRDs

// SchedulerClient is a client for the external scheduler API.
// It can be used by both the ReservationReconciler and FailoverReservationController.
type SchedulerClient struct {
	// URL of the external scheduler API.
	URL string
	// HTTP client to use for requests.
	HTTPClient *http.Client
}

// NewSchedulerClient creates a new SchedulerClient with a default timeout.
func NewSchedulerClient(url string) *SchedulerClient {
	return &SchedulerClient{
		URL: url,
		HTTPClient: &http.Client{
			Timeout: DefaultHTTPTimeout,
		},
	}
}

// ScheduleReservationRequest contains the parameters for scheduling a reservation.
type ScheduleReservationRequest struct {
	// InstanceUUID is the unique identifier for the reservation (usually the reservation name).
	InstanceUUID string
	// ProjectID is the OpenStack project ID.
	ProjectID string
	// FlavorName is the name of the flavor.
	FlavorName string
	// FlavorExtraSpecs are extra specifications for the flavor.
	FlavorExtraSpecs map[string]string
	// MemoryMB is the memory in MB.
	MemoryMB uint64
	// VCPUs is the number of virtual CPUs.
	VCPUs uint64
	// EligibleHosts is the list of hosts that can be considered for placement.
	EligibleHosts []api.ExternalSchedulerHost
	// IgnoreHosts is a list of hosts to ignore during scheduling.
	// This is used for failover reservations to avoid placing on the same host as the VMs.
	IgnoreHosts []string
	// Pipeline is the name of the pipeline to execute.
	// If empty, the default pipeline will be used.
	Pipeline string
	// AvailabilityZone is the availability zone to schedule in.
	// This is used by the filter_correct_az filter to ensure hosts are in the correct AZ.
	AvailabilityZone string
	// SchedulerHints are hints passed to the scheduler pipeline.
	// Used to set _nova_check_type for evacuation intent detection.
	SchedulerHints map[string]any
}

// ScheduleReservationResponse contains the result of scheduling a reservation.
type ScheduleReservationResponse struct {
	// Hosts is the ordered list of hosts that the reservation can be placed on.
	// The first host is the best choice.
	Hosts []string
}

// ScheduleReservation calls the external scheduler API to find a host for a reservation.
// The context should contain GlobalRequestID and RequestID for logging (use WithGlobalRequestID/WithRequestID).
func (c *SchedulerClient) ScheduleReservation(ctx context.Context, req ScheduleReservationRequest) (*ScheduleReservationResponse, error) {
	logger := loggerFromContext(ctx)

	// Build weights map (all zero for reservations)
	weights := make(map[string]float64, len(req.EligibleHosts))
	for _, host := range req.EligibleHosts {
		weights[host.ComputeHost] = 0.0
	}

	// Build ignore hosts pointer
	var ignoreHosts *[]string
	if len(req.IgnoreHosts) > 0 {
		ignoreHosts = &req.IgnoreHosts
	}

	// Build the context with request IDs
	var globalReqID *string
	if greq := GlobalRequestIDFromContext(ctx); greq != "" {
		globalReqID = &greq
	}

	// Build the external scheduler request
	externalSchedulerRequest := api.ExternalSchedulerRequest{
		Pipeline: req.Pipeline,
		Hosts:    req.EligibleHosts,
		Weights:  weights,
		Context: api.NovaRequestContext{
			RequestID:       RequestIDFromContext(ctx),
			GlobalRequestID: globalReqID,
			ProjectID:       req.ProjectID,
		},
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID:     req.InstanceUUID,
				NumInstances:     1, // One for each reservation.
				ProjectID:        req.ProjectID,
				AvailabilityZone: req.AvailabilityZone,
				IgnoreHosts:      ignoreHosts,
				SchedulerHints:   req.getSchedulerHints(),
				Flavor: api.NovaObject[api.NovaFlavor]{
					Data: api.NovaFlavor{
						Name:       req.FlavorName,
						ExtraSpecs: req.FlavorExtraSpecs,
						MemoryMB:   req.MemoryMB,
						VCPUs:      req.VCPUs,
						// Disk is currently not considered.
					},
				},
			},
		},
	}

	logger.V(1).Info("sending external scheduler request",
		"url", c.URL,
		"pipeline", req.Pipeline,
		"instanceUUID", req.InstanceUUID,
		"flavorName", req.FlavorName,
		"flavorExtraSpecs", req.FlavorExtraSpecs,
		"eligibleHostsCount", len(req.EligibleHosts),
		"ignoreHostsCount", len(req.IgnoreHosts),
		"hasSchedulerHints", len(req.SchedulerHints) > 0,
		"availabilityZone", req.AvailabilityZone)

	// Marshal the request
	reqBody, err := json.Marshal(externalSchedulerRequest)
	if err != nil {
		logger.Error(err, "failed to marshal external scheduler request")
		return nil, fmt.Errorf("failed to marshal external scheduler request: %w", err)
	}

	// Create HTTP request with context
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(reqBody))
	if err != nil {
		logger.Error(err, "failed to create HTTP request")
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request and measure duration
	startTime := time.Now()
	response, err := c.HTTPClient.Do(httpReq)
	duration := time.Since(startTime)
	if err != nil {
		logger.Error(err, "failed to send external scheduler request")
		return nil, fmt.Errorf("failed to send external scheduler request: %w", err)
	}
	defer response.Body.Close()

	// Check response status
	if response.StatusCode != http.StatusOK {
		logger.Error(nil, "external scheduler returned non-OK status", "statusCode", response.StatusCode)
		return nil, fmt.Errorf("external scheduler returned status %d", response.StatusCode)
	}

	// Decode the response
	var externalSchedulerResponse api.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		logger.Error(err, "failed to decode external scheduler response")
		return nil, fmt.Errorf("failed to decode external scheduler response: %w", err)
	}

	logger.V(1).Info("received external scheduler response",
		"hostsCount", len(externalSchedulerResponse.Hosts),
		"duration", duration.Round(time.Millisecond))

	return &ScheduleReservationResponse{
		Hosts: externalSchedulerResponse.Hosts,
	}, nil
}

// getSchedulerHints returns the scheduler hints, or an empty map if nil.
func (req ScheduleReservationRequest) getSchedulerHints() map[string]any {
	if req.SchedulerHints == nil {
		return make(map[string]any)
	}
	return req.SchedulerHints
}
