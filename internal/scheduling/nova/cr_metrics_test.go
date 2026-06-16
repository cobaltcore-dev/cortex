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
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/crs"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
)

func TestLogNoHostFound(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme hv1: %v", err)
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

	makeReadyReservationSlot := func(name, targetHost string, totalMemMiB, allocatedMemMiB int64) *v1alpha1.Reservation {
		res := makeReservationSlot(name, totalMemMiB, allocatedMemMiB)
		res.Spec.TargetHost = targetHost
		res.Status.Conditions = []metav1.Condition{{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			LastTransitionTime: metav1.Now(),
		}}
		return res
	}

	makeHV := func(name string, capacityMiB int64) *hv1.Hypervisor {
		return &hv1.Hypervisor{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: hv1.HypervisorStatus{
				EffectiveCapacity: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(capacityMiB*1024*1024, resource.BinarySI),
				},
				Allocation: map[hv1.ResourceName]resource.Quantity{
					hv1.ResourceMemory: *resource.NewQuantity(0, resource.BinarySI),
				},
			},
		}
	}

	tests := []struct {
		name                string
		objects             []client.Object
		requestHosts        []api.ExternalSchedulerHost
		payg                bool
		expectedCase        string // "" means no counter increment expected
		expectedFlavorGroup string // defaults to flavorGroup if empty
	}{
		{
			name:         "no_cr: no active CRs",
			objects:      []client.Object{flavorKnowledge()},
			expectedCase: "no_cr",
		},
		{
			name: "cr_exhausted: CRs fully occupied",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 8192),
			},
			expectedCase: "cr_exhausted",
		},
		{
			name: "slot_exhausted: slot exists but fully allocated",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
				makeHV("host-1", 16384),
				makeReadyReservationSlot("slot-full", "host-1", 8192, 8192),
			},
			requestHosts: []api.ExternalSchedulerHost{{ComputeHost: "host-1"}},
			expectedCase: "slot_exhausted",
		},
		{
			name: "slot_blocked: free slot on candidate host",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 4096),
				makeHV("host-1", 16384),
				makeReadyReservationSlot("slot-free", "host-1", 8192, 4096),
			},
			requestHosts: []api.ExternalSchedulerHost{{ComputeHost: "host-1"}},
			expectedCase: "slot_blocked",
		},
		{
			name: "no_cr: inactive CR (pending state) is filtered by IsActive()",
			objects: []client.Object{
				flavorKnowledge(),
				makeCRObject(v1alpha1.CommitmentStatusPending, 8192, 0),
			},
			expectedCase: "no_cr",
		},
		{
			name: "no_cr: CR for wrong project is filtered by MatchesGroup()",
			objects: []client.Object{
				flavorKnowledge(),
				func() *v1alpha1.CommittedResource {
					cr := makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 0)
					cr.Spec.ProjectID = "other-project"
					return cr
				}(),
			},
			expectedCase: "no_cr",
		},
		{
			name:         "PAYG: flavor not in any group",
			objects:      []client.Object{flavorKnowledge()},
			payg:         true,
			expectedCase: "",
		},
		{
			name: "error: knowledge CRD unavailable",
			objects: []client.Object{
				makeCRObject(v1alpha1.CommitmentStatusConfirmed, 8192, 0),
			},
			expectedCase:        "error",
			expectedFlavorGroup: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			counter := crs.NewNoHostFoundCounter()
			reg := prometheus.NewRegistry()
			reg.MustRegister(counter)

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client: fakeClient,
				},
				CRRecorder: crs.Recorder{
					Client:             fakeClient,
					NoHostFoundCounter: counter,
				},
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
				Hosts:   tt.requestHosts,
			}
			decision := &v1alpha1.Decision{
				Spec: v1alpha1.DecisionSpec{Intent: api.CreateIntent},
			}

			controller.CRRecorder.RecordNoHostFound(context.Background(), decision, request)

			if tt.expectedCase == "" {
				total := testutil.ToFloat64(counter.WithLabelValues("no_cr", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("cr_exhausted", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("slot_exhausted", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("slot_blocked", flavorGroup, string(api.CreateIntent))) +
					testutil.ToFloat64(counter.WithLabelValues("error", "unknown", string(api.CreateIntent)))
				if total != 0 {
					t.Errorf("expected no counter increment, got total %.0f", total)
				}
			} else {
				expectedFG := tt.expectedFlavorGroup
				if expectedFG == "" {
					expectedFG = flavorGroup
				}
				got := testutil.ToFloat64(counter.WithLabelValues(tt.expectedCase, expectedFG, string(api.CreateIntent)))
				if got != 1 {
					t.Errorf("counter[case=%q, flavorGroup=%q, intent=%q] = %.0f, want 1",
						tt.expectedCase, expectedFG, string(api.CreateIntent), got)
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
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme hv1: %v", err)
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

	// Zero hosts → pipeline returns no TargetHost → triggers RecordNoHostFound path.
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

			counter := crs.NewNoHostFoundCounter()
			reg := prometheus.NewRegistry()
			reg.MustRegister(counter)

			controller := &FilterWeigherPipelineController{
				BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
					Client:          fakeClient,
					Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
					PipelineConfigs: make(map[string]v1alpha1.Pipeline),
					HistoryManager:  lib.HistoryClient{Client: fakeClient},
				},
				FeatureGates: FeatureGates{CommittedResourceTracking: enabled},
				CRRecorder: crs.Recorder{
					Client:             fakeClient,
					NoHostFoundCounter: counter,
				},
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
