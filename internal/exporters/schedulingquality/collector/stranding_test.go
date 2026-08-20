// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"math"
	"testing"
)

func TestBalanceScore(t *testing.T) {
	tests := []struct {
		name    string
		effCap  map[string]float64
		alloc   map[string]float64
		wantMin float64
		wantMax float64
	}{
		{
			name:    "perfectly balanced 50/50",
			effCap:  map[string]float64{"cpu": 100, "memory": 1000},
			alloc:   map[string]float64{"cpu": 50, "memory": 500},
			wantMin: 0.99,
			wantMax: 1.01,
		},
		{
			name:    "perfectly balanced 0/0",
			effCap:  map[string]float64{"cpu": 100, "memory": 1000},
			alloc:   map[string]float64{"cpu": 0, "memory": 0},
			wantMin: 0.99,
			wantMax: 1.01,
		},
		{
			name:    "maximally imbalanced: cpu full, memory empty",
			effCap:  map[string]float64{"cpu": 100, "memory": 1000},
			alloc:   map[string]float64{"cpu": 100, "memory": 0},
			wantMin: 0.0,
			wantMax: 0.6, // stddev = 0.5, so score = 0.5
		},
		{
			name:    "moderate imbalance: 90% cpu, 10% memory",
			effCap:  map[string]float64{"cpu": 100, "memory": 1000},
			alloc:   map[string]float64{"cpu": 90, "memory": 100},
			wantMin: 0.55,
			wantMax: 0.65,
		},
		{
			name:    "single dimension: always balanced",
			effCap:  map[string]float64{"cpu": 100},
			alloc:   map[string]float64{"cpu": 90},
			wantMin: 0.99,
			wantMax: 1.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rv := resourceVec{EffectiveCapacity: tt.effCap, Allocation: tt.alloc}
			got := rv.balanceScore()
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("balanceScore() = %v, want [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestStrandedResources_Exhausted(t *testing.T) {
	min := minPlaceableUnit{CPU: 1, Memory: 512 * 1024 * 1024} // 1 core, 512 MB

	// CPU exhausted, memory has free capacity.
	rv := resourceVec{
		EffectiveCapacity: map[string]float64{"cpu": 64, "memory": 256e9},
		Allocation:        map[string]float64{"cpu": 64, "memory": 128e9},
	}
	stranded := strandedResources(rv, min)

	// All free memory is stranded because CPU is exhausted.
	if stranded["memory"] != 128e9 {
		t.Errorf("stranded memory = %v, want %v", stranded["memory"], 128e9)
	}
	// CPU should have nothing stranded (it's the exhausted dimension, not stranded).
	if stranded["cpu"] != 0 {
		t.Errorf("stranded cpu = %v, want 0", stranded["cpu"])
	}
}

func TestStrandedResources_BothExhausted(t *testing.T) {
	min := minPlaceableUnit{CPU: 1, Memory: 512 * 1024 * 1024}

	rv := resourceVec{
		EffectiveCapacity: map[string]float64{"cpu": 64, "memory": 256e9},
		Allocation:        map[string]float64{"cpu": 64, "memory": 256e9},
	}
	stranded := strandedResources(rv, min)

	// Both exhausted: nothing is stranded (nothing free at all).
	if len(stranded) != 0 {
		t.Errorf("stranded = %v, want empty", stranded)
	}
}

func TestStrandedResources_Proportional(t *testing.T) {
	min := minPlaceableUnit{CPU: 2, Memory: 4e9} // 2 cores, 4 GB per unit

	// 10 free CPU, 12 GB free memory.
	// Placeable units: min(10/2, 12e9/4e9) = min(5, 3) = 3
	// Usable CPU = 3*2 = 6, stranded CPU = 10 - 6 = 4
	// Usable Mem = 3*4e9 = 12e9, stranded Mem = 0
	rv := resourceVec{
		EffectiveCapacity: map[string]float64{"cpu": 64, "memory": 256e9},
		Allocation:        map[string]float64{"cpu": 54, "memory": 244e9},
	}
	stranded := strandedResources(rv, min)

	if stranded["cpu"] != 4 {
		t.Errorf("stranded cpu = %v, want 4", stranded["cpu"])
	}
	if stranded["memory"] != 0 {
		t.Errorf("stranded memory = %v, want 0", stranded["memory"])
	}
}

func TestStrandedResources_NothingStranded(t *testing.T) {
	min := minPlaceableUnit{CPU: 1, Memory: 1e9}

	// Perfectly proportional free capacity: 10 CPU, 10 GB.
	// Placeable = min(10/1, 10e9/1e9) = 10.
	// Usable CPU = 10, Usable Mem = 10e9. Nothing stranded.
	rv := resourceVec{
		EffectiveCapacity: map[string]float64{"cpu": 64, "memory": 256e9},
		Allocation:        map[string]float64{"cpu": 54, "memory": 246e9},
	}
	stranded := strandedResources(rv, min)

	if stranded["cpu"] != 0 {
		t.Errorf("stranded cpu = %v, want 0", stranded["cpu"])
	}
	if stranded["memory"] != 0 {
		t.Errorf("stranded memory = %v, want 0", stranded["memory"])
	}
}

func TestFragmentationRatio(t *testing.T) {
	tests := []struct {
		name     string
		stranded float64
		free     float64
		want     float64
	}{
		{"no free capacity", 0, 0, 0},
		{"no stranding", 0, 100, 0},
		{"half stranded", 50, 100, 0.5},
		{"all stranded", 100, 100, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fragmentationRatio(tt.stranded, tt.free)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("fragmentationRatio(%v, %v) = %v, want %v", tt.stranded, tt.free, got, tt.want)
			}
		})
	}
}

func TestTetrisAlignmentScore(t *testing.T) {
	tests := []struct {
		name    string
		free    map[string]float64
		demand  map[string]float64
		wantMin float64
		wantMax float64
	}{
		{
			name:    "perfectly aligned",
			free:    map[string]float64{"cpu": 10, "memory": 20},
			demand:  map[string]float64{"cpu": 5, "memory": 10},
			wantMin: 0.99,
			wantMax: 1.01,
		},
		{
			name:    "orthogonal: free cpu, demand memory",
			free:    map[string]float64{"cpu": 10, "memory": 0},
			demand:  map[string]float64{"cpu": 0, "memory": 10},
			wantMin: -0.01,
			wantMax: 0.01,
		},
		{
			name:    "moderate alignment",
			free:    map[string]float64{"cpu": 10, "memory": 2},
			demand:  map[string]float64{"cpu": 1, "memory": 4},
			wantMin: 0.4,
			wantMax: 0.8,
		},
		{
			name:    "zero free",
			free:    map[string]float64{"cpu": 0, "memory": 0},
			demand:  map[string]float64{"cpu": 1, "memory": 4},
			wantMin: -0.01,
			wantMax: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tetrisAlignmentScore(tt.free, tt.demand)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("tetrisAlignmentScore() = %v, want [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}
