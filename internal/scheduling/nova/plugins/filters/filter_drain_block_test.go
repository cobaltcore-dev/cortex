// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func blockingRequest(name string, blocked ...string) *v1alpha1.ReschedulingRequest {
	return &v1alpha1.ReschedulingRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReschedulingRequestSpec{Action: v1alpha1.ReschedulingActionDrain},
		Status:     v1alpha1.ReschedulingRequestStatus{BlockedHosts: blocked},
	}
}

func TestFilterDrainBlockStep_Run(t *testing.T) {
	request := api.ExternalSchedulerRequest{
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
			{ComputeHost: "host3"},
		},
	}

	tests := []struct {
		name     string
		requests []client.Object
		expected []string
		filtered []string
	}{
		{
			name:     "no rescheduling requests keeps all hosts",
			expected: []string{"host1", "host2", "host3"},
		},
		{
			name:     "request with no blocked hosts keeps all hosts",
			requests: []client.Object{blockingRequest("rr")},
			expected: []string{"host1", "host2", "host3"},
		},
		{
			name:     "blocked hosts are filtered out",
			requests: []client.Object{blockingRequest("rr", "host2")},
			expected: []string{"host1", "host3"},
			filtered: []string{"host2"},
		},
		{
			name: "blocked hosts across multiple requests are all filtered",
			requests: []client.Object{
				blockingRequest("rr1", "host1"),
				blockingRequest("rr2", "host3"),
			},
			expected: []string{"host2"},
			filtered: []string{"host1", "host3"},
		},
		{
			name:     "blocked host not in the request is ignored",
			requests: []client.Object{blockingRequest("rr", "other-host")},
			expected: []string{"host1", "host2", "host3"},
		},
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterDrainBlockStep{}
			step.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.requests...).Build()
			result, err := step.Run(slog.Default(), request)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			for _, host := range tt.expected {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present", host)
				}
			}
			for _, host := range tt.filtered {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}
			if len(result.Activations) != len(tt.expected) {
				t.Errorf("expected %d hosts, got %d", len(tt.expected), len(result.Activations))
			}
		})
	}
}
