// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

func TestExplainWeighing(t *testing.T) {
	tests := []struct {
		name     string
		result   *v1alpha1.DecisionResult
		contains []string // substrings that must appear in output
		excludes []string // substrings that must NOT appear in output
		empty    bool     // expect empty string
	}{
		{
			name:   "nil result returns empty",
			result: nil,
			empty:  true,
		},
		{
			name: "single host returns empty",
			result: &v1alpha1.DecisionResult{
				OrderedHosts:         []string{"host-a"},
				NormalizedInWeights:  map[string]float64{"host-a": 0.5},
				AggregatedOutWeights: map[string]float64{"host-a": 0.8},
			},
			empty: true,
		},
		{
			name: "only filter steps returns empty",
			result: &v1alpha1.DecisionResult{
				NormalizedInWeights:  map[string]float64{"host-a": 0.5, "host-b": 0.3, "host-c": 0.1},
				AggregatedOutWeights: map[string]float64{"host-a": 0.5, "host-b": 0.3},
				OrderedHosts:         []string{"host-a", "host-b"},
				StepResults: []v1alpha1.StepResult{
					// A true filter: activations do NOT include host-b (it was filtered after this step).
					{StepName: "filter_az", Activations: map[string]float64{"host-a": 1.0}},
				},
			},
			empty: true,
		},
		{
			name: "two hosts single weigher positive multiplier",
			result: func() *v1alpha1.DecisionResult {
				// Simulate: multiplier = 2.0, act[a] = 1.0, act[b] = -0.5
				// contrib[a] = 2.0 * tanh(1.0) = 1.5231
				// contrib[b] = 2.0 * tanh(-0.5) = -0.9242
				mult := 2.0
				actA, actB := 1.0, -0.5
				inA, inB := 0.3, 0.3
				outA := inA + mult*math.Tanh(actA)
				outB := inB + mult*math.Tanh(actB)
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  map[string]float64{"host-a": inA, "host-b": inB},
					AggregatedOutWeights: map[string]float64{"host-a": outA, "host-b": outB},
					OrderedHosts:         []string{"host-a", "host-b"},
					StepResults: []v1alpha1.StepResult{
						{StepName: "weigher_cpu", Activations: map[string]float64{"host-a": actA, "host-b": actB}},
					},
				}
			}(),
			contains: []string{"host-a", "host-b", "weigher_cpu", "is #1 because of"},
		},
		{
			name: "two hosts single weigher negative multiplier",
			result: func() *v1alpha1.DecisionResult {
				// Simulate: multiplier = -1.0 (inverts behavior)
				// A host with HIGH raw activation is penalized.
				// act[a] = -2.0 (high penalty activates less due to neg mult -> boosts)
				// act[b] = 3.0 (high activation penalized by neg mult -> hurts)
				mult := -1.0
				actA, actB := -2.0, 3.0
				inA, inB := 0.0, 0.0
				outA := inA + mult*math.Tanh(actA)
				outB := inB + mult*math.Tanh(actB)
				// outA = 0 + (-1)*tanh(-2) = 0 + (-1)*(-0.964) = +0.964
				// outB = 0 + (-1)*tanh(3) = 0 + (-1)*(0.995) = -0.995
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  map[string]float64{"host-a": inA, "host-b": inB},
					AggregatedOutWeights: map[string]float64{"host-a": outA, "host-b": outB},
					OrderedHosts:         []string{"host-a", "host-b"},
					StepResults: []v1alpha1.StepResult{
						{StepName: "kvm_binpack", Activations: map[string]float64{"host-a": actA, "host-b": actB}},
					},
				}
			}(),
			contains: []string{"host-a", "is #1 because of", "kvm_binpack"},
		},
		{
			name: "three hosts two weighers with counterfactual",
			result: func() *v1alpha1.DecisionResult {
				// weigher_cpu (mult=3.0) strongly favors host-a
				// weigher_mem (mult=1.0) slightly favors host-b
				// Without weigher_cpu, host-b should be #1.
				multCPU, multMem := 3.0, 1.0
				actCPU := map[string]float64{"h1": 1.0, "h2": 0.2, "h3": -0.5}
				actMem := map[string]float64{"h1": -0.3, "h2": 0.8, "h3": 0.1}
				in := map[string]float64{"h1": 0.0, "h2": 0.0, "h3": 0.0}
				out := map[string]float64{}
				for _, h := range []string{"h1", "h2", "h3"} {
					out[h] = in[h] + multCPU*math.Tanh(actCPU[h]) + multMem*math.Tanh(actMem[h])
				}
				// Sort to determine OrderedHosts.
				hosts := []string{"h1", "h2", "h3"}
				sortByWeight(hosts, out)
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  in,
					AggregatedOutWeights: out,
					OrderedHosts:         hosts,
					StepResults: []v1alpha1.StepResult{
						{StepName: "weigher_cpu", Activations: actCPU},
						{StepName: "weigher_mem", Activations: actMem},
					},
				}
			}(),
			contains: []string{"Without weigher_cpu", "would be #1 instead"},
		},
		{
			name: "opposing weigher reported",
			result: func() *v1alpha1.DecisionResult {
				// weigher_boost (mult=2.0) pushes host-a up
				// weigher_penalty (mult=-0.5) opposes host-a
				// Net: host-a still wins due to weigher_boost dominance.
				multBoost, multPenalty := 2.0, -0.5
				actBoost := map[string]float64{"x": 1.5, "y": -0.5}
				actPenalty := map[string]float64{"x": 1.0, "y": -1.0}
				in := map[string]float64{"x": 0.0, "y": 0.0}
				out := map[string]float64{}
				for _, h := range []string{"x", "y"} {
					out[h] = in[h] + multBoost*math.Tanh(actBoost[h]) + multPenalty*math.Tanh(actPenalty[h])
				}
				hosts := []string{"x", "y"}
				sortByWeight(hosts, out)
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  in,
					AggregatedOutWeights: out,
					OrderedHosts:         hosts,
					StepResults: []v1alpha1.StepResult{
						{StepName: "weigher_boost", Activations: actBoost},
						{StepName: "weigher_penalty", Activations: actPenalty},
					},
				}
			}(),
			contains: []string{"opposed this ranking"},
		},
		{
			name: "negligible impact weigher reported",
			result: func() *v1alpha1.DecisionResult {
				// weigher_big (mult=5.0) dominates
				// weigher_tiny (mult=0.001) is negligible
				multBig, multTiny := 5.0, 0.001
				actBig := map[string]float64{"a": 2.0, "b": -1.0}
				actTiny := map[string]float64{"a": 0.5, "b": 0.4}
				in := map[string]float64{"a": 0.0, "b": 0.0}
				out := map[string]float64{}
				for _, h := range []string{"a", "b"} {
					out[h] = in[h] + multBig*math.Tanh(actBig[h]) + multTiny*math.Tanh(actTiny[h])
				}
				hosts := []string{"a", "b"}
				sortByWeight(hosts, out)
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  in,
					AggregatedOutWeights: out,
					OrderedHosts:         hosts,
					StepResults: []v1alpha1.StepResult{
						{StepName: "weigher_big", Activations: actBig},
						{StepName: "weigher_tiny", Activations: actTiny},
					},
				}
			}(),
			contains: []string{"weigher_tiny", "negligible"},
		},
		{
			name: "initial weight bias dominates when activations are zero",
			result: &v1alpha1.DecisionResult{
				NormalizedInWeights:  map[string]float64{"fast": 0.8, "slow": 0.2},
				AggregatedOutWeights: map[string]float64{"fast": 0.8, "slow": 0.2},
				OrderedHosts:         []string{"fast", "slow"},
				StepResults: []v1alpha1.StepResult{
					{StepName: "weigher_noop", Activations: map[string]float64{"fast": 0.0, "slow": 0.0}},
				},
			},
			// Matrix is singular (all-zero activations), so fallback reports initial bias.
			contains: []string{"initial weight bias", "+0.60"},
		},
		{
			name: "mixed filter and weigher steps ignores filters",
			result: func() *v1alpha1.DecisionResult {
				mult := 1.5
				actW := map[string]float64{"a": 1.0, "b": -0.5}
				in := map[string]float64{"a": 0.0, "b": 0.0, "c": 0.0}
				out := map[string]float64{}
				for _, h := range []string{"a", "b"} {
					out[h] = in[h] + mult*math.Tanh(actW[h])
				}
				hosts := []string{"a", "b"}
				sortByWeight(hosts, out)
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  map[string]float64{"a": 0.0, "b": 0.0},
					AggregatedOutWeights: out,
					OrderedHosts:         hosts,
					StepResults: []v1alpha1.StepResult{
						// Filter step: removed host c (activations don't include all OrderedHosts... wait, they do include a and b).
						// Actually a filter that removed c would have activations for a and b only.
						// Since OrderedHosts = [a, b], this LOOKS like a weigher.
						// Let's make the filter clearly filter: it has activations for a, b but not c.
						// But since c is not in OrderedHosts, the filter will still "match" as weigher.
						// To properly test: make a filter that has activations for only 'a' (not 'b').
						{StepName: "filter_az", Activations: map[string]float64{"a": 1.0}},
						{StepName: "weigher_cpu", Activations: actW},
					},
				}
			}(),
			contains: []string{"weigher_cpu"},
			excludes: []string{"filter_az"},
		},
		{
			name: "under-determined system falls back to gap-only explanation",
			result: &v1alpha1.DecisionResult{
				NormalizedInWeights:  map[string]float64{"a": 0.0, "b": 0.0},
				AggregatedOutWeights: map[string]float64{"a": 1.0, "b": 0.5},
				OrderedHosts:         []string{"a", "b"},
				StepResults: []v1alpha1.StepResult{
					{StepName: "w1", Activations: map[string]float64{"a": 1.0, "b": 0.5}},
					{StepName: "w2", Activations: map[string]float64{"a": 0.8, "b": 0.3}},
					{StepName: "w3", Activations: map[string]float64{"a": 0.6, "b": 0.1}},
				},
			},
			// 2 hosts, 3 weighers -> M < N -> under-determined, falls back to gap report
			contains: []string{"a is #1 over b"},
		},
		{
			name: "multiplier recovery accuracy",
			result: func() *v1alpha1.DecisionResult {
				// Known multipliers: [2.5, -1.0, 0.7]
				mults := []float64{2.5, -1.0, 0.7}
				acts := []map[string]float64{
					{"h1": 0.8, "h2": -0.3, "h3": 1.2, "h4": -0.7},
					{"h1": 0.5, "h2": 1.0, "h3": -0.2, "h4": 0.3},
					{"h1": -1.0, "h2": 0.6, "h3": 0.4, "h4": -0.1},
				}
				hosts := []string{"h1", "h2", "h3", "h4"}
				in := map[string]float64{"h1": 0.1, "h2": 0.2, "h3": -0.1, "h4": 0.0}
				out := make(map[string]float64)
				for _, h := range hosts {
					out[h] = in[h]
					for i, m := range mults {
						out[h] += m * math.Tanh(acts[i][h])
					}
				}
				sortByWeight(hosts, out)
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  in,
					AggregatedOutWeights: out,
					OrderedHosts:         hosts,
					StepResults: []v1alpha1.StepResult{
						{StepName: "w_big", Activations: acts[0]},
						{StepName: "w_neg", Activations: acts[1]},
						{StepName: "w_small", Activations: acts[2]},
					},
				}
			}(),
			contains: []string{"top-3", "is #1 because of"},
		},
		{
			name: "counterfactual not reported when removal does not change #1",
			result: func() *v1alpha1.DecisionResult {
				// Two weighers both favor host-a. Removing either still leaves a as #1.
				mult1, mult2 := 2.0, 2.0
				act1 := map[string]float64{"a": 1.0, "b": -1.0}
				act2 := map[string]float64{"a": 0.8, "b": -0.8}
				in := map[string]float64{"a": 0.0, "b": 0.0}
				out := map[string]float64{}
				for _, h := range []string{"a", "b"} {
					out[h] = in[h] + mult1*math.Tanh(act1[h]) + mult2*math.Tanh(act2[h])
				}
				return &v1alpha1.DecisionResult{
					NormalizedInWeights:  in,
					AggregatedOutWeights: out,
					OrderedHosts:         []string{"a", "b"},
					StepResults: []v1alpha1.StepResult{
						{StepName: "w1", Activations: act1},
						{StepName: "w2", Activations: act2},
					},
				}
			}(),
			excludes: []string{"Without", "would be #1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExplainWeighing(tt.result)
			if tt.empty {
				if got != "" {
					t.Errorf("expected empty string, got:\n%s", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected non-empty explanation, got empty string")
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing expected substring %q.\nGot:\n%s", want, got)
				}
			}
			for _, notWant := range tt.excludes {
				if strings.Contains(got, notWant) {
					t.Errorf("output should NOT contain %q.\nGot:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestRecoverMultipliers(t *testing.T) {
	// Verify that recovered multipliers match the known ground truth.
	knownMults := []float64{2.5, -1.0, 0.7}
	acts := []map[string]float64{
		{"h1": 0.8, "h2": -0.3, "h3": 1.2, "h4": -0.7, "h5": 0.4},
		{"h1": 0.5, "h2": 1.0, "h3": -0.2, "h4": 0.3, "h5": -0.9},
		{"h1": -1.0, "h2": 0.6, "h3": 0.4, "h4": -0.1, "h5": 0.2},
	}
	hosts := []string{"h1", "h2", "h3", "h4", "h5"}
	normalizedIn := map[string]float64{"h1": 0.1, "h2": 0.2, "h3": -0.1, "h4": 0.0, "h5": 0.3}
	aggregatedOut := make(map[string]float64)
	for _, h := range hosts {
		aggregatedOut[h] = normalizedIn[h]
		for i, m := range knownMults {
			aggregatedOut[h] += m * math.Tanh(acts[i][h])
		}
	}

	steps := []v1alpha1.StepResult{
		{StepName: "w1", Activations: acts[0]},
		{StepName: "w2", Activations: acts[1]},
		{StepName: "w3", Activations: acts[2]},
	}

	recovered, ok := recoverMultipliers(steps, hosts, normalizedIn, aggregatedOut)
	if !ok {
		t.Fatal("recoverMultipliers failed unexpectedly")
	}
	if len(recovered) != len(knownMults) {
		t.Fatalf("got %d multipliers, want %d", len(recovered), len(knownMults))
	}
	for i, want := range knownMults {
		if math.Abs(recovered[i]-want) > 1e-6 {
			t.Errorf("multiplier[%d] = %.6f, want %.6f", i, recovered[i], want)
		}
	}
}

func TestSolveLinearSystem(t *testing.T) {
	// Simple 2x2 system: 2x + y = 5, x + 3y = 10 => x=1, y=3
	a := [][]float64{{2, 1}, {1, 3}}
	b := []float64{5, 10}
	x, ok := solveLinearSystem(a, b)
	if !ok {
		t.Fatal("solveLinearSystem failed")
	}
	if math.Abs(x[0]-1.0) > 1e-10 || math.Abs(x[1]-3.0) > 1e-10 {
		t.Errorf("got x=%v, want [1, 3]", x)
	}

	// Singular matrix should return false.
	singularA := [][]float64{{1, 2}, {2, 4}}
	singularB := []float64{3, 6}
	_, ok = solveLinearSystem(singularA, singularB)
	if ok {
		t.Error("expected failure for singular matrix")
	}
}

func TestIdentifyWeigherSteps(t *testing.T) {
	result := &v1alpha1.DecisionResult{
		OrderedHosts: []string{"a", "b", "c"},
		StepResults: []v1alpha1.StepResult{
			// Filter: missing host c
			{StepName: "filter_x", Activations: map[string]float64{"a": 1.0, "b": 1.0}},
			// Weigher: has all ordered hosts
			{StepName: "weigher_y", Activations: map[string]float64{"a": 0.5, "b": 0.3, "c": -0.2}},
			// Empty step: should be excluded
			{StepName: "empty_step", Activations: map[string]float64{}},
			// Another weigher
			{StepName: "weigher_z", Activations: map[string]float64{"a": 1.0, "b": 0.0, "c": 0.5, "d": 0.1}},
		},
	}

	steps := identifyWeigherSteps(result)
	if len(steps) != 2 {
		t.Fatalf("got %d weigher steps, want 2", len(steps))
	}
	if steps[0].StepName != "weigher_y" {
		t.Errorf("step[0] = %q, want weigher_y", steps[0].StepName)
	}
	if steps[1].StepName != "weigher_z" {
		t.Errorf("step[1] = %q, want weigher_z", steps[1].StepName)
	}
}

// sortByWeight sorts hosts in descending order of their weight (for test setup).
func sortByWeight(hosts []string, weights map[string]float64) {
	sort.Slice(hosts, func(i, j int) bool {
		return weights[hosts[i]] > weights[hosts[j]]
	})
}

// TestExplainWeighingDemo is a demonstration test that simulates a realistic
// Nova scheduling scenario with multiple weighers (including a negative
// multiplier for balancing) and prints the full explanation output. Run with:
//
//	go test ./internal/scheduling/lib/ -run TestExplainWeighingDemo -v
func TestExplainWeighingDemo(t *testing.T) {
	// Simulates a Nova scheduling pipeline with 5 compute hosts and 3 weighers:
	//   - kvm_binpack (mult=-1.0): inverted to achieve memory balancing
	//   - kvm_failover_evacuation (mult=2.0): prefers hosts with fewer VMs to evacuate
	//   - kvm_prefer_smaller_hosts (mult=0.5): slight preference for smaller hosts
	//
	// Host nova-compute-01 wins because kvm_failover_evacuation strongly favors it,
	// despite kvm_binpack opposing it (since it has high memory usage).
	multBinpack := -1.0
	multFailover := 2.0
	multSmaller := 0.5

	hosts := []string{
		"nova-compute-01",
		"nova-compute-02",
		"nova-compute-03",
		"nova-compute-04",
		"nova-compute-05",
	}

	// Raw activations (before tanh) — simulating real scheduler outputs.
	actBinpack := map[string]float64{
		"nova-compute-01": 0.8,  // high memory usage -> high binpack score
		"nova-compute-02": 0.3,  // moderate
		"nova-compute-03": -0.2, // low usage
		"nova-compute-04": 0.6,  // moderately full
		"nova-compute-05": -0.5, // nearly empty
	}
	actFailover := map[string]float64{
		"nova-compute-01": 1.5,  // few VMs to evacuate in failure -> strongly preferred
		"nova-compute-02": 0.4,  // moderate evacuation risk
		"nova-compute-03": -0.1, // slight risk
		"nova-compute-04": -0.8, // high evacuation cost
		"nova-compute-05": 0.2,  // low risk
	}
	actSmaller := map[string]float64{
		"nova-compute-01": -0.3, // large host
		"nova-compute-02": 0.1,  // medium
		"nova-compute-03": 0.7,  // smaller
		"nova-compute-04": -0.1, // medium-large
		"nova-compute-05": 1.2,  // smallest
	}

	// Simulate the pipeline: compute aggregated output weights.
	normalizedIn := map[string]float64{
		"nova-compute-01": 0.10,
		"nova-compute-02": 0.08,
		"nova-compute-03": 0.05,
		"nova-compute-04": 0.12,
		"nova-compute-05": 0.06,
	}
	aggregatedOut := make(map[string]float64, len(hosts))
	for _, h := range hosts {
		aggregatedOut[h] = normalizedIn[h] +
			multBinpack*math.Tanh(actBinpack[h]) +
			multFailover*math.Tanh(actFailover[h]) +
			multSmaller*math.Tanh(actSmaller[h])
	}

	// Sort hosts by aggregated weight to get OrderedHosts.
	sortByWeight(hosts, aggregatedOut)

	result := &v1alpha1.DecisionResult{
		NormalizedInWeights:  normalizedIn,
		AggregatedOutWeights: aggregatedOut,
		OrderedHosts:         hosts,
		TargetHost:           &hosts[0],
		StepResults: []v1alpha1.StepResult{
			{StepName: "kvm_binpack", Activations: actBinpack},
			{StepName: "kvm_failover_evacuation", Activations: actFailover},
			{StepName: "kvm_prefer_smaller_hosts", Activations: actSmaller},
		},
	}

	explanation := ExplainWeighing(result)
	if explanation == "" {
		t.Fatal("expected non-empty explanation")
	}

	t.Logf("=== Demo: Full scheduling explanation ===\n\n%s\n", explanation)

	// Verify key properties of the explanation.
	if !strings.Contains(explanation, "nova-compute-01") {
		t.Error("expected #1 host in explanation")
	}
	if !strings.Contains(explanation, "kvm_failover_evacuation") {
		t.Error("expected dominant weigher in explanation")
	}
	if !strings.Contains(explanation, "kvm_binpack") {
		t.Error("expected opposing weigher in explanation")
	}
}
