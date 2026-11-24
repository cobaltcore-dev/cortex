// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/manila"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/nova"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack/placement"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/prometheus"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
	"sigs.k8s.io/yaml"
)

// Helm templates containing the knowledges deployed with cortex.
var knowledgeFiles = []string{
	"../../../../helm/bundles/cortex-nova/templates/knowledges.yaml",
	"../../../../helm/bundles/cortex-nova/templates/knowledges_kvm.yaml",
	"../../../../helm/bundles/cortex-manila/templates/knowledges.yaml",
}

// Load the expression knowledges from the helm templates, skipping lines
// with template directives.
func loadExpressionKnowledges(t *testing.T) map[string]v1alpha1.Knowledge {
	t.Helper()
	knowledges := map[string]v1alpha1.Knowledge{}
	for _, file := range knowledgeFiles {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for line := range strings.SplitSeq(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "{{") {
				lines = append(lines, line)
			}
		}
		for doc := range strings.SplitSeq(strings.Join(lines, "\n"), "\n---") {
			var k v1alpha1.Knowledge
			if err := yaml.Unmarshal([]byte(doc), &k); err != nil {
				t.Fatalf("failed to parse knowledge in %s: %v", file, err)
			}
			if k.Kind == "Knowledge" && k.Spec.Expression != nil {
				knowledges[k.Name] = k
			}
		}
	}
	return knowledges
}

func usage(class string, ratio float32, total, used, reserved int) placement.InventoryUsage {
	return placement.InventoryUsage{
		ResourceProviderUUID: "1",
		InventoryClassName:   class,
		AllocationRatio:      ratio,
		Total:                total,
		Used:                 used,
		Reserved:             reserved,
	}
}

var (
	utilizationHypervisors = []nova.Hypervisor{
		{ID: "1", Hostname: "hostname1", ServiceHost: "host1"},
		// No usage data for this host.
		{ID: "2", Hostname: "hostname2", ServiceHost: "host2"},
	}
	utilization = compute.HostUtilization{
		ComputeHost:            "host1",
		TotalVCPUsAllocatable:  100,
		TotalRAMAllocatableMB:  1000,
		TotalDiskAllocatableGB: 2000,
		VCPUsUsed:              25,
		RAMUsedMB:              500,
		DiskUsedGB:             1000,
		VCPUsUtilizedPct:       25,
		RAMUtilizedPct:         50,
		DiskUtilizedPct:        50,
	}
	contentionMetrics = []prometheus.VROpsHostMetric{
		{HostSystem: "hostsystem1", Value: 30.0},
		{HostSystem: "hostsystem2", Value: 40.0},
		{HostSystem: "hostsystem1", Value: 50.0},
		{HostSystem: "unresolved", Value: 99.0},
	}
	contentionResolved = []compute.ResolvedVROpsHostsystem{
		{VROpsHostsystem: "hostsystem2", NovaComputeHost: "compute_host2"},
		{VROpsHostsystem: "hostsystem1", NovaComputeHost: "compute_host1"},
	}
	contentionMultiMetrics = []prometheus.VROpsHostMetric{
		{HostSystem: "hostsystem1", Value: 30.0},
		{HostSystem: "hostsystem1", Value: 50.0},
		{HostSystem: "hostsystem2", Value: 10.0},
		{HostSystem: "unresolved", Value: 99.0},
	}
	contentionMultiResolved = []compute.ResolvedVROpsHostsystem{
		{VROpsHostsystem: "hostsystem1", NovaComputeHost: "compute_host1"},
		{VROpsHostsystem: "hostsystem2", NovaComputeHost: "compute_host1"},
	}
)

func TestExpressionKnowledges(t *testing.T) {
	withOvercommit := utilization
	withOvercommit.TotalVCPUsAllocatable = 200
	withOvercommit.VCPUsUsed = 50

	tests := []struct {
		name      string
		knowledge string
		env       map[string]any
		want      any
	}{
		{
			name:      "joins traits per compute host",
			knowledge: "host-capabilities",
			env: map[string]any{
				"hypervisors": []nova.Hypervisor{
					{ID: "uuid2", ServiceHost: "host2", HypervisorType: "QEMU"},
					{ID: "uuid1", ServiceHost: "host1", HypervisorType: "QEMU"},
					// Hypervisors without traits are skipped.
					{ID: "uuid3", ServiceHost: "host3", HypervisorType: "QEMU"},
				},
				"traits": []placement.Trait{
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_1"},
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_2"},
					{ResourceProviderUUID: "uuid2", Name: "TRAIT_3"},
					{ResourceProviderUUID: "unknown", Name: "TRAIT_4"},
				},
			},
			want: []compute.HostCapabilities{
				{ComputeHost: "host1", Traits: "TRAIT_1,TRAIT_2", HypervisorType: "QEMU"},
				{ComputeHost: "host2", Traits: "TRAIT_3", HypervisorType: "QEMU"},
			},
		},
		{
			name:      "sorts traits of all hypervisors of a host and hypervisor type",
			knowledge: "host-capabilities",
			env: map[string]any{
				"hypervisors": []nova.Hypervisor{
					{ID: "uuid1", ServiceHost: "host1", HypervisorType: "VMware vCenter Server"},
					{ID: "uuid2", ServiceHost: "host1", HypervisorType: "VMware vCenter Server"},
					{ID: "uuid3", ServiceHost: "host1", HypervisorType: "ironic"},
				},
				"traits": []placement.Trait{
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_C"},
					{ResourceProviderUUID: "uuid1", Name: "TRAIT_A"},
					{ResourceProviderUUID: "uuid2", Name: "TRAIT_B"},
					{ResourceProviderUUID: "uuid3", Name: "TRAIT_D"},
				},
			},
			want: []compute.HostCapabilities{
				{ComputeHost: "host1", Traits: "TRAIT_A,TRAIT_B,TRAIT_C", HypervisorType: "VMware vCenter Server"},
				{ComputeHost: "host1", Traits: "TRAIT_D", HypervisorType: "ironic"},
			},
		},
		{
			name:      "no hypervisors",
			knowledge: "host-utilization",
			env: map[string]any{
				"hypervisors":     []nova.Hypervisor{},
				"inventoryUsages": []placement.InventoryUsage{},
			},
			want: []compute.HostUtilization{},
		},
		{
			name:      "no reserved capacity",
			knowledge: "host-utilization",
			env: map[string]any{
				"hypervisors": utilizationHypervisors,
				"inventoryUsages": []placement.InventoryUsage{
					usage("VCPU", 1, 100, 25, 0),
					usage("MEMORY_MB", 1, 1000, 500, 0),
					usage("DISK_GB", 1, 2000, 1000, 0),
				},
			},
			want: []compute.HostUtilization{utilization},
		},
		{
			name:      "reserved capacity",
			knowledge: "host-utilization",
			env: map[string]any{
				"hypervisors": utilizationHypervisors,
				"inventoryUsages": []placement.InventoryUsage{
					usage("VCPU", 1, 105, 25, 5),
					usage("MEMORY_MB", 1, 1100, 500, 100),
					usage("DISK_GB", 1, 2100, 1000, 100),
				},
			},
			want: []compute.HostUtilization{utilization},
		},
		{
			name:      "overcommit factor",
			knowledge: "host-utilization",
			env: map[string]any{
				"hypervisors": utilizationHypervisors,
				"inventoryUsages": []placement.InventoryUsage{
					// Total allocatable vcpus: (110-10)*2 = 200, used already includes overcommit.
					usage("VCPU", 2, 110, 50, 10),
					usage("MEMORY_MB", 1, 1000, 500, 0),
					usage("DISK_GB", 1, 2000, 1000, 0),
				},
			},
			want: []compute.HostUtilization{withOvercommit},
		},
		{
			name:      "incomplete inventory is skipped",
			knowledge: "host-utilization",
			env: map[string]any{
				"hypervisors": utilizationHypervisors,
				"inventoryUsages": []placement.InventoryUsage{
					usage("VCPU", 1, 100, 25, 0),
					usage("MEMORY_MB", 1, 1000, 500, 0),
				},
			},
			want: []compute.HostUtilization{},
		},
		{
			name:      "resolves availability zones from aggregates",
			knowledge: "host-az",
			env: map[string]any{
				"hypervisors": []nova.Hypervisor{
					{ID: "uuid4", ServiceHost: "host4"},
					{ID: "uuid1", ServiceHost: "host1"},
					{ID: "uuid2", ServiceHost: "host2"},
					{ID: "uuid3", ServiceHost: "host3"},
				},
				"aggregates": []nova.Aggregate{
					// Aggregates without availability zone are ignored.
					{UUID: "agg1", Name: "something_else", ComputeHost: new("host1"), Metadata: "{}"},
					{UUID: "agg2", Name: "az1", AvailabilityZone: new("az1"), ComputeHost: new("host1"), Metadata: "{}"},
					// Only an aggregate without availability zone for host2.
					{UUID: "agg3", Name: "something_else_again", ComputeHost: new("host2"), Metadata: "{}"},
					// No aggregate for host3.
					{UUID: "agg4", Name: "az2", AvailabilityZone: new("az2"), ComputeHost: new("host4"), Metadata: "{}"},
					// Aggregates without compute host are ignored.
					{UUID: "agg5", Name: "az3", AvailabilityZone: new("az3"), Metadata: "{}"},
				},
			},
			want: []compute.HostAZ{
				{ComputeHost: "host1", AvailabilityZone: new("az1")},
				{ComputeHost: "host2"},
				{ComputeHost: "host3"},
				{ComputeHost: "host4", AvailabilityZone: new("az2")},
			},
		},
		{
			name:      "uses the lexicographically smallest of multiple availability zones",
			knowledge: "host-az",
			env: map[string]any{
				"hypervisors": []nova.Hypervisor{{ID: "uuid1", ServiceHost: "host1"}},
				"aggregates": []nova.Aggregate{
					{UUID: "agg1", AvailabilityZone: new("az-b"), ComputeHost: new("host1")},
					{UUID: "agg2", AvailabilityZone: new("az-a"), ComputeHost: new("host1")},
					{UUID: "agg3", AvailabilityZone: new("az-c"), ComputeHost: new("host1")},
				},
			},
			want: []compute.HostAZ{{ComputeHost: "host1", AvailabilityZone: new("az-a")}},
		},
		{
			name:      "no hypervisors",
			knowledge: "host-az",
			env: map[string]any{
				"hypervisors": []nova.Hypervisor{},
				"aggregates":  []nova.Aggregate{{UUID: "agg1", AvailabilityZone: new("az1"), ComputeHost: new("host1")}},
			},
			want: []compute.HostAZ{},
		},
		{
			name:      "max steal time per server",
			knowledge: "kvm-libvirt-domain-cpu-steal-pct",
			env: map[string]any{
				"metrics": []prometheus.KVMDomainMetric{
					{Domain: "instance-00000001", Value: 15.5},
					{Domain: "instance-00000001", Value: 25.0},
					{Domain: "instance-00000002", Value: 10.2},
					{Domain: "instance-00000003", Value: 30.8},
					// Metrics without matching server are skipped.
					{Domain: "instance-00000004", Value: 50.0},
				},
				"servers": []nova.Server{
					{ID: "uuid-3", OSEXTSRVATTRInstanceName: "instance-00000003", OSEXTSRVATTRHost: "compute-host-1"},
					{ID: "uuid-1", OSEXTSRVATTRInstanceName: "instance-00000001", OSEXTSRVATTRHost: "compute-host-1"},
					{ID: "uuid-2", OSEXTSRVATTRInstanceName: "instance-00000002", OSEXTSRVATTRHost: "compute-host-2"},
				},
			},
			want: []compute.LibvirtDomainCPUStealPct{
				{InstanceUUID: "uuid-1", Host: "compute-host-1", MaxStealTimePct: 25.0},
				{InstanceUUID: "uuid-2", Host: "compute-host-2", MaxStealTimePct: 10.2},
				{InstanceUUID: "uuid-3", Host: "compute-host-1", MaxStealTimePct: 30.8},
			},
		},
		{
			name:      "resolves hostsystems to compute hosts",
			knowledge: "vmware-resolved-hostsystems",
			env: map[string]any{
				"metrics": []prometheus.VROpsVMMetric{
					{HostSystem: "hostsystem2", InstanceUUID: "uuid2"},
					{HostSystem: "hostsystem1", InstanceUUID: "uuid1"},
					// Duplicate metrics of the same server are deduplicated.
					{HostSystem: "hostsystem1", InstanceUUID: "uuid1"},
					// Metrics of unknown servers are skipped.
					{HostSystem: "hostsystem3", InstanceUUID: "unknown"},
					// Servers without host are skipped.
					{HostSystem: "hostsystem4", InstanceUUID: "uuid4"},
				},
				"servers": []nova.Server{
					{ID: "uuid1", OSEXTSRVATTRHost: "service_host1"},
					{ID: "uuid2", OSEXTSRVATTRHost: "service_host2"},
					{ID: "uuid4", OSEXTSRVATTRHost: ""},
				},
			},
			want: []compute.ResolvedVROpsHostsystem{
				{VROpsHostsystem: "hostsystem1", NovaComputeHost: "service_host1"},
				{VROpsHostsystem: "hostsystem2", NovaComputeHost: "service_host2"},
			},
		},
		{
			name:      "average project cpu per compute host",
			knowledge: "vmware-project-noisiness",
			env: map[string]any{
				"metrics": []prometheus.VROpsVMMetric{
					{Project: "project1", Value: 50, InstanceUUID: "uuid1"},
					{Project: "project1", Value: 60, InstanceUUID: "uuid2"},
					{Project: "project2", Value: 70, InstanceUUID: "uuid3"},
				},
				"servers": []nova.Server{
					{ID: "uuid1", TenantID: "project1", OSEXTSRVATTRHypervisorHostname: "host1"},
					{ID: "uuid2", TenantID: "project1", OSEXTSRVATTRHypervisorHostname: "host2"},
					{ID: "uuid3", TenantID: "project2", OSEXTSRVATTRHypervisorHostname: "host1"},
					// Servers without metrics are skipped.
					{ID: "uuid4", TenantID: "project3", OSEXTSRVATTRHypervisorHostname: "host2"},
				},
				"hypervisors": []nova.Hypervisor{
					{ID: "1", Hostname: "host1", ServiceHost: "service_host1"},
					{ID: "2", Hostname: "host2", ServiceHost: "service_host2"},
				},
			},
			want: []compute.VROpsProjectNoisiness{
				{Project: "project1", ComputeHost: "service_host1", AvgCPUOfProject: 55},
				{Project: "project1", ComputeHost: "service_host2", AvgCPUOfProject: 55},
				{Project: "project2", ComputeHost: "service_host1", AvgCPUOfProject: 70},
			},
		},
		{
			name:      "average and maximum per compute host",
			knowledge: "vmware-long-term-contended-hosts",
			env:       map[string]any{"metrics": contentionMetrics, "resolvedHostsystems": genericFeatures(t, contentionResolved)},
			want: []compute.VROpsHostsystemContentionLongTerm{
				{ComputeHost: "compute_host1", AvgCPUContention: 40.0, MaxCPUContention: 50.0},
				{ComputeHost: "compute_host2", AvgCPUContention: 40.0, MaxCPUContention: 40.0},
			},
		},
		{
			name:      "average of averages and maximum of maximums across hostsystems",
			knowledge: "vmware-long-term-contended-hosts",
			env:       map[string]any{"metrics": contentionMultiMetrics, "resolvedHostsystems": genericFeatures(t, contentionMultiResolved)},
			// avg((30+50)/2, 10) = 25, max(50, 10) = 50
			want: []compute.VROpsHostsystemContentionLongTerm{
				{ComputeHost: "compute_host1", AvgCPUContention: 25.0, MaxCPUContention: 50.0},
			},
		},
		{
			name:      "no resolved hostsystems",
			knowledge: "vmware-long-term-contended-hosts",
			env: map[string]any{
				"metrics":             []prometheus.VROpsHostMetric{{HostSystem: "hostsystem1", Value: 30.0}},
				"resolvedHostsystems": []map[string]any{},
			},
			want: []compute.VROpsHostsystemContentionLongTerm{},
		},
		{
			name:      "average and maximum per compute host",
			knowledge: "vmware-short-term-contended-hosts",
			env:       map[string]any{"metrics": contentionMetrics, "resolvedHostsystems": genericFeatures(t, contentionResolved)},
			want: []compute.VROpsHostsystemContentionShortTerm{
				{ComputeHost: "compute_host1", AvgCPUContention: 40.0, MaxCPUContention: 50.0},
				{ComputeHost: "compute_host2", AvgCPUContention: 40.0, MaxCPUContention: 40.0},
			},
		},
		{
			name:      "average of averages and maximum of maximums across hostsystems",
			knowledge: "vmware-short-term-contended-hosts",
			env:       map[string]any{"metrics": contentionMultiMetrics, "resolvedHostsystems": genericFeatures(t, contentionMultiResolved)},
			want: []compute.VROpsHostsystemContentionShortTerm{
				{ComputeHost: "compute_host1", AvgCPUContention: 25.0, MaxCPUContention: 50.0},
			},
		},
		{
			name:      "node cpu usage per storage pool",
			knowledge: "netapp-storage-pool-cpu-usage-manila",
			env: map[string]any{
				"storagePools": []manila.StoragePool{
					{Name: "pool2", Pool: "aggr2"},
					{Name: "pool1", Pool: "aggr1"},
					// Aggregate spanning multiple nodes yields one feature per node.
					{Name: "pool3", Pool: "aggr3"},
					// Pools without aggregate labels are skipped.
					{Name: "pool4", Pool: "aggr4"},
				},
				"aggregateLabels": []prometheus.NetAppAggregateLabelsMetric{
					{Aggregate: "aggr1", Node: "node1"},
					{Aggregate: "aggr2", Node: "node2"},
					{Aggregate: "aggr3", Node: "node1"},
					{Aggregate: "aggr3", Node: "node2"},
					// Duplicate labels are ignored.
					{Aggregate: "aggr3", Node: "node2"},
				},
				"nodeCPUBusy": []prometheus.NetAppNodeMetric{
					{Node: "node1", Value: 10.5},
					{Node: "node1", Value: 20.0},
					{Node: "node2", Value: 30.0},
					{Node: "node2", Value: 40.0},
				},
			},
			want: []storage.StoragePoolCPUUsage{
				{StoragePoolName: "pool1", AvgCPUUsagePct: 15.25, MaxCPUUsagePct: 20.0},
				{StoragePoolName: "pool2", AvgCPUUsagePct: 35.0, MaxCPUUsagePct: 40.0},
				{StoragePoolName: "pool3", AvgCPUUsagePct: 15.25, MaxCPUUsagePct: 20.0},
				{StoragePoolName: "pool3", AvgCPUUsagePct: 35.0, MaxCPUUsagePct: 40.0},
			},
		},
	}

	knowledges := loadExpressionKnowledges(t)
	covered := map[string]bool{}
	for _, tt := range tests {
		covered[tt.knowledge] = true
	}
	for name := range knowledges {
		if !covered[name] {
			t.Errorf("expression knowledge %q has no test case", name)
		}
	}
	for _, tt := range tests {
		t.Run(tt.knowledge+"/"+tt.name, func(t *testing.T) {
			k, ok := knowledges[tt.knowledge]
			if !ok {
				t.Fatalf("expression knowledge %q not found in helm templates", tt.knowledge)
			}
			got, err := extractor.EvaluateExpression(t.Context(), *k.Spec.Expression, tt.env)
			if err != nil {
				t.Fatalf("expression failed: %v", err)
			}
			// Consumers unbox the features into their own type, so all keys must
			// match its json fields.
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			decoded := reflect.New(reflect.TypeOf(tt.want))
			if err := decoder.Decode(decoded.Interface()); err != nil {
				t.Fatalf("features don't match %T: %v", tt.want, err)
			}
			if g, w := canonical(t, decoded.Elem().Interface()), canonical(t, tt.want); !slices.Equal(g, w) {
				t.Errorf("expected %v, got %v", w, g)
			}
		})
	}
}

// Features of an expression knowledge as passed to other expressions, which
// are generic json values since their type isn't known.
func genericFeatures[F any](t *testing.T, features []F) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(features)
	if err != nil {
		t.Fatal(err)
	}
	var generic []map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	return generic
}

// Canonical form of features for comparison, ignoring their order and
// floating point differences from summing in different orders.
func canonical(t *testing.T, features any) []string {
	t.Helper()
	raw, err := json.Marshal(features)
	if err != nil {
		t.Fatal(err)
	}
	var generic []any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(generic))
	for i, f := range generic {
		raw, err := json.Marshal(roundFloats(f))
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(raw)
	}
	slices.Sort(out)
	return out
}

func roundFloats(v any) any {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'g', 10, 64)
	case map[string]any:
		for k, e := range x {
			x[k] = roundFloats(e)
		}
	case []any:
		for i, e := range x {
			x[i] = roundFloats(e)
		}
	}
	return v
}
