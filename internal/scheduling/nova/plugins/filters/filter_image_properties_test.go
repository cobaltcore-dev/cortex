// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// requestWith builds an ExternalSchedulerRequest with the given hosts and image
// properties.
func requestWith(hosts []string, properties map[string]any) api.ExternalSchedulerRequest {
	schedHosts := make([]api.ExternalSchedulerHost, 0, len(hosts))
	for _, h := range hosts {
		schedHosts = append(schedHosts, api.ExternalSchedulerHost{ComputeHost: h})
	}
	request := api.ExternalSchedulerRequest{Hosts: schedHosts}
	request.Spec.Data.Image.Data.Properties = api.NovaObject[map[string]any]{Data: properties}
	return request
}

// requestWithIntent builds an ExternalSchedulerRequest like requestWith, but
// additionally sets the _nova_check_type scheduler hint so GetIntent resolves
// to the given intent.
func requestWithIntent(hosts []string, properties map[string]any, checkType string) api.ExternalSchedulerRequest {
	request := requestWith(hosts, properties)
	request.Spec.Data.SchedulerHints = map[string]any{"_nova_check_type": checkType}
	return request
}

func TestFilterImagePropertiesStep_Run(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// host1 and host2 are known kvm hypervisors, host3 is not registered.
	hvs := []client.Object{
		&hv1.Hypervisor{ObjectMeta: v1.ObjectMeta{Name: "host1"}},
		&hv1.Hypervisor{ObjectMeta: v1.ObjectMeta{Name: "host2"}},
	}

	tests := []struct {
		name           string
		request        api.ExternalSchedulerRequest
		expectedHosts  []string
		filteredHosts  []string
		expectedEvents []lib.FilterWeigherPipelineStepEvent
	}{
		{
			name:          "kvm image keeps all hosts",
			request:       requestWith([]string{"host1", "host2", "host3"}, map[string]any{"img_hv_type": "kvm"}),
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name:          "vmware image filters out known kvm hypervisors",
			request:       requestWith([]string{"host1", "host2", "host3"}, map[string]any{"hypervisor_type": "vmware"}),
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
		{
			name:          "baremetal image filters out known kvm hypervisors",
			request:       requestWith([]string{"host1", "host3"}, map[string]any{"img_hv_type": "baremetal"}),
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1"},
		},
		{
			name:          "undeterminable hypervisor type keeps all hosts",
			request:       requestWith([]string{"host1", "host2", "host3"}, nil),
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
			expectedEvents: []lib.FilterWeigherPipelineStepEvent{
				{Name: "image_properties_hv_type_undetermined", Labels: map[string]string{"intent": "unknown"}},
			},
		},
		{
			name:          "unsupported hypervisor type keeps all hosts",
			request:       requestWith([]string{"host1", "host2"}, map[string]any{"img_hv_type": "xen"}),
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
			expectedEvents: []lib.FilterWeigherPipelineStepEvent{
				{Name: "image_properties_hv_type_undetermined", Labels: map[string]string{"intent": "unknown"}},
			},
		},
		{
			name:          "empty host list",
			request:       requestWith([]string{}, map[string]any{"hypervisor_type": "vmware"}),
			expectedHosts: []string{},
			filteredHosts: []string{},
		},
		{
			name:          "reserve_for_failover intent skips filter and keeps all hosts",
			request:       requestWithIntent([]string{"host1", "host2", "host3"}, map[string]any{"hypervisor_type": "vmware"}, "reserve_for_failover"),
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name:          "reuse_failover_reservation intent skips filter and keeps all hosts",
			request:       requestWithIntent([]string{"host1", "host2", "host3"}, map[string]any{"hypervisor_type": "vmware"}, "reuse_failover_reservation"),
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name:          "reserve_for_committed_resource intent skips filter and keeps all hosts",
			request:       requestWithIntent([]string{"host1", "host2", "host3"}, map[string]any{"hypervisor_type": "vmware"}, "reserve_for_committed_resource"),
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name:          "capacity_probe intent skips filter and keeps all hosts",
			request:       requestWithIntent([]string{"host1", "host2", "host3"}, map[string]any{"hypervisor_type": "vmware"}, "capacity_probe"),
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
		},
		{
			name:          "create intent still filters out known kvm hypervisors",
			request:       requestWithIntent([]string{"host1", "host2", "host3"}, map[string]any{"hypervisor_type": "vmware"}, "create"),
			expectedHosts: []string{"host3"},
			filteredHosts: []string{"host1", "host2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterImagePropertiesStep{}
			step.Client = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hvs...).
				Build()
			result, err := step.Run(slog.Default(), tt.request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}

			if len(result.Events) != len(tt.expectedEvents) {
				t.Errorf("expected %d events, got %d", len(tt.expectedEvents), len(result.Events))
			}
			for i, expectedEvent := range tt.expectedEvents {
				if i >= len(result.Events) {
					break
				}
				gotEvent := result.Events[i]
				if gotEvent.Name != expectedEvent.Name {
					t.Errorf("expected event name %q, got %q", expectedEvent.Name, gotEvent.Name)
				}
				if len(gotEvent.Labels) != len(expectedEvent.Labels) {
					t.Errorf("expected event labels %v, got %v", expectedEvent.Labels, gotEvent.Labels)
				}
				for k, v := range expectedEvent.Labels {
					if gotEvent.Labels[k] != v {
						t.Errorf("expected event label %q=%q, got %q", k, v, gotEvent.Labels[k])
					}
				}
			}
		})
	}
}
