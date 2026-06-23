// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package crs

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

func TestRecordCRAllocation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme hv1: %v", err)
	}

	const (
		instanceUUID = "vm-uuid-1"
		projectID    = "project-1"
		flavorName   = "m1.large"
		flavorGroup  = "m1"
		selectedHost = "compute-1"
	)

	ratio := uint64(2048)
	fg := compute.FlavorGroupFeature{
		Name:           flavorGroup,
		Flavors:        []compute.FlavorInGroup{{Name: flavorName, VCPUs: 2, MemoryMB: 4096}},
		LargestFlavor:  compute.FlavorInGroup{Name: flavorName, VCPUs: 2, MemoryMB: 4096},
		SmallestFlavor: compute.FlavorInGroup{Name: flavorName, VCPUs: 2, MemoryMB: 4096},
		RamCoreRatio:   &ratio,
	}

	flavorKnowledge := func() *v1alpha1.Knowledge {
		raw, err := v1alpha1.BoxFeatureList([]compute.FlavorGroupFeature{fg})
		if err != nil {
			t.Fatalf("BoxFeatureList: %v", err)
		}
		return &v1alpha1.Knowledge{
			ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
			Status: v1alpha1.KnowledgeStatus{
				Raw: raw,
				Conditions: []metav1.Condition{{
					Type:               v1alpha1.KnowledgeConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "Ready",
					LastTransitionTime: metav1.Now(),
				}},
			},
		}
	}

	makeReservation := func(name string, memMiB, cpus int64, proj, group, host string, allocs map[string]v1alpha1.CommittedResourceAllocation) *v1alpha1.Reservation {
		return &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Type:       v1alpha1.ReservationTypeCommittedResource,
				TargetHost: host,
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(memMiB*1024*1024, resource.BinarySI),
					hv1.ResourceCPU:    *resource.NewQuantity(cpus, resource.DecimalSI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     proj,
					ResourceGroup: group,
					Allocations:   allocs,
				},
			},
		}
	}

	makeRequest := func(uuid, proj, flavor string) api.ExternalSchedulerRequest {
		return api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{
					InstanceUUID: uuid,
					Flavor: api.NovaObject[api.NovaFlavor]{
						Data: api.NovaFlavor{Name: flavor, MemoryMB: 4096, VCPUs: 2},
					},
				},
			},
			Context: api.NovaRequestContext{ProjectID: proj},
		}
	}

	makeDecision := func(host string, candidates ...string) *v1alpha1.Decision {
		h := host
		return &v1alpha1.Decision{
			Status: v1alpha1.DecisionStatus{
				Result: &v1alpha1.DecisionResult{
					TargetHost:   &h,
					OrderedHosts: candidates,
				},
			},
		}
	}

	makeCRObject := func() *v1alpha1.CommittedResource {
		return &v1alpha1.CommittedResource{
			ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:       projectID,
				FlavorGroupName: flavorGroup,
				State:           v1alpha1.CommitmentStatusConfirmed,
				Amount:          *resource.NewQuantity(int64(8192)*1024*1024, resource.BinarySI),
			},
		}
	}

	setReady := func(res *v1alpha1.Reservation) *v1alpha1.Reservation {
		res.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			LastTransitionTime: metav1.Now(),
		}}
		return res
	}

	vmAlloc := func() map[string]v1alpha1.CommittedResourceAllocation {
		return map[string]v1alpha1.CommittedResourceAllocation{
			instanceUUID: {
				CreationTimestamp: metav1.Now(),
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(int64(4096)*1024*1024, resource.BinarySI),
					hv1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
				},
			},
		}
	}

	tests := []struct {
		name             string
		objects          []client.Object
		request          api.ExternalSchedulerRequest
		decision         *v1alpha1.Decision
		checkSlot        string
		expectAllocation bool
		expectedCRSlot   string // if non-empty, asserts PlacementCounter cr_slot label
	}{
		{
			name: "writes allocation into matching reservation",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(),
				setReady(makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil)),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: true,
		},
		{
			name: "idempotent: UUID already in allocations",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(),
				setReady(makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, vmAlloc())),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: true,
		},
		{
			name: "PAYG: flavor not in any group",
			objects: []client.Object{
				flavorKnowledge(),
				setReady(makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil)),
			},
			request:          makeRequest(instanceUUID, projectID, "unknown-flavor"),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
		},
		{
			name: "no matching reservation: host mismatch",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(),
				setReady(makeReservation("slot-1", 8192, 8, projectID, flavorGroup, "other-host", nil)),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
		},
		{
			name: "no slot fits: all capacity used",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(),
				setReady(makeReservation("slot-full", 4096, 2, projectID, flavorGroup, selectedHost,
					map[string]v1alpha1.CommittedResourceAllocation{
						"other-vm": {
							Resources: map[hv1.ResourceName]resource.Quantity{
								hv1.ResourceMemory: *resource.NewQuantity(int64(4096)*1024*1024, resource.BinarySI),
								hv1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
							},
						},
					})),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-full",
			expectAllocation: false,
		},
		{
			name: "inactive CR (pending state) is filtered by IsActive(): no allocation",
			objects: []client.Object{
				flavorKnowledge(),
				func() *v1alpha1.CommittedResource {
					cr := makeCRObject()
					cr.Spec.State = v1alpha1.CommitmentStatusPending
					return cr
				}(),
				setReady(makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil)),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
		},
		{
			name: "no knowledge CRD: logs error, no allocation",
			objects: []client.Object{
				makeReservation("slot-1", 8192, 8, projectID, flavorGroup, selectedHost, nil),
			},
			request:          makeRequest(instanceUUID, projectID, flavorName),
			decision:         makeDecision(selectedHost, selectedHost),
			checkSlot:        "slot-1",
			expectAllocation: false,
			expectedCRSlot:   "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			counter := NewPlacementCounter()
			reg := prometheus.NewRegistry()
			reg.MustRegister(counter)

			recorder := Recorder{
				Client:           fakeClient,
				PlacementCounter: counter,
			}

			recorder.RecordPlacement(context.Background(), tt.decision, tt.request)

			var res v1alpha1.Reservation
			if err := fakeClient.Get(context.Background(), client.ObjectKey{Name: tt.checkSlot}, &res); err != nil {
				t.Fatalf("Get reservation %q: %v", tt.checkSlot, err)
			}
			_, hasAlloc := res.Spec.CommittedResourceReservation.Allocations[instanceUUID]
			if tt.expectAllocation && !hasAlloc {
				t.Errorf("expected allocation for UUID %q but none found", instanceUUID)
			}
			if !tt.expectAllocation && hasAlloc {
				t.Errorf("expected no allocation for UUID %q but one was written", instanceUUID)
			}
			if tt.expectedCRSlot != "" {
				intent := string(tt.decision.Spec.Intent)
				got := testutil.ToFloat64(counter.WithLabelValues("unknown", intent, tt.expectedCRSlot))
				if got != 1 {
					t.Errorf("PlacementCounter[flavor_group=unknown, intent=%q, cr_slot=%q] = %.0f, want 1",
						intent, tt.expectedCRSlot, got)
				}
			}
		})
	}
}
