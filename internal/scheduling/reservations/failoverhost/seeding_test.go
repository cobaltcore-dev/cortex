// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"errors"
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeFlavorClient is a FlavorClient returning a fixed flavor list (or an error),
// recording whether Connect was called so the lazy-connect behaviour can be
// asserted.
type fakeFlavorClient struct {
	flavors    []Flavor
	listErr    error
	connectErr error
	connects   int
}

func (f *fakeFlavorClient) Connect(context.Context, client.Client) error {
	f.connects++
	return f.connectErr
}

func (f *fakeFlavorClient) ListFlavors(context.Context) ([]Flavor, error) {
	return f.flavors, f.listErr
}

// hvInAZ builds a hypervisor labelled with the given availability zone.
func hvInAZ(name, az string) hv1.Hypervisor {
	return hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{corev1.LabelTopologyZone: az},
	}}
}

func TestSeedsCrossesFlavorsWithAZs(t *testing.T) {
	// Two flavors and two distinct AZs across three hosts (az1 appears twice, so
	// it must be deduplicated) yield 2×2 = 4 seeds, sorted by az then flavor name.
	fc := &fakeFlavorClient{flavors: []Flavor{{Name: "large"}, {Name: "small"}}}
	s := &novaSeeder{flavorClient: fc}
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

func TestSeedsConnectsOnceThenReuses(t *testing.T) {
	fc := &fakeFlavorClient{flavors: []Flavor{{Name: "f"}}}
	s := &novaSeeder{flavorClient: fc}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{"a": hvInAZ("a", "az1")}}

	for range 3 {
		if _, err := s.Seeds(context.Background(), topo); err != nil {
			t.Fatalf("Seeds: %v", err)
		}
	}
	if fc.connects != 1 {
		t.Errorf("Connect called %d times, want 1", fc.connects)
	}
}

func TestSeedsReconnectsAfterConnectFailure(t *testing.T) {
	// A connect failure must not latch the connected flag, so the next pass
	// retries rather than running forever against an unconnected client.
	fc := &fakeFlavorClient{connectErr: errors.New("auth down")}
	s := &novaSeeder{flavorClient: fc}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{"a": hvInAZ("a", "az1")}}

	if _, err := s.Seeds(context.Background(), topo); err == nil {
		t.Fatal("Seeds: expected connect error, got nil")
	}
	fc.connectErr = nil
	fc.flavors = []Flavor{{Name: "f"}}
	if _, err := s.Seeds(context.Background(), topo); err != nil {
		t.Fatalf("Seeds after recovery: %v", err)
	}
	if fc.connects != 2 {
		t.Errorf("Connect called %d times, want 2 (one failed, one retried)", fc.connects)
	}
}

func TestSeedsPropagatesListError(t *testing.T) {
	fc := &fakeFlavorClient{listErr: errors.New("nova down")}
	s := &novaSeeder{flavorClient: fc}
	topo := topology{hypervisors: map[string]hv1.Hypervisor{"a": hvInAZ("a", "az1")}}
	if _, err := s.Seeds(context.Background(), topo); err == nil {
		t.Fatal("Seeds: expected list error, got nil")
	}
}
