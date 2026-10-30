// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func TestIntegration(t *testing.T) {
	const traitX = "CUSTOM_TRAIT_X"
	testCases := []IntegrationTestCase{
		// =====================================================================
		// Whole-host evacuability
		// =====================================================================
		{
			// The general flavor makes all four hosts mutually evacuable, so the
			// clustering lumps the two plain "basic" hosts into the same partition
			// as the two trait-X hosts and can reserve a basic host as their
			// failover target. But a CUSTOM_TRAIT_X VM on a special host could never
			// restart on a basic host: the real scheduler rejects the trait-X flavor
			// there. The two special hosts on their own form a valid trait-X
			// partition (and the two basic hosts a valid general one), so the
			// coverage is genuinely achievable — the algorithm just must not merge
			// the incompatible hosts together. This fails today because a single
			// shared flavor is enough to forge a rescue link, and neither the clique
			// step nor the disjoint step re-checks that the target covers the
			// source's other flavors.
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
// what the generation pipeline sees.
func (env *integrationTestEnv) AcceptsByHost(t *testing.T) map[string]map[string]bool {
	t.Helper()
	finder := &evacuationPathFinder{httpClient: http.DefaultClient, schedulerURL: env.schedulerURL}
	accepts := map[string]map[string]bool{}
	for _, flavor := range env.flavors {
		accepting, err := finder.fetchSeedCandidates(context.Background(), Seed{
			Flavor: Flavor{
				Name:       flavor.Name,
				MemoryMB:   flavor.MemoryMB,
				VCPUs:      flavor.VCPUs,
				ExtraSpecs: flavor.ExtraSpecs,
			},
			AvailabilityZone: "az1",
		}, env.hypervisors)
		if err != nil {
			t.Fatalf("probe accepting for %q: %v", flavor.Name, err)
		}
		for _, hv := range accepting {
			if accepts[hv.Name] == nil {
				accepts[hv.Name] = map[string]bool{}
			}
			accepts[hv.Name][flavor.Name] = true
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

// ============================================================================
// Scheduler HTTP front-end
// ============================================================================

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
