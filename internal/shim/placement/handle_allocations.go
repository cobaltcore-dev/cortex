// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// allocationsRequest is the JSON body for PUT /allocations/{consumer_uuid}
// (microversion 1.28+).
type allocationsRequest struct {
	Allocations        map[string]allocationEntry `json:"allocations"`
	ConsumerGeneration *int64                     `json:"consumer_generation"`
	ProjectID          string                     `json:"project_id"`
	UserID             string                     `json:"user_id"`
	ConsumerType       string                     `json:"consumer_type,omitempty"`
}

// allocationEntry represents a single resource provider's allocation within
// a consumer's allocation set.
type allocationEntry struct {
	Resources map[string]int64 `json:"resources"`
}

// allocationsResponse is the JSON body returned by
// GET /allocations/{consumer_uuid} (microversion 1.28+).
//
// https://docs.openstack.org/api-ref/placement/#list-allocations
type allocationsResponse struct {
	Allocations        map[string]allocationEntry `json:"allocations"`
	ConsumerGeneration *int64                     `json:"consumer_generation,omitempty"`
	ProjectID          string                     `json:"project_id,omitempty"`
	UserID             string                     `json:"user_id,omitempty"`
}

// placementToHVResources converts Placement resource class amounts to
// Hypervisor CRD resource quantities.
// VCPU → cpu (1:1), MEMORY_MB → memory (MB→bytes), DISK_GB → disk (GB→bytes).
func placementToHVResources(resources map[string]int64) map[hv1.ResourceName]resource.Quantity {
	out := make(map[hv1.ResourceName]resource.Quantity, len(resources))
	for name, amount := range resources {
		switch name {
		case "VCPU":
			out[hv1.ResourceCPU] = *resource.NewQuantity(amount, resource.DecimalSI)
		case "MEMORY_MB":
			out[hv1.ResourceMemory] = *resource.NewQuantity(amount*1024*1024, resource.BinarySI)
		default:
			out[hv1.ResourceName(name)] = *resource.NewQuantity(amount, resource.DecimalSI)
		}
	}
	return out
}

// hvToPlacementResources converts Hypervisor CRD resource quantities back to
// Placement resource class amounts.
func hvToPlacementResources(resources map[hv1.ResourceName]resource.Quantity) map[string]int64 {
	out := make(map[string]int64, len(resources))
	for name, qty := range resources {
		switch name {
		case hv1.ResourceCPU:
			out["VCPU"] = qty.Value()
		case hv1.ResourceMemory:
			out["MEMORY_MB"] = qty.Value() / (1024 * 1024)
		default:
			out[string(name)] = qty.Value()
		}
	}
	return out
}

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
//
// https://docs.openstack.org/api-ref/placement/#manage-allocations
func (s *Shim) HandleManageAllocations(w http.ResponseWriter, r *http.Request) {
	switch s.featureModeFromConfOrHeader(r, s.config.Features.Allocations) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.manageAllocationsHybrid(w, r)
	case FeatureModeCRD:
		s.manageAllocationsCRD(w, r)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
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
//
// https://docs.openstack.org/api-ref/placement/#list-allocations
func (s *Shim) HandleListAllocations(w http.ResponseWriter, r *http.Request) {
	consumerUUID, ok := requiredUUIDPathParam(w, r, "consumer_uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.Allocations) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.listAllocationsHybrid(w, r, consumerUUID)
	case FeatureModeCRD:
		s.listAllocationsCRD(w, r, consumerUUID)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
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
//
// https://docs.openstack.org/api-ref/placement/#update-allocations
func (s *Shim) HandleUpdateAllocations(w http.ResponseWriter, r *http.Request) {
	consumerUUID, ok := requiredUUIDPathParam(w, r, "consumer_uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.Allocations) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.updateAllocationsHybrid(w, r, consumerUUID)
	case FeatureModeCRD:
		s.updateAllocationsCRD(w, r, consumerUUID)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// HandleDeleteAllocations handles DELETE /allocations/{consumer_uuid} requests.
//
// Removes all allocation records for the consumer across all resource
// providers. Returns 204 No Content on success, or 404 Not Found if the
// consumer has no existing allocations.
//
// https://docs.openstack.org/api-ref/placement/#delete-allocations
func (s *Shim) HandleDeleteAllocations(w http.ResponseWriter, r *http.Request) {
	consumerUUID, ok := requiredUUIDPathParam(w, r, "consumer_uuid")
	if !ok {
		return
	}
	switch s.featureModeFromConfOrHeader(r, s.config.Features.Allocations) {
	case FeatureModePassthrough:
		s.forward(w, r)
	case FeatureModeHybrid:
		s.deleteAllocationsHybrid(w, r, consumerUUID)
	case FeatureModeCRD:
		s.deleteAllocationsCRD(w, r, consumerUUID)
	default:
		http.Error(w, "unknown feature mode", http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// PUT /allocations/{consumer_uuid} — hybrid and crd
// ---------------------------------------------------------------------------

func (s *Shim) updateAllocationsHybrid(w http.ResponseWriter, r *http.Request, consumerUUID string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "failed to read request body")
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req allocationsRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	kvmAllocs := make(map[string]allocationEntry)
	nonKvmAllocs := make(map[string]allocationEntry)
	kvmHypervisors := make(map[string]*hv1.Hypervisor)

	for rpUUID, entry := range req.Allocations {
		var hvs hv1.HypervisorList
		if err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: rpUUID}); err == nil && len(hvs.Items) == 1 {
			kvmAllocs[rpUUID] = entry
			hv := hvs.Items[0]
			kvmHypervisors[rpUUID] = &hv
		} else {
			nonKvmAllocs[rpUUID] = entry
		}
	}

	// Forward non-KVM allocations to upstream first.
	if len(nonKvmAllocs) > 0 {
		upstreamReq := allocationsRequest{
			Allocations:        nonKvmAllocs,
			ConsumerGeneration: req.ConsumerGeneration,
			ProjectID:          req.ProjectID,
			UserID:             req.UserID,
			ConsumerType:       req.ConsumerType,
		}
		upstreamBody, err := json.Marshal(upstreamReq)
		if err != nil {
			log.Error(err, "failed to marshal upstream request")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(upstreamBody))
		rec := &statusRecorder{ResponseWriter: w, header: make(http.Header)}
		s.forward(rec, r)
		if rec.statusCode >= 300 {
			// Upstream failed — copy its response to the client.
			for k, vs := range rec.header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.statusCode)
			w.Write(rec.body.Bytes()) //nolint:errcheck
			return
		}
	}

	// Write KVM allocations to CRD.
	if err := s.writeKVMBookings(ctx, consumerUUID, &req, kvmAllocs, kvmHypervisors); err != nil {
		log.Error(err, "failed to write KVM bookings")
		if apierrors.IsConflict(err) {
			http.Error(w, "consumer generation conflict", http.StatusConflict)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Shim) updateAllocationsCRD(w http.ResponseWriter, r *http.Request, consumerUUID string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var req allocationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	kvmAllocs := make(map[string]allocationEntry)
	kvmHypervisors := make(map[string]*hv1.Hypervisor)

	for rpUUID, entry := range req.Allocations {
		var hvs hv1.HypervisorList
		if err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: rpUUID}); err != nil || len(hvs.Items) != 1 {
			log.Info("resource provider not found in CRD (crd mode)", "rpUUID", rpUUID)
			http.Error(w, fmt.Sprintf("resource provider %s not found", rpUUID), http.StatusBadRequest)
			return
		}
		kvmAllocs[rpUUID] = entry
		hv := hvs.Items[0]
		kvmHypervisors[rpUUID] = &hv
	}

	if err := s.writeKVMBookings(ctx, consumerUUID, &req, kvmAllocs, kvmHypervisors); err != nil {
		log.Error(err, "failed to write KVM bookings")
		if apierrors.IsConflict(err) {
			http.Error(w, "consumer generation conflict", http.StatusConflict)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeKVMBookings writes consumer bookings to the respective Hypervisor CRs.
// It performs consumer generation checks before writing.
func (s *Shim) writeKVMBookings(
	ctx context.Context,
	consumerUUID string,
	req *allocationsRequest,
	kvmAllocs map[string]allocationEntry,
	kvmHypervisors map[string]*hv1.Hypervisor,
) error {
	log := logf.FromContext(ctx)

	hvGR := schema.GroupResource{Group: "kvm.cloud.sap", Resource: "hypervisors"}
	for rpUUID, entry := range kvmAllocs {
		hv := kvmHypervisors[rpUUID]
		existing := hv1.GetConsumer(hv.Spec.Bookings, consumerUUID)

		// Consumer generation check.
		if existing == nil && req.ConsumerGeneration != nil {
			return apierrors.NewConflict(hvGR, hv.Name,
				fmt.Errorf("consumer %s does not exist but generation is non-null", consumerUUID))
		}
		if existing != nil && req.ConsumerGeneration == nil {
			return apierrors.NewConflict(hvGR, hv.Name,
				fmt.Errorf("consumer %s exists but generation is null", consumerUUID))
		}
		if existing != nil && req.ConsumerGeneration != nil && existing.ConsumerGeneration != nil {
			if *req.ConsumerGeneration != *existing.ConsumerGeneration {
				return apierrors.NewConflict(hvGR, hv.Name,
					fmt.Errorf("consumer %s generation mismatch: got %d, want %d", consumerUUID, *req.ConsumerGeneration, *existing.ConsumerGeneration))
			}
		}

		// Compute new generation.
		var newGen int64
		if existing != nil && existing.ConsumerGeneration != nil {
			newGen = *existing.ConsumerGeneration + 1
		} else {
			newGen = 1
		}

		newBooking := hv1.Booking{
			Consumer: &hv1.ConsumerBooking{
				UUID:               consumerUUID,
				Resources:          placementToHVResources(entry.Resources),
				ConsumerGeneration: &newGen,
				ConsumerType:       req.ConsumerType,
				ProjectID:          req.ProjectID,
				UserID:             req.UserID,
			},
		}

		// Replace or append.
		var newBookings []hv1.Booking
		replaced := false
		for _, b := range hv.Spec.Bookings {
			if b.Consumer != nil && b.Consumer.UUID == consumerUUID {
				newBookings = append(newBookings, newBooking)
				replaced = true
			} else {
				newBookings = append(newBookings, b)
			}
		}
		if !replaced {
			newBookings = append(newBookings, newBooking)
		}
		hv.Spec.Bookings = newBookings

		if err := s.Update(ctx, hv); err != nil {
			log.Error(err, "failed to update hypervisor bookings", "hypervisor", hv.Name, "consumer", consumerUUID)
			return err
		}
		log.Info("wrote consumer booking to hypervisor", "hypervisor", hv.Name, "consumer", consumerUUID, "rpUUID", rpUUID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GET /allocations/{consumer_uuid} — hybrid and crd
// ---------------------------------------------------------------------------

func (s *Shim) listAllocationsHybrid(w http.ResponseWriter, r *http.Request, consumerUUID string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	s.forwardWithHook(w, r, func(w http.ResponseWriter, resp *http.Response) {
		var upstreamResp allocationsResponse
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&upstreamResp); err != nil {
				log.Error(err, "failed to decode upstream allocations response")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		} else {
			// If upstream returned non-200, initialize empty.
			upstreamResp.Allocations = make(map[string]allocationEntry)
		}
		if upstreamResp.Allocations == nil {
			upstreamResp.Allocations = make(map[string]allocationEntry)
		}

		// Look up consumer in CRD.
		var hvs hv1.HypervisorList
		if err := s.List(ctx, &hvs, client.MatchingFields{idxBookingConsumerUUID: consumerUUID}); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to look up consumer in CRD")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Merge CRD bookings into the response. CRD takes precedence on collision.
		for _, hv := range hvs.Items {
			consumer := hv1.GetConsumer(hv.Spec.Bookings, consumerUUID)
			if consumer == nil {
				continue
			}
			rpUUID := hv.Status.HypervisorID
			upstreamResp.Allocations[rpUUID] = allocationEntry{
				Resources: hvToPlacementResources(consumer.Resources),
			}
			if upstreamResp.ConsumerGeneration == nil {
				upstreamResp.ConsumerGeneration = consumer.ConsumerGeneration
			}
			if upstreamResp.ProjectID == "" {
				upstreamResp.ProjectID = consumer.ProjectID
			}
			if upstreamResp.UserID == "" {
				upstreamResp.UserID = consumer.UserID
			}
		}

		s.writeJSON(w, http.StatusOK, upstreamResp)
	})
}

func (s *Shim) listAllocationsCRD(w http.ResponseWriter, r *http.Request, consumerUUID string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	if err := s.List(ctx, &hvs, client.MatchingFields{idxBookingConsumerUUID: consumerUUID}); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to look up consumer in CRD")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	resp := allocationsResponse{
		Allocations: make(map[string]allocationEntry),
	}

	for _, hv := range hvs.Items {
		consumer := hv1.GetConsumer(hv.Spec.Bookings, consumerUUID)
		if consumer == nil {
			continue
		}
		rpUUID := hv.Status.HypervisorID
		resp.Allocations[rpUUID] = allocationEntry{
			Resources: hvToPlacementResources(consumer.Resources),
		}
		resp.ConsumerGeneration = consumer.ConsumerGeneration
		resp.ProjectID = consumer.ProjectID
		resp.UserID = consumer.UserID
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// DELETE /allocations/{consumer_uuid} — hybrid and crd
// ---------------------------------------------------------------------------

func (s *Shim) deleteAllocationsHybrid(w http.ResponseWriter, r *http.Request, consumerUUID string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	// Forward to upstream first.
	rec := &statusRecorder{ResponseWriter: w, header: make(http.Header)}
	s.forward(rec, r)
	// Upstream returning 404 is acceptable — the consumer may only exist in CRD.
	if rec.statusCode >= 300 && rec.statusCode != http.StatusNotFound {
		for k, vs := range rec.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.statusCode)
		w.Write(rec.body.Bytes()) //nolint:errcheck
		return
	}

	// Remove from CRD.
	if err := s.removeConsumerFromCRD(ctx, consumerUUID); err != nil {
		log.Error(err, "failed to remove consumer from CRD")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Shim) deleteAllocationsCRD(w http.ResponseWriter, r *http.Request, consumerUUID string) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	if err := s.List(ctx, &hvs, client.MatchingFields{idxBookingConsumerUUID: consumerUUID}); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to look up consumer in CRD")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if len(hvs.Items) == 0 {
		http.Error(w, "consumer not found", http.StatusNotFound)
		return
	}

	if err := s.removeConsumerFromCRD(ctx, consumerUUID); err != nil {
		log.Error(err, "failed to remove consumer from CRD")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// removeConsumerFromCRD removes all booking entries for a consumer from all
// Hypervisor CRs that hold it.
func (s *Shim) removeConsumerFromCRD(ctx context.Context, consumerUUID string) error {
	log := logf.FromContext(ctx)

	var hvs hv1.HypervisorList
	if err := s.List(ctx, &hvs, client.MatchingFields{idxBookingConsumerUUID: consumerUUID}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	for i := range hvs.Items {
		hv := &hvs.Items[i]
		var newBookings []hv1.Booking
		for _, b := range hv.Spec.Bookings {
			if b.Consumer != nil && b.Consumer.UUID == consumerUUID {
				continue
			}
			newBookings = append(newBookings, b)
		}
		hv.Spec.Bookings = newBookings
		if err := s.Update(ctx, hv); err != nil {
			log.Error(err, "failed to update hypervisor after removing consumer", "hypervisor", hv.Name, "consumer", consumerUUID)
			return err
		}
		log.Info("removed consumer booking from hypervisor", "hypervisor", hv.Name, "consumer", consumerUUID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// POST /allocations — hybrid and crd
// ---------------------------------------------------------------------------

// manageAllocationsRequest represents the batch body for POST /allocations.
// It is keyed by consumer UUID.
type manageAllocationsRequest map[string]allocationsRequest

func (s *Shim) manageAllocationsHybrid(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "failed to read request body")
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var batch manageAllocationsRequest
	if err := json.Unmarshal(bodyBytes, &batch); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Separate KVM from non-KVM across all consumers.
	type kvmWork struct {
		consumerUUID   string
		req            *allocationsRequest
		kvmAllocs      map[string]allocationEntry
		kvmHypervisors map[string]*hv1.Hypervisor
	}
	var kvmWorkItems []kvmWork
	nonKvmBatch := make(manageAllocationsRequest)

	for consumerUUID, consumerReq := range batch {
		kvmA := make(map[string]allocationEntry)
		nonKvmA := make(map[string]allocationEntry)
		kvmHVs := make(map[string]*hv1.Hypervisor)

		for rpUUID, entry := range consumerReq.Allocations {
			var hvs hv1.HypervisorList
			if err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: rpUUID}); err == nil && len(hvs.Items) == 1 {
				kvmA[rpUUID] = entry
				hv := hvs.Items[0]
				kvmHVs[rpUUID] = &hv
			} else {
				nonKvmA[rpUUID] = entry
			}
		}

		if len(nonKvmA) > 0 {
			nonKvmBatch[consumerUUID] = allocationsRequest{
				Allocations:        nonKvmA,
				ConsumerGeneration: consumerReq.ConsumerGeneration,
				ProjectID:          consumerReq.ProjectID,
				UserID:             consumerReq.UserID,
				ConsumerType:       consumerReq.ConsumerType,
			}
		}
		if len(kvmA) > 0 {
			cr := consumerReq
			kvmWorkItems = append(kvmWorkItems, kvmWork{
				consumerUUID:   consumerUUID,
				req:            &cr,
				kvmAllocs:      kvmA,
				kvmHypervisors: kvmHVs,
			})
		}
	}

	// Forward non-KVM portion to upstream first.
	if len(nonKvmBatch) > 0 {
		upstreamBody, err := json.Marshal(nonKvmBatch)
		if err != nil {
			log.Error(err, "failed to marshal upstream batch request")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(upstreamBody))
		rec := &statusRecorder{ResponseWriter: w, header: make(http.Header)}
		s.forward(rec, r)
		if rec.statusCode >= 300 {
			for k, vs := range rec.header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.statusCode)
			w.Write(rec.body.Bytes()) //nolint:errcheck
			return
		}
	}

	// Write KVM bookings.
	for _, work := range kvmWorkItems {
		if err := s.writeKVMBookings(ctx, work.consumerUUID, work.req, work.kvmAllocs, work.kvmHypervisors); err != nil {
			log.Error(err, "failed to write KVM bookings in batch", "consumer", work.consumerUUID)
			if apierrors.IsConflict(err) {
				http.Error(w, "consumer generation conflict", http.StatusConflict)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Shim) manageAllocationsCRD(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx)

	var batch manageAllocationsRequest
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	for consumerUUID, consumerReq := range batch {
		kvmAllocs := make(map[string]allocationEntry)
		kvmHypervisors := make(map[string]*hv1.Hypervisor)

		for rpUUID, entry := range consumerReq.Allocations {
			var hvs hv1.HypervisorList
			if err := s.List(ctx, &hvs, client.MatchingFields{idxHypervisorOpenStackId: rpUUID}); err != nil || len(hvs.Items) != 1 {
				log.Info("resource provider not found in CRD (crd mode)", "rpUUID", rpUUID)
				http.Error(w, fmt.Sprintf("resource provider %s not found", rpUUID), http.StatusBadRequest)
				return
			}
			kvmAllocs[rpUUID] = entry
			hv := hvs.Items[0]
			kvmHypervisors[rpUUID] = &hv
		}

		cr := consumerReq
		if err := s.writeKVMBookings(ctx, consumerUUID, &cr, kvmAllocs, kvmHypervisors); err != nil {
			log.Error(err, "failed to write KVM bookings in batch", "consumer", consumerUUID)
			if apierrors.IsConflict(err) {
				http.Error(w, "consumer generation conflict", http.StatusConflict)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// statusRecorder captures the upstream response so we can inspect status before
// committing to write the real response.
// ---------------------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
	header     http.Header
	body       bytes.Buffer
}

func (r *statusRecorder) Header() http.Header {
	return r.header
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}
