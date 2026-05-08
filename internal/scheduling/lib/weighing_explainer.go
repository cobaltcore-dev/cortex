// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
)

const (
	// maxExplainedHosts limits the pairwise explanation to the top N hosts.
	maxExplainedHosts = 3

	// negligibleContributionThreshold is the absolute contribution value below
	// which a weigher is considered to have negligible impact on a host pair.
	negligibleContributionThreshold = 0.01

	// singularityEpsilon is the minimum pivot magnitude during Gaussian
	// elimination. Below this the system is considered ill-conditioned.
	singularityEpsilon = 1e-10
)

// weigherContribution holds the signed contribution of a single weigher to the
// score gap between two hosts. A positive value means the weigher favors the
// higher-ranked host; negative means it opposes the observed ranking.
type weigherContribution struct {
	stepName     string
	contribution float64
}

// ExplainWeighing produces a human-readable explanation of how weigher steps
// influenced the ranking of the top hosts in a scheduling decision.
//
// The algorithm works in three stages:
//
//  1. Multiplier recovery: The pipeline applies weights via an additive formula:
//     AggregatedOut[h] = NormalizedIn[h] + sum_i(mult_i * tanh(act_i[h])).
//     Since DecisionResult stores raw activations but not multipliers, we recover
//     them by solving the over-determined linear system (M hosts, N weighers)
//     via least-squares (normal equations). This handles negative multipliers
//     correctly and produces exact results when M >= N.
//
//  2. Counterfactual analysis: For the #1 host, we ask "if weigher X were
//     removed, would a different host be selected?" This identifies decisive
//     weighers whose removal would change the scheduling outcome.
//
//  3. Pairwise decomposition: For each consecutive pair in the top-N ranking,
//     we report which weigher contributed most to the gap, and flag any weigher
//     that opposed the outcome (negative contribution to a positive gap).
//
// Returns an empty string when the result is nil, has fewer than 2 ordered
// hosts, or contains no weigher steps.
func ExplainWeighing(result *v1alpha1.DecisionResult) string {
	if result == nil || len(result.OrderedHosts) < 2 {
		return ""
	}

	weigherSteps := identifyWeigherSteps(result)
	if len(weigherSteps) == 0 {
		return ""
	}

	// Determine how many top hosts to explain.
	topN := min(maxExplainedHosts, len(result.OrderedHosts))
	topHosts := result.OrderedHosts[:topN]

	// Recover the multipliers from the linear system:
	//   sum_i(mult_i * tanh(act_i[h])) = AggregatedOut[h] - NormalizedIn[h]
	// using all ordered hosts as data points for a least-squares fit.
	// Falls back to initial-bias-only explanation if recovery fails (e.g., all
	// activations are zero making the matrix singular, or under-determined).
	multipliers, ok := recoverMultipliers(weigherSteps, result.OrderedHosts, result.NormalizedInWeights, result.AggregatedOutWeights)
	if !ok {
		return explainWithoutMultipliers(result, topHosts, weigherSteps)
	}

	// Precompute effective contributions: contribution[weigherIdx][host] gives
	// the signed weight contribution of that weigher to that host's final score.
	// Computed for ALL ordered hosts (not just top-N) so counterfactual analysis
	// correctly considers hosts outside the top-N that might rise to #1.
	contributions := make([]map[string]float64, len(weigherSteps))
	for i, step := range weigherSteps {
		contributions[i] = make(map[string]float64, len(result.OrderedHosts))
		for _, h := range result.OrderedHosts {
			contributions[i][h] = multipliers[i] * math.Tanh(step.Activations[h])
		}
	}

	var sb strings.Builder

	// --- Header: state the ranking being explained ---
	sb.WriteString("Weighing impact on top-")
	fmt.Fprintf(&sb, "%d (", topN)
	for i, h := range topHosts {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(h)
	}
	sb.WriteString("):\n")

	// --- Counterfactual analysis for the #1 host ---
	// Ask: "Is there a single weigher whose removal would dethrone the #1 host?"
	// Evaluated over ALL ordered hosts so we don't miss a host outside the top-N
	// that would rise to #1 when a weigher is removed.
	counterfactualReported := false
	for i, step := range weigherSteps {
		newRanking := computeCounterfactualRanking(result.OrderedHosts, result.AggregatedOutWeights, contributions[i])
		if newRanking[0] != topHosts[0] {
			fmt.Fprintf(&sb, "  Without %s, %s would be #1 instead of %s.\n",
				step.StepName, newRanking[0], topHosts[0])
			counterfactualReported = true
			break
		}
	}

	// --- Pairwise decomposition for each consecutive pair in top-N ---
	// Track which weighers have negligible impact across ALL pairs.
	negligibleCandidates := make(map[string]bool, len(weigherSteps))
	for _, step := range weigherSteps {
		negligibleCandidates[step.StepName] = true
	}

	for rank := range topN - 1 {
		higher := topHosts[rank]
		lower := topHosts[rank+1]
		totalGap := result.AggregatedOutWeights[higher] - result.AggregatedOutWeights[lower]

		// Compute the initial weight bias (contribution from NormalizedInWeights).
		initialBias := 0.0
		if result.NormalizedInWeights != nil {
			initialBias = result.NormalizedInWeights[higher] - result.NormalizedInWeights[lower]
		}

		// Gather per-weigher contributions to this pair's gap.
		pairContribs := make([]weigherContribution, len(weigherSteps))
		for i, step := range weigherSteps {
			c := contributions[i][higher] - contributions[i][lower]
			pairContribs[i] = weigherContribution{stepName: step.StepName, contribution: c}
			if math.Abs(c) >= negligibleContributionThreshold {
				delete(negligibleCandidates, step.StepName)
			}
		}

		// Find leading positive contributor (supports the ranking).
		var leading *weigherContribution
		for j := range pairContribs {
			if pairContribs[j].contribution > negligibleContributionThreshold {
				if leading == nil || pairContribs[j].contribution > leading.contribution {
					leading = &pairContribs[j]
				}
			}
		}

		// Report the leading cause for this pair.
		switch {
		case leading != nil:
			fmt.Fprintf(&sb, "  %s is #%d because of %s (contributed %+.2f to gap of %.2f).\n",
				higher, rank+1, leading.stepName, leading.contribution, totalGap)
		case math.Abs(initialBias) > negligibleContributionThreshold:
			fmt.Fprintf(&sb, "  %s is #%d due to initial weight bias (%+.2f).\n",
				higher, rank+1, initialBias)
		case !counterfactualReported:
			fmt.Fprintf(&sb, "  %s is #%d over %s by a narrow margin (gap: %.4f).\n",
				higher, rank+1, lower, totalGap)
		}

		// Report the strongest opposing weigher (if significant).
		var opposing *weigherContribution
		for j := range pairContribs {
			if pairContribs[j].contribution < -negligibleContributionThreshold {
				if opposing == nil || pairContribs[j].contribution < opposing.contribution {
					opposing = &pairContribs[j]
				}
			}
		}
		if opposing != nil {
			fmt.Fprintf(&sb, "  %s opposed this ranking (contributed %.2f).\n",
				opposing.stepName, opposing.contribution)
		}
	}

	// --- Negligible-impact weighers ---
	if len(negligibleCandidates) > 0 {
		names := sortedMapKeys(negligibleCandidates)
		fmt.Fprintf(&sb, "  %s had negligible impact on top-%d ordering.\n",
			strings.Join(names, ", "), topN)
	}

	return strings.TrimSpace(sb.String())
}

// explainWithoutMultipliers provides a simpler explanation when multiplier
// recovery is not possible (e.g., all activations are zero, or the system is
// under-determined). It reports only the initial weight bias and raw activation
// differentials without exact contribution magnitudes.
func explainWithoutMultipliers(result *v1alpha1.DecisionResult, topHosts []string, _ []v1alpha1.StepResult) string {
	topN := len(topHosts)
	var sb strings.Builder

	sb.WriteString("Weighing impact on top-")
	fmt.Fprintf(&sb, "%d (", topN)
	for i, h := range topHosts {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(h)
	}
	sb.WriteString("):\n")

	for rank := range topN - 1 {
		higher := topHosts[rank]
		lower := topHosts[rank+1]

		initialBias := 0.0
		if result.NormalizedInWeights != nil {
			initialBias = result.NormalizedInWeights[higher] - result.NormalizedInWeights[lower]
		}

		if math.Abs(initialBias) > negligibleContributionThreshold {
			fmt.Fprintf(&sb, "  %s is #%d due to initial weight bias (%+.2f).\n",
				higher, rank+1, initialBias)
		} else {
			totalGap := result.AggregatedOutWeights[higher] - result.AggregatedOutWeights[lower]
			fmt.Fprintf(&sb, "  %s is #%d over %s (gap: %.4f).\n",
				higher, rank+1, lower, totalGap)
		}
	}

	return strings.TrimSpace(sb.String())
}

// identifyWeigherSteps returns the subset of step results that represent
// weigher (scoring) steps rather than filter steps. A weigher step is one
// whose activation map contains entries for ALL hosts in OrderedHosts —
// filters reduce the host set while weighers score all remaining hosts.
func identifyWeigherSteps(result *v1alpha1.DecisionResult) []v1alpha1.StepResult {
	orderedHostSet := make(map[string]struct{}, len(result.OrderedHosts))
	for _, h := range result.OrderedHosts {
		orderedHostSet[h] = struct{}{}
	}

	var weigherSteps []v1alpha1.StepResult
	for _, step := range result.StepResults {
		if len(step.Activations) == 0 {
			continue
		}
		isWeigher := true
		for h := range orderedHostSet {
			if _, exists := step.Activations[h]; !exists {
				isWeigher = false
				break
			}
		}
		if isWeigher {
			weigherSteps = append(weigherSteps, step)
		}
	}
	return weigherSteps
}

// recoverMultipliers solves for the weigher multipliers using least-squares.
//
// The additive pipeline formula guarantees:
//
//	AggregatedOut[h] - NormalizedIn[h] = sum_i(mult_i * tanh(act_i[h]))
//
// for every host h. This forms a linear system A*x = b where:
//   - A is an M×N matrix: A[h][i] = tanh(activation_i[h])
//   - b is an M-vector: b[h] = AggregatedOut[h] - NormalizedIn[h]
//   - x is the N-vector of unknown multipliers
//
// With M hosts (rows) and N weighers (columns), and typically M >> N, we
// solve the normal equations: (A^T * A) * x = A^T * b.
//
// Returns the recovered multipliers and true on success, or nil and false if
// the system is under-determined or ill-conditioned.
func recoverMultipliers(
	weigherSteps []v1alpha1.StepResult,
	orderedHosts []string,
	normalizedIn map[string]float64,
	aggregatedOut map[string]float64,
) ([]float64, bool) {

	M := len(orderedHosts) // number of data points (hosts)
	N := len(weigherSteps) // number of unknowns (multipliers)

	if M < N || N == 0 {
		return nil, false
	}

	// Build the M×N matrix A where A[h][i] = tanh(activation_i[host_h])
	// and the M-vector b where b[h] = aggregatedOut[host_h] - normalizedIn[host_h].
	A := make([][]float64, M)
	b := make([]float64, M)
	for h, host := range orderedHosts {
		A[h] = make([]float64, N)
		for i, step := range weigherSteps {
			A[h][i] = math.Tanh(step.Activations[host])
		}
		b[h] = aggregatedOut[host] - normalizedIn[host]
	}

	// Compute A^T * A (N×N symmetric matrix).
	ata := make([][]float64, N)
	for i := range N {
		ata[i] = make([]float64, N)
		for j := range N {
			sum := 0.0
			for h := range M {
				sum += A[h][i] * A[h][j]
			}
			ata[i][j] = sum
		}
	}

	// Compute A^T * b (N-vector).
	atb := make([]float64, N)
	for i := range N {
		sum := 0.0
		for h := range M {
			sum += A[h][i] * b[h]
		}
		atb[i] = sum
	}

	// Solve the N×N system (A^T A) x = A^T b.
	return solveLinearSystem(ata, atb)
}

// solveLinearSystem solves a square linear system Ax = b using Gaussian
// elimination with partial pivoting. Returns the solution vector and true
// on success, or nil and false if the matrix is singular (pivot < epsilon).
func solveLinearSystem(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	if n == 0 {
		return nil, false
	}

	// Create augmented matrix [A|b] to avoid modifying the inputs.
	aug := make([][]float64, n)
	for i := range n {
		aug[i] = make([]float64, n+1)
		copy(aug[i][:n], a[i])
		aug[i][n] = b[i]
	}

	// Forward elimination with partial pivoting.
	for col := range n {
		// Find the row with the largest absolute value in this column.
		maxRow := col
		maxVal := math.Abs(aug[col][col])
		for row := col + 1; row < n; row++ {
			if v := math.Abs(aug[row][col]); v > maxVal {
				maxVal = v
				maxRow = row
			}
		}

		if maxVal < singularityEpsilon {
			return nil, false
		}

		// Swap rows if needed.
		if maxRow != col {
			aug[col], aug[maxRow] = aug[maxRow], aug[col]
		}

		// Eliminate below the pivot.
		pivot := aug[col][col]
		for row := col + 1; row < n; row++ {
			factor := aug[row][col] / pivot
			for j := col; j <= n; j++ {
				aug[row][j] -= factor * aug[col][j]
			}
		}
	}

	// Back substitution.
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		if math.Abs(aug[i][i]) < singularityEpsilon {
			return nil, false
		}
		sum := aug[i][n]
		for j := i + 1; j < n; j++ {
			sum -= aug[i][j] * x[j]
		}
		x[i] = sum / aug[i][i]
	}
	return x, true
}

// computeCounterfactualRanking computes what the ranking of topHosts would be
// if a single weigher's contributions were removed. It subtracts the weigher's
// per-host contribution from the aggregated weights and re-sorts.
func computeCounterfactualRanking(
	topHosts []string,
	aggregatedOut map[string]float64,
	weigherContribs map[string]float64,
) []string {
	// Compute hypothetical scores without this weigher.
	hypothetical := make(map[string]float64, len(topHosts))
	for _, h := range topHosts {
		hypothetical[h] = aggregatedOut[h] - weigherContribs[h]
	}

	// Sort by hypothetical score descending.
	ranking := make([]string, len(topHosts))
	copy(ranking, topHosts)
	sort.Slice(ranking, func(i, j int) bool {
		return hypothetical[ranking[i]] > hypothetical[ranking[j]]
	})
	return ranking
}

// sortedMapKeys returns the keys of a bool map in sorted order.
func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
