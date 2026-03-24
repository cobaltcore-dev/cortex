// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

//nolint:unparam,unused // test helper functions have fixed parameters for simplicity
package commitments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/liquid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// ============================================================================
// Integration Tests
// ============================================================================

func TestCommitmentChangeIntegration(t *testing.T) {
	m1Tiny := &TestFlavor{Name: "m1.tiny", Group: "gp_1", MemoryMB: 256, VCPUs: 1}
	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}
	m1Large := &TestFlavor{Name: "m1.large", Group: "hana_1", MemoryMB: 4096, VCPUs: 16}
	m1XL := &TestFlavor{Name: "m1.xl", Group: "hana_1", MemoryMB: 8192, VCPUs: 32}

	testCases := []CommitmentChangeTestCase{
		{
			Name:    "Shrinking CR - unused reservations removed, used reservations untouched",
			VMs:     []*TestVM{{UUID: "vm-a1", Flavor: m1Large, ProjectID: "project-A", Host: "host-1", AZ: "az-a"}},
			Flavors: []*TestFlavor{m1Small, m1Large},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-123", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-a1"}},
				{CommitmentID: "uuid-123", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-123", Host: "host-3", Flavor: m1Small, ProjectID: "project-A"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234, createCommitment("ram_hana_1", "project-A", "uuid-123", "confirmed", 2)),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-123", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-a1"}},
				{CommitmentID: "uuid-123", Host: "host-3", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:                 "Insufficient capacity when increasing CR",
			VMs:                  []*TestVM{},
			Flavors:              []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{{CommitmentID: "uuid-456", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"}},
			CommitmentRequest:    newCommitmentRequest("az-a", false, 1234, createCommitment("ram_hana_1", "project-A", "uuid-456", "confirmed", 3)),
			AvailableResources:   &AvailableResources{PerHost: map[string]int64{"host-1": 1024, "host-2": 0}},
			ExpectedReservations: []*TestReservation{{CommitmentID: "uuid-456", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"}},
			ExpectedAPIResponse:  newAPIResponse("1 commitment(s) failed", "commitment uuid-456: not sufficient capacity"),
		},
		{
			Name:                 "Invalid CR name - too long",
			VMs:                  []*TestVM{},
			Flavors:              []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", strings.Repeat("long-", 13), "confirmed", 3),
			),
			AvailableResources:   &AvailableResources{},
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  newAPIResponse("1 commitment(s) failed", "commitment long-long-long-long-long-long-long-long-long-long-long-long-long-: unexpected commitment format"),
		},
		{
			Name:                 "Invalid CR name - spaces",
			VMs:                  []*TestVM{},
			Flavors:              []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid with space", "confirmed", 3),
			),
			AvailableResources:   &AvailableResources{},
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  newAPIResponse("1 commitment(s) failed", "commitment uuid with space: unexpected commitment format"),
		},
		{
			Name:    "Swap capacity between CRs - order dependent - delete-first succeeds",
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-456", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-456", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"}},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-456", "confirmed", 0),
				createCommitment("ram_hana_1", "project-B", "uuid-123", "confirmed", 2),
			),
			AvailableResources: &AvailableResources{PerHost: map[string]int64{"host-1": 0, "host-2": 0}},
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-123", Host: "host-1", Flavor: m1Small, ProjectID: "project-B"},
				{CommitmentID: "uuid-123", Host: "host-2", Flavor: m1Small, ProjectID: "project-B"}},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "Swap capacity between CRs - order dependent - create-first fails",
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-123", Host: "host-1", Flavor: m1Small, ProjectID: "project-B"},
				{CommitmentID: "uuid-123", Host: "host-2", Flavor: m1Small, ProjectID: "project-B"}},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-456", "confirmed", 2),
				createCommitment("ram_hana_1", "project-B", "uuid-123", "confirmed", 0),
			),
			AvailableResources: &AvailableResources{PerHost: map[string]int64{"host-1": 0, "host-2": 0}},
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-123", Host: "host-1", Flavor: m1Small, ProjectID: "project-B"},
				{CommitmentID: "uuid-123", Host: "host-2", Flavor: m1Small, ProjectID: "project-B"}},
			ExpectedAPIResponse: newAPIResponse("1 commitment(s) failed", "commitment uuid-456: not sufficient capacity"),
		},
		{
			Name: "Flavor bin-packing - mixed sizes when largest doesn't fit",
			// Greedy selection: 10GB request with 8/4/1GB flavors → picks 1×8GB + 2×1GB
			Flavors: []*TestFlavor{m1XL, m1Large, m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-binpack", "confirmed", 10),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-binpack", Flavor: m1XL, ProjectID: "project-A"},
				{CommitmentID: "uuid-binpack", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-binpack", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name: "Version mismatch - request rejected with 409 Conflict",
			// InfoVersion validation prevents stale requests (1233 vs 1234)
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1233,
				createCommitment("ram_hana_1", "project-A", "uuid-version", "confirmed", 2),
			),
			EnvInfoVersion:       1234,
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  APIResponseExpectation{StatusCode: 409},
		},
		{
			Name: "Multi-project rollback - one failure rolls back all",
			// Transactional: project-B fails (insufficient capacity) → both projects rollback
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-project-a", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-project-a", "confirmed", 2),
				createCommitment("ram_hana_1", "project-B", "uuid-project-b", "confirmed", 2),
			),
			AvailableResources: &AvailableResources{PerHost: map[string]int64{"host-1": 1024, "host-2": 0}},
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-project-a", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse("uuid-project-b", "not sufficient capacity"),
		},
		{
			Name: "Rollback with VMs allocated - limitation: VM allocations not rolled back",
			// Controller will eventually clean up and repair inconsistent state
			VMs:     []*TestVM{{UUID: "vm-rollback", Flavor: m1Small, ProjectID: "project-A", Host: "host-1", AZ: "az-a"}},
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "commitment-A", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-rollback"}},
				{CommitmentID: "commitment-A", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "commitment-A", "confirmed", 0),
				createCommitment("ram_hana_1", "project-B", "commitment-B", "confirmed", 6),
			),
			AvailableResources: &AvailableResources{PerHost: map[string]int64{"host-1": 0}},
			ExpectedReservations: []*TestReservation{
				// Rollback creates unscheduled reservations (empty Host accepts any in matching)
				{CommitmentID: "commitment-A", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "commitment-A", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse("commitment-B", "not sufficient capacity"),
		},
		{
			Name:    "New commitment creation - from zero to N reservations",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-new", "confirmed", 3),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-new", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-new", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-new", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "New commitment creation - large batch",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-new", "confirmed", 200),
			),
			ExpectedReservations: func() []*TestReservation {
				var reservations []*TestReservation
				for range 200 {
					reservations = append(reservations, &TestReservation{
						CommitmentID: "uuid-new",
						Flavor:       m1Small,
						ProjectID:    "project-A",
					})
				}
				return reservations
			}(),
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name: "With reservations of custom size - total unchanged",
			// Preserves custom-sized reservations when total matches (2×2GB = 4GB)
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-custom", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Host: "host-2", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-custom", "confirmed", 4),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-custom", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Host: "host-2", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name: "With reservations of custom size - increase total",
			// 4GB (2×2GB custom) → 6GB: preserves custom sizes, adds standard-sized reservations
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-custom", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Host: "host-2", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-custom", "confirmed", 6),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-custom", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Host: "host-2", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-custom", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name: "With reservations of custom size - decrease total",
			// 4GB (2×2GB custom) → 3GB: removes 1×2GB custom, adds 1×1GB standard
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-custom", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Host: "host-2", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-custom", "confirmed", 3),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-custom", Flavor: m1Small, ProjectID: "project-A", MemoryMB: 2048},
				{CommitmentID: "uuid-custom", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "Complete commitment deletion - N to zero reservations",
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-delete", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-delete", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-delete", Host: "host-3", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-b-1", Host: "host-3", Flavor: m1Small, ProjectID: "project-B"},
				{CommitmentID: "uuid-a-1", Host: "host-3", Flavor: m1Small, ProjectID: "project-A"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-delete", "confirmed", 0),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-b-1", Host: "host-3", Flavor: m1Small, ProjectID: "project-B"},
				{CommitmentID: "uuid-a-1", Host: "host-3", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "VM allocation preservation - keep VMs during growth",
			VMs:     []*TestVM{{UUID: "vm-existing", Flavor: m1Small, ProjectID: "project-A", Host: "host-1", AZ: "az-a"}},
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-growth", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-existing"}},
				{CommitmentID: "uuid-growth", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-growth", "confirmed", 3),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-growth", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-existing"}},
				{CommitmentID: "uuid-growth", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-growth", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "Multi-project success - both projects succeed",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-a", "confirmed", 2),
				createCommitment("ram_hana_1", "project-B", "uuid-b", "confirmed", 2),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-a", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-a", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-b", Flavor: m1Small, ProjectID: "project-B"},
				{CommitmentID: "uuid-b", Flavor: m1Small, ProjectID: "project-B"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name: "Multiple flavor groups - ram_hana_1 and ram_hana_2",
			// Amount in multiples of smallest flavor: hana_1 (2×1GB), hana_2 (2×2GB)
			Flavors: []*TestFlavor{
				m1Small,
				{Name: "m2.small", Group: "hana_2", MemoryMB: 2048, VCPUs: 8},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-hana1", "confirmed", 2),
				createCommitment("ram_hana_2", "project-A", "uuid-hana2", "confirmed", 2),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-hana1", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-hana1", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-hana2", Flavor: &TestFlavor{Name: "m2.small", Group: "hana_2", MemoryMB: 2048, VCPUs: 8}, ProjectID: "project-A"},
				{CommitmentID: "uuid-hana2", Flavor: &TestFlavor{Name: "m2.small", Group: "hana_2", MemoryMB: 2048, VCPUs: 8}, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "Unknown flavor group - clear rejection message",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_nonexistent", "project-A", "uuid-unknown", "confirmed", 2),
			),
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  newAPIResponse("flavor group not found"),
		},
		{
			Name: "Three-way capacity swap - complex reallocation",
			// A:2→0, B:1→0, C:0→3 in single transaction
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-a", Host: "host-1", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-a", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
				{CommitmentID: "uuid-b", Host: "host-3", Flavor: m1Small, ProjectID: "project-B"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-a", "confirmed", 0),
				createCommitment("ram_hana_1", "project-B", "uuid-b", "confirmed", 0),
				createCommitment("ram_hana_1", "project-C", "uuid-c", "confirmed", 3),
			),
			AvailableResources: &AvailableResources{PerHost: map[string]int64{"host-1": 0, "host-2": 0, "host-3": 0}},
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-c", Host: "host-1", Flavor: m1Small, ProjectID: "project-C"},
				{CommitmentID: "uuid-c", Host: "host-2", Flavor: m1Small, ProjectID: "project-C"},
				{CommitmentID: "uuid-c", Host: "host-3", Flavor: m1Small, ProjectID: "project-C"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "Reservation repair - existing reservations with wrong metadata",
			Flavors: []*TestFlavor{m1Small, m1Large},
			ExistingReservations: []*TestReservation{
				{CommitmentID: "uuid-repair", Host: "host-preserved", Flavor: m1Small, ProjectID: "project-A", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Host: "host-1", Flavor: m1Small, ProjectID: "wrong-project", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Host: "host-2", Flavor: &TestFlavor{Name: "m1.small", Group: "hana_13", MemoryMB: 1024, VCPUs: 4}, ProjectID: "project-A", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Host: "host-4", Flavor: m1Small, ProjectID: "project-A", AZ: "wrong-az"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-repair", "confirmed", 8, "az-a"),
			),
			ExpectedReservations: []*TestReservation{
				{CommitmentID: "uuid-repair", Host: "host-preserved", Flavor: m1Small, ProjectID: "project-A", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Flavor: m1Small, ProjectID: "project-A", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Flavor: m1Small, ProjectID: "project-A", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Flavor: m1Small, ProjectID: "project-A", AZ: "az-a"},
				{CommitmentID: "uuid-repair", Flavor: m1Large, ProjectID: "project-A", AZ: "az-a"},
			},
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:                 "Empty request - no commitment changes",
			Flavors:              []*TestFlavor{m1Small},
			CommitmentRequest:    newCommitmentRequest("az-a", false, 1234),
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  newAPIResponse(),
		},
		{
			Name:    "Dry run request - feature not yet implemented",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", true, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-dryrun", "confirmed", 2),
			),
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  newAPIResponse("Dry run not supported"),
		},
		{
			Name:    "Knowledge not ready - clear rejection with RetryAt",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-knowledge", "confirmed", 2),
			),
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse: APIResponseExpectation{
				StatusCode:     503,
				RetryAtPresent: false,
			},
			EnvInfoVersion: -1, // Skip Knowledge CRD creation
		},
		{
			Name:    "API disabled - returns 503 Service Unavailable",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-disabled", "confirmed", 2),
			),
			CustomConfig: func() *Config {
				cfg := DefaultConfig()
				cfg.EnableChangeCommitmentsAPI = false
				return &cfg
			}(),
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse: APIResponseExpectation{
				StatusCode: 503,
			},
		},
		{
			Name: "Multiple commitments insufficient capacity - all listed in error",
			// Tests that multiple failed commitments are all mentioned in the rejection reason
			Flavors: []*TestFlavor{m1Small, m1Tiny},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-multi-fail-1", "confirmed", 3),
				createCommitment("ram_hana_1", "project-B", "uuid-multi-fail-2", "confirmed", 3),
				createCommitment("ram_gp_1", "project-C", "uuid-would-not-fail", "confirmed", 1), // would be rolled back, but not part of the reject reason
			),
			AvailableResources:   &AvailableResources{PerHost: map[string]int64{"host-1": 256}},
			ExpectedReservations: []*TestReservation{},
			ExpectedAPIResponse:  newAPIResponse("2 commitment(s) failed", "commitment uuid-multi-fail-1: not sufficient capacity", "commitment uuid-multi-fail-2: not sufficient capacity"),
		},
		{
			Name: "Deletion priority during rollback - unscheduled removed first",
			// Tests that during rollback, unscheduled reservations (no TargetHost) are deleted first,
			// preserving scheduled reservations (with TargetHost), especially those with VM allocations
			VMs:     []*TestVM{{UUID: "vm-priority", Flavor: m1Small, ProjectID: "project-A", Host: "host-1", AZ: "az-a"}},
			Flavors: []*TestFlavor{m1Small},
			ExistingReservations: []*TestReservation{
				// Reservation with VM allocation - should be preserved (lowest deletion priority)
				{CommitmentID: "commitment-1", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-priority"}},
				// Scheduled but unused - medium deletion priority
				{CommitmentID: "commitment-1", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "commitment-1", "confirmed", 4),
			),
			AvailableResources: &AvailableResources{PerHost: map[string]int64{"host-1": 0, "host-2": 1024}},
			ExpectedReservations: []*TestReservation{
				// After rollback, should preserve the scheduled reservations (especially with VMs)
				// and remove unscheduled ones first
				{CommitmentID: "commitment-1", Host: "host-1", Flavor: m1Small, ProjectID: "project-A", VMs: []string{"vm-priority"}},
				{CommitmentID: "commitment-1", Host: "host-2", Flavor: m1Small, ProjectID: "project-A"},
			},
			ExpectedAPIResponse: newAPIResponse("commitment commitment-1: not sufficient capacity"),
		},
		{
			Name:    "Watch timeout with custom config - triggers rollback with timeout error",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("ram_hana_1", "project-A", "uuid-timeout", "confirmed", 2),
			),
			// With 0ms timeout, the watch will timeout immediately before reservations become ready
			CustomConfig: func() *Config {
				cfg := DefaultConfig()
				cfg.ChangeAPIWatchReservationsTimeout = 0 * time.Millisecond
				cfg.ChangeAPIWatchReservationsPollInterval = 100 * time.Millisecond
				return &cfg
			}(),
			ExpectedReservations: []*TestReservation{}, // Rollback removes all reservations
			ExpectedAPIResponse:  newAPIResponse("timeout reached while processing commitment changes"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runCommitmentChangeTest(t, tc)
		})
	}
}

// runCommitmentChangeTest executes a single commitment change integration test case.
func runCommitmentChangeTest(t *testing.T, tc CommitmentChangeTestCase) {
	t.Helper()

	// Convert test types to actual types
	var vms []VM
	for _, testVM := range tc.VMs {
		vms = append(vms, testVM.ToVM())
	}

	var flavorInGroups []compute.FlavorInGroup
	for _, testFlavor := range tc.Flavors {
		flavorInGroups = append(flavorInGroups, testFlavor.ToFlavorInGroup())
	}

	// Use EnvInfoVersion if specified (non-zero), otherwise default to CommitmentRequest.InfoVersion
	envInfoVersion := tc.CommitmentRequest.InfoVersion
	if tc.EnvInfoVersion != 0 {
		envInfoVersion = tc.EnvInfoVersion
	}

	flavorGroups := TestFlavorGroup{
		infoVersion: envInfoVersion,
		flavors:     flavorInGroups,
	}.ToFlavorGroupsKnowledge()

	// Convert existing reservations with auto-numbering per commitment
	var existingReservations []*v1alpha1.Reservation
	numberCounters := make(map[string]int)
	for _, testRes := range tc.ExistingReservations {
		number := numberCounters[testRes.CommitmentID]
		numberCounters[testRes.CommitmentID]++
		existingReservations = append(existingReservations, testRes.toReservation(number))
	}

	// Create test environment with available resources and custom config if provided
	env := newCommitmentTestEnv(t, vms, nil, existingReservations, flavorGroups, tc.AvailableResources, tc.CustomConfig)
	defer env.Close()

	t.Log("Initial state:")
	env.LogStateSummary()

	// Call commitment change API
	reqJSON := buildRequestJSON(tc.CommitmentRequest)
	resp, respJSON, statusCode := env.CallChangeCommitmentsAPI(reqJSON)

	t.Log("After API call:")
	env.LogStateSummary()

	// Verify API response
	env.VerifyAPIResponse(tc.ExpectedAPIResponse, resp, respJSON, statusCode)

	// Verify reservations using content-based matching
	env.VerifyReservationsMatch(tc.ExpectedReservations)

	// Log final test result
	if t.Failed() {
		t.Log("❌ Test FAILED")
	} else {
		t.Log("✅ Test PASSED")
	}
}

// ============================================================================
// Test Types & Constants
// ============================================================================

const (
	defaultFlavorDiskGB          = 40
	flavorGroupsKnowledgeName    = "flavor-groups"
	knowledgeRecencyDuration     = 60 * time.Second
	defaultCommitmentExpiryYears = 1
)

type CommitmentChangeTestCase struct {
	Name                 string
	VMs                  []*TestVM
	Flavors              []*TestFlavor
	ExistingReservations []*TestReservation
	CommitmentRequest    CommitmentChangeRequest
	ExpectedReservations []*TestReservation
	ExpectedAPIResponse  APIResponseExpectation
	AvailableResources   *AvailableResources // If nil, all reservations accepted without checks
	EnvInfoVersion       int64               // Override InfoVersion for version mismatch tests
	CustomConfig         *Config             // Override default config for testing timeout behavior
}

// AvailableResources defines available memory per host (MB).
// Scheduler uses first-come-first-serve. CPU is ignored.
type AvailableResources struct {
	PerHost map[string]int64 // host -> available memory MB
}

type TestFlavorGroup struct {
	infoVersion int64
	flavors     []compute.FlavorInGroup
}

func (tfg TestFlavorGroup) ToFlavorGroupsKnowledge() FlavorGroupsKnowledge {
	groupMap := make(map[string][]compute.FlavorInGroup)

	for _, flavor := range tfg.flavors {
		groupName := flavor.ExtraSpecs["quota:hw_version"]
		if groupName == "" {
			panic("Flavor " + flavor.Name + " is missing quota:hw_version in extra specs")
		}
		groupMap[groupName] = append(groupMap[groupName], flavor)
	}

	var groups []compute.FlavorGroupFeature
	for groupName, groupFlavors := range groupMap {
		if len(groupFlavors) == 0 {
			continue
		}

		// Sort descending: required by reservation manager's flavor selection
		sort.Slice(groupFlavors, func(i, j int) bool {
			return groupFlavors[i].MemoryMB > groupFlavors[j].MemoryMB
		})

		smallest := groupFlavors[len(groupFlavors)-1]
		largest := groupFlavors[0]

		groups = append(groups, compute.FlavorGroupFeature{
			Name:           groupName,
			Flavors:        groupFlavors,
			SmallestFlavor: smallest,
			LargestFlavor:  largest,
		})
	}

	return FlavorGroupsKnowledge{
		InfoVersion: tfg.infoVersion,
		Groups:      groups,
	}
}

type FlavorGroupsKnowledge struct {
	InfoVersion int64
	Groups      []compute.FlavorGroupFeature
}

type CommitmentChangeRequest struct {
	AZ          string
	DryRun      bool
	InfoVersion int64
	Commitments []TestCommitment
}

type TestCommitment struct {
	ResourceName   liquid.ResourceName
	ProjectID      string
	ConfirmationID string
	State          string
	Amount         uint64
}

type APIResponseExpectation struct {
	StatusCode             int
	RejectReasonSubstrings []string
	RetryAtPresent         bool
}

type ReservationVerification struct {
	Host        string
	Allocations map[string]string
}

type VM struct {
	UUID              string
	FlavorName        string
	ProjectID         string
	CurrentHypervisor string
	AvailabilityZone  string
	Resources         map[string]int64
	FlavorExtraSpecs  map[string]string
}

type TestFlavor struct {
	Name     string
	Group    string
	MemoryMB int64
	VCPUs    int64
	DiskGB   uint64
}

func (f *TestFlavor) ToFlavorInGroup() compute.FlavorInGroup {
	diskGB := f.DiskGB
	if diskGB == 0 {
		diskGB = defaultFlavorDiskGB
	}
	return compute.FlavorInGroup{
		Name:     f.Name,
		MemoryMB: uint64(f.MemoryMB), //nolint:gosec // test values are always positive
		VCPUs:    uint64(f.VCPUs),    //nolint:gosec // test values are always positive
		DiskGB:   diskGB,
		ExtraSpecs: map[string]string{
			"quota:hw_version": f.Group,
		},
	}
}

type TestVM struct {
	UUID      string
	Flavor    *TestFlavor
	ProjectID string
	Host      string
	AZ        string
}

func (vm *TestVM) ToVM() VM {
	return VM{
		UUID:              vm.UUID,
		FlavorName:        vm.Flavor.Name,
		ProjectID:         vm.ProjectID,
		CurrentHypervisor: vm.Host,
		AvailabilityZone:  vm.AZ,
		Resources: map[string]int64{
			"memory": vm.Flavor.MemoryMB,
			"vcpus":  vm.Flavor.VCPUs,
		},
		FlavorExtraSpecs: map[string]string{
			"quota:hw_version": vm.Flavor.Group,
		},
	}
}

type TestReservation struct {
	CommitmentID string
	Host         string // Empty = any host accepted in matching
	Flavor       *TestFlavor
	ProjectID    string
	VMs          []string // VM UUIDs
	MemoryMB     int64    // If 0, uses Flavor.MemoryMB; else custom size
	AZ           string
}

func (tr *TestReservation) toReservation(number int) *v1alpha1.Reservation {
	name := fmt.Sprintf("commitment-%s-%d", tr.CommitmentID, number)

	memoryMB := tr.MemoryMB
	if memoryMB == 0 {
		memoryMB = tr.Flavor.MemoryMB
	}

	specAllocations := make(map[string]v1alpha1.CommittedResourceAllocation)
	statusAllocations := make(map[string]string)
	for _, vmUUID := range tr.VMs {
		specAllocations[vmUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: metav1.Now(),
			Resources: map[hv1.ResourceName]resource.Quantity{
				"memory": resource.MustParse(strconv.FormatInt(memoryMB, 10) + "Mi"),
				"cpu":    resource.MustParse(strconv.FormatInt(tr.Flavor.VCPUs, 10)),
			},
		}
		statusAllocations[vmUUID] = tr.Host
	}

	spec := v1alpha1.ReservationSpec{
		Type:       v1alpha1.ReservationTypeCommittedResource,
		TargetHost: tr.Host,
		Resources: map[hv1.ResourceName]resource.Quantity{
			"memory": resource.MustParse(strconv.FormatInt(memoryMB, 10) + "Mi"),
			"cpu":    resource.MustParse(strconv.FormatInt(tr.Flavor.VCPUs, 10)),
		},
		CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
			CommitmentUUID: tr.CommitmentID,
			ProjectID:      tr.ProjectID,
			ResourceName:   tr.Flavor.Name,
			ResourceGroup:  tr.Flavor.Group,
			Allocations:    specAllocations,
		},
	}

	if tr.AZ != "" {
		spec.AvailabilityZone = tr.AZ
	}

	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
			},
		},
		Spec: spec,
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			Host: tr.Host,
			CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
				Allocations: statusAllocations,
			},
		},
	}
}

// ============================================================================
// Test Environment
// ============================================================================

type CommitmentTestEnv struct {
	T                  *testing.T
	Scheme             *runtime.Scheme
	K8sClient          client.Client
	VMSource           *MockVMSource
	FlavorGroups       FlavorGroupsKnowledge
	HTTPServer         *httptest.Server
	API                *HTTPAPI
	availableResources map[string]int64 // host -> available memory MB
	processedReserv    map[string]bool  // track processed reservations
	mu                 sync.Mutex       // protects availableResources and processedReserv
}

// FakeReservationController simulates synchronous reservation controller.
type FakeReservationController struct {
	env *CommitmentTestEnv
}

func (c *FakeReservationController) OnReservationCreated(res *v1alpha1.Reservation) {
	c.env.processNewReservation(res)
}

func (c *FakeReservationController) OnReservationDeleted(res *v1alpha1.Reservation) {
	c.env.mu.Lock()
	defer c.env.mu.Unlock()

	// Return memory when Delete() is called directly (before deletion timestamp is set)
	if c.env.availableResources != nil && res.Status.Host != "" {
		memoryQuantity := res.Spec.Resources["memory"]
		memoryBytes := memoryQuantity.Value()
		memoryMB := memoryBytes / (1024 * 1024)

		if _, exists := c.env.availableResources[res.Status.Host]; exists {
			c.env.availableResources[res.Status.Host] += memoryMB
			c.env.T.Logf("↩ Returned %d MB to %s (now %d MB available) via OnReservationDeleted for %s",
				memoryMB, res.Status.Host, c.env.availableResources[res.Status.Host], res.Name)
		}
	}

	// Clear tracking so recreated reservations with same name are processed
	delete(c.env.processedReserv, res.Name)
}

// operationInterceptorClient routes reservation events to FakeReservationController.
type operationInterceptorClient struct {
	client.Client
	controller *FakeReservationController
}

func (d *operationInterceptorClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	err := d.Client.Create(ctx, obj, opts...)
	if err != nil {
		return err
	}

	if res, ok := obj.(*v1alpha1.Reservation); ok {
		d.controller.OnReservationCreated(res)
	}

	return nil
}

func (d *operationInterceptorClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if res, ok := obj.(*v1alpha1.Reservation); ok {
		d.controller.OnReservationDeleted(res)
	}

	return d.Client.Delete(ctx, obj, opts...)
}

func (env *CommitmentTestEnv) Close() {
	if env.HTTPServer != nil {
		env.HTTPServer.Close()
	}
}

func newCommitmentTestEnv(
	t *testing.T,
	vms []VM,
	hypervisors []*hv1.Hypervisor,
	reservations []*v1alpha1.Reservation,
	flavorGroups FlavorGroupsKnowledge,
	resources *AvailableResources,
	customConfig *Config,
) *CommitmentTestEnv {

	t.Helper()

	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	objects := make([]client.Object, 0, len(hypervisors)+len(reservations))
	for _, hv := range hypervisors {
		objects = append(objects, hv)
	}
	for _, res := range reservations {
		objects = append(objects, res)
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hv1 scheme: %v", err)
	}

	// InfoVersion of -1 skips Knowledge CRD creation (tests "not ready" scenario)
	if flavorGroups.InfoVersion != -1 {
		knowledgeCRD := createKnowledgeCRD(flavorGroups)
		objects = append(objects, knowledgeCRD)
	}

	baseK8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		WithStatusSubresource(&v1alpha1.Knowledge{}).
		WithIndex(&v1alpha1.Reservation{}, "spec.type", func(obj client.Object) []string {
			res := obj.(*v1alpha1.Reservation)
			return []string{string(res.Spec.Type)}
		}).
		Build()

	var availableResources map[string]int64
	if resources != nil && resources.PerHost != nil {
		availableResources = make(map[string]int64)
		for host, memMB := range resources.PerHost {
			availableResources[host] = memMB
		}
	}

	env := &CommitmentTestEnv{
		T:                  t,
		Scheme:             scheme,
		K8sClient:          nil, // Will be set below
		VMSource:           NewMockVMSource(vms),
		FlavorGroups:       flavorGroups,
		HTTPServer:         nil, // Will be set below
		API:                nil, // Will be set below
		availableResources: availableResources,
		processedReserv:    make(map[string]bool),
	}

	controller := &FakeReservationController{env: env}
	wrappedClient := &operationInterceptorClient{
		Client:     baseK8sClient,
		controller: controller,
	}
	env.K8sClient = wrappedClient

	// Use custom config if provided, otherwise use default
	var api *HTTPAPI
	if customConfig != nil {
		api = NewAPIWithConfig(wrappedClient, *customConfig)
	} else {
		api = NewAPI(wrappedClient)
	}
	mux := http.NewServeMux()
	registry := prometheus.NewRegistry()
	api.Init(mux, registry)
	httpServer := httptest.NewServer(mux)

	env.HTTPServer = httpServer
	env.API = api

	return env
}

// ============================================================================
// Environment Helper Methods
// ============================================================================

// ListVMs returns all VMs from the VMSource.
func (env *CommitmentTestEnv) ListVMs() []VM {
	vms, err := env.VMSource.ListVMs(context.Background())
	if err != nil {
		env.T.Fatalf("Failed to list VMs: %v", err)
	}
	return vms
}

// ListReservations returns all reservations.
func (env *CommitmentTestEnv) ListReservations() []v1alpha1.Reservation {
	var list v1alpha1.ReservationList
	if err := env.K8sClient.List(context.Background(), &list); err != nil {
		env.T.Fatalf("Failed to list reservations: %v", err)
	}
	return list.Items
}

// ListHypervisors returns all hypervisors.
func (env *CommitmentTestEnv) ListHypervisors() []hv1.Hypervisor {
	var list hv1.HypervisorList
	if err := env.K8sClient.List(context.Background(), &list); err != nil {
		env.T.Fatalf("Failed to list hypervisors: %v", err)
	}
	return list.Items
}

// LogStateSummary logs a summary of the current state.
func (env *CommitmentTestEnv) LogStateSummary() {
	env.T.Helper()

	hypervisors := env.ListHypervisors()
	vms := env.ListVMs()
	reservations := env.ListReservations()

	env.T.Log("=== State Summary ===")
	env.T.Logf("Hypervisors: %d", len(hypervisors))
	env.T.Logf("VMs: %d", len(vms))
	env.T.Logf("Reservations: %d", len(reservations))

	for _, res := range reservations {
		allocCount := 0
		if res.Status.CommittedResourceReservation != nil {
			allocCount = len(res.Status.CommittedResourceReservation.Allocations)
		}
		env.T.Logf("  - %s (host: %s, allocations: %d)", res.Name, res.Status.Host, allocCount)
	}
	env.T.Log("=====================")
}

// CallChangeCommitmentsAPI calls the change commitments API endpoint with JSON.
// It uses a hybrid approach: fast polling during API execution + synchronous final pass.
func (env *CommitmentTestEnv) CallChangeCommitmentsAPI(reqJSON string) (resp liquid.CommitmentChangeResponse, respJSON string, statusCode int) {
	env.T.Helper()

	// Start fast polling in background to handle reservations during API execution
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(5 * time.Millisecond) // Very fast - 5ms
		defer ticker.Stop()
		defer close(done)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				env.processReservations()
			}
		}
	}()

	// Make HTTP request
	url := env.HTTPServer.URL + "/v1/commitments/change-commitments"
	httpResp, err := http.Post(url, "application/json", bytes.NewReader([]byte(reqJSON))) //nolint:gosec,noctx // test server URL, not user input
	if err != nil {
		cancel()
		<-done
		env.T.Fatalf("Failed to make HTTP request: %v", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		cancel()
		<-done
		env.T.Fatalf("Failed to read response body: %v", err)
	}

	respJSON = string(respBytes)

	// Parse response - only for 200 OK responses
	// Non-200 responses (like 409 Conflict for version mismatch) use plain text via http.Error()
	if httpResp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			cancel()
			<-done
			env.T.Fatalf("Failed to unmarshal response: %v", err)
		}
	}

	// Stop background polling
	cancel()
	<-done

	// Final synchronous pass to ensure all reservations are processed
	// This eliminates any race conditions
	env.processReservations()

	statusCode = httpResp.StatusCode
	return resp, respJSON, statusCode
}

// processReservations handles all reservation lifecycle events synchronously.
// This includes marking reservations as Ready/Failed and removing finalizers from deleted reservations.
func (env *CommitmentTestEnv) processReservations() {
	ctx := context.Background()
	reservations := env.ListReservations()

	for _, res := range reservations {
		// Handle deletion - return memory to host and remove finalizers
		if !res.DeletionTimestamp.IsZero() {
			env.T.Logf("Processing deletion for reservation %s (host: %s)", res.Name, res.Status.Host)

			env.mu.Lock()
			// Return memory to host if resource tracking is enabled
			if env.availableResources != nil {
				env.T.Logf("Resource tracking enabled, returning memory for %s", res.Name)
				memoryQuantity := res.Spec.Resources["memory"]
				memoryBytes := memoryQuantity.Value()
				memoryMB := memoryBytes / (1024 * 1024)

				env.T.Logf("Reservation %s has host=%s, memory=%d MB", res.Name, res.Status.Host, memoryMB)

				// Check if host exists in our tracking
				if _, exists := env.availableResources[res.Status.Host]; !exists {
					env.mu.Unlock()
					env.T.Fatalf("Host %s not found in available resources for reservation %s - this indicates an inconsistency",
						res.Status.Host, res.Name)
				}

				// Return memory to host
				env.availableResources[res.Status.Host] += memoryMB
				env.T.Logf("↩ Returned %d MB to %s (now %d MB available) from deleted reservation %s",
					memoryMB, res.Status.Host, env.availableResources[res.Status.Host], res.Name)
			} else {
				env.T.Logf("Resource tracking NOT enabled for %s", res.Name)
			}

			// Clear tracking so recreated reservations with same name are processed
			delete(env.processedReserv, res.Name)
			env.mu.Unlock()

			// Remove finalizers to allow deletion
			if len(res.Finalizers) > 0 {
				res.Finalizers = []string{}
				if err := env.K8sClient.Update(ctx, &res); err != nil {
					// Ignore errors - might be already deleted
					continue
				}
			}
			continue
		}

		// Skip if already processed (has a condition set)
		if env.hasCondition(&res) {
			continue
		}

		env.mu.Lock()
		alreadyProcessed := env.processedReserv[res.Name]
		env.mu.Unlock()

		// Skip if already tracked as processed
		if alreadyProcessed {
			continue
		}

		// Process new reservation with resource-based scheduling
		env.processNewReservation(&res)
	}
}

// hasCondition checks if a reservation has any Ready condition set.
func (env *CommitmentTestEnv) hasCondition(res *v1alpha1.Reservation) bool {
	for _, cond := range res.Status.Conditions {
		if cond.Type == v1alpha1.ReservationConditionReady {
			return true
		}
	}
	return false
}

// processNewReservation implements first-come-first-serve scheduling based on available resources.
// It tries to find a host with enough memory capacity and assigns the reservation to that host.
func (env *CommitmentTestEnv) processNewReservation(res *v1alpha1.Reservation) {
	env.mu.Lock()
	defer env.mu.Unlock()

	env.processedReserv[res.Name] = true

	if res.Spec.CommittedResourceReservation == nil || res.Spec.CommittedResourceReservation.ResourceGroup == "" || res.Spec.Resources == nil || res.Spec.Resources["memory"] == (resource.Quantity{}) {
		env.markReservationFailedStatus(res, "invalid reservation spec")
		env.T.Logf("✗ Invalid reservation spec for %s: marking as failed (resource group: %s, resources: %v)", res.Name, res.Spec.CommittedResourceReservation.ResourceGroup, res.Spec.Resources)
		return
	}

	// If no available resources configured, accept all reservations without host assignment
	if env.availableResources == nil {
		env.T.Logf("✓ Scheduled reservation %s - no resource tracking, simply accept", res.Name)
		env.markReservationSchedulerProcessedStatus(res, "some-host")
		return
	}

	// Get required memory from reservation spec
	memoryQuantity := res.Spec.Resources["memory"]
	memoryBytes := memoryQuantity.Value()
	memoryMB := memoryBytes / (1024 * 1024)

	// First-come-first-serve: find first host with enough capacity
	// Sort hosts to ensure deterministic behavior (Go map iteration is random)
	hosts := make([]string, 0, len(env.availableResources))
	for host := range env.availableResources {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)

	var selectedHost string
	for _, host := range hosts {
		if env.availableResources[host] >= memoryMB {
			selectedHost = host
			break
		}
	}

	if selectedHost != "" {
		// SUCCESS: Schedule on this host
		env.availableResources[selectedHost] -= memoryMB

		// Update reservation with selected host
		ctx := context.Background()

		// Update spec (TargetHost)
		res.Spec.TargetHost = selectedHost
		if err := env.K8sClient.Update(ctx, res); err != nil {
			env.T.Logf("Warning: Failed to update reservation spec: %v", err)
		}

		// Update status (Host) - requires Status().Update
		res.Status.Host = selectedHost
		if err := env.K8sClient.Status().Update(ctx, res); err != nil {
			env.T.Logf("Warning: Failed to update reservation status host: %v", err)
		}

		env.markReservationSchedulerProcessedStatus(res, selectedHost)
		env.T.Logf("✓ Scheduled reservation %s on %s (%d MB used, %d MB remaining)",
			res.Name, selectedHost, memoryMB, env.availableResources[selectedHost])
	} else {
		env.markReservationSchedulerProcessedStatus(res, "")
		env.T.Logf("✗ Failed to schedule reservation %s (needs %d MB, no host has capacity)",
			res.Name, memoryMB)
	}
}

// markReservationSchedulerProcessedStatus updates a reservation to have Ready=True status (scheduling can be succeeded or not - depending on host status)
func (env *CommitmentTestEnv) markReservationSchedulerProcessedStatus(res *v1alpha1.Reservation, host string) {
	ctx := context.Background()

	// Update spec first
	res.Spec.TargetHost = host
	if err := env.K8sClient.Update(ctx, res); err != nil {
		env.T.Logf("Warning: Failed to update reservation spec: %v", err)
		return
	}

	// Then update status
	res.Status.Host = host
	res.Status.Conditions = []metav1.Condition{
		{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ReservationActive",
			Message:            "Reservation is ready (set by test controller)",
			LastTransitionTime: metav1.Now(),
		},
	}
	if err := env.K8sClient.Status().Update(ctx, res); err != nil {
		env.T.Logf("Warning: Failed to update reservation status: %v", err)
	}
}

// markReservationFailedStatus updates a reservation to have Ready=False status
func (env *CommitmentTestEnv) markReservationFailedStatus(res *v1alpha1.Reservation, reason string) {
	res.Status.Conditions = []metav1.Condition{
		{
			Type:               v1alpha1.ReservationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Reservation invalid",
			Message:            reason,
			LastTransitionTime: metav1.Now(),
		},
	}

	if err := env.K8sClient.Status().Update(context.Background(), res); err != nil {
		// Ignore errors - might be deleted during update
		return
	}
}

// VerifyAPIResponse verifies the API response matches expectations.
// For rejection reasons, it checks if ALL expected substrings are present in the actual rejection reason.
func (env *CommitmentTestEnv) VerifyAPIResponse(expected APIResponseExpectation, actual liquid.CommitmentChangeResponse, respJSON string, statusCode int) {
	env.T.Helper()

	if statusCode != expected.StatusCode {
		env.T.Errorf("Expected status code %d, got %d", expected.StatusCode, statusCode)
	}

	if len(expected.RejectReasonSubstrings) > 0 {
		if actual.RejectionReason == "" {
			env.T.Errorf("Expected rejection reason containing substrings %v, got none", expected.RejectReasonSubstrings)
		} else {
			// Check that ALL expected substrings are present
			for _, substring := range expected.RejectReasonSubstrings {
				if !strings.Contains(actual.RejectionReason, substring) {
					env.T.Errorf("Expected rejection reason to contain %q, but got %q", substring, actual.RejectionReason)
				}
			}
		}
	} else {
		if actual.RejectionReason != "" {
			env.T.Errorf("Expected no rejection reason, got %q", actual.RejectionReason)
		}
	}

	// Check RetryAt field presence in JSON (avoids dealing with option.Option type)
	retryAtPresent := strings.Contains(respJSON, `"retryAt"`)
	if expected.RetryAtPresent {
		if !retryAtPresent {
			env.T.Error("Expected retryAt field to be present in JSON response, but it was not found")
		}
	} else {
		if retryAtPresent {
			env.T.Error("Expected retryAt field to be absent from JSON response, but it was found")
		}
	}
}

// VerifyReservationsMatch verifies that actual reservations match expected reservations by content.
func (env *CommitmentTestEnv) VerifyReservationsMatch(expected []*TestReservation) {
	env.T.Helper()

	actualReservations := env.ListReservations()

	// Make copies of both lists so we can remove matched items
	expectedCopy := make([]*TestReservation, len(expected))
	copy(expectedCopy, expected)

	actualCopy := make([]v1alpha1.Reservation, len(actualReservations))
	copy(actualCopy, actualReservations)

	// Track unmatched items for detailed reporting
	var unmatchedExpected []*TestReservation
	var unmatchedActual []v1alpha1.Reservation

	// Greedy matching: while there are expected items, find matches and remove
	for len(expectedCopy) > 0 {
		exp := expectedCopy[0]
		found := false

		// Find first actual that matches this expected
		for i, actual := range actualCopy {
			if env.reservationMatches(exp, &actual) {
				expectedCopy = expectedCopy[1:]
				actualCopy = append(actualCopy[:i], actualCopy[i+1:]...)
				found = true
				break
			}
		}

		if !found {
			unmatchedExpected = append(unmatchedExpected, exp)
			expectedCopy = expectedCopy[1:]
		}
	}

	unmatchedActual = actualCopy

	// If there are any mismatches, print detailed comparison
	if len(unmatchedExpected) > 0 || len(unmatchedActual) > 0 {
		env.T.Error("❌ Reservation mismatch detected!")
		env.T.Log("")
		env.T.Log("═══════════════════════════════════════════════════════════════")
		env.T.Log("EXPECTED RESERVATIONS:")
		env.T.Log("═══════════════════════════════════════════════════════════════")
		env.printExpectedReservations(expected, unmatchedExpected)

		env.T.Log("")
		env.T.Log("═══════════════════════════════════════════════════════════════")
		env.T.Log("ACTUAL RESERVATIONS:")
		env.T.Log("═══════════════════════════════════════════════════════════════")
		env.printActualReservations(actualReservations, unmatchedActual)

		env.T.Log("")
		env.T.Log("═══════════════════════════════════════════════════════════════")
		env.T.Log("DIFF SUMMARY:")
		env.T.Log("═══════════════════════════════════════════════════════════════")
		env.printDiffSummary(unmatchedExpected, unmatchedActual)
		env.T.Log("═══════════════════════════════════════════════════════════════")
	}
}

// String returns a compact string representation of a TestReservation.
func (tr *TestReservation) String() string {
	flavorName := ""
	flavorGroup := ""
	if tr.Flavor != nil {
		flavorName = tr.Flavor.Name
		flavorGroup = tr.Flavor.Group
	}

	host := tr.Host
	if host == "" {
		host = "<any>"
	}

	az := tr.AZ
	if az == "" {
		az = "<any>"
	}

	vmInfo := ""
	if len(tr.VMs) > 0 {
		vmInfo = fmt.Sprintf(" VMs=%v", tr.VMs)
	}

	return fmt.Sprintf("%s/%s/%s(%s)/%s/az=%s%s", tr.CommitmentID, tr.ProjectID, flavorName, flavorGroup, host, az, vmInfo)
}

// compactReservationString returns a compact string representation of an actual Reservation.
func compactReservationString(res *v1alpha1.Reservation) string {
	commitmentID := "<none>"
	projectID := "<none>"
	flavorName := "<none>"
	flavorGroup := "<none>"
	vmCount := 0

	if res.Spec.CommittedResourceReservation != nil {
		commitmentID = res.Spec.CommittedResourceReservation.CommitmentUUID
		projectID = res.Spec.CommittedResourceReservation.ProjectID
		flavorName = res.Spec.CommittedResourceReservation.ResourceName
		flavorGroup = res.Spec.CommittedResourceReservation.ResourceGroup
		if res.Status.CommittedResourceReservation != nil {
			vmCount = len(res.Status.CommittedResourceReservation.Allocations)
		}
	}

	host := res.Status.Host
	if host == "" {
		host = "<unscheduled>"
	}

	az := res.Spec.AvailabilityZone
	if az == "" {
		az = "<none>"
	}

	vmInfo := ""
	if vmCount > 0 {
		vmInfo = fmt.Sprintf(" VMs=%d", vmCount)
	}

	return fmt.Sprintf("%s/%s/%s(%s)/%s/az=%s%s", commitmentID, projectID, flavorName, flavorGroup, host, az, vmInfo)
}

// printExpectedReservations prints all expected reservations with markers for unmatched ones.
func (env *CommitmentTestEnv) printExpectedReservations(all, unmatched []*TestReservation) {
	env.T.Helper()

	unmatchedMap := make(map[*TestReservation]bool)
	for _, res := range unmatched {
		unmatchedMap[res] = true
	}

	if len(all) == 0 {
		env.T.Log("  (none)")
		return
	}

	for i, res := range all {
		marker := "✓"
		if unmatchedMap[res] {
			marker = "✗"
		}
		env.T.Logf("  %s [%d] %s", marker, i+1, res.String())
	}

	env.T.Logf("  Total: %d (%d matched, %d missing)",
		len(all), len(all)-len(unmatched), len(unmatched))
}

// printActualReservations prints all actual reservations with markers for unmatched ones.
func (env *CommitmentTestEnv) printActualReservations(all, unmatched []v1alpha1.Reservation) {
	env.T.Helper()

	unmatchedMap := make(map[string]bool)
	for _, res := range unmatched {
		unmatchedMap[res.Name] = true
	}

	if len(all) == 0 {
		env.T.Log("  (none)")
		return
	}

	for i, res := range all {
		marker := "✓"
		if unmatchedMap[res.Name] {
			marker = "⊕"
		}
		env.T.Logf("  %s [%d] %s", marker, i+1, compactReservationString(&res))
	}

	env.T.Logf("  Total: %d (%d matched, %d unexpected)",
		len(all), len(all)-len(unmatched), len(unmatched))
}

// printDiffSummary prints a summary of differences between expected and actual.
func (env *CommitmentTestEnv) printDiffSummary(unmatchedExpected []*TestReservation, unmatchedActual []v1alpha1.Reservation) {
	env.T.Helper()

	if len(unmatchedExpected) > 0 {
		env.T.Logf("  MISSING (%d expected, not found):", len(unmatchedExpected))
		for _, res := range unmatchedExpected {
			env.T.Logf("    • %s", res.String())
		}
	}

	if len(unmatchedActual) > 0 {
		env.T.Logf("  UNEXPECTED (%d found, not expected):", len(unmatchedActual))
		for _, res := range unmatchedActual {
			env.T.Logf("    • %s", compactReservationString(&res))
		}
	}

	if len(unmatchedExpected) == 0 && len(unmatchedActual) == 0 {
		env.T.Log("  ✓ All match!")
	}
}

// reservationMatches checks if an actual reservation matches an expected one.
// All fields are checked comprehensively for complete validation.
func (env *CommitmentTestEnv) reservationMatches(expected *TestReservation, actual *v1alpha1.Reservation) bool {
	// Check CommitmentID (from reservation name prefix)
	if !strings.HasPrefix(actual.Name, "commitment-"+expected.CommitmentID+"-") {
		return false
	}

	// Check that CommittedResourceReservation spec exists
	if actual.Spec.CommittedResourceReservation == nil {
		return false
	}

	// Check CommitmentUUID in spec matches
	if actual.Spec.CommittedResourceReservation.CommitmentUUID != expected.CommitmentID {
		return false
	}

	// Check ProjectID
	if actual.Spec.CommittedResourceReservation.ProjectID != expected.ProjectID {
		return false
	}

	// Check ResourceName (flavor name)
	if expected.Flavor != nil {
		if actual.Spec.CommittedResourceReservation.ResourceName != expected.Flavor.Name {
			return false
		}
	}

	// Check ResourceGroup (flavor group)
	if expected.Flavor != nil {
		if actual.Spec.CommittedResourceReservation.ResourceGroup != expected.Flavor.Group {
			return false
		}
	}

	// Check Host (if specified in expected)
	if expected.Host != "" && actual.Status.Host != expected.Host {
		return false
	}

	// Check AZ (if specified in expected)
	if expected.AZ != "" && actual.Spec.AvailabilityZone != expected.AZ {
		return false
	}

	// Check Memory (use custom MemoryMB if non-zero, otherwise use flavor size)
	expectedMemoryMB := expected.MemoryMB
	if expectedMemoryMB == 0 && expected.Flavor != nil {
		expectedMemoryMB = expected.Flavor.MemoryMB
	}
	memoryQuantity := actual.Spec.Resources["memory"]
	actualMemoryBytes := memoryQuantity.Value()
	actualMemoryMB := actualMemoryBytes / (1024 * 1024)
	if actualMemoryMB != expectedMemoryMB {
		return false
	}

	// Check CPU (from flavor if available)
	if expected.Flavor != nil {
		cpuQuantity := actual.Spec.Resources["cpu"]
		actualCPU := cpuQuantity.Value()
		if actualCPU != expected.Flavor.VCPUs {
			return false
		}
	}

	// Check VM allocations (set comparison - order doesn't matter)
	if !env.vmAllocationsMatch(expected.VMs, actual) {
		return false
	}

	// Check reservation type
	if actual.Spec.Type != v1alpha1.ReservationTypeCommittedResource {
		return false
	}

	return true
}

// vmAllocationsMatch checks if VM allocations match (set comparison).
func (env *CommitmentTestEnv) vmAllocationsMatch(expectedVMs []string, actual *v1alpha1.Reservation) bool {
	if actual.Status.CommittedResourceReservation == nil {
		return len(expectedVMs) == 0
	}

	actualVMs := make(map[string]bool)
	for vmUUID := range actual.Status.CommittedResourceReservation.Allocations {
		actualVMs[vmUUID] = true
	}

	// Check counts match
	if len(expectedVMs) != len(actualVMs) {
		return false
	}

	// Check all expected VMs are in actual
	for _, vmUUID := range expectedVMs {
		if !actualVMs[vmUUID] {
			return false
		}
	}

	return true
}

// ============================================================================
// Mock VM Source
// ============================================================================

// MockVMSource implements VMSource for testing.
type MockVMSource struct {
	VMs []VM
}

// NewMockVMSource creates a new MockVMSource with the given VMs.
func NewMockVMSource(vms []VM) *MockVMSource {
	return &MockVMSource{VMs: vms}
}

// ListVMs returns the configured VMs.
func (s *MockVMSource) ListVMs(_ context.Context) ([]VM, error) {
	return s.VMs, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// newHypervisorWithAZ creates a Hypervisor CRD with the given parameters including availability zone.
func newHypervisorWithAZ(name string, cpuCap, memoryGi, cpuAlloc, memoryGiAlloc int, instances []hv1.Instance, traits []string, az string) *hv1.Hypervisor {
	labels := make(map[string]string)
	if az != "" {
		labels[corev1.LabelTopologyZone] = az
	}
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Status: hv1.HypervisorStatus{
			Capacity: map[hv1.ResourceName]resource.Quantity{
				"cpu":    resource.MustParse(strconv.Itoa(cpuCap)),
				"memory": resource.MustParse(strconv.Itoa(memoryGi) + "Gi"),
			},
			Allocation: map[hv1.ResourceName]resource.Quantity{
				"cpu":    resource.MustParse(strconv.Itoa(cpuAlloc)),
				"memory": resource.MustParse(strconv.Itoa(memoryGiAlloc) + "Gi"),
			},
			NumInstances: len(instances),
			Instances:    instances,
			Traits:       traits,
		},
	}
}

// createCommitment creates a TestCommitment for use in test cases.
// The az parameter is optional - if empty string, no AZ constraint is set.
func createCommitment(resourceName, projectID, confirmationID, state string, amount uint64, az ...string) TestCommitment {
	return TestCommitment{
		ResourceName:   liquid.ResourceName(resourceName),
		ProjectID:      projectID,
		ConfirmationID: confirmationID,
		State:          state,
		Amount:         amount,
	}
}

// newCommitmentRequest creates a CommitmentChangeRequest with the given commitments.
func newCommitmentRequest(az string, dryRun bool, infoVersion int64, commitments ...TestCommitment) CommitmentChangeRequest {
	return CommitmentChangeRequest{
		AZ:          az,
		DryRun:      dryRun,
		InfoVersion: infoVersion,
		Commitments: commitments,
	}
}

// newAPIResponse creates an APIResponseExpectation with 200 OK status.
func newAPIResponse(rejectReasonSubstrings ...string) APIResponseExpectation {
	return APIResponseExpectation{
		StatusCode:             200,
		RejectReasonSubstrings: rejectReasonSubstrings,
	}
}

// buildRequestJSON converts a test CommitmentChangeRequest to JSON string.
// Builds the nested JSON structure directly for simplicity.
func buildRequestJSON(req CommitmentChangeRequest) string {
	// Group commitments by project and resource for nested structure
	type projectResources map[liquid.ResourceName][]TestCommitment
	byProject := make(map[string]projectResources)

	for _, commit := range req.Commitments {
		if byProject[commit.ProjectID] == nil {
			byProject[commit.ProjectID] = make(projectResources)
		}
		byProject[commit.ProjectID][commit.ResourceName] = append(
			byProject[commit.ProjectID][commit.ResourceName],
			commit,
		)
	}

	// Build nested JSON structure
	var projectParts []string
	for projectID, resources := range byProject {
		var resourceParts []string
		for resourceName, commits := range resources {
			var commitParts []string
			for _, c := range commits {
				expiryTime := time.Now().Add(time.Duration(defaultCommitmentExpiryYears) * 365 * 24 * time.Hour)
				commitParts = append(commitParts, fmt.Sprintf(`{"uuid":"%s","newStatus":"%s","amount":%d,"expiresAt":"%s"}`,
					c.ConfirmationID, c.State, c.Amount, expiryTime.Format(time.RFC3339)))
			}
			resourceParts = append(resourceParts, fmt.Sprintf(`"%s":{"commitments":[%s]}`,
				resourceName, strings.Join(commitParts, ",")))
		}
		projectParts = append(projectParts, fmt.Sprintf(`"%s":{"byResource":{%s}}`,
			projectID, strings.Join(resourceParts, ",")))
	}

	return fmt.Sprintf(`{"az":"%s","dryRun":%t,"infoVersion":%d,"byProject":{%s}}`,
		req.AZ, req.DryRun, req.InfoVersion, strings.Join(projectParts, ","))
}

// createKnowledgeCRD creates a Knowledge CRD populated with flavor groups.
func createKnowledgeCRD(flavorGroups FlavorGroupsKnowledge) *v1alpha1.Knowledge {
	rawExt, err := v1alpha1.BoxFeatureList(flavorGroups.Groups)
	if err != nil {
		panic("Failed to box flavor groups: " + err.Error())
	}

	lastContentChange := time.Unix(flavorGroups.InfoVersion, 0)

	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: flavorGroupsKnowledgeName,
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Extractor: v1alpha1.KnowledgeExtractorSpec{
				Name: flavorGroupsKnowledgeName,
			},
			Recency: metav1.Duration{Duration: knowledgeRecencyDuration},
		},
		Status: v1alpha1.KnowledgeStatus{
			LastExtracted:     metav1.Time{Time: lastContentChange},
			LastContentChange: metav1.Time{Time: lastContentChange},
			Raw:               rawExt,
			RawLength:         len(flavorGroups.Groups),
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.KnowledgeConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             "KnowledgeReady",
					Message:            "Flavor groups knowledge is ready",
					LastTransitionTime: metav1.Time{Time: lastContentChange},
				},
			},
		},
	}
}
