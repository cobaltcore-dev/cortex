// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package kvm

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/knowledge/api/features/kvm"
	"github.com/cobaltcore-dev/cortex/lib/db"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

// Decision represents a descheduling decision for testing
type Decision struct {
	VMID   string
	Reason string
	Host   string
}

func TestAvoidHighStealPctStep_Run(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	// Create the feature table
	tablemap := testDB.AddTable(kvm.LibvirtDomainCPUStealPct{})
	err := testDB.CreateTable(tablemap)
	if err != nil {
		t.Fatalf("failed to create tables: %v", err)
	}

	tests := []struct {
		name              string
		threshold         float64
		features          []kvm.LibvirtDomainCPUStealPct
		expectedDecisions int
		expectedVMs       []string
		expectSkip        bool
	}{
		{
			name:       "skip when threshold is zero",
			threshold:  0.0,
			features:   []kvm.LibvirtDomainCPUStealPct{},
			expectSkip: true,
		},
		{
			name:       "skip when threshold is negative",
			threshold:  -5.0,
			features:   []kvm.LibvirtDomainCPUStealPct{},
			expectSkip: true,
		},
		{
			name:              "no VMs above threshold",
			threshold:         80.0,
			expectedDecisions: 0,
			features: []kvm.LibvirtDomainCPUStealPct{
				{InstanceUUID: "vm-1", Host: "host1", MaxStealTimePct: 50.0},
				{InstanceUUID: "vm-2", Host: "host2", MaxStealTimePct: 75.0},
				{InstanceUUID: "vm-3", Host: "host1", MaxStealTimePct: 60.0},
			},
		},
		{
			name:              "some VMs above threshold",
			threshold:         70.0,
			expectedDecisions: 2,
			expectedVMs:       []string{"vm-2", "vm-4"},
			features: []kvm.LibvirtDomainCPUStealPct{
				{InstanceUUID: "vm-1", Host: "host1", MaxStealTimePct: 50.0},
				{InstanceUUID: "vm-2", Host: "host2", MaxStealTimePct: 75.0},
				{InstanceUUID: "vm-3", Host: "host1", MaxStealTimePct: 60.0},
				{InstanceUUID: "vm-4", Host: "host3", MaxStealTimePct: 85.5},
			},
		},
		{
			name:              "all VMs above threshold",
			threshold:         40.0,
			expectedDecisions: 3,
			expectedVMs:       []string{"vm-1", "vm-2", "vm-3"},
			features: []kvm.LibvirtDomainCPUStealPct{
				{InstanceUUID: "vm-1", Host: "host1", MaxStealTimePct: 50.0},
				{InstanceUUID: "vm-2", Host: "host2", MaxStealTimePct: 75.0},
				{InstanceUUID: "vm-3", Host: "host1", MaxStealTimePct: 60.0},
			},
		},
		{
			name:              "VM exactly at threshold (should not be selected)",
			threshold:         75.0,
			expectedDecisions: 1,
			expectedVMs:       []string{"vm-3"},
			features: []kvm.LibvirtDomainCPUStealPct{
				{InstanceUUID: "vm-1", Host: "host1", MaxStealTimePct: 50.0},
				{InstanceUUID: "vm-2", Host: "host2", MaxStealTimePct: 75.0}, // exactly at threshold
				{InstanceUUID: "vm-3", Host: "host1", MaxStealTimePct: 75.1}, // above threshold
			},
		},
		{
			name:              "empty database",
			threshold:         50.0,
			expectedDecisions: 0,
			features:          []kvm.LibvirtDomainCPUStealPct{},
		},
		{
			name:              "high precision values",
			threshold:         75.555,
			expectedDecisions: 1,
			expectedVMs:       []string{"vm-2"},
			features: []kvm.LibvirtDomainCPUStealPct{
				{InstanceUUID: "vm-1", Host: "host1", MaxStealTimePct: 75.554},
				{InstanceUUID: "vm-2", Host: "host2", MaxStealTimePct: 75.556},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up the table before each test
			_, err := testDB.Exec("DELETE FROM " + kvm.LibvirtDomainCPUStealPct{}.TableName())
			if err != nil {
				t.Fatalf("failed to clean table: %v", err)
			}

			// Insert test data
			for _, feature := range tt.features {
				err := testDB.Insert(&feature)
				if err != nil {
					t.Fatalf("failed to insert feature: %v", err)
				}
			}

			step := &AvoidHighStealPctStep{}
			step.Options.MaxStealPctOverObservedTimeSpan = tt.threshold
			step.DB = &testDB

			// Run the step
			decisions, err := step.Run()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check if step should be skipped
			if tt.expectSkip {
				if len(decisions) != 0 {
					t.Errorf("expected step to be skipped (no decisions), got %d decisions", len(decisions))
				}
				return
			}

			// Check number of decisions
			if len(decisions) != tt.expectedDecisions {
				t.Errorf("expected %d decisions, got %d", tt.expectedDecisions, len(decisions))
			}

			// Check that the correct VMs were selected
			if tt.expectedVMs != nil {
				actualVMs := make([]string, len(decisions))
				for i, decision := range decisions {
					actualVMs[i] = decision.VMID
				}

				if !equalSlices(actualVMs, tt.expectedVMs) {
					t.Errorf("expected VMs %v, got %v", tt.expectedVMs, actualVMs)
				}
			}

			// Validate decision details
			for _, decision := range decisions {
				if decision.VMID == "" {
					t.Error("decision should have non-empty VMID")
				}
				if decision.Host == "" {
					t.Error("decision should have non-empty Host")
				}
				if decision.Reason == "" {
					t.Error("decision should have non-empty Reason")
				}

				// Find the corresponding feature to validate reason
				var matchingFeature *kvm.LibvirtDomainCPUStealPct
				for _, feature := range tt.features {
					if feature.InstanceUUID == decision.VMID {
						matchingFeature = &feature
						break
					}
				}

				if matchingFeature == nil {
					t.Errorf("could not find matching feature for decision VMID %s", decision.VMID)
					continue
				}

				// Verify the host matches
				if decision.Host != matchingFeature.Host {
					t.Errorf("expected host %s for VM %s, got %s",
						matchingFeature.Host, decision.VMID, decision.Host)
				}

				// Verify the steal percentage is above threshold
				if matchingFeature.MaxStealTimePct <= tt.threshold {
					t.Errorf("VM %s has steal pct %.2f%% which should not exceed threshold %.2f%%",
						decision.VMID, matchingFeature.MaxStealTimePct, tt.threshold)
				}
			}
		})
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	// Create maps for comparison (order doesn't matter)
	mapA := make(map[string]bool)
	mapB := make(map[string]bool)

	for _, v := range a {
		mapA[v] = true
	}
	for _, v := range b {
		mapB[v] = true
	}

	for k := range mapA {
		if !mapB[k] {
			return false
		}
	}
	for k := range mapB {
		if !mapA[k] {
			return false
		}
	}

	return true
}
