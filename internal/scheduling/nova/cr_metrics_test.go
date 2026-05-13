// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

// makeCR builds a CommittedResource for testing.
func makeCR(state v1alpha1.CommitmentStatus, amountMiB, usedMiB int64) v1alpha1.CommittedResource {
	cr := v1alpha1.CommittedResource{
		Spec: v1alpha1.CommittedResourceSpec{
			State:  state,
			Amount: *resource.NewQuantity(amountMiB*1024*1024, resource.BinarySI),
		},
	}
	if usedMiB > 0 {
		cr.Status.UsedResources = map[string]resource.Quantity{
			"memory": *resource.NewQuantity(usedMiB*1024*1024, resource.BinarySI),
		}
	}
	return cr
}

// makeSlot builds a Reservation slot for testing.
func makeSlot(projectID, flavorGroup string, totalMemMiB, allocatedMemMiB int64) v1alpha1.Reservation {
	res := v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "slot-" + flavorGroup},
		Spec: v1alpha1.ReservationSpec{
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: *resource.NewQuantity(totalMemMiB*1024*1024, resource.BinarySI),
			},
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
				ProjectID:     projectID,
				ResourceGroup: flavorGroup,
			},
		},
	}
	if allocatedMemMiB > 0 {
		res.Spec.CommittedResourceReservation.Allocations = map[string]v1alpha1.CommittedResourceAllocation{
			"some-vm": {
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(allocatedMemMiB*1024*1024, resource.BinarySI),
				},
			},
		}
	}
	return res
}

func TestClassifyNoHostFound(t *testing.T) {
	const (
		proj  = "project-1"
		group = "kvm_v2_hana_s"
	)

	tests := []struct {
		name         string
		activeCRs    []v1alpha1.CommittedResource
		reservations []v1alpha1.Reservation
		expectedCase string
	}{
		{
			name:         "D: no active CRs for project+flavor group",
			activeCRs:    nil,
			reservations: nil,
			expectedCase: "D",
		},
		{
			name: "A: CRs fully occupied (used == capacity)",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 8192),
			},
			reservations: nil,
			expectedCase: "A",
		},
		{
			name: "A: CRs fully occupied (used > capacity)",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 10000),
			},
			reservations: nil,
			expectedCase: "A",
		},
		{
			name: "A: multiple CRs, total used >= total capacity",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 4096, 4096),
				makeCR(v1alpha1.CommitmentStatusGuaranteed, 4096, 4096),
			},
			reservations: nil,
			expectedCase: "A",
		},
		{
			name: "B: CRs have capacity but no free reservation slot",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
			},
			reservations: []v1alpha1.Reservation{
				makeSlot(proj, group, 8192, 8192), // slot fully allocated
			},
			expectedCase: "B",
		},
		{
			name: "B: CRs have capacity, no slots at all",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 0),
			},
			reservations: nil,
			expectedCase: "B",
		},
		{
			name: "C: free slot exists",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
			},
			reservations: []v1alpha1.Reservation{
				makeSlot(proj, group, 8192, 4096), // slot has 4096 MiB free
			},
			expectedCase: "C",
		},
		{
			name: "C: one slot full, one slot free",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 16384, 4096),
			},
			reservations: []v1alpha1.Reservation{
				makeSlot(proj, group, 8192, 8192), // full
				makeSlot(proj, group, 8192, 0),    // free
			},
			expectedCase: "C",
		},
		{
			name: "slots for other project are ignored",
			activeCRs: []v1alpha1.CommittedResource{
				makeCR(v1alpha1.CommitmentStatusConfirmed, 8192, 0),
			},
			reservations: []v1alpha1.Reservation{
				makeSlot("other-project", group, 8192, 0), // different project
			},
			expectedCase: "B",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyNoHostFound(tt.activeCRs, tt.reservations, proj, group)
			if got != tt.expectedCase {
				t.Errorf("classifyNoHostFound() = %q, want %q", got, tt.expectedCase)
			}
		})
	}
}

func TestReservationRemainingMemory(t *testing.T) {
	tests := []struct {
		name        string
		totalMemMiB int64
		usedMemMiB  int64
		wantBytes   int64
	}{
		{"empty slot", 8192, 0, 8192 * 1024 * 1024},
		{"partially used", 8192, 4096, 4096 * 1024 * 1024},
		{"fully used", 8192, 8192, 0},
		{"over-allocated (clamped to zero)", 4096, 8192, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := makeSlot("proj", "group", tt.totalMemMiB, tt.usedMemMiB)
			got := reservationRemainingMemory(res)
			if got != tt.wantBytes {
				t.Errorf("reservationRemainingMemory() = %d, want %d", got, tt.wantBytes)
			}
		})
	}
}

func TestLogNoHostFound(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	const (
		projectID   = "project-1"
		flavorName  = "m1.large"
		flavorGroup = "m1"
		instanceID  = "vm-uuid-1"
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

	makeCRObject := func(state v1alpha1.CommitmentStatus, amountMiB, usedMiB int64) *v1alpha1.CommittedResource {
		cr := &v1alpha1.CommittedResource{
			ObjectMeta: metav1.ObjectMeta{Name: "cr-1"},
			Spec: v1alpha1.CommittedResourceSpec{
				ProjectID:       projectID,
				FlavorGroupName: flavorGroup,
				State:           state,
				Amount:          *resource.NewQuantity(amountMiB*1024*1024, resource.BinarySI),
			},
		}
		if usedMiB > 0 {
			cr.Status.UsedResources = map[string]resource.Quantity{
				"memory": *resource.NewQuantity(usedMiB*1024*1024, resource.BinarySI),
			}
		}
		return cr
	}

	makeReservationSlot := func(name string, totalMemMiB, allocatedMemMiB int64) *v1alpha1.Reservation {
		res := &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
				},
			},
			Spec: v1alpha1.ReservationSpec{
				Resources: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(totalMemMiB*1024*1024, resource.BinarySI),
				},
				CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
					ProjectID:     projectID,
					ResourceGroup: flavorGroup,
				},
			},
		}
		if allocatedMemMiB > 0 {
			res.Spec.CommittedResourceReservation.Allocations = map[string]v1alpha1.CommittedResourceAllocation{
				"some-vm": {
					Resources: map[hv1.ResourceName]resource.Quantity{
						hv1.ResourceMemory: *resource.NewQuantity(allocatedMemMiB*1024*1024, resource.BinarySI),
					},
				},
			}
		}
		return res
	}

	tests := []struct {
		name         string
		objects      []client.Object
		payg         bool
		expectedCase string // "" means no counter increment expected
	}{
		{
			name:         "D: no active CRs",
			objects:      []client.Object{flavorKnowledge()},
			expectedCase: "D",
		},
		{
			name: "A: CRs fully occupied",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 8192),
			},
			expectedCase: "A",
		},
		{
			name: "B: capacity exists but no free slot",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
				makeReservationSlot("slot-full", 8192, 8192),
			},
			expectedCase: "B",
		},
		{
			name: "C: free slot exists",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
				makeReservationSlot("slot-free", 8192, 0),
			},
			expectedCase: "C",
		},
		{
			name:         "PAYG: flavor not in any group",
			objects:      []client.Object{flavorKnowledge()},
			payg:         true,
			expectedCase: "",
		},
		{
			name: "no knowledge CRD: no counter increment",
			objects: []client.Object{
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 0),
			},
			expectedCase: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			counter := NewNoHostFoundCounter()
			reg := prometheus.NewRegistry()
			reg.MustRegister(counter)

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client: fakeClient,
				},
				NoHostFoundCounter: counter,
			}

			requestFlavorName := flavorName
			if tt.payg {
				requestFlavorName = "unknown-flavor"
			}
			request := api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						InstanceUUID: instanceID,
						Flavor: api.NovaObject[api.NovaFlavor]{
							Data: api.NovaFlavor{Name: requestFlavorName},
						},
					},
				},
				Context: api.NovaRequestContext{ProjectID: projectID},
			}
			decision := &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{Intent: api.CreateIntent},
			}

			controller.logNoHostFound(context.Background(), decision, request)

			if tt.expectedCase == "" {
				total := testutil.ToFloat64(counter.WithLabelValues("A", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("B", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("C", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("D", flavorGroup, string(api.CreateIntent)))
				if total != 0 {
					t.Errorf("expected no counter increment, got total %.0f", total)
				}
			} else {
				got := testutil.ToFloat64(counter.WithLabelValues(tt.expectedCase, flavorGroup, string(api.CreateIntent)))
				if got != 1 {
					t.Errorf("counter[case=%q, flavorGroup=%q, intent=%q] = %.0f, want 1",
						tt.expectedCase, flavorGroup, string(api.CreateIntent), got)
				}
			}
		})
	}
}

func TestFeatureGate_CommittedResourceTracking(t *testing.T) {
	const (
		projectID   = "project-1"
		flavorName  = "m1.large"
		flavorGroup = "m1"
	)

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	ratio := uint64(2048)
	fg := compute.FlavorGroupFeature{
		Name:           flavorGroup,
		Flavors:        []compute.FlavorInGroup{{Name: flavorName, VCPUs: 2, MemoryMB: 4096}},
		LargestFlavor:  compute.FlavorInGroup{Name: flavorName, VCPUs: 2, MemoryMB: 4096},
		SmallestFlavor: compute.FlavorInGroup{Name: flavorName, VCPUs: 2, MemoryMB: 4096},
		RamCoreRatio:   &ratio,
	}
	raw, err := v1alpha1.BoxFeatureList([]compute.FlavorGroupFeature{fg})
	if err != nil {
		t.Fatalf("BoxFeatureList: %v", err)
	}
	flavorKnowledge := &v1alpha1.Knowledge{
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

	// Zero hosts → pipeline returns no TargetHost → triggers logNoHostFound path.
	novaRequest := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				InstanceUUID: "test-instance",
				Flavor:       api.NovaObject[api.NovaFlavor]{Data: api.NovaFlavor{Name: flavorName}},
			},
		},
		Context:  api.NovaRequestContext{ProjectID: projectID},
		Hosts:    []api.ExternalSchedulerHost{},
		Pipeline: "test-pipeline",
	}
	novaRaw, err := json.Marshal(novaRequest)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	pipelineConf := v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
		Spec: v1alpha1.PipelineSpec{
			Type:             v1alpha1.PipelineTypeFilterWeigher,
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			CreateHistory:    true,
			Filters:          []v1alpha1.FilterSpec{},
			Weighers:         []v1alpha1.WeigherSpec{},
		},
	}

	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("CommittedResourceTracking=%v", enabled), func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(flavorKnowledge).
				WithStatusSubresource(&v1alpha1.Decision{}, &v1alpha1.History{}).
				Build()

			counter := NewNoHostFoundCounter()
			reg := prometheus.NewRegistry()
			reg.MustRegister(counter)

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          fakeClient,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
					HistoryManager:  lib.HistoryClient{Client: fakeClient},
				},
				FeatureGates:       conf.FeatureGates{CommittedResourceTracking: enabled},
				NoHostFoundCounter: counter,
			}
			controller.PipelineConfigs["test-pipeline"] = pipelineConf
			initResult := controller.InitPipeline(context.Background(), pipelineConf)
			if len(initResult.FilterErrors) > 0 || len(initResult.WeigherErrors) > 0 {
				t.Fatalf("pipeline init errors: filters=%v weighers=%v", initResult.FilterErrors, initResult.WeigherErrors)
			}
			controller.Pipelines["test-pipeline"] = initResult.Pipeline

			decision := &v1alpha1.Decision{
				ObjectMeta: metav1.ObjectMeta{Name: "test-decision", Namespace: "default"},
				Spec: v1alpha1.DecisionSpec{
					SchedulingDomain: v1alpha1.SchedulingDomainNova,
					PipelineRef:      corev1.ObjectReference{Name: "test-pipeline"},
					NovaRaw:          &runtime.RawExtension{Raw: novaRaw},
				},
			}

			if err := controller.ProcessNewDecisionFromAPI(context.Background(), decision); err != nil {
				t.Fatalf("ProcessNewDecisionFromAPI: %v", err)
			}

			count := testutil.CollectAndCount(counter)
			if enabled && count == 0 {
				t.Error("expected counter increment with CommittedResourceTracking=true, got 0")
			}
			if !enabled && count != 0 {
				t.Errorf("expected no counter increment with CommittedResourceTracking=false, got %d", count)
			}
		})
	}
}
