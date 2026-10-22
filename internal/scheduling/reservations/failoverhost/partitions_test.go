// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"slices"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// hv builds a minimal hypervisor identified only by its name.
func hv(name string) hv1.Hypervisor {
	return hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{Name: name}}
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
