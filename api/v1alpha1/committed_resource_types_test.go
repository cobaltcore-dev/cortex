// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import "testing"

func TestCommittedResourceSpec_IsActive(t *testing.T) {
	tests := []struct {
		state CommitmentStatus
		want  bool
	}{
		{CommitmentStatusConfirmed, true},
		{CommitmentStatusGuaranteed, true},
		{CommitmentStatusPlanned, false},
		{CommitmentStatusPending, false},
		{CommitmentStatusSuperseded, false},
		{CommitmentStatusExpired, false},
	}
	for _, tt := range tests {
		spec := CommittedResourceSpec{State: tt.state}
		if got := spec.IsActive(); got != tt.want {
			t.Errorf("IsActive() with state %q = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestCommittedResource_IsActive(t *testing.T) {
	tests := []struct {
		state CommitmentStatus
		want  bool
	}{
		{CommitmentStatusConfirmed, true},
		{CommitmentStatusGuaranteed, true},
		{CommitmentStatusPlanned, false},
		{CommitmentStatusPending, false},
		{CommitmentStatusSuperseded, false},
		{CommitmentStatusExpired, false},
	}
	for _, tt := range tests {
		cr := CommittedResource{Spec: CommittedResourceSpec{State: tt.state}}
		if got := cr.IsActive(); got != tt.want {
			t.Errorf("IsActive() with state %q = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestCommittedResource_MatchesGroup(t *testing.T) {
	cr := CommittedResource{
		Spec: CommittedResourceSpec{
			ProjectID:       "proj-1",
			FlavorGroupName: "kvm_v2",
		},
	}
	tests := []struct {
		project string
		group   string
		want    bool
	}{
		{"proj-1", "kvm_v2", true},
		{"proj-X", "kvm_v2", false},
		{"proj-1", "kvm_v3", false},
		{"proj-X", "kvm_v3", false},
	}
	for _, tt := range tests {
		if got := cr.MatchesGroup(tt.project, tt.group); got != tt.want {
			t.Errorf("MatchesGroup(%q, %q) = %v, want %v", tt.project, tt.group, got, tt.want)
		}
	}
}
