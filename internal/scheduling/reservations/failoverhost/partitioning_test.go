// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"slices"
	"strings"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// hv builds a minimal hypervisor identified only by its name.
func hv(name string) hv1.Hypervisor {
	return hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// hvWithMemory builds a hypervisor with the given name and memory capacity in GiB.
func hvWithMemory(name string, memGiB int64) hv1.Hypervisor {
	h := hv(name)
	h.Status.Capacity = map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceMemory: *resource.NewQuantity(memGiB*(1<<30), resource.BinarySI),
	}
	return h
}

// edgesHV turns directed hypervisor pairs into failover paths, preserving the
// hypervisors' capacity (unlike edges, which rebuilds bare hosts from names). No
// supporting flavors are attached; flavor propagation is covered separately.
func edgesHV(pairs ...[2]hv1.Hypervisor) []Path {
	paths := make([]Path, 0, len(pairs))
	for _, p := range pairs {
		paths = append(paths, Path{Source: p[0], Target: p[1]})
	}
	return paths
}

// edges turns a list of directed name pairs into failover paths.
func edges(pairs ...[2]string) []Path {
	paths := make([]Path, 0, len(pairs))
	for _, p := range pairs {
		paths = append(paths, Path{Source: hv(p[0]), Target: hv(p[1])})
	}
	return paths
}

// partitionNames extracts the sorted host names of each partition.
func partitionNames(partitions []Partition) [][]string {
	out := make([][]string, 0, len(partitions))
	for _, partition := range partitions {
		names := make([]string, 0, len(partition.Hosts))
		for _, hv := range partition.Hosts {
			names = append(names, hv.Name)
		}
		slices.Sort(names)
		out = append(out, names)
	}
	return out
}

func TestBuildPartitions(t *testing.T) {
	tests := []struct {
		name  string
		paths []Path
		want  [][]string
	}{
		{
			name:  "empty input",
			paths: nil,
			want:  [][]string{},
		},
		{
			name:  "single mutual edge forms a partition",
			paths: edges([2]string{"a", "b"}, [2]string{"b", "a"}),
			want:  [][]string{{"a", "b"}},
		},
		{
			name:  "one-directional edge is not a partition",
			paths: edges([2]string{"a", "b"}),
			// b is empty (no outgoing edge) and a can reach b, so a-b form one.
			want: [][]string{{"a", "b"}},
		},
		{
			name: "fully connected triangle",
			paths: edges(
				[2]string{"a", "b"}, [2]string{"b", "a"},
				[2]string{"b", "c"}, [2]string{"c", "b"},
				[2]string{"a", "c"}, [2]string{"c", "a"},
			),
			want: [][]string{{"a", "b", "c"}},
		},
		{
			name: "two separate partitions",
			paths: edges(
				[2]string{"a", "b"}, [2]string{"b", "a"},
				[2]string{"c", "d"}, [2]string{"d", "c"},
			),
			want: [][]string{{"a", "b"}, {"c", "d"}},
		},
		{
			name: "missing mutual edge splits the clique",
			paths: edges(
				[2]string{"a", "b"}, [2]string{"b", "a"},
				[2]string{"b", "c"}, [2]string{"c", "b"},
				// a and c are not mutually reachable.
			),
			// a-b and b-c are the two maximal cliques.
			want: [][]string{{"a", "b"}, {"b", "c"}},
		},
		{
			name: "empty host recruited into partition reaching it",
			paths: edges(
				[2]string{"a", "b"}, [2]string{"b", "a"},
				// e has no outgoing edges but both a and b reach it.
				[2]string{"a", "e"}, [2]string{"b", "e"},
			),
			want: [][]string{{"a", "b", "e"}},
		},
		{
			name: "empty host not reachable by all stays alone",
			paths: edges(
				[2]string{"a", "b"}, [2]string{"b", "a"},
				// only a reaches e, so e cannot be recruited.
				[2]string{"a", "e"},
			),
			want: [][]string{{"a", "b"}, {"e"}},
		},
		{
			name: "larger empty host is not recruited",
			paths: edgesHV(
				[2]hv1.Hypervisor{hvWithMemory("a", 1), hvWithMemory("b", 1)},
				[2]hv1.Hypervisor{hvWithMemory("b", 1), hvWithMemory("a", 1)},
				// a and b reach the empty host e, but e is larger than they are,
				// so they could never evacuate onto it and it stays alone.
				[2]hv1.Hypervisor{hvWithMemory("a", 1), hvWithMemory("e", 4)},
				[2]hv1.Hypervisor{hvWithMemory("b", 1), hvWithMemory("e", 4)},
			),
			want: [][]string{{"a", "b"}, {"e"}},
		},
		{
			name: "equal-sized empty host is recruited",
			paths: edgesHV(
				[2]hv1.Hypervisor{hvWithMemory("a", 4), hvWithMemory("b", 4)},
				[2]hv1.Hypervisor{hvWithMemory("b", 4), hvWithMemory("a", 4)},
				[2]hv1.Hypervisor{hvWithMemory("a", 4), hvWithMemory("e", 4)},
				[2]hv1.Hypervisor{hvWithMemory("b", 4), hvWithMemory("e", 4)},
			),
			want: [][]string{{"a", "b", "e"}},
		},
	}

	builder := cliquePartitionBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := builder.BuildPartitions(tt.paths)
			gotNames := partitionNames(got)
			if !slices.EqualFunc(gotNames, tt.want, slices.Equal) {
				t.Errorf("got %v, want %v", gotNames, tt.want)
			}
		})
	}
}

// hvWithAggregates builds a hypervisor with the given name and aggregates.
func hvWithAggregates(name string, aggregates ...string) hv1.Hypervisor {
	h := hv(name)
	h.Spec.Aggregates = aggregates
	return h
}

// parts wraps plain host slices into flavorless Partitions, for the
// characterize tests that only exercise the host dimensions.
func parts(hostSets ...[]hv1.Hypervisor) []Partition {
	out := make([]Partition, 0, len(hostSets))
	for _, hosts := range hostSets {
		out = append(out, Partition{Hosts: hosts})
	}
	return out
}

func TestCharacterizePartitions(t *testing.T) {
	t.Run("no partitions", func(t *testing.T) {
		if got := characterizePartitions(nil); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("distinguishing aggregate is reported per partition", func(t *testing.T) {
		labels := characterizePartitions(parts(
			[]hv1.Hypervisor{hvWithAggregates("a", "hana"), hvWithAggregates("b", "hana")},
			[]hv1.Hypervisor{hvWithAggregates("c", "general"), hvWithAggregates("d", "general")},
		))
		want := []string{"spec.aggregates=[hana]", "spec.aggregates=[general]"}
		if !slices.Equal(labels, want) {
			t.Errorf("got %v, want %v", labels, want)
		}
	})

	t.Run("field shared across all partitions is dropped", func(t *testing.T) {
		// Both partitions share the same aggregate, so it distinguishes neither.
		labels := characterizePartitions(parts(
			[]hv1.Hypervisor{hvWithAggregates("a", "hana")},
			[]hv1.Hypervisor{hvWithAggregates("b", "hana")},
		))
		for i, label := range labels {
			if strings.Contains(label, "aggregates") {
				t.Errorf("partition %d label %q should not mention the shared aggregate", i, label)
			}
		}
	})

	t.Run("single partition keeps its distinguishing fields", func(t *testing.T) {
		// With only one partition nothing is fleet-wide, so shared fields survive.
		labels := characterizePartitions(parts(
			[]hv1.Hypervisor{hvWithAggregates("a", "hana")},
		))
		if len(labels) != 1 || !strings.Contains(labels[0], "spec.aggregates=[hana]") {
			t.Errorf("got %v, want a label containing spec.aggregates=[hana]", labels)
		}
	})

	t.Run("empty partition yields an empty label", func(t *testing.T) {
		labels := characterizePartitions(parts([]hv1.Hypervisor{}))
		if len(labels) != 1 || labels[0] != "" {
			t.Errorf("got %v, want one empty label", labels)
		}
	})
}

// pathWithFlavors builds a directed edge between bare hosts carrying the given
// supporting flavors.
func pathWithFlavors(src, dst string, flavors ...string) Path {
	return Path{Source: hv(src), Target: hv(dst), Flavors: flavors}
}

func TestBuildPartitionsFlavors(t *testing.T) {
	t.Run("hosts with differing accept sets split into separate partitions", func(t *testing.T) {
		// a and b accept {flavorAll, flavorAB}; c accepts only flavorAll. A single
		// shared flavor would once have merged all three, but c could never be a
		// failover target for a or b's flavorAB vms, so the builder must not merge
		// it in. a-b form one partition running both flavors; c, with no mutual peer
		// of a matching accept set, falls out as its own single-host partition.
		paths := []Path{
			pathWithFlavors("a", "b", "flavorAll", "flavorAB"),
			pathWithFlavors("b", "a", "flavorAll", "flavorAB"),
			pathWithFlavors("b", "c", "flavorAll"),
			pathWithFlavors("c", "b", "flavorAll"),
			pathWithFlavors("a", "c", "flavorAll"),
			pathWithFlavors("c", "a", "flavorAll"),
		}
		got := cliquePartitionBuilder{}.BuildPartitions(paths)
		wantNames := [][]string{{"a", "b"}, {"c"}}
		if !slices.EqualFunc(partitionNames(got), wantNames, slices.Equal) {
			t.Fatalf("partitions = %v, want %v", partitionNames(got), wantNames)
		}
		if want := []string{"flavorAB", "flavorAll"}; !slices.Equal(got[0].Flavors, want) {
			t.Errorf("partition a-b flavors = %v, want %v", got[0].Flavors, want)
		}
	})

	t.Run("recruited host contributes its inbound-edge flavors", func(t *testing.T) {
		// e has no outgoing edge (so the builder treats it as an empty host to
		// recruit), but it is the target of a<->b's probes for flavorX, which by the
		// path finder's semantics means e co-accepted flavorX too. Since a and b also
		// accept flavorX, it survives the all-hosts intersection and is listed.
		paths := []Path{
			{Source: hvWithMemory("a", 4), Target: hvWithMemory("b", 4), Flavors: []string{"flavorX"}},
			{Source: hvWithMemory("b", 4), Target: hvWithMemory("a", 4), Flavors: []string{"flavorX"}},
			{Source: hvWithMemory("a", 4), Target: hvWithMemory("e", 4), Flavors: []string{"flavorX"}},
			{Source: hvWithMemory("b", 4), Target: hvWithMemory("e", 4), Flavors: []string{"flavorX"}},
		}
		got := cliquePartitionBuilder{}.BuildPartitions(paths)
		if len(got) != 1 || len(got[0].Hosts) != 3 {
			t.Fatalf("got %d partitions %v, want one of 3 hosts", len(got), partitionNames(got))
		}
		if want := []string{"flavorX"}; !slices.Equal(got[0].Flavors, want) {
			t.Errorf("flavors = %v, want %v", got[0].Flavors, want)
		}
	})

	t.Run("empty host with a differing accept set is not recruited", func(t *testing.T) {
		// a and b accept {flavorX, flavorAB}; the empty host e is only ever the
		// target of flavorX probes, so it accepts {flavorX}. Recruiting e would let
		// it become a failover target that cannot run a or b's flavorAB vms, so the
		// builder must leave it out. a-b keep both flavors; e stays on its own.
		paths := []Path{
			{Source: hvWithMemory("a", 4), Target: hvWithMemory("b", 4), Flavors: []string{"flavorX", "flavorAB"}},
			{Source: hvWithMemory("b", 4), Target: hvWithMemory("a", 4), Flavors: []string{"flavorX", "flavorAB"}},
			{Source: hvWithMemory("a", 4), Target: hvWithMemory("e", 4), Flavors: []string{"flavorX"}},
			{Source: hvWithMemory("b", 4), Target: hvWithMemory("e", 4), Flavors: []string{"flavorX"}},
		}
		got := cliquePartitionBuilder{}.BuildPartitions(paths)
		wantNames := [][]string{{"a", "b"}, {"e"}}
		if !slices.EqualFunc(partitionNames(got), wantNames, slices.Equal) {
			t.Fatalf("partitions = %v, want %v", partitionNames(got), wantNames)
		}
		if want := []string{"flavorAB", "flavorX"}; !slices.Equal(got[0].Flavors, want) {
			t.Errorf("partition a-b flavors = %v, want %v", got[0].Flavors, want)
		}
	})
}

func TestDescribePartition(t *testing.T) {
	tests := []struct {
		name    string
		label   string
		flavors []string
		want    string
	}{
		{"label and flavors", "spec.aggregates=[hana]", []string{"a", "b"}, "spec.aggregates=[hana] | flavors: a, b"},
		{"label only", "spec.aggregates=[hana]", nil, "spec.aggregates=[hana]"},
		{"flavors only", "", []string{"a", "b"}, "flavors: a, b"},
		{"neither", "", nil, ""},
		{
			name:    "flavor list is capped",
			label:   "",
			flavors: []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7"},
			want:    "flavors: f1, f2, f3, f4, f5 (+2 more)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describePartition(tt.label, tt.flavors); got != tt.want {
				t.Errorf("describePartition(%q, %v) = %q, want %q", tt.label, tt.flavors, got, tt.want)
			}
		})
	}
}
