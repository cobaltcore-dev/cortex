// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"net/http"
)

// HandleManageAllocations handles POST /allocations requests.
//
// Atomically creates, updates, or deletes allocations for multiple consumers
// in a single request. This is the primary mechanism for operations that must
// modify allocations across several consumers atomically, such as live
// migrations and move operations where resources are transferred from one
// consumer to another. Available since microversion 1.13.
//
// The request body is keyed by consumer UUID, each containing an allocations
// dictionary (keyed by resource provider UUID), along with project_id and
// user_id. Since microversion 1.28, consumer_generation enables consumer-
// level concurrency control. Since microversion 1.38, a consumer_type field
// (e.g. INSTANCE, MIGRATION) is supported. Returns 204 No Content on
// success, or 409 Conflict if inventory is insufficient or a concurrent
// update is detected (error code: placement.concurrent_update).
func (s *Shim) HandleManageAllocations(w http.ResponseWriter, r *http.Request) {
	s.dispatchPassthroughOnly(w, r, s.config.Features.Allocations)
}

// HandleListAllocations handles GET /allocations/{consumer_uuid} requests.
//
// Returns all allocation records for the consumer identified by
// {consumer_uuid}, across all resource providers. The response contains an
// allocations dictionary keyed by resource provider UUID. If the consumer has
// no allocations, an empty dictionary is returned.
//
// The response has grown across microversions: project_id and user_id were
// added at 1.12, consumer_generation at 1.28, and consumer_type at 1.38.
// The consumer_generation and consumer_type fields are absent when the
// consumer has no allocations.
func (s *Shim) HandleListAllocations(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "consumer_uuid"); !ok {
		return
	}
	s.dispatchPassthroughOnly(w, r, s.config.Features.Allocations)
}

// HandleUpdateAllocations handles PUT /allocations/{consumer_uuid} requests.
//
// Creates or replaces all allocation records for a single consumer. If
// allocations already exist for this consumer, they are entirely replaced
// by the new set. The request format changed at microversion 1.12 from an
// array-based layout to an object keyed by resource provider UUID.
// Microversion 1.28 added consumer_generation for concurrency control,
// and 1.38 introduced consumer_type.
//
// Returns 204 No Content on success. Returns 409 Conflict if there is
// insufficient inventory or if a concurrent update was detected.
func (s *Shim) HandleUpdateAllocations(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "consumer_uuid"); !ok {
		return
	}
	s.dispatchPassthroughOnly(w, r, s.config.Features.Allocations)
}

// HandleDeleteAllocations handles DELETE /allocations/{consumer_uuid} requests.
//
// Removes all allocation records for the consumer across all resource
// providers. Returns 204 No Content on success, or 404 Not Found if the
// consumer has no existing allocations.
func (s *Shim) HandleDeleteAllocations(w http.ResponseWriter, r *http.Request) {
	if _, ok := requiredUUIDPathParam(w, r, "consumer_uuid"); !ok {
		return
	}
	s.dispatchPassthroughOnly(w, r, s.config.Features.Allocations)
}
