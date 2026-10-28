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
// hypervisors' capacity (unlike edges, which rebuilds bare hosts from names).
func edgesHV(pairs ...[2]hv1.Hypervisor) [][2]hv1.Hypervisor {
	return pairs
}

// edges turns a list of directed name pairs into failover paths.
func edges(pairs ...[2]string) [][2]hv1.Hypervisor {
	paths := make([][2]hv1.Hypervisor, 0, len(pairs))
	for _, p := range pairs {
		paths = append(paths, [2]hv1.Hypervisor{hv(p[0]), hv(p[1])})
	}
	return paths
}

// partitionNames extracts the sorted host names of each partition.
func partitionNames(partitions [][]hv1.Hypervisor) [][]string {
	out := make([][]string, 0, len(partitions))
	for _, partition := range partitions {
		names := make([]string, 0, len(partition))
		for _, hv := range partition {
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
		paths [][2]hv1.Hypervisor
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

func TestCharacterizePartitions(t *testing.T) {
	t.Run("no partitions", func(t *testing.T) {
		if got := characterizePartitions(nil); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("distinguishing aggregate is reported per partition", func(t *testing.T) {
		labels := characterizePartitions([][]hv1.Hypervisor{
			{hvWithAggregates("a", "hana"), hvWithAggregates("b", "hana")},
			{hvWithAggregates("c", "general"), hvWithAggregates("d", "general")},
		})
		want := []string{"spec.aggregates=[hana]", "spec.aggregates=[general]"}
		if !slices.Equal(labels, want) {
			t.Errorf("got %v, want %v", labels, want)
		}
	})

	t.Run("field shared across all partitions is dropped", func(t *testing.T) {
		// Both partitions share the same aggregate, so it distinguishes neither.
		labels := characterizePartitions([][]hv1.Hypervisor{
			{hvWithAggregates("a", "hana")},
			{hvWithAggregates("b", "hana")},
		})
		for i, label := range labels {
			if strings.Contains(label, "aggregates") {
				t.Errorf("partition %d label %q should not mention the shared aggregate", i, label)
			}
		}
	})

	t.Run("single partition keeps its distinguishing fields", func(t *testing.T) {
		// With only one partition nothing is fleet-wide, so shared fields survive.
		labels := characterizePartitions([][]hv1.Hypervisor{
			{hvWithAggregates("a", "hana")},
		})
		if len(labels) != 1 || !strings.Contains(labels[0], "spec.aggregates=[hana]") {
			t.Errorf("got %v, want a label containing spec.aggregates=[hana]", labels)
		}
	})

	t.Run("empty partition yields an empty label", func(t *testing.T) {
		labels := characterizePartitions([][]hv1.Hypervisor{{}})
		if len(labels) != 1 || labels[0] != "" {
			t.Errorf("got %v, want one empty label", labels)
		}
	})
}
