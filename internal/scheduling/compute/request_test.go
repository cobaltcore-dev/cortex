// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestHypervisorFamily(t *testing.T) {
	// The family is the distinction the pipeline uses; the specific type carries
	// the QEMU/CH detail only Nova side effects need.
	cases := []struct {
		hvType HypervisorType
		family HypervisorFamily
	}{
		{HypervisorTypeQEMU, HypervisorFamilyKVM},
		{HypervisorTypeCH, HypervisorFamilyKVM},
		{HypervisorTypeVMware, HypervisorFamilyVMware},
	}
	for _, c := range cases {
		r := SchedulerRequest{HypervisorType: c.hvType, HypervisorFamily: c.family}
		if r.HypervisorFamily != c.family {
			t.Errorf("type %q: family = %q, want %q", c.hvType, r.HypervisorFamily, c.family)
		}
	}
}

func TestIsForcedDestination(t *testing.T) {
	cases := []struct {
		name   string
		req    SchedulerRequest
		forced bool
	}{
		{"no force", SchedulerRequest{}, false},
		{"force host, no intent", SchedulerRequest{ForceHosts: []string{"h1"}}, true},
		{"force node, no intent", SchedulerRequest{ForceNodes: []string{"n1"}}, true},
		{"force host, unknown intent", SchedulerRequest{ForceHosts: []string{"h1"}, Intent: v1alpha1.SchedulingIntentUnknown}, true},
		{"force host, concrete intent must still filter", SchedulerRequest{ForceHosts: []string{"h1"}, Intent: "evacuate"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.req.IsForcedDestination(); got != c.forced {
				t.Errorf("IsForcedDestination() = %v, want %v", got, c.forced)
			}
		})
	}
}

func TestForcedHosts(t *testing.T) {
	hosts := []Host{
		{ComputeHost: "Host-A", HypervisorHostname: "node-a"},
		{ComputeHost: "host-b", HypervisorHostname: "node-b"},
		{ComputeHost: "host-b", HypervisorHostname: "node-b2"}, // duplicate compute host
	}
	cases := []struct {
		name  string
		force []string
		nodes []string
		want  []string
	}{
		{"force_hosts case-insensitive", []string{"host-a"}, nil, []string{"Host-A"}},
		{"force_nodes exact", nil, []string{"node-b"}, []string{"host-b"}},
		{"both intersect", []string{"host-b"}, []string{"node-b2"}, []string{"host-b"}},
		{"no match", []string{"nope"}, nil, []string{}},
		{"dedup compute host", []string{"host-b"}, nil, []string{"host-b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := SchedulerRequest{Hosts: hosts, ForceHosts: c.force, ForceNodes: c.nodes}
			got := r.ForcedHosts()
			if !slices.Equal(got, c.want) {
				t.Errorf("ForcedHosts() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFilterNarrowsHostsAndWeights(t *testing.T) {
	r := SchedulerRequest{
		Hosts: []Host{
			{ComputeHost: "a"}, {ComputeHost: "b"}, {ComputeHost: "c"},
		},
		Weights: map[string]float64{"a": 1, "b": 2, "c": 3},
	}
	filtered := r.Filter(map[string]float64{"a": 10, "c": 30}).(SchedulerRequest)
	got := filtered.GetHosts()
	slices.Sort(got)
	if !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("Filter kept hosts %v, want [a c]", got)
	}
	if filtered.Weights["a"] != 10 || filtered.Weights["c"] != 30 {
		t.Fatalf("Filter weights = %v, want a:10 c:30", filtered.Weights)
	}
	if _, ok := filtered.Weights["b"]; ok {
		t.Fatalf("Filter should have dropped host b from weights: %v", filtered.Weights)
	}
}
