// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// flavorGroupsKnowledge builds a ready flavor-groups Knowledge CRD whose groups
// carry the given flavors. The grouping is irrelevant for seeding, so the test
// flavors are all placed in a single group.
func flavorGroupsKnowledge(t *testing.T, ready bool, flavors ...compute.FlavorInGroup) *v1alpha1.Knowledge {
	t.Helper()
	raw, err := v1alpha1.BoxFeatureList([]compute.FlavorGroupFeature{{
		Name:    "group",
		Flavors: flavors,
	}})
	if err != nil {
		t.Fatalf("box features: %v", err)
	}
	k := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "flavor-groups"},
		Status:     v1alpha1.KnowledgeStatus{Raw: raw},
	}
	if ready {
		k.Status.Conditions = []metav1.Condition{{
			Type:   v1alpha1.KnowledgeConditionReady,
			Status: metav1.ConditionTrue,
		}}
	}
	return k
}

// hvInAZ builds a hypervisor labelled with the given availability zone.
func hvInAZ(name, az string) hv1.Hypervisor {
	return hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{corev1.LabelTopologyZone: az},
	}}
}

// qemuFlavor builds a flavor with a supported (qemu) hypervisor type so it
// passes the hypervisor-type filter in Seeds.
func qemuFlavor(name string) compute.FlavorInGroup {
	return compute.FlavorInGroup{Name: name, ExtraSpecs: map[string]string{"capabilities:hypervisor_type": "qemu"}}
}

func TestSeedsCrossesFlavorsWithAZs(t *testing.T) {
	// Two flavors and two distinct AZs across three hosts (az1 appears twice, so
	// it must be deduplicated) yield 2×2 = 4 seeds, sorted by az then flavor name.
	k8s := newTestClient(t, flavorGroupsKnowledge(t, true, qemuFlavor("large"), qemuFlavor("small")))
	s := &seeder{k8s: k8s}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{
		"a": hvInAZ("a", "az1"),
		"b": hvInAZ("b", "az1"),
		"c": hvInAZ("c", "az2"),
	}}

	seeds, err := s.Seeds(context.Background(), topo)
	if err != nil {
		t.Fatalf("Seeds: %v", err)
	}
	got := make([]string, len(seeds))
	for i, sd := range seeds {
		got[i] = sd.AvailabilityZone + "/" + sd.Flavor.Name
	}
	want := []string{"az1/large", "az1/small", "az2/large", "az2/small"}
	if len(got) != len(want) {
		t.Fatalf("seeds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seed[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSeedsFiltersUnsupportedHypervisorTypes(t *testing.T) {
	// Only flavors whose hypervisor type is qemu or ch are seeded; others (and
	// flavors with no hypervisor type at all) are skipped.
	k8s := newTestClient(t, flavorGroupsKnowledge(t, true,
		qemuFlavor("qemu-flavor"),
		compute.FlavorInGroup{Name: "ch-flavor", ExtraSpecs: map[string]string{"capabilities:hypervisor_type": "CH"}},
		compute.FlavorInGroup{Name: "vmware-flavor", ExtraSpecs: map[string]string{"capabilities:hypervisor_type": "vmware"}},
		compute.FlavorInGroup{Name: "no-type-flavor"},
	))
	s := &seeder{k8s: k8s}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{"a": hvInAZ("a", "az1")}}

	seeds, err := s.Seeds(context.Background(), topo)
	if err != nil {
		t.Fatalf("Seeds: %v", err)
	}
	got := make([]string, len(seeds))
	for i, sd := range seeds {
		got[i] = sd.Flavor.Name
	}
	want := []string{"ch-flavor", "qemu-flavor"}
	if len(got) != len(want) {
		t.Fatalf("seeds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seed[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSeedsFailsWhenKnowledgeNotReady(t *testing.T) {
	// A flavor-groups CRD that has not reached Ready aborts the pass rather than
	// seeding from a partial (or empty) catalogue.
	k8s := newTestClient(t, flavorGroupsKnowledge(t, false, qemuFlavor("f")))
	s := &seeder{k8s: k8s}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{"a": hvInAZ("a", "az1")}}
	if _, err := s.Seeds(context.Background(), topo); err == nil {
		t.Fatal("Seeds: expected not-ready error, got nil")
	}
}

func TestSeedsFailsWhenKnowledgeMissing(t *testing.T) {
	// No flavor-groups CRD at all is an error, not an empty seed set.
	k8s := newTestClient(t)
	s := &seeder{k8s: k8s}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{"a": hvInAZ("a", "az1")}}
	if _, err := s.Seeds(context.Background(), topo); err == nil {
		t.Fatal("Seeds: expected missing-knowledge error, got nil")
	}
}
