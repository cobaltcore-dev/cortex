// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Seed is a single hypothetical placement the PathFinder probes the scheduler
// with: a flavor in a given availability zone. It replaces the running vm the
// old pathfinder seeded from.
//
// Seeding from flavors rather than vms fixes two blind spots of the vm-based
// approach:
//
//   - A group of hosts that happen to run no vms produced no probe at all, so
//     its partition was never discovered — even though it is perfectly capable
//     of absorbing a failover once a vm lands there. A flavor seed exists
//     whether or not anything is currently scheduled, so the partition is found
//     up front.
//   - A single "exotic" vm on a host (one whose scheduling constraints no peer
//     satisfies) emptied that host's candidate intersection and kept it out of
//     the partition it otherwise belongs to. Flavor seeds carry no host
//     affiliation, so one odd flavor can never veto a host from a partition it
//     matches under every other flavor.
type Seed struct {
	// Flavor is the flavor to probe placement for.
	Flavor Flavor
	// AvailabilityZone is the AZ to probe in, drawn from the hypervisor CRDs.
	AvailabilityZone string
}

// Seeder produces the seeds a single calculation pass probes with. It is the
// first input to the pipeline, replacing the per-host vm enumeration.
type Seeder interface {
	// Seeds returns every (flavor, availability zone) permutation to probe. The
	// topology supplies the availability zones (the distinct topology-zone labels
	// of the known hypervisors); the flavors come from Nova. Any error aborts the
	// pass, exactly like a failed scheduler probe, so partitions are never
	// recomputed from a shrunken seed set.
	Seeds(ctx context.Context, topo topology) ([]Seed, error)
}

// novaSeeder builds seeds by crossing the Nova flavor catalogue with the
// availability zones present in the fleet. The flavors come from Nova (they are
// not derivable from the CRDs); the AZs come from the hypervisor CRDs, so the
// seeder needs no extra OpenStack call to enumerate them.
type novaSeeder struct {
	// flavorClient lists the Nova flavors to permute. Connected lazily on the
	// first Seeds call, so controller setup needs no OpenStack round-trip and a
	// transient auth failure at startup simply aborts that pass instead of the
	// whole controller.
	flavorClient FlavorClient
	// k8s reads the credential secrets the flavor client authenticates with.
	k8s client.Client
	// connected guards the one-time flavorClient.Connect.
	connected bool
}

// Seeds implements Seeder. It lists the Nova flavors, collects the distinct
// availability zones from the hypervisor CRDs, and returns every (flavor, AZ)
// pair. The output is sorted (by AZ then flavor name) so a pass is deterministic
// despite the map iteration over hypervisors.
func (s *novaSeeder) Seeds(ctx context.Context, topo topology) ([]Seed, error) {
	log := logf.FromContext(ctx)

	if !s.connected {
		if err := s.flavorClient.Connect(ctx, s.k8s); err != nil {
			return nil, err
		}
		s.connected = true
	}

	flavors, err := s.flavorClient.ListFlavors(ctx)
	if err != nil {
		return nil, err
	}

	// Collect the distinct availability zones from the hypervisor CRDs. collect
	// already guarantees every hypervisor carries a topology-zone label; we read
	// from every known hypervisor (including empty ones) so a partition is seeded
	// in each AZ it spans.
	azSet := map[string]bool{}
	for _, hv := range topo.hypervisors {
		if az, ok := hv.Labels[corev1.LabelTopologyZone]; ok && az != "" {
			azSet[az] = true
		}
	}
	azs := make([]string, 0, len(azSet))
	for az := range azSet {
		azs = append(azs, az)
	}
	sort.Strings(azs)
	// Sort flavors by name so the nested loop below emits seeds in a stable order
	// (ascending az, then ascending flavor name) despite the map iteration above.
	sort.SliceStable(flavors, func(i, j int) bool { return flavors[i].Name < flavors[j].Name })

	seeds := make([]Seed, 0, len(flavors)*len(azs))
	for _, az := range azs {
		for _, flavor := range flavors {
			seeds = append(seeds, Seed{Flavor: flavor, AvailabilityZone: az})
		}
	}
	log.V(1).Info("generated failover seeds",
		"flavors", len(flavors), "availabilityZones", len(azs), "seeds", len(seeds))
	return seeds, nil
}
