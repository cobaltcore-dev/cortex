// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// Integration Tests
//
// These exercise the whole failover-host generation pipeline (seeder ->
// path finder -> clique builder -> planner) against a *real* Nova
// filter/weigher pipeline served over HTTP, rather than a stubbed scheduler.
// The invariant under test is the ADR 0011 whole-host guarantee: a reserved
// failover target must be able to absorb the entire workload of every host it
// is recorded as rescuing. Since eligibility is static (ADR A5) that reduces
// to: the target must accept every protected flavor its source hosts accept.
// ============================================================================

// IntegrationTestCase defines one failover-host generation scenario.
type IntegrationTestCase struct {
	Name        string
	Hypervisors []*hv1.Hypervisor
	Flavors     []compute.FlavorInGroup

	// Ratio/Minimum configure the planner. Ratio defaults to 1.0 and Minimum to
	// 1 when left zero, so a scenario reserves as many targets as the partition
	// allows unless it says otherwise.
	Ratio   float64
	Minimum int

	// ExpectReservations asserts at least one reservation is produced (most
	// scenarios want this; a scenario proving nothing is reserved sets it false).
	ExpectReservations bool

	// ForbiddenTargets names hosts that must never be reserved as a failover
	// target, e.g. a host too small to absorb a larger peer's whole workload.
	ForbiddenTargets []string

	// ExpectReservationCount, when non-zero, asserts the exact number of
	// reservations produced. Used by scenarios that must reserve more than one
	// target so the sibling-source wiring can be checked.
	ExpectReservationCount int

	// ExpectSiblingSources asserts every reservation lists all the other
	// reservations' target hosts among its source hosts — the post-failover
	// shortcut a multi-target partition relies on.
	ExpectSiblingSources bool
}

func TestIntegration(t *testing.T) {
	const traitX = "CUSTOM_TRAIT_X"
	testCases := []IntegrationTestCase{
		// =====================================================================
		// Whole-host evacuability
		// =====================================================================
		{
			// The general flavor makes all four hosts mutually evacuable, so a
			// naive clustering would lump the two plain "basic" hosts into the same
			// partition as the two trait-X hosts and could reserve a basic host as
			// their failover target. But a CUSTOM_TRAIT_X VM on a special host could
			// never restart on a basic host: the real scheduler rejects the trait-X
			// flavor there. The two special hosts on their own form a valid trait-X
			// partition (and the two basic hosts a valid general one), so the
			// coverage is genuinely achievable — the algorithm just must not merge
			// the incompatible hosts together. A single shared flavor is enough to
			// forge a rescue link, so this is the regression guard for the
			// same-flavor-set check that keeps the clique step from merging hosts
			// that do not accept exactly the same flavors.
			Name: "trait-incompatible host is not reserved as a failover target",
			Hypervisors: []*hv1.Hypervisor{
				hvWithTraits("basic-1"),
				hvWithTraits("basic-2"),
				hvWithTraits("special-1", traitX),
				hvWithTraits("special-2", traitX),
			},
			Flavors: []compute.FlavorInGroup{
				flavor("general", ""),
				flavor("traitx", traitX),
			},
			// Production defaults: floor(4 × 0.1) = 0, raised to the minimum of 1,
			// so exactly one host is reserved. It is the cheapest/first by name,
			// "basic-1", which cannot run traitx — so even a single reservation
			// violates the whole-host guarantee.
			Ratio:              0.1,
			Minimum:            1,
			ExpectReservations: true,
		},
		// =====================================================================
		// Baseline: a homogeneous partition is safe
		// =====================================================================
		{
			// Three identical hosts accept the one flavor, so any of them can
			// rescue any other: whichever becomes the target trivially satisfies
			// the whole-host invariant. Guards against the check above being
			// vacuously true.
			Name: "homogeneous partition reserves a compatible target",
			Hypervisors: []*hv1.Hypervisor{
				hvWithTraits("hv-1"),
				hvWithTraits("hv-2"),
				hvWithTraits("hv-3"),
			},
			Flavors:            []compute.FlavorInGroup{flavor("general", "")},
			ExpectReservations: true,
		},
		// =====================================================================
		// Whole-host capacity fit
		// =====================================================================
		{
			// Two large hosts and one small host all accept the one general flavor,
			// so under AssumeEmptyHosts the real capacity filter admits all three and
			// they co-accept the seed. But an evacuation moves a whole host's
			// workload at once, so a large host's VMs could never fit on the small
			// one: the path finder's whole-host fit check must drop every
			// large→small edge, leaving "small" only ever a source, never a
			// mutual-edge target. The small host must therefore never be reserved —
			// even though it happily accepts the flavor — or a large peer's workload
			// would have nowhere to land.
			Name: "undersized host is never reserved as a failover target",
			Hypervisors: []*hv1.Hypervisor{
				buildHV("large-1", 128, 64),
				buildHV("large-2", 128, 64),
				buildHV("small-1", 16, 8),
			},
			Flavors:            []compute.FlavorInGroup{flavor("general", "")},
			ExpectReservations: true,
			ForbiddenTargets:   []string{"small-1"},
		},
		// =====================================================================
		// Availability-zone isolation
		// =====================================================================
		{
			// Two hosts in az1 and two in az2 all accept the one flavor, but the real
			// AZ filter only ever admits hosts of the seed's own zone, so a seed in
			// az1 co-accepts only the az1 hosts and vice versa. The two zones must
			// therefore form two separate partitions: no reservation may record a
			// cross-AZ source, because a VM in az1 could not be evacuated onto an az2
			// host. Each zone reserves its own target, so the fleet yields exactly
			// two reservations — a wrong merge into one four-host partition would, at
			// the default ratio of 1.0, cap at three instead.
			Name: "hosts in different AZs are not merged into one partition",
			Hypervisors: []*hv1.Hypervisor{
				hvPtrInAZ("az1-1", "az1"),
				hvPtrInAZ("az1-2", "az1"),
				hvPtrInAZ("az2-1", "az2"),
				hvPtrInAZ("az2-2", "az2"),
			},
			Flavors:                []compute.FlavorInGroup{flavor("general", "")},
			ExpectReservations:     true,
			ExpectReservationCount: 2,
		},
		// =====================================================================
		// Multi-target partition: siblings list each other as sources
		// =====================================================================
		{
			// A ratio of 1.0 would reserve the whole partition, but the never-reserve-
			// all rule caps it at len(partition)-1, so a three-host partition reserves
			// two targets. Each reservation must then list the *other* reserved host
			// among its sources: once a reserved host absorbs an evacuation and
			// returns to service it instantly needs a target of its own, which is why
			// the planner keeps the sibling reserved hosts in each other's source
			// lists. Whole-host evacuability still holds since the hosts are identical.
			Name: "multi-target partition records sibling reserved hosts as sources",
			Hypervisors: []*hv1.Hypervisor{
				hvWithTraits("hv-1"),
				hvWithTraits("hv-2"),
				hvWithTraits("hv-3"),
			},
			Flavors:                []compute.FlavorInGroup{flavor("general", "")},
			Ratio:                  1.0,
			Minimum:                1,
			ExpectReservations:     true,
			ExpectReservationCount: 2,
			ExpectSiblingSources:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			env := newIntegrationTestEnv(t, tc.Hypervisors, tc.Flavors)
			defer env.Close()

			ratio, minimum := tc.Ratio, tc.Minimum
			if ratio == 0 {
				ratio = 1.0
			}
			if minimum == 0 {
				minimum = 1
			}

			desired := env.Generate(t, ratio, minimum)
			if tc.ExpectReservations && len(desired) == 0 {
				t.Fatal("expected at least one failover host reservation, got none")
			}
			if tc.ExpectReservationCount != 0 && len(desired) != tc.ExpectReservationCount {
				t.Fatalf("expected %d reservations, got %d", tc.ExpectReservationCount, len(desired))
			}
			for _, res := range desired {
				if slices.Contains(tc.ForbiddenTargets, res.Spec.TargetHost) {
					t.Errorf("host %q was reserved as a failover target but must never be", res.Spec.TargetHost)
				}
			}
			if tc.ExpectSiblingSources {
				env.VerifySiblingSources(t, desired)
			}

			env.VerifyWholeHostEvacuable(t, desired)
		})
	}
}

// ============================================================================
// Test Environment
// ============================================================================

// integrationTestEnv wires a real Nova filter/weigher pipeline (served over
// HTTP) on top of a fake k8s client holding the fleet and the flavor-groups
// Knowledge CRD, so the generation pipeline can be driven end to end.
type integrationTestEnv struct {
	k8sClient    client.Client
	server       *httptest.Server
	schedulerURL string
	flavors      []compute.FlavorInGroup
	hypervisors  map[string]hv1.Hypervisor
}

func (env *integrationTestEnv) Close() { env.server.Close() }

// newIntegrationTestEnv builds the fake cluster and the real scheduler pipeline.
func newIntegrationTestEnv(t *testing.T, hypervisors []*hv1.Hypervisor, flavors []compute.FlavorInGroup) *integrationTestEnv {
	t.Helper()

	objects := make([]client.Object, 0, len(hypervisors)+1)
	hvsByName := make(map[string]hv1.Hypervisor, len(hypervisors))
	for _, hv := range hypervisors {
		objects = append(objects, hv)
		hvsByName[hv.Name] = *hv
	}
	objects = append(objects, flavorGroupsKnowledge(t, true, flavors...))

	k8sClient := newTestClient(t, objects...)

	controller := &nova.FilterWeigherPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor: sharedPipelineMonitor(),
	}
	// Mirror the production KVM pipeline for the dimensions that shape a failover
	// partition: AZ, trait compatibility and whole-host capacity.
	pipeline := v1alpha1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: evacProbePipeline},
		Spec: v1alpha1.PipelineSpec{
			Type: v1alpha1.PipelineTypeFilterWeigher,
			Filters: []v1alpha1.FilterSpec{
				{Name: "filter_correct_az"},
				{Name: "filter_has_requested_traits"},
				{Name: "filter_has_enough_capacity"},
			},
		},
	}
	result := lib.InitNewFilterWeigherPipeline(
		context.Background(), k8sClient, pipeline.Name,
		filters.Index, pipeline.Spec.Filters,
		weighers.Index, pipeline.Spec.Weighers,
		controller.Monitor,
	)
	if len(result.FilterErrors) > 0 || len(result.WeigherErrors) > 0 {
		t.Fatalf("init pipeline: filters=%v weighers=%v", result.FilterErrors, result.WeigherErrors)
	}
	controller.Pipelines[pipeline.Name] = result.Pipeline
	controller.PipelineConfigs[pipeline.Name] = pipeline

	api := &integrationHTTPAPI{delegate: controller}
	mux := http.NewServeMux()
	mux.HandleFunc("/scheduler/nova/external", api.handle)
	server := httptest.NewServer(mux)

	return &integrationTestEnv{
		k8sClient:    k8sClient,
		server:       server,
		schedulerURL: server.URL + "/scheduler/nova/external",
		flavors:      flavors,
		hypervisors:  hvsByName,
	}
}

// ============================================================================
// Environment Helper Methods
// ============================================================================

// Generate runs the real generation pipeline (real seeder, HTTP path finder,
// clique builder and planner) and returns the desired reservations.
func (env *integrationTestEnv) Generate(t *testing.T, ratio float64, minimum int) map[corev1.ObjectReference]v1alpha1.Reservation {
	t.Helper()
	g := &pipelineGenerator{
		Client:             env.k8sClient,
		seeder:             &seeder{k8s: env.k8sClient},
		pathFinder:         &evacuationPathFinder{httpClient: http.DefaultClient, schedulerURL: env.schedulerURL},
		partitionBuilder:   cliquePartitionBuilder{},
		reservationPlanner: ratioReservationPlanner{ratio: ratio, minimum: minimum},
	}
	desired, _, ok := g.Generate(context.Background())
	if !ok {
		t.Fatal("Generate returned ok=false")
	}
	return desired
}

// AcceptsByHost returns, per host, the set of flavors the real scheduler accepts
// on it — the ground truth the reservations must respect. It reuses the path
// finder's own evacuation-intent empty-host probe so the answer matches exactly
// what the generation pipeline sees. It probes every availability zone present
// in the fleet (the AZ filter only ever admits hosts of the seed's own zone), so
// the ground truth covers multi-AZ fleets too.
func (env *integrationTestEnv) AcceptsByHost(t *testing.T) map[string]map[string]bool {
	t.Helper()
	zones := map[string]bool{}
	for _, hv := range env.hypervisors {
		zones[hv.Labels[corev1.LabelTopologyZone]] = true
	}
	finder := &evacuationPathFinder{httpClient: http.DefaultClient, schedulerURL: env.schedulerURL}
	accepts := map[string]map[string]bool{}
	for _, flavor := range env.flavors {
		for zone := range zones {
			accepting, err := finder.fetchSeedCandidates(context.Background(), Seed{
				Flavor: Flavor{
					Name:       flavor.Name,
					MemoryMB:   flavor.MemoryMB,
					VCPUs:      flavor.VCPUs,
					ExtraSpecs: flavor.ExtraSpecs,
				},
				AvailabilityZone: zone,
			}, env.hypervisors)
			if err != nil {
				t.Fatalf("probe accepting for %q in %q: %v", flavor.Name, zone, err)
			}
			for _, hv := range accepting {
				if accepts[hv.Name] == nil {
					accepts[hv.Name] = map[string]bool{}
				}
				accepts[hv.Name][flavor.Name] = true
			}
		}
	}
	return accepts
}

// ============================================================================
// Verification Helpers
// ============================================================================

// VerifyWholeHostEvacuable asserts the ADR 0011 invariant: for every reservation,
// the target host must accept every protected flavor each of its recorded source
// hosts accepts. Otherwise that source's VMs of the uncovered flavor would have
// nowhere to restart on the reserved target.
func (env *integrationTestEnv) VerifyWholeHostEvacuable(t *testing.T, desired map[corev1.ObjectReference]v1alpha1.Reservation) {
	t.Helper()
	accepts := env.AcceptsByHost(t)
	for _, res := range desired {
		target := res.Spec.TargetHost
		if res.Spec.FailoverHostReservation == nil {
			t.Fatalf("reservation %s has no FailoverHostReservation spec", res.Name)
		}
		for _, source := range res.Spec.FailoverHostReservation.SourceHosts {
			for flavor := range accepts[source] {
				if !accepts[target][flavor] {
					t.Errorf("reservation %s: target %q is recorded as a failover host for source %q, "+
						"but cannot run flavor %q that %q accepts — the source's %q VMs could not be evacuated onto it",
						res.Name, target, source, flavor, source, flavor)
				}
			}
		}
	}
}

// VerifySiblingSources asserts that in a partition reserving more than one
// failover host, every reservation lists each *other* reserved host among its
// source hosts. The planner keeps the reserved hosts in each other's source
// lists on purpose: once a reserved host absorbs an evacuation and returns to
// service it instantly needs a failover target of its own, and leaving it a
// recorded source means that target is already in place.
func (env *integrationTestEnv) VerifySiblingSources(t *testing.T, desired map[corev1.ObjectReference]v1alpha1.Reservation) {
	t.Helper()
	targets := make([]string, 0, len(desired))
	for _, res := range desired {
		targets = append(targets, res.Spec.TargetHost)
	}
	for _, res := range desired {
		if res.Spec.FailoverHostReservation == nil {
			t.Fatalf("reservation %s has no FailoverHostReservation spec", res.Name)
		}
		for _, sibling := range targets {
			if sibling == res.Spec.TargetHost {
				continue
			}
			if !slices.Contains(res.Spec.FailoverHostReservation.SourceHosts, sibling) {
				t.Errorf("reservation for target %q does not list sibling reserved host %q as a source",
					res.Spec.TargetHost, sibling)
			}
		}
	}
}

// evacProbePipeline is the name of the real Nova filter/weigher pipeline the
// probe exercises.
const evacProbePipeline = "kvm-general-purpose-load-balancing"

// integrationHTTPAPI is a thin, deterministic HTTP front-end for the real Nova
// filter/weigher pipeline — the same shape the failover package's integration
// tests use. It does NOT shuffle, so the probe answers are stable.
type integrationHTTPAPI struct {
	delegate nova.HTTPAPIDelegate
}

func (a *integrationHTTPAPI) handle(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var reqData novaapi.ExternalSchedulerRequest
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		http.Error(w, "decode", http.StatusBadRequest)
		return
	}
	rawBytes, err := json.Marshal(reqData)
	if err != nil {
		http.Error(w, "marshal", http.StatusInternalServerError)
		return
	}
	decision := &v1alpha1.Decision{
		TypeMeta:   metav1.TypeMeta{Kind: "Decision", APIVersion: "cortex.cloud/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{GenerateName: "nova-"},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			PipelineRef:      corev1.ObjectReference{Name: evacProbePipeline},
			ResourceID:       reqData.Spec.Data.InstanceUUID,
			NovaRaw:          &runtime.RawExtension{Raw: rawBytes},
		},
	}
	if err := a.delegate.ProcessNewDecisionFromAPI(r.Context(), decision); err != nil {
		http.Error(w, "process", http.StatusInternalServerError)
		return
	}
	if decision.Status.Result == nil {
		http.Error(w, "no result", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(novaapi.ExternalSchedulerResponse{Hosts: decision.Status.Result.OrderedHosts}); err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
	}
}

// ============================================================================
// Object Creation Functions
// ============================================================================

// hvWithTraits builds a fixed-size hypervisor in az1 with the given traits. The
// size is uniform across the fleet so every host is a valid gang-fit target for
// every other, isolating the trait (not capacity) as the compatibility axis.
func hvWithTraits(name string, traits ...string) *hv1.Hypervisor {
	h := buildHV(name, 64, 32)
	h.Status.Traits = traits
	return h
}

// hvPtrInAZ builds a fixed-size hypervisor (matching hvWithTraits) in the given
// availability zone, so a scenario can exercise the AZ filter rather than only
// az1 (buildHV, which it reuses, hardcodes az1). It is distinct from seeding
// test's hvInAZ, which returns a value and sets no capacity.
func hvPtrInAZ(name, az string) *hv1.Hypervisor {
	h := buildHV(name, 64, 32)
	h.Labels[corev1.LabelTopologyZone] = az
	return h
}

// flavor builds a generic flavor that requires the given trait (or none). Its
// hypervisor type is set to a supported value ("ch") only so the seeder keeps
// it; the specific type is irrelevant to what this test exercises.
func flavor(name, requiredTrait string) compute.FlavorInGroup {
	extra := map[string]string{"capabilities:hypervisor_type": "ch"}
	if requiredTrait != "" {
		extra["trait:"+requiredTrait] = "required"
	}
	return compute.FlavorInGroup{Name: name, VCPUs: 1, MemoryMB: 1024, ExtraSpecs: extra}
}

// sharedPipelineMonitor guards the one-time Prometheus collector registration
// the pipeline monitor performs, so multiple environments can be built in one
// test run without a duplicate-registration panic.
var (
	pipelineMonitorOnce sync.Once
	pipelineMonitor     lib.FilterWeigherPipelineMonitor
)

func sharedPipelineMonitor() lib.FilterWeigherPipelineMonitor {
	pipelineMonitorOnce.Do(func() { pipelineMonitor = lib.NewPipelineMonitor() })
	return pipelineMonitor
}
