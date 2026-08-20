// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"math"

	"k8s.io/apimachinery/pkg/api/resource"
)

// resourceVec holds capacity and allocation for a set of resource dimensions.
type resourceVec struct {
	EffectiveCapacity map[string]float64
	Allocation        map[string]float64
}

// free returns effective_capacity - allocation for each dimension; floored at 0.
func (v resourceVec) free() map[string]float64 {
	f := make(map[string]float64, len(v.EffectiveCapacity))
	for dim, cap := range v.EffectiveCapacity {
		alloc := v.Allocation[dim]
		if cap-alloc > 0 {
			f[dim] = cap - alloc
		}
	}
	return f
}

// fractions returns allocation / effective_capacity per dimension.
// Returns 0 for dimensions with zero capacity.
func (v resourceVec) fractions() map[string]float64 {
	f := make(map[string]float64, len(v.EffectiveCapacity))
	for dim, cap := range v.EffectiveCapacity {
		if cap > 0 {
			f[dim] = v.Allocation[dim] / cap
		}
	}
	return f
}

// balanceScore computes the Kubernetes balanced-allocation score:
//
//	score = 1 - stddev(fraction_i)
//
// Returns 1.0 for perfectly balanced, 0.0 for maximally imbalanced.
// With a single dimension the score is always 1.0.
func (v resourceVec) balanceScore() float64 {
	fracs := v.fractions()
	n := float64(len(fracs))
	if n <= 1 {
		return 1.0
	}

	var sum float64
	for _, f := range fracs {
		sum += f
	}
	mean := sum / n

	var variance float64
	for _, f := range fracs {
		d := f - mean
		variance += d * d
	}
	variance /= n
	std := math.Sqrt(variance)

	score := 1.0 - std
	if score < 0 {
		return 0
	}
	return score
}

// minPlaceableUnit defines the smallest VM that can be placed, per dimension.
// Used as the threshold below which remaining capacity is considered stranded.
type minPlaceableUnit struct {
	CPU    float64 // cores
	Memory float64 // bytes
}

// strandedResources computes stranded capacity per dimension on a single host.
//
// A resource dimension is stranded when the free capacity in that dimension
// cannot be consumed because another dimension's free capacity is below the
// minimum placeable unit.
//
// Returns a map from dimension name to stranded quantity.
func strandedResources(v resourceVec, min minPlaceableUnit) map[string]float64 {
	free := v.free()
	stranded := make(map[string]float64)

	// Map dimension names to their minimum placeable thresholds.
	thresholds := map[string]float64{
		"cpu":    min.CPU,
		"memory": min.Memory,
	}

	// For each dimension, check if it's below the minimum placeable unit.
	// If so, all other dimensions' free capacity is stranded.
	exhausted := make(map[string]bool)
	for dim, threshold := range thresholds {
		if free[dim] < threshold {
			exhausted[dim] = true
		}
	}

	if len(exhausted) == 0 {
		// No dimension is exhausted; compute proportional stranding.
		// Even when all dimensions have free capacity above the minimum,
		// imbalanced free capacity creates stranding. The stranded amount
		// per dimension equals the free capacity that exceeds what the
		// bottleneck dimension can support.
		//
		// For CPU and memory: how many minimum units can we place?
		// placeable = min(free_cpu / min_cpu, free_mem / min_mem)
		// stranded_cpu = free_cpu - placeable * min_cpu
		// stranded_mem = free_mem - placeable * min_mem
		if min.CPU > 0 && min.Memory > 0 {
			freeCPU := free["cpu"]
			freeMem := free["memory"]
			placeableByCPU := math.Floor(freeCPU / min.CPU)
			placeableByMem := math.Floor(freeMem / min.Memory)
			placeable := math.Min(placeableByCPU, placeableByMem)

			cpuUsable := placeable * min.CPU
			memUsable := placeable * min.Memory

			if freeCPU-cpuUsable > 0 {
				stranded["cpu"] = freeCPU - cpuUsable
			}
			if freeMem-memUsable > 0 {
				stranded["memory"] = freeMem - memUsable
			}
		}
		return stranded
	}

	// At least one dimension is exhausted.
	// All free capacity in non-exhausted dimensions is stranded.
	for dim, f := range free {
		if !exhausted[dim] && f > 0 {
			stranded[dim] = f
		}
	}
	return stranded
}

// fragmentationRatio computes the cluster-wide fragmentation ratio:
//
//	fragmentation = total_stranded / total_free
//
// per dimension. Returns 0 if total free is 0.
func fragmentationRatio(totalStranded, totalFree float64) float64 {
	if totalFree <= 0 {
		return 0
	}
	return totalStranded / totalFree
}

// tetrisAlignmentScore computes the Tetris dot-product alignment score
// between a workload demand vector and the host's available capacity vector.
// Both vectors are normalized to unit length before computing the dot product.
//
// For a host with free capacity, this measures how well the free capacity
// shape matches the expected workload shape. Score range: [0, 1].
// 1.0 = perfectly aligned (free capacity proportions match demand proportions).
func tetrisAlignmentScore(freeVec, demandVec map[string]float64) float64 {
	// Compute norms.
	var freeNorm, demandNorm float64
	for dim, f := range freeVec {
		d := demandVec[dim]
		freeNorm += f * f
		demandNorm += d * d
	}
	freeNorm = math.Sqrt(freeNorm)
	demandNorm = math.Sqrt(demandNorm)

	if freeNorm == 0 || demandNorm == 0 {
		return 0
	}

	// Dot product of normalized vectors.
	var dot float64
	for dim, f := range freeVec {
		d := demandVec[dim]
		dot += (f / freeNorm) * (d / demandNorm)
	}
	return dot
}

// quantityToFloat64 converts a resource.Quantity to float64.
// For CPU: returns cores (e.g., "4" -> 4.0, "500m" -> 0.5).
// For Memory: returns bytes.
func quantityToFloat64(q resource.Quantity, dimName string) float64 {
	switch dimName {
	case "cpu":
		return float64(q.MilliValue()) / 1000.0
	default:
		// Memory and other resources: use raw value (bytes).
		return float64(q.Value())
	}
}
