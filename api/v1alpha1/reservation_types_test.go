// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import "testing"

func TestCommittedResourceReservationSpec_MatchesGroup(t *testing.T) {
	spec := CommittedResourceReservationSpec{
		ProjectID:     "proj-1",
		ResourceGroup: "kvm_v2",
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
		if got := spec.MatchesGroup(tt.project, tt.group); got != tt.want {
			t.Errorf("MatchesGroup(%q, %q) = %v, want %v", tt.project, tt.group, got, tt.want)
		}
	}
}
