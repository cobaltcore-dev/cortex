// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Flavor is the slice of a flavor the seeder permutes into a scheduler probe:
// its name, size, and extra specs (which carry the hypervisor type, traits and
// other constraints the pipeline filters on). It deliberately mirrors only the
// fields fed into api.NovaFlavor, so the rest of the flavor-group shape never
// leaks into the pathfinder.
type Flavor struct {
	// Name is the flavor name, forwarded as the probe's flavor name so
	// flavor-name-sensitive filters behave as in a real placement.
	Name string
	// MemoryMB and VCPUs size the probe. RAM is in MiB (despite the "MB" label),
	// matching what the scheduler expects on NovaFlavor.MemoryMB.
	MemoryMB uint64
	VCPUs    uint64
	// ExtraSpecs carries the flavor's extra specs (hypervisor type, custom
	// traits, hardware requirements) so the same filters apply as for a real vm.
	ExtraSpecs map[string]string
}

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
	// Seeds returns every (flavor, availability zone) permutation to probe,
	// restricted to flavors whose hypervisor type this controller supports (see
	// the hvType filter in seeder.Seeds). The topology supplies the availability
	// zones (the distinct topology-zone labels of the known hypervisors); the
	// flavors come from the flavor-groups Knowledge CRD. Any error aborts the
	// pass, exactly like a failed scheduler probe, so partitions are never
	// recomputed from a shrunken seed set.
	Seeds(ctx context.Context, topo topology) ([]Seed, error)
}

// seeder builds seeds by crossing the flavor catalogue with the availability
// zones present in the fleet. The flavors come from the flavor-groups Knowledge
// CRD (already extracted into the cluster, so no extra OpenStack round-trip is
// needed); the AZs come from the hypervisor CRDs.
type seeder struct {
	// k8s reads the flavor-groups Knowledge CRD.
	k8s client.Client
}

// Seeds implements Seeder. It reads the flavors from the flavor-groups Knowledge
// CRD, collects the distinct availability zones from the hypervisor CRDs, and
// returns every (flavor, AZ) pair. The output is sorted (by AZ then flavor name)
// so a pass is deterministic despite the map iteration over hypervisors.
func (s *seeder) Seeds(ctx context.Context, topo topology) ([]Seed, error) {
	log := logf.FromContext(ctx)

	flavors, err := s.listFlavors(ctx)
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
			// Only seed with flavors whose capabilities:hypervisor_type extra-spec
			// names a hypervisor this controller supports: "qemu" (the Nova label
			// for KVM guests, which the probe mirrors with an img_hv_type=KVM image,
			// see fetchSeedCandidates) and "ch" (Cloud Hypervisor). A flavor with no
			// hypervisor type, or one we don't support, is skipped so it never
			// contributes a probe — and hence never shapes a partition — for a guest
			// kind the failover machinery doesn't handle.
			hvType, ok := flavor.ExtraSpecs["capabilities:hypervisor_type"]
			if !ok {
				log.V(1).Info("skipping flavor with no hypervisor type",
					"flavor", flavor.Name)
				continue
			}
			if !strings.EqualFold(hvType, "qemu") && !strings.EqualFold(hvType, "ch") {
				log.V(1).Info("skipping flavor with unsupported hypervisor type",
					"flavor", flavor.Name, "hvType", hvType)
				continue
			}
			seeds = append(seeds, Seed{Flavor: flavor, AvailabilityZone: az})
		}
	}
	log.V(1).Info("generated failover seeds",
		"flavors", len(flavors), "availabilityZones", len(azs), "seeds", len(seeds))
	return seeds, nil
}

// listFlavors reads every flavor from the flavor-groups Knowledge CRD. The CRD
// is cluster-scoped and must be ready; otherwise the pass aborts rather than
// seeding from a partial catalogue. Flavors from every group are flattened into
// a single list — the groups themselves carry no meaning for seeding, only their
// member flavors do.
func (s *seeder) listFlavors(ctx context.Context) ([]Flavor, error) {
	knowledge := &v1alpha1.Knowledge{}
	if err := s.k8s.Get(ctx, types.NamespacedName{Name: "flavor-groups"}, knowledge); err != nil {
		return nil, fmt.Errorf("failed to get flavor groups knowledge: %w", err)
	}
	if !meta.IsStatusConditionTrue(knowledge.Status.Conditions, v1alpha1.KnowledgeConditionReady) {
		return nil, errors.New("flavor groups knowledge is not ready")
	}
	groups, err := v1alpha1.UnboxFeatureList[compute.FlavorGroupFeature](knowledge.Status.Raw)
	if err != nil {
		return nil, fmt.Errorf("failed to unbox flavor group features: %w", err)
	}
	var flavors []Flavor
	for _, group := range groups {
		for _, f := range group.Flavors {
			flavors = append(flavors, Flavor{
				Name:       f.Name,
				MemoryMB:   f.MemoryMB,
				VCPUs:      f.VCPUs,
				ExtraSpecs: f.ExtraSpecs,
			})
		}
	}
	return flavors, nil
}
