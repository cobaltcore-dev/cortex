// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestFilterLiveMigratableStep_isLiveMigration(t *testing.T) {
	tests := []struct {
		name     string
		request  api.ExternalSchedulerRequest
		expected bool
	}{
		{
			name: "Live migration request",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Live migration request with list hint",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": []any{"live_migrate"},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Non-live migration request",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "rebuild",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Missing check type hint",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"other_hint": "value",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Nil scheduler hints",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: nil,
					},
				},
			},
			expected: false,
		},
		{
			name: "Empty scheduler hints",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterLiveMigratableStep{}
			result := step.isLiveMigration(tt.request)
			if result != tt.expected {
				t.Errorf("isLiveMigration() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFilterLiveMigratableStep_checkHasSufficientFeatures(t *testing.T) {
	tests := []struct {
		name      string
		sourceHV  hv1.Hypervisor
		targetHV  hv1.Hypervisor
		expectErr bool
		errMsg    string
	}{
		{
			name: "Matching architectures and capabilities",
			sourceHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedCpuModes: []string{"host-passthrough", "custom"},
						SupportedFeatures: []string{"sev", "sgx"},
						SupportedDevices:  []string{"video", "disk"},
					},
				},
			},
			targetHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedCpuModes: []string{"host-passthrough", "custom", "host-model"},
						SupportedFeatures: []string{"sev", "sgx", "apic"},
						SupportedDevices:  []string{"video", "disk", "network"},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "Different CPU architectures",
			sourceHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
				},
			},
			targetHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "aarch64",
					},
				},
			},
			expectErr: true,
			errMsg:    "cpu architectures do not match",
		},
		{
			name: "Missing CPU mode on target",
			sourceHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedCpuModes: []string{"host-passthrough", "custom"},
					},
				},
			},
			targetHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedCpuModes: []string{"host-passthrough"},
					},
				},
			},
			expectErr: true,
			errMsg:    "cpu modes do not match",
		},
		{
			name: "Missing feature on target",
			sourceHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedFeatures: []string{"sev", "sgx"},
					},
				},
			},
			targetHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedFeatures: []string{"sev"},
					},
				},
			},
			expectErr: true,
			errMsg:    "hv features do not match",
		},
		{
			name: "Missing device on target",
			sourceHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedDevices: []string{"video", "disk"},
					},
				},
			},
			targetHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedDevices: []string{"video"},
					},
				},
			},
			expectErr: true,
			errMsg:    "emulated devices do not match",
		},
		{
			name: "Empty capabilities - should pass",
			sourceHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedCpuModes: []string{},
						SupportedFeatures: []string{},
						SupportedDevices:  []string{},
					},
				},
			},
			targetHV: hv1.Hypervisor{
				Status: hv1.HypervisorStatus{
					Capabilities: hv1.Capabilities{
						HostCpuArch: "x86_64",
					},
					DomainCapabilities: hv1.DomainCapabilities{
						SupportedCpuModes: []string{"host-passthrough"},
						SupportedFeatures: []string{"sev"},
						SupportedDevices:  []string{"video"},
					},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := &FilterLiveMigratableStep{}
			err := step.checkHasSufficientFeatures(tt.sourceHV, tt.targetHV)
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestFilterLiveMigratableStep_Run(t *testing.T) {
	tests := []struct {
		name          string
		request       api.ExternalSchedulerRequest
		hypervisors   []hv1.Hypervisor
		expectedHosts []string
		filteredHosts []string
		expectErr     bool
	}{
		{
			name: "Not a live migration - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "rebuild",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
			expectErr:     false,
		},
		{
			name: "Live migration without source_host - all hosts pass",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{},
			expectErr:     false,
		},
		{
			name: "Live migration with compatible hosts",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
							SupportedFeatures: []string{"sev"},
							SupportedDevices:  []string{"video"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
							SupportedFeatures: []string{"sev", "sgx"},
							SupportedDevices:  []string{"video", "disk"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
							SupportedFeatures: []string{"sev"},
							SupportedDevices:  []string{"video"},
						},
					},
				},
			},
			expectedHosts: []string{"host1", "host2", "host3"},
			filteredHosts: []string{},
			expectErr:     false,
		},
		{
			name: "Live migration with incompatible architecture",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "aarch64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
						},
					},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "Live migration with missing CPU modes",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough", "custom"},
							SupportedFeatures: []string{"sev"},
							SupportedDevices:  []string{"video"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough"},
							SupportedFeatures: []string{"sev"},
							SupportedDevices:  []string{"video"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedCpuModes: []string{"host-passthrough", "custom", "host-model"},
							SupportedFeatures: []string{"sev"},
							SupportedDevices:  []string{"video"},
						},
					},
				},
			},
			expectedHosts: []string{"host1", "host3"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "Live migration with missing features",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedFeatures: []string{"sev", "sgx"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedFeatures: []string{"sev"},
						},
					},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "Live migration with missing devices",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedDevices: []string{"video", "disk", "network"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
						DomainCapabilities: hv1.DomainCapabilities{
							SupportedDevices: []string{"video", "disk"},
						},
					},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2"},
			expectErr:     false,
		},
		{
			name: "Live migration with non-existent target host",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host-nonexistent"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
			},
			expectedHosts: []string{"host1", "host2"},
			filteredHosts: []string{"host-nonexistent"},
			expectErr:     false,
		},
		{
			name: "Live migration with all hosts filtered",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{
					{ComputeHost: "host1"},
					{ComputeHost: "host2"},
					{ComputeHost: "host3"},
				},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host2"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "aarch64"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host3"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "ppc64le"},
					},
				},
			},
			expectedHosts: []string{"host1"},
			filteredHosts: []string{"host2", "host3"},
			expectErr:     false,
		},
		{
			name: "Live migration with empty host list",
			request: api.ExternalSchedulerRequest{
				Spec: api.NovaObject[api.NovaSpec]{
					Data: api.NovaSpec{
						SchedulerHints: map[string]any{
							"_nova_check_type": "live_migrate",
							"source_host":      "host1",
						},
					},
				},
				Hosts: []api.ExternalSchedulerHost{},
			},
			hypervisors: []hv1.Hypervisor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "host1"},
					Status: hv1.HypervisorStatus{
						Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
					},
				},
			},
			expectedHosts: []string{},
			filteredHosts: []string{},
			expectErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with hypervisors
			scheme := runtime.NewScheme()
			if err := hv1.AddToScheme(scheme); err != nil {
				t.Fatalf("Failed to add hv1 scheme: %v", err)
			}
			objs := make([]client.Object, len(tt.hypervisors))
			for i := range tt.hypervisors {
				objs[i] = &tt.hypervisors[i]
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			step := &FilterLiveMigratableStep{
				BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]{
					Client: fakeClient,
				},
			}

			result, err := step.Run(slog.Default(), tt.request)

			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// Check expected hosts are present
			for _, host := range tt.expectedHosts {
				if _, ok := result.Activations[host]; !ok {
					t.Errorf("expected host %s to be present in activations", host)
				}
			}

			// Check filtered hosts are not present
			for _, host := range tt.filteredHosts {
				if _, ok := result.Activations[host]; ok {
					t.Errorf("expected host %s to be filtered out", host)
				}
			}

			// Check total count
			if len(result.Activations) != len(tt.expectedHosts) {
				t.Errorf("expected %d hosts, got %d", len(tt.expectedHosts), len(result.Activations))
			}
		})
	}
}

func TestFilterLiveMigratableStep_Run_SourceHostNotFound(t *testing.T) {
	request := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				SchedulerHints: map[string]any{
					"_nova_check_type": "live_migrate",
					"source_host":      "nonexistent-host",
				},
			},
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
		},
	}

	hypervisors := []hv1.Hypervisor{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "host1"},
			Status: hv1.HypervisorStatus{
				Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "host2"},
			Status: hv1.HypervisorStatus{
				Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"},
			},
		},
	}

	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hv1 scheme: %v", err)
	}
	objs := make([]client.Object, len(hypervisors))
	for i := range hypervisors {
		objs[i] = &hypervisors[i]
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()

	step := &FilterLiveMigratableStep{
		BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]{
			Client: fakeClient,
		},
	}

	_, err := step.Run(slog.Default(), request)
	if err == nil {
		t.Errorf("expected error when source host not found, got none")
	}
	if err != nil && err.Error() != "source host hypervisor not found" {
		t.Errorf("expected 'source host hypervisor not found' error, got %v", err)
	}
}

func TestFilterLiveMigratableStep_Run_ClientError(t *testing.T) {
	request := api.ExternalSchedulerRequest{
		Spec: api.NovaObject[api.NovaSpec]{
			Data: api.NovaSpec{
				SchedulerHints: map[string]any{
					"_nova_check_type": "live_migrate",
					"source_host":      "host1",
				},
			},
		},
		Hosts: []api.ExternalSchedulerHost{
			{ComputeHost: "host1"},
		},
	}

	// Create a client that will fail on List operations
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hv1 scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return context.Canceled
			},
		}).
		Build()

	step := &FilterLiveMigratableStep{
		BaseFilter: lib.BaseFilter[api.ExternalSchedulerRequest, lib.EmptyStepOpts]{
			Client: fakeClient,
		},
	}

	_, err := step.Run(slog.Default(), request)
	if err == nil {
		t.Errorf("expected error when client fails, got none")
	}
}
