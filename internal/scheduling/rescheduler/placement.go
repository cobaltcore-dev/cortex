// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"context"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
)

// PlacementRequest asks the placement pipeline to discover or reserve candidate
// hosts. Options distinguishes the read-only discovery pass from the fresh,
// constrained reservation pass. Its fields mirror what placement needs today
// (see reservations.ScheduleReservationRequest).
// The concrete type is expected to change once placement moves in-process, so it
// is kept local to the rescheduler and is not api.ExternalSchedulerRequest.
type PlacementRequest struct {
	InstanceUUID     string
	ProjectID        string
	FlavorName       string
	FlavorExtraSpecs map[string]string
	MemoryMB         uint64
	VCPUs            uint64
	EligibleHosts    []api.ExternalSchedulerHost
	IgnoreHosts      []string
	AllowedHosts     []string `json:"allowed_hosts,omitempty"`
	Pipeline         string
	AvailabilityZone string
	SchedulerHints   map[string]any
	ImageProperties  map[string]any
	Options          scheduling.Options
}

// PlacementResult is the outcome of a placement request: an ordered list of
// candidate hosts (best first), or an error.
type PlacementResult struct {
	Candidates []string
	Err        error
}

// PlacementBackend computes candidate discovery and reservation-time placement.
// It is the seam behind the scheduler's placement channel: today an HTTP call
// to the external scheduler API, later an in-process call to the scheduling
// pipeline. Swapping the backend does not change the channel wiring or scheduler.
type PlacementBackend interface {
	Schedule(ctx context.Context, req PlacementRequest) PlacementResult
}

// StubPlacementBackend returns a fixed host without contacting a scheduler.
// It is used by tests; production uses HTTPPlacementBackend.
type StubPlacementBackend struct {
	// Host is the host returned for every request. Defaults to "stub-host".
	Host string
}

func (b StubPlacementBackend) Schedule(_ context.Context, _ PlacementRequest) PlacementResult {
	host := b.Host
	if host == "" {
		host = "stub-host"
	}
	return PlacementResult{Candidates: []string{host}}
}

// HTTPPlacementBackend computes placement by calling the external scheduler API
// over HTTP via the shared SchedulerClient. It is a stop-gap: the FIXME on
// SchedulerClient applies here too (we should not send ourselves REST calls),
// and this backend is expected to be replaced by in-process communication.
type HTTPPlacementBackend struct {
	client *reservations.SchedulerClient
}

// NewHTTPPlacementBackend returns an HTTP placement backend targeting the given
// external scheduler URL.
func NewHTTPPlacementBackend(url string) *HTTPPlacementBackend {
	return &HTTPPlacementBackend{client: reservations.NewSchedulerClient(url)}
}

func (b *HTTPPlacementBackend) Schedule(ctx context.Context, req PlacementRequest) PlacementResult {
	resp, err := b.client.ScheduleReservation(ctx, reservations.ScheduleReservationRequest{
		InstanceUUID:     req.InstanceUUID,
		ProjectID:        req.ProjectID,
		FlavorName:       req.FlavorName,
		FlavorExtraSpecs: req.FlavorExtraSpecs,
		MemoryMB:         req.MemoryMB,
		VCPUs:            req.VCPUs,
		EligibleHosts:    req.EligibleHosts,
		IgnoreHosts:      req.IgnoreHosts,
		Pipeline:         req.Pipeline,
		AvailabilityZone: req.AvailabilityZone,
		SchedulerHints:   req.SchedulerHints,
		ImageProperties:  req.ImageProperties,
	}, req.Options)
	if err != nil {
		return PlacementResult{Err: err}
	}
	return PlacementResult{Candidates: resp.Hosts}
}
