// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

// Contract test for SkipPlacementContextFilters: every filter that claims to respect this
// option must pass all hosts when it is set, even when the request would normally filter some.
// Add a row to skipCases whenever a new filter is wired to SkipPlacementContextFilters.

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/scheduling"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type skipCase struct {
	name    string
	request api.ExternalSchedulerRequest
	objects []client.Object
	newStep func(client.Client) interface {
		Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
	}
}

var skipCases = []skipCase{
	{
		name: "filter_instance_group_affinity",
		// Affinity group on host1 only — host2 would be filtered without the flag.
		request: func() api.ExternalSchedulerRequest {
			r := newNovaRequest("vm", "proj", "m1.small", "gp", 1, "1Gi", false, []string{"host1", "host2"})
			r.Spec.Data.InstanceGroup = &api.NovaObject[api.NovaInstanceGroup]{
				Data: api.NovaInstanceGroup{Policy: "affinity", Hosts: []string{"host1"}},
			}
			return r
		}(),
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterInstanceGroupAffinityStep{}
			s.Client = c
			return s
		},
	},
	{
		name: "filter_instance_group_anti_affinity",
		// host2 already has the group member vm; max_server_per_host=1 → host2 filtered.
		request: func() api.ExternalSchedulerRequest {
			r := newNovaRequest("vm-new", "proj", "m1.small", "gp", 1, "1Gi", false, []string{"host1", "host2"})
			r.Spec.Data.InstanceGroup = &api.NovaObject[api.NovaInstanceGroup]{
				Data: api.NovaInstanceGroup{
					Policy:  "anti-affinity",
					Members: []string{"vm-existing"},
					Rules:   map[string]any{"max_server_per_host": 1},
				},
			}
			return r
		}(),
		objects: []client.Object{
			&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host1"}},
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host2"},
				Status:     hv1.HypervisorStatus{Instances: []hv1.Instance{{ID: "vm-existing"}}},
			},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterInstanceGroupAntiAffinityStep{}
			s.Client = c
			return s
		},
	},
	{
		name: "filter_aggregate_metadata",
		// host1 is in an aggregate restricting to project-x; request is project-y → host1 filtered.
		request: api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{ProjectID: "project-y"},
			},
			Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
		},
		objects: []client.Object{
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host1"},
				Status: hv1.HypervisorStatus{
					Aggregates: []hv1.Aggregate{{
						Name:     "restricted",
						Metadata: map[string]string{"filter_tenant_id": "project-x"},
					}},
				},
			},
			&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host2"}},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterAggregateMetadata{}
			s.Client = c
			return s
		},
	},
	{
		name: "filter_live_migratable",
		// host2 has a different CPU arch than the source → incompatible for live migration → filtered.
		request: api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{
					SchedulerHints: map[string]any{
						"_nova_check_type": "live_migrate",
						"source_host":      "source-host",
					},
				},
			},
			Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
		},
		objects: []client.Object{
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "source-host"},
				Status:     hv1.HypervisorStatus{Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"}},
			},
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host1"},
				Status:     hv1.HypervisorStatus{Capabilities: hv1.Capabilities{HostCpuArch: "x86_64"}},
			},
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host2"},
				Status:     hv1.HypervisorStatus{Capabilities: hv1.Capabilities{HostCpuArch: "aarch64"}},
			},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterLiveMigratableStep{}
			s.Client = c
			return s
		},
	},
	{
		name: "filter_requested_destination",
		// RequestedDestination forces host1 — host2 would be filtered.
		request: api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{
					RequestedDestination: &api.NovaObject[api.NovaRequestedDestination]{
						Data: api.NovaRequestedDestination{Host: "host1"},
					},
				},
			},
			Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
		},
		objects: []client.Object{
			&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host1"}},
			&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host2"}},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterRequestedDestinationStep{}
			s.Client = c
			return s
		},
	},
	{
		name: "filter_quota_enforcement",
		// Enforce mode: quota fully consumed (PaygUsage == Quota) → all hosts filtered.
		request: func() api.ExternalSchedulerRequest {
			r := newNovaRequest("vm", "project-no-quota", "m1.small", "gp", 1, "1Gi", false, []string{"host1", "host2"})
			r.Spec.Data.AvailabilityZone = "az-1"
			return r
		}(),
		objects: []client.Object{
			&v1alpha1.ProjectQuota{
				ObjectMeta: metav1.ObjectMeta{Name: "quota-project-no-quota-az-1"},
				Spec: v1alpha1.ProjectQuotaSpec{
					ProjectID:        "project-no-quota",
					AvailabilityZone: "az-1",
					Quota:            map[string]int64{"hw_version_gp_ram": 10, "hw_version_gp_cores": 10, "hw_version_gp_instances": 1},
				},
				Status: v1alpha1.ProjectQuotaStatus{
					PaygUsage: map[string]int64{"hw_version_gp_ram": 10, "hw_version_gp_cores": 10, "hw_version_gp_instances": 1},
				},
			},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterQuotaEnforcement{}
			s.Client = c
			s.Options = FilterQuotaEnforcementOpts{Enforce: true}
			return s
		},
	},
	{
		name: "filter_allowed_projects",
		// host2 restricts to project-x; request is project-y → host2 filtered.
		request: api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{ProjectID: "project-y"},
			},
			Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
		},
		objects: []client.Object{
			&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host1"}},
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host2"},
				Spec:       hv1.HypervisorSpec{AllowedProjects: []string{"project-x"}},
			},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterAllowedProjectsStep{}
			s.Client = c
			return s
		},
	},
	{
		name: "filter_external_customer",
		// Domain matches external prefix; host1 lacks the exclusive trait → host1 filtered.
		request: api.ExternalSchedulerRequest{
			Spec: api.NovaObject[api.NovaSpec]{
				Data: api.NovaSpec{
					SchedulerHints: map[string]any{"domain_name": "iaas-customer"},
				},
			},
			Hosts: []api.ExternalSchedulerHost{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
		},
		objects: []client.Object{
			&hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: "host1"}},
			&hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{Name: "host2"},
				Status:     hv1.HypervisorStatus{Traits: []string{"CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"}},
			},
		},
		newStep: func(c client.Client) interface {
			Run(*slog.Logger, api.ExternalSchedulerRequest) (*lib.FilterWeigherPipelineStepResult, error)
		} {
			s := &FilterExternalCustomerStep{}
			s.Client = c
			s.Options = FilterExternalCustomerStepOpts{CustomerDomainNamePrefixes: []string{"iaas-"}}
			return s
		},
	},
}

func TestSkipPlacementContextFilters(t *testing.T) {
	scheme := buildSkipTestScheme(t)

	for _, tc := range skipCases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).Build()
			step := tc.newStep(c)

			// Without the flag: verify the scenario actually filters something (proves setup is valid).
			withoutFlag := tc.request
			withoutFlag.Options = scheduling.Options{}
			resultWithout, err := step.Run(slog.Default(), withoutFlag)
			if err != nil {
				t.Fatalf("Run without flag: unexpected error: %v", err)
			}
			if len(resultWithout.Activations) == len(tc.request.Hosts) {
				t.Fatalf("setup invalid: expected at least one host to be filtered without the flag, but all %d passed", len(tc.request.Hosts))
			}

			// With the flag: all hosts must pass.
			withFlag := tc.request
			withFlag.Options = scheduling.Options{SkipPlacementContextFilters: true}
			resultWith, err := step.Run(slog.Default(), withFlag)
			if err != nil {
				t.Fatalf("Run with SkipPlacementContextFilters=true: unexpected error: %v", err)
			}
			if len(resultWith.Activations) != len(tc.request.Hosts) {
				t.Errorf("expected all %d hosts to pass with SkipPlacementContextFilters=true, got %d",
					len(tc.request.Hosts), len(resultWith.Activations))
			}
		})
	}
}

func buildSkipTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add hv1 to scheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 to scheme: %v", err)
	}
	return scheme
}
