// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package reservations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("scheduler-client").WithValues("module", "reservations")

// NOTE+FIXME: we should not send ourselves REST API calls. This needs to be replaced by direct Go call (if possible) or communication via CRDs

// SchedulerClient is a client for the external scheduler API.
// It can be used by both the ReservationReconciler and FailoverReservationController.
type SchedulerClient struct {
	// URL of the external scheduler API.
	URL string
	// HTTP client to use for requests.
	HTTPClient *http.Client
}

// NewSchedulerClient creates a new SchedulerClient.
func NewSchedulerClient(url string) *SchedulerClient {
	return &SchedulerClient{
		URL:        url,
		HTTPClient: &http.Client{},
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
}

// ScheduleReservationResponse contains the result of scheduling a reservation.
type ScheduleReservationResponse struct {
	// Hosts is the ordered list of hosts that the reservation can be placed on.
	// The first host is the best choice.
	Hosts []string
}

// ScheduleReservation calls the external scheduler API to find a host for a reservation.
func (c *SchedulerClient) ScheduleReservation(ctx context.Context, req ScheduleReservationRequest) (*ScheduleReservationResponse, error) {
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

	// Build the external scheduler request
	externalSchedulerRequest := api.ExternalSchedulerRequest{
		Reservation: true,
		Pipeline:    req.Pipeline,
		Hosts:       req.EligibleHosts,
		Weights:     weights,
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID:     req.InstanceUUID,
				NumInstances:     1, // One for each reservation.
				ProjectID:        req.ProjectID,
				AvailabilityZone: req.AvailabilityZone,
				IgnoreHosts:      ignoreHosts,
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

	log.V(1).Info("sending external scheduler request",
		"url", c.URL,
		"instanceUUID", req.InstanceUUID,
		"projectID", req.ProjectID,
		"flavorName", req.FlavorName,
		"flavorExtraSpecs", req.FlavorExtraSpecs,
		"memoryMB", req.MemoryMB,
		"vcpus", req.VCPUs,
		"eligibleHostsCount", len(req.EligibleHosts),
		"ignoreHosts", req.IgnoreHosts)

	// Marshal the request
	reqBody, err := json.Marshal(externalSchedulerRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal external scheduler request: %w", err)
	}

	// Create HTTP request with context
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request
	response, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send external scheduler request: %w", err)
	}
	defer response.Body.Close()

	// Check response status
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("external scheduler returned status %d", response.StatusCode)
	}

	// Decode the response
	var externalSchedulerResponse api.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		return nil, fmt.Errorf("failed to decode external scheduler response: %w", err)
	}

	return &ScheduleReservationResponse{
		Hosts: externalSchedulerResponse.Hosts,
	}, nil
}
