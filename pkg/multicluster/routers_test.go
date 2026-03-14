// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package multicluster

import (
	"testing"

	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHypervisorResourceRouter_Match(t *testing.T) {
	router := HypervisorResourceRouter{}

	tests := []struct {
		name      string
		obj       any
		labels    map[string]string
		wantMatch bool
		wantErr   bool
	}{
		{
			name: "matching AZ",
			obj: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"topology.kubernetes.io/zone": "qa-de-1a"},
				},
			},
			labels:    map[string]string{"az": "qa-de-1a"},
			wantMatch: true,
		},
		{
			name: "non-matching AZ",
			obj: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"topology.kubernetes.io/zone": "qa-de-1a"},
				},
			},
			labels:    map[string]string{"az": "qa-de-1b"},
			wantMatch: false,
		},
		{
			name:    "not a Hypervisor",
			obj:     "not-a-hypervisor",
			labels:  map[string]string{"az": "qa-de-1a"},
			wantErr: true,
		},
		{
			name: "cluster missing az label",
			obj: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"topology.kubernetes.io/zone": "qa-de-1a"},
				},
			},
			labels:  map[string]string{},
			wantErr: true,
		},
		{
			name: "hypervisor missing zone label",
			obj: hv1.Hypervisor{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
			labels:  map[string]string{"az": "qa-de-1a"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, err := router.Match(tt.obj, tt.labels)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if match != tt.wantMatch {
				t.Errorf("expected match=%v, got %v", tt.wantMatch, match)
			}
		})
	}
}
