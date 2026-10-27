// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
)

// stubScheduler returns a path finder wired to an httptest server whose per-vm
// answers come from candidatesByVM (keyed by vm name / InstanceUUID); a vm in
// errVMs gets an HTTP 500. The handler asserts the request carries valid options,
// a non-empty host set, and a kvm img_hv_type, mirroring what the real scheduler
// requires — so a regression that drops any of them is caught here. The caller
// closes the server.
func stubScheduler(t *testing.T, candidatesByVM map[string][]string, errVMs map[string]bool) (*evacuationPathFinder, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.ExternalSchedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := req.Options.Validate(); err != nil {
			t.Errorf("invalid scheduling options: %v", err)
		}
		if !req.Options.AssumeEmptyHosts {
			// A failover target is reserved empty, so candidates must be judged by
			// total capacity. Dropping this would orphan every busy host.
			t.Errorf("request for vm %q must set AssumeEmptyHosts", req.Spec.Data.InstanceUUID)
		}
		if len(req.Hosts) == 0 {
			t.Errorf("request for vm %q has no candidate hosts", req.Spec.Data.InstanceUUID)
		}
		if got := req.Spec.Data.Image.Data.Properties.Data["img_hv_type"]; got != string(api.NovaImageMetaHVTypeKVM) {
			// The hypervisor-type filter needs img_hv_type to judge flavor/image
			// compatibility; dropping it would make that filter misbehave.
			t.Errorf("request for vm %q must set img_hv_type=%q, got %q", req.Spec.Data.InstanceUUID, api.NovaImageMetaHVTypeKVM, got)
		}
		if errVMs[req.Spec.Data.InstanceUUID] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		hosts := candidatesByVM[req.Spec.Data.InstanceUUID]
		if err := json.NewEncoder(w).Encode(api.ExternalSchedulerResponse{Hosts: hosts}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	finder := &evacuationPathFinder{httpClient: server.Client(), schedulerURL: server.URL}
	return finder, server
}

// pathNames collapses failover edges into sorted "source->target" strings, for
// order-independent comparison.
func pathNames(paths [][2]hv1.Hypervisor) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p[0].Name+"->"+p[1].Name)
	}
	slices.Sort(out)
	return out
}

// topoWith builds a topology of the named hosts (each sized memGi GiB) and the
// vms placed on them, so FindPaths can be exercised without a k8s client.
func topoWith(hostMemGi map[string]int64, vmsByHost map[string][]string) topology {
	hypervisors := map[string]hv1.Hypervisor{}
	for name, memGi := range hostMemGi {
		hypervisors[name] = *buildHV(name, memGi, 1)
	}
	vms := map[string][]vmv1alpha1.VirtualMachine{}
	for host, vmNames := range vmsByHost {
		for _, name := range vmNames {
			vms[host] = append(vms[host], *buildVM(name, host))
		}
	}
	return topology{hypervisors: hypervisors, vmsByHost: vms}
}

func TestFindPathsErrorsOnLookupFailure(t *testing.T) {
	// A source host runs two vms. One vm's lookup fails with a scheduler error.
	// That's an infrastructure failure, not an answer, so FindPaths must return
	// an error to abort the pass rather than silently dropping paths — otherwise
	// the caller would publish an empty desired state and prune valid
	// reservations.
	finder, server := stubScheduler(t,
		map[string][]string{"vmGood": {"b"}},
		map[string]bool{"vmBad": true},
	)
	defer server.Close()

	// vmBad is listed first so the error hits the seeding vm, exactly the case
	// the old index-based seeding mishandled.
	topo := topoWith(
		map[string]int64{"src": 32, "b": 32},
		map[string][]string{"src": {"vmBad", "vmGood"}},
	)
	if _, err := finder.FindPaths(context.Background(), topo); err == nil {
		t.Fatal("FindPaths: expected error on scheduler lookup failure, got nil")
	}
}

func TestFindPathsIntersectsCandidatesAcrossVMs(t *testing.T) {
	// Host "src" runs two vms. vm1 can go to {a,b}, vm2 can go to {b,c}. Only
	// "b" is a valid target for the whole host, so only src->b is a path.
	finder, server := stubScheduler(t, map[string][]string{
		"vm1": {"a", "b"},
		"vm2": {"b", "c"},
	}, nil)
	defer server.Close()

	topo := topoWith(
		map[string]int64{"src": 32, "a": 32, "b": 32, "c": 32},
		map[string][]string{"src": {"vm1", "vm2"}},
	)
	got, err := finder.FindPaths(context.Background(), topo)
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	if want := []string{"src->b"}; !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}

func TestFindPathsDropsTargetSmallerThanSource(t *testing.T) {
	// "big" (64Gi) runs one vm that can evacuate to "small" (16Gi) and "equal"
	// (64Gi). The gang-fit filter drops "small", keeping only big->equal.
	finder, server := stubScheduler(t, map[string][]string{
		"vm1": {"small", "equal"},
	}, nil)
	defer server.Close()

	topo := topoWith(
		map[string]int64{"big": 64, "small": 16, "equal": 64},
		map[string][]string{"big": {"vm1"}},
	)
	got, err := finder.FindPaths(context.Background(), topo)
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	if want := []string{"big->equal"}; !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}

func TestFindPathsAreMutualForEquallySizedPeers(t *testing.T) {
	// Two equal hosts whose single vm can evacuate onto the other yield both
	// directions, which the partition builder later turns into one partition.
	finder, server := stubScheduler(t, map[string][]string{
		"vmA": {"hvB"},
		"vmB": {"hvA"},
	}, nil)
	defer server.Close()

	topo := topoWith(
		map[string]int64{"hvA": 64, "hvB": 64},
		map[string][]string{"hvA": {"vmA"}, "hvB": {"vmB"}},
	)
	got, err := finder.FindPaths(context.Background(), topo)
	if err != nil {
		t.Fatalf("FindPaths: %v", err)
	}
	if want := []string{"hvA->hvB", "hvB->hvA"}; !slices.Equal(pathNames(got), want) {
		t.Errorf("paths = %v, want %v", pathNames(got), want)
	}
}
