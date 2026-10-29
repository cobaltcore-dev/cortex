// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"cmp"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newInflightIntegrationServer serves the Nova external scheduler API backed by
// a real capacity-filter pipeline and, if withInflights, the in-flight producer.
func newInflightIntegrationServer(t *testing.T, withInflights bool, resizeUnknownSource string, objects ...client.Object) (*IntegrationTestServer, client.Client) {
	t.Helper()
	scheme := buildTestScheme(t)
	if err := vmv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Reservation{}).Build()
	controller := &FilterWeigherPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor:        getSharedMonitor(),
		InFlightConfig: InFlightConfig{TTLSeconds: 3600, MaxCandidates: 3},
	}
	if withInflights {
		controller.inflightProducer = &inflightProducer{Client: k8sClient, OwnerResolver: vmOwnerResolver{Client: k8sClient}, TTL: time.Hour, ResizeUnknownSource: cmp.Or(resizeUnknownSource, resizeUnknownSourceFail)}
	}
	pipeline := v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "kvm-general-purpose-load-balancing"},
		Spec: v1alpha1.PipelineSpec{
			Type:    v1alpha1.PipelineTypeFilterWeigher,
			Filters: []v1alpha1.FilterSpec{{Name: "filter_host_instructions"}, {Name: "filter_has_enough_capacity"}},
		},
	}
	result := lib.InitNewFilterWeigherPipeline(t.Context(), k8sClient, pipeline.Name,
		filters.Index, pipeline.Spec.Filters, weighers.Index, pipeline.Spec.Weighers, controller.Monitor)
	if len(result.FilterErrors) > 0 || len(result.WeigherErrors) > 0 {
		t.Fatalf("init pipeline: %+v", result)
	}
	controller.Pipelines[pipeline.Name] = result.Pipeline
	controller.PipelineConfigs[pipeline.Name] = pipeline

	api := &httpAPI{monitor: lib.NewSchedulerMonitor(), delegate: controller}
	mux := http.NewServeMux()
	mux.HandleFunc("/scheduler/nova/external", api.NovaExternalScheduler)
	server := &IntegrationTestServer{Server: httptest.NewServer(mux), K8sClient: k8sClient, Controller: controller}
	t.Cleanup(server.Close)
	return server, k8sClient
}

// inflightIntegrationHost builds a hypervisor in az-1 with the given CPU
// capacity and allocation, plenty of memory, and the given running VMs.
func inflightIntegrationHost(name, cpuCap, cpuAlloc string, instances ...string) *hv1.Hypervisor {
	hv := newHypervisor(name, cpuCap, cpuAlloc, "64Gi", "0")
	hv.Labels = map[string]string{corev1.LabelTopologyZone: "az-1"}
	for _, id := range instances {
		hv.Status.Instances = append(hv.Status.Instances, hv1.Instance{ID: id})
	}
	return hv
}

// inflightIntegrationRequest builds a KVM request for a 4 vCPU / 4 GiB flavor.
func inflightIntegrationRequest(vmID, checkType string, hosts ...string) novaapi.ExternalSchedulerRequest {
	req := newNovaRequest(vmID, "project-1", "m1.small", "hw-1", 4, "4Gi", false, hosts, "kvm-general-purpose-load-balancing")
	req.Spec.Data.AvailabilityZone = "az-1"
	if checkType != "" {
		req.Spec.Data.SchedulerHints = map[string]any{"_nova_check_type": []any{checkType}}
	}
	return req
}

func TestIntegration_InflightReservations(t *testing.T) {
	type step struct {
		request   novaapi.ExternalSchedulerRequest
		wantHosts []string // Checked as a set when non-nil.
		wantCount int      // Checked when wantHosts is nil.
	}
	tests := []struct {
		name                string
		withInflights       bool
		hosts               []*hv1.Hypervisor
		objects             []client.Object
		resizeUnknownSource string
		steps               []step
		// wantHolds maps target host to the expected number of in-flight reservations.
		wantHolds map[string]int
	}{
		{
			name:          "initial placement blocks every returned candidate",
			withInflights: true,
			hosts:         []*hv1.Hypervisor{inflightIntegrationHost("host-a", "4", "0"), inflightIntegrationHost("host-b", "4", "0")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "", "host-a", "host-b"), wantHosts: []string{"host-a", "host-b"}},
				// Both hosts are pessimistically held for vm-1, so vm-2 cannot be placed.
				{request: inflightIntegrationRequest("vm-2", "", "host-a", "host-b"), wantHosts: []string{}},
			},
			wantHolds: map[string]int{"host-a": 1, "host-b": 1},
		},
		{
			name:          "without in-flight reservations concurrent placements overlap",
			withInflights: false,
			hosts:         []*hv1.Hypervisor{inflightIntegrationHost("host-a", "4", "0"), inflightIntegrationHost("host-b", "4", "0")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "", "host-a", "host-b"), wantHosts: []string{"host-a", "host-b"}},
				{request: inflightIntegrationRequest("vm-2", "", "host-a", "host-b"), wantHosts: []string{"host-a", "host-b"}},
			},
			wantHolds: map[string]int{},
		},
		{
			name:          "initial placement is limited to three candidates",
			withInflights: true,
			hosts: []*hv1.Hypervisor{
				inflightIntegrationHost("host-a", "4", "0"), inflightIntegrationHost("host-b", "4", "0"),
				inflightIntegrationHost("host-c", "4", "0"), inflightIntegrationHost("host-d", "4", "0"),
			},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "", "host-a", "host-b", "host-c", "host-d"), wantCount: 3},
				// Exactly one host is left over for the next placement.
				{request: inflightIntegrationRequest("vm-2", "", "host-a", "host-b", "host-c", "host-d"), wantCount: 1},
			},
			wantHolds: map[string]int{"host-a": 1, "host-b": 1, "host-c": 1, "host-d": 1},
		},
		{
			name:          "live migration blocks the target for later placements",
			withInflights: true,
			hosts: []*hv1.Hypervisor{
				inflightIntegrationHost("host-src", "8", "4", "vm-1"),
				inflightIntegrationHost("host-b", "4", "0"), inflightIntegrationHost("host-c", "4", "0"),
			},
			steps: []step{
				{request: withSourceHost(inflightIntegrationRequest("vm-1", "live_migrate", "host-b"), "host-src"), wantHosts: []string{"host-b"}},
				{request: inflightIntegrationRequest("vm-2", "", "host-b", "host-c"), wantHosts: []string{"host-c"}},
			},
			wantHolds: map[string]int{"host-src": 1, "host-b": 1, "host-c": 1},
		},
		{
			name:          "resize to a different host holds target and source",
			withInflights: true,
			// vm-1 is a KVM instance on host-src (old size 4 vCPU) resized onto
			// host-large. The pipeline scores the requested flavor; a source hold
			// keeps host-src and a target hold keeps host-large.
			hosts: []*hv1.Hypervisor{
				inflightIntegrationHost("host-src", "8", "4", "vm-1"),
				inflightIntegrationHost("host-large", "8", "0"),
			},
			objects: []client.Object{inflightIntegrationVM("vm-1", "host-src", "4", "4Gi")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "resize", "host-large"), wantHosts: []string{"host-large"}},
			},
			wantHolds: map[string]int{"host-src": 1, "host-large": 1},
		},
		{
			name:          "resize on the same host holds target and source there",
			withInflights: true,
			// vm-1 resizes in place on host-a; both the old and new size are held
			// on host-a, so two holds land on the same host.
			hosts:   []*hv1.Hypervisor{inflightIntegrationHost("host-a", "16", "4", "vm-1")},
			objects: []client.Object{inflightIntegrationVM("vm-1", "host-a", "4", "4Gi")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "resize", "host-a"), wantHosts: []string{"host-a"}},
			},
			wantHolds: map[string]int{"host-a": 2},
		},
		{
			name:                "resize of an unmanaged source holds only the target",
			withInflights:       true,
			resizeUnknownSource: "skip",
			// vm-1 has no VM CRD and is on no hypervisor (e.g. a VMware source),
			// so only the target hold is created on the KVM destination.
			hosts: []*hv1.Hypervisor{inflightIntegrationHost("host-large", "8", "0")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "resize", "host-large"), wantHosts: []string{"host-large"}},
			},
			wantHolds: map[string]int{"host-large": 1},
		},
		{
			name:          "evacuation returns a single host without creating holds",
			withInflights: true,
			hosts: []*hv1.Hypervisor{
				inflightIntegrationHost("host-a", "4", "0"), inflightIntegrationHost("host-b", "4", "0"), inflightIntegrationHost("host-c", "4", "0"),
			},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "evacuate", "host-a", "host-b", "host-c"), wantCount: 1},
			},
			wantHolds: map[string]int{},
		},
		{
			name:          "new request for the same VM ignores its obsolete holds",
			withInflights: true,
			hosts:         []*hv1.Hypervisor{inflightIntegrationHost("host-a", "4", "0")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "", "host-a"), wantHosts: []string{"host-a"}},
				// Nova serializes operations per VM, so vm-1's earlier holds no longer block it.
				{request: inflightIntegrationRequest("vm-1", "", "host-a"), wantHosts: []string{"host-a"}},
				// Holds of vm-1 still block other VMs until the release controller removes them.
				{request: inflightIntegrationRequest("vm-2", "", "host-a"), wantHosts: []string{}},
			},
			wantHolds: map[string]int{"host-a": 2},
		},
		{
			name:          "holds of several VMs add up",
			withInflights: true,
			hosts: []*hv1.Hypervisor{
				inflightIntegrationHost("host-a", "4", "0"), inflightIntegrationHost("host-b", "4", "0"), inflightIntegrationHost("host-c", "4", "0"),
			},
			steps: []step{
				{request: withSingleCandidate(inflightIntegrationRequest("vm-1", "", "host-a", "host-b", "host-c")), wantCount: 1},
				{request: withSingleCandidate(inflightIntegrationRequest("vm-2", "", "host-a", "host-b", "host-c")), wantCount: 1},
				{request: withSingleCandidate(inflightIntegrationRequest("vm-3", "", "host-a", "host-b", "host-c")), wantCount: 1},
				{request: withSingleCandidate(inflightIntegrationRequest("vm-4", "", "host-a", "host-b", "host-c")), wantHosts: []string{}},
			},
			wantHolds: map[string]int{"host-a": 1, "host-b": 1, "host-c": 1},
		},
		{
			name:          "holds only block capacity that is actually used up",
			withInflights: true,
			hosts:         []*hv1.Hypervisor{inflightIntegrationHost("host-a", "8", "0")},
			steps: []step{
				{request: inflightIntegrationRequest("vm-1", "", "host-a"), wantHosts: []string{"host-a"}},
				{request: inflightIntegrationRequest("vm-2", "", "host-a"), wantHosts: []string{"host-a"}},
				{request: inflightIntegrationRequest("vm-3", "", "host-a"), wantHosts: []string{}},
			},
			wantHolds: map[string]int{"host-a": 2},
		},
		{
			name:          "live migration retry skips the ignored target",
			withInflights: true,
			hosts: []*hv1.Hypervisor{
				inflightIntegrationHost("host-src", "8", "4", "vm-1"),
				inflightIntegrationHost("host-b", "4", "0"), inflightIntegrationHost("host-c", "4", "0"),
			},
			steps: []step{
				{request: withSourceHost(inflightIntegrationRequest("vm-1", "live_migrate", "host-b"), "host-src"), wantHosts: []string{"host-b"}},
				// Nova retries with the failed target in ignore_hosts.
				{request: withIgnoredHosts(withSourceHost(inflightIntegrationRequest("vm-1", "live_migrate", "host-b", "host-c"), "host-src"), "host-b"), wantHosts: []string{"host-c"}},
			},
			wantHolds: map[string]int{"host-src": 2, "host-b": 1, "host-c": 1},
		},
		{
			name:          "skip_inflight requests create no holds",
			withInflights: true,
			hosts:         []*hv1.Hypervisor{inflightIntegrationHost("host-a", "4", "0")},
			steps: []step{
				{request: withSkipInflight(inflightIntegrationRequest("vm-1", "", "host-a")), wantHosts: []string{"host-a"}},
				{request: withSkipInflight(inflightIntegrationRequest("vm-2", "", "host-a")), wantHosts: []string{"host-a"}},
			},
			wantHolds: map[string]int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.hosts)+len(tt.objects))
			for _, hv := range tt.hosts {
				objects = append(objects, hv)
			}
			objects = append(objects, tt.objects...)
			server, k8sClient := newInflightIntegrationServer(t, tt.withInflights, tt.resizeUnknownSource, objects...)
			returned := 0
			anyResize := false
			for i, s := range tt.steps {
				if intent, err := s.request.GetIntent(); err == nil && intent == novaapi.ResizeIntent {
					anyResize = true
				}
				got := server.SendPlacementRequest(t, s.request).Hosts
				returned += len(got)
				if s.wantHosts != nil && !sameHosts(got, s.wantHosts) {
					t.Fatalf("step %d: hosts = %v, want %v", i, got, s.wantHosts)
				}
				if s.wantHosts == nil && len(got) != s.wantCount {
					t.Fatalf("step %d: got %d hosts %v, want %d", i, len(got), got, s.wantCount)
				}
			}
			holds := &v1alpha1.ReservationList{}
			if err := k8sClient.List(t.Context(), holds); err != nil {
				t.Fatal(err)
			}
			got := map[string]int{}
			for _, hold := range holds.Items {
				got[hold.Spec.TargetHost]++
			}
			if len(got) != len(tt.wantHolds) {
				t.Fatalf("holds per host = %v, want %v", got, tt.wantHolds)
			}
			for host, n := range tt.wantHolds {
				if got[host] != n {
					t.Fatalf("holds per host = %v, want %v", got, tt.wantHolds)
				}
			}
			if len(tt.wantHolds) > 0 && !anyResize {
				// Every in-flight hold besides migration source holds matches a
				// returned host. Resize creates an extra source hold whose host
				// is asserted via wantHolds instead, so skip the cross-check.
				if want := len(holds.Items) - countSourceHolds(holds.Items); returned != want {
					t.Fatalf("returned %d hosts but created %d target holds", returned, want)
				}
			}
		})
	}
}

func withSourceHost(req novaapi.ExternalSchedulerRequest, source string) novaapi.ExternalSchedulerRequest {
	req.Spec.Data.SchedulerHints["source_host"] = []any{source}
	return req
}

func withSingleCandidate(req novaapi.ExternalSchedulerRequest) novaapi.ExternalSchedulerRequest {
	req.Options.MaxCandidates = 1
	return req
}

func withIgnoredHosts(req novaapi.ExternalSchedulerRequest, hosts ...string) novaapi.ExternalSchedulerRequest {
	req.Spec.Data.IgnoreHosts = &hosts
	return req
}

func withSkipInflight(req novaapi.ExternalSchedulerRequest) novaapi.ExternalSchedulerRequest {
	req.Options.SkipInflight = true
	return req
}

func sameHosts(got, want []string) bool {
	got, want = slices.Clone(got), slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}

func countSourceHolds(holds []v1alpha1.Reservation) int {
	n := 0
	for _, hold := range holds {
		if hold.Spec.InFlightReservation.Intent == novaapi.LiveMigrationIntent && hold.Spec.TargetHost == "host-src" {
			n++
		}
	}
	return n
}

// inflightIntegrationVM builds a VM CRD placed on host with the given old size,
// used as a KVM resize source.
func inflightIntegrationVM(name, host, cpu, memory string) *vmv1alpha1.VirtualMachine {
	vm := &vmv1alpha1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: name}}
	vm.Status.Host = &vmv1alpha1.HostRef{Name: host}
	vm.Spec.Resources = map[vmv1alpha1.ResourceName]resource.Quantity{
		vmv1alpha1.ResourceCPU:    resource.MustParse(cpu),
		vmv1alpha1.ResourceMemory: resource.MustParse(memory),
	}
	return vm
}
