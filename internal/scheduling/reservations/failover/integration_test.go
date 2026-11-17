// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// exclTrait marks hypervisors reserved for external customer domains.
const exclTrait = "CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE"

// passthroughGatherer is a nova.CandidateGatherer that leaves the request's
// preselected hosts unchanged (the tests seed their own eligible hosts).
type passthroughGatherer struct{}

func (passthroughGatherer) MutateWithAllCandidates(context.Context, *compute.PlacementRequest) error {
	return nil
}

// ============================================================================
// Integration Tests
// ============================================================================

// IntegrationTestCase defines a unified test case for all failover reservation integration tests.
type IntegrationTestCase struct {
	Name               string
	Hypervisors        []*hv1.Hypervisor
	Reservations       []*v1alpha1.Reservation
	VMs                []reservations.VM
	FlavorRequirements map[string]int

	// Verification options
	ExpectedMinRes      int                          // Minimum expected reservations after reconcile
	ExpectedMaxRes      int                          // Maximum expected reservations after reconcile (0 = no max check)
	VerifyVMReservation []string                     // VM UUIDs to verify have reservations
	VMsToRemove         map[string]map[string]string // reservationName -> vmUUID -> expectedHost (verify VM removed from reservation)

	// Domain-filter verification (used with DomainFilters).
	ExpectNoReservation       []string            // VM UUIDs that must NOT end up with any failover slot
	AllowedReservationHosts   map[string][]string // vmUUID -> hosts its slot may be on (slot host must be one of these)
	ForbiddenReservationHosts map[string][]string // vmUUID -> hosts its slot must never be on

	// Test behavior options
	ReconcileCount               int                 // Number of reconciles to run (default: 1)
	SkipFailureSimulation        bool                // Set to true to skip failure simulation tests (default: run simulation)
	UseTraitsFilter              bool                // Use traits filter pipeline instead of default
	DomainFilters                *domainFilterConfig // When set, enable the external-customer and domain-pinning filters
	ValidateExistingReservations bool                // When set, run per-reservation validation (scheduler path) after reconcile
}

func TestIntegration(t *testing.T) {
	testCases := []IntegrationTestCase{
		// =====================================================================
		// Basic Failover Reservation Tests
		// =====================================================================
		{
			Name: "2 VMs on 2 hosts, scheduler decides placement",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 8, 16, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 8, 16, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 8, 16, 0, 0, nil, nil),
				newHypervisor("host4", 8, 16, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
			},
			FlavorRequirements:  map[string]int{"m1.large": 1},
			ExpectedMinRes:      1,
			ExpectedMaxRes:      2,
			VerifyVMReservation: []string{"vm-1", "vm-2"},
		},
		{
			Name: "1 VM already has reservation, create 1 new",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 8, 16, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 8, 16, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 8, 16, 0, 0, nil, nil),
				newHypervisor("host4", 8, 16, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("existing-res-1", "host2", 8192, 4, map[string]string{"vm-1": "host1"}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 1},
			ExpectedMinRes:     2,
			ExpectedMaxRes:     2,
		},
		{
			Name: "VM with non-matching flavor, no reservations created",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 8, 16, 2, 4, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 8, 16, 0, 0, nil, nil),
				newHypervisor("host3", 8, 16, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.small", "project-A", "host1", 4096, 2),
			},
			FlavorRequirements: map[string]int{"m1.large": 1}, // m1.small not in requirements
			ExpectedMinRes:     0,
			ExpectedMaxRes:     0,
		},
		{
			Name: "Reuse existing reservation - VM3 can share reservation with VM1",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-3", Name: "vm-3", Active: true}}, nil),
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),
				newHypervisor("host5", 16, 32, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("existing-res-1", "host2", 8192, 4, map[string]string{"vm-1": "host1"}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
				newVM("vm-3", "m1.large", "project-A", "host3", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 1},
			ExpectedMinRes:     2,
			ExpectedMaxRes:     3,
		},

		// =====================================================================
		// Availability Zone Tests
		// =====================================================================
		{
			Name: "VMs in different AZs get reservations only in their own AZ",
			Hypervisors: []*hv1.Hypervisor{
				// AZ-A hosts
				newHypervisorWithAZ("host-a1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-a1", Name: "vm-a1", Active: true}}, nil, "az-a"),
				newHypervisorWithAZ("host-a2", 16, 32, 0, 0, nil, nil, "az-a"), // Empty host for failover in AZ-A
				// AZ-B hosts
				newHypervisorWithAZ("host-b1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-b1", Name: "vm-b1", Active: true}}, nil, "az-b"),
				newHypervisorWithAZ("host-b2", 16, 32, 0, 0, nil, nil, "az-b"), // Empty host for failover in AZ-B
			},
			VMs: []reservations.VM{
				newVMWithAZ("vm-a1", "m1.large", "project-A", "host-a1", 8192, 4, "az-a"),
				newVMWithAZ("vm-b1", "m1.large", "project-A", "host-b1", 8192, 4, "az-b"),
			},
			FlavorRequirements:  map[string]int{"m1.large": 1},
			ExpectedMinRes:      2, // Each VM gets a reservation in its own AZ
			ExpectedMaxRes:      2,
			VerifyVMReservation: []string{"vm-a1", "vm-b1"},
		},
		{
			Name: "VM in AZ-A cannot get reservation on AZ-B host (only AZ-B hosts available)",
			Hypervisors: []*hv1.Hypervisor{
				// AZ-A hosts - only one host with VM, no empty hosts for failover
				newHypervisorWithAZ("host-a1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-a1", Name: "vm-a1", Active: true}}, nil, "az-a"),
				// AZ-B hosts - empty hosts available but wrong AZ for vm-a1
				newHypervisorWithAZ("host-b1", 16, 32, 0, 0, nil, nil, "az-b"),
				newHypervisorWithAZ("host-b2", 16, 32, 0, 0, nil, nil, "az-b"),
			},
			VMs: []reservations.VM{
				newVMWithAZ("vm-a1", "m1.large", "project-A", "host-a1", 8192, 4, "az-a"),
			},
			FlavorRequirements:    map[string]int{}, // Empty - don't require failover for this test
			ExpectedMinRes:        0,                // No reservation can be created - no hosts in AZ-A
			ExpectedMaxRes:        0,
			SkipFailureSimulation: true, // Skip failure simulation since no reservations
		},
		{
			Name: "Multiple VMs in same AZ share reservations correctly",
			Hypervisors: []*hv1.Hypervisor{
				// AZ-A hosts
				newHypervisorWithAZ("host-a1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-a1", Name: "vm-a1", Active: true}}, nil, "az-a"),
				newHypervisorWithAZ("host-a2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-a2", Name: "vm-a2", Active: true}}, nil, "az-a"),
				newHypervisorWithAZ("host-a3", 16, 32, 0, 0, nil, nil, "az-a"), // Empty host for failover
				// AZ-B hosts
				newHypervisorWithAZ("host-b1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-b1", Name: "vm-b1", Active: true}}, nil, "az-b"),
				newHypervisorWithAZ("host-b2", 16, 32, 0, 0, nil, nil, "az-b"), // Empty host for failover
			},
			VMs: []reservations.VM{
				newVMWithAZ("vm-a1", "m1.large", "project-A", "host-a1", 8192, 4, "az-a"),
				newVMWithAZ("vm-a2", "m1.large", "project-A", "host-a2", 8192, 4, "az-a"),
				newVMWithAZ("vm-b1", "m1.large", "project-A", "host-b1", 8192, 4, "az-b"),
			},
			FlavorRequirements:  map[string]int{"m1.large": 1},
			ExpectedMinRes:      2, // VMs in AZ-A can share, VM in AZ-B gets its own
			ExpectedMaxRes:      3,
			VerifyVMReservation: []string{"vm-a1", "vm-a2", "vm-b1"},
		},

		// =====================================================================
		// Multi-Host Failure Tolerance Tests (n=2)
		// =====================================================================
		{
			Name: "4 VMs on 4 hosts with n=2 failure tolerance",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-3", Name: "vm-3", Active: true}}, nil),
				newHypervisor("host4", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-4", Name: "vm-4", Active: true}}, nil),
				newHypervisor("host5", 32, 64, 0, 0, nil, nil),
				newHypervisor("host6", 32, 64, 0, 0, nil, nil),
				newHypervisor("host7", 32, 64, 0, 0, nil, nil),
				newHypervisor("host8", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
				newVM("vm-3", "m1.large", "project-A", "host3", 8192, 4),
				newVM("vm-4", "m1.large", "project-A", "host4", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 2},
			ExpectedMinRes:     4,
			ExpectedMaxRes:     8,
		},
		{
			Name: "5 VMs on 5 hosts with n=2 failure tolerance",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-3", Name: "vm-3", Active: true}}, nil),
				newHypervisor("host4", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-4", Name: "vm-4", Active: true}}, nil),
				newHypervisor("host5", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-5", Name: "vm-5", Active: true}}, nil),
				newHypervisor("host6", 32, 64, 0, 0, nil, nil),
				newHypervisor("host7", 32, 64, 0, 0, nil, nil),
				newHypervisor("host8", 32, 64, 0, 0, nil, nil),
				newHypervisor("host9", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
				newVM("vm-3", "m1.large", "project-A", "host3", 8192, 4),
				newVM("vm-4", "m1.large", "project-A", "host4", 8192, 4),
				newVM("vm-5", "m1.large", "project-A", "host5", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 2},
			ExpectedMinRes:     5,
			ExpectedMaxRes:     10,
		},
		{
			Name: "5 hosts with existing reservations and n=2 failure tolerance",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 32, 64, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 32, 64, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 32, 64, 4, 8, []hv1.Instance{{ID: "vm-3", Name: "vm-3", Active: true}}, nil),
				newHypervisor("host4", 32, 64, 0, 0, nil, nil),
				newHypervisor("host5", 32, 64, 0, 0, nil, nil),
				newHypervisor("host6", 32, 64, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("existing-res-1", "host4", 8192, 4, map[string]string{"vm-1": "host1"}),
				newReservation("existing-res-2", "host5", 8192, 4, map[string]string{"vm-2": "host2"}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
				newVM("vm-3", "m1.large", "project-A", "host3", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 2},
			ExpectedMinRes:     4,
			ExpectedMaxRes:     6,
		},

		// =====================================================================
		// Incorrect Reservation Cleanup Tests
		// =====================================================================
		{
			Name: "VM deleted - remove from reservation allocations",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 16, 32, 0, 0, nil, nil),
				newHypervisor("host3", 16, 32, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("res-1", "host2", 8192, 4, map[string]string{
					"vm-1":       "host1",
					"vm-deleted": "host3", // This VM no longer exists
				}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 1},
			VMsToRemove: map[string]map[string]string{
				"res-1": {"vm-deleted": "host3"},
			},
			ReconcileCount:        2, // First removes incorrect, second creates new
			SkipFailureSimulation: false,
		},
		{
			Name: "VM moved to different hypervisor - remove from reservation allocations",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 0, 0, nil, nil),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("res-1", "host4", 8192, 4, map[string]string{
					"vm-1": "host1", // vm-1 was on host1, but now it's on host2
					"vm-2": "host3",
				}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host2", 8192, 4), // Moved from host1 to host2
				newVM("vm-2", "m1.large", "project-A", "host3", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 1},
			VMsToRemove: map[string]map[string]string{
				"res-1": {"vm-1": "host1"},
			},
			ReconcileCount:        2,
			SkipFailureSimulation: false,
		},
		{
			Name: "VM on same host as reservation - remove due to eligibility",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 0, 0, nil, nil),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("res-1", "host3", 8192, 4, map[string]string{
					"vm-1": "host1", // vm-1 was on host1, but now it's on host3 (same as reservation!)
					"vm-2": "host2",
				}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host3", 8192, 4), // Same as reservation!
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4),
			},
			FlavorRequirements: map[string]int{"m1.large": 1},
			VMsToRemove: map[string]map[string]string{
				"res-1": {"vm-1": "host1"},
			},
			ReconcileCount:        2,
			SkipFailureSimulation: false,
		},
		{
			Name: "Mixed scenario - deleted VM, moved VM, and valid VM",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-1", Name: "vm-1", Active: true}}, nil),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-2", Name: "vm-2", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 0, 0, nil, nil),
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				newReservation("res-1", "host3", 8192, 4, map[string]string{
					"vm-1":       "host1", // Valid
					"vm-2":       "host3", // Moved from host3 to host2
					"vm-deleted": "host4", // Deleted
				}),
			},
			VMs: []reservations.VM{
				newVM("vm-1", "m1.large", "project-A", "host1", 8192, 4),
				newVM("vm-2", "m1.large", "project-A", "host2", 8192, 4), // Moved from host3 to host2
			},
			FlavorRequirements: map[string]int{"m1.large": 1},
			VMsToRemove: map[string]map[string]string{
				"res-1": {"vm-2": "host3", "vm-deleted": "host4"},
			},
			ReconcileCount:        2,
			SkipFailureSimulation: false,
		},

		// =====================================================================
		// Traits Filter Tests
		// =====================================================================
		{
			Name: "HANA VM gets reservation on HANA host, regular VM on any host",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-hana-1", Name: "vm-hana-1", Active: true}}, []string{"CUSTOM_HANA"}),
				newHypervisor("host2", 16, 32, 0, 0, nil, []string{"CUSTOM_HANA"}),
				newHypervisor("host3", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-regular-1", Name: "vm-regular-1", Active: true}}, nil),
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithExtraSpecs("vm-hana-1", "m1.hana", "project-A", "host1", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "required"}),
				newVMWithExtraSpecs("vm-regular-1", "m1.large", "project-A", "host3", 8192, 4, nil),
			},
			FlavorRequirements: map[string]int{"m1.hana": 1, "m1.large": 1},
			ExpectedMinRes:     1,
			UseTraitsFilter:    true,
		},
		{
			Name: "VM with forbidden trait cannot use host with that trait",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-no-hana-1", Name: "vm-no-hana-1", Active: true}}, nil),
				newHypervisor("host2", 16, 32, 0, 0, nil, []string{"CUSTOM_HANA"}), // Has HANA trait
				newHypervisor("host3", 16, 32, 0, 0, nil, nil),                     // No HANA trait
			},
			VMs: []reservations.VM{
				newVMWithExtraSpecs("vm-no-hana-1", "m1.no-hana", "project-A", "host1", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "forbidden"}),
			},
			FlavorRequirements: map[string]int{"m1.no-hana": 1},
			ExpectedMinRes:     1,
			UseTraitsFilter:    true,
		},
		{
			Name: "Mixed required and forbidden traits - VMs placed on correct hosts",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-hana-1", Name: "vm-hana-1", Active: true}}, []string{"CUSTOM_HANA"}),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-no-hana-1", Name: "vm-no-hana-1", Active: true}}, nil),
				newHypervisor("host3", 16, 32, 0, 0, nil, []string{"CUSTOM_HANA"}), // HANA host for failover
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),                     // Non-HANA host for failover
			},
			VMs: []reservations.VM{
				newVMWithExtraSpecs("vm-hana-1", "m1.hana", "project-A", "host1", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "required"}),
				newVMWithExtraSpecs("vm-no-hana-1", "m1.no-hana", "project-A", "host2", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "forbidden"}),
			},
			FlavorRequirements: map[string]int{"m1.hana": 1, "m1.no-hana": 1},
			ExpectedMinRes:     2, // Each VM needs its own reservation on compatible host
			UseTraitsFilter:    true,
		},
		{
			Name: "Multiple HANA VMs share reservation on HANA host",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-hana-1", Name: "vm-hana-1", Active: true}}, []string{"CUSTOM_HANA"}),
				newHypervisor("host2", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-hana-2", Name: "vm-hana-2", Active: true}}, []string{"CUSTOM_HANA"}),
				newHypervisor("host3", 16, 32, 0, 0, nil, []string{"CUSTOM_HANA"}), // Empty HANA host for failover
				newHypervisor("host4", 16, 32, 0, 0, nil, nil),                     // Non-HANA host (not usable for HANA VMs)
			},
			VMs: []reservations.VM{
				newVMWithExtraSpecs("vm-hana-1", "m1.hana", "project-A", "host1", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "required"}),
				newVMWithExtraSpecs("vm-hana-2", "m1.hana", "project-A", "host2", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "required"}),
			},
			FlavorRequirements: map[string]int{"m1.hana": 1},
			ExpectedMinRes:     1, // Both HANA VMs can share reservation on host3
			UseTraitsFilter:    true,
		},
		{
			Name: "HANA VM uses kvm-hana-bin-packing pipeline (trait:CUSTOM_HANA_EXCLUSIVE_HOST=required)",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("host1", 16, 32, 4, 8, []hv1.Instance{{ID: "vm-hana-exclusive-1", Name: "vm-hana-exclusive-1", Active: true}}, []string{"CUSTOM_HANA_EXCLUSIVE_HOST"}),
				newHypervisor("host2", 16, 32, 0, 0, nil, []string{"CUSTOM_HANA_EXCLUSIVE_HOST"}),
			},
			VMs: []reservations.VM{
				newVMWithExtraSpecs("vm-hana-exclusive-1", "m1.hana", "project-A", "host1", 8192, 4,
					map[string]string{"trait:CUSTOM_HANA_EXCLUSIVE_HOST": "required"}),
			},
			FlavorRequirements:  map[string]int{"m1.hana": 1},
			ExpectedMinRes:      1,
			ExpectedMaxRes:      1,
			VerifyVMReservation: []string{"vm-hana-exclusive-1"},
			UseTraitsFilter:     true,
		},
		// =====================================================================
		// Domain-filter failover slot tests (external customer + domain pinning).
		// Each scenario appears twice: once with enough hosts in the VM's eligible
		// pool so a failover slot can be created and evacuation works (simulation
		// enabled), and once with too few hosts so no slot is possible (simulation
		// skipped). Hosts carry ample free capacity.
		// Trait CUSTOM_EXTERNAL_CUSTOMER_EXCLUSIVE is abbreviated via exclTrait.
		// =====================================================================

		// --- Scenario A: allow-listed, unpinned external customer ---
		{
			Name: "Domain filters: unpinned external customer with enough shared hosts gets slot and fails over",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-shared", Name: "vm-shared", Active: true}}, []string{exclTrait}),
				newHypervisor("excl-shared-2", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("excl-pinned-a", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-shared", "m1.large", "project-shared", "iaas-shared-cust", "excl-shared-1", 8192, 4),
			},
			FlavorRequirements:      map[string]int{"m1.large": 1},
			DomainFilters:           &domainFilterConfig{allowedDomains: []string{"iaas-pinned-a", "iaas-shared-*"}, domainHypervisors: map[string][]string{"iaas-pinned-a": {"excl-pinned-a"}}},
			AllowedReservationHosts: map[string][]string{"vm-shared": {"excl-shared-2"}},
			// Only the shared pool has 2 hosts, so failover works within it.
			SkipFailureSimulation: false,
		},
		{
			Name: "Domain filters: unpinned external customer with only one shared host gets no slot",
			Hypervisors: []*hv1.Hypervisor{
				// Single shared exclusive host, which is also where the VM runs.
				newHypervisor("excl-shared-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-shared-solo", Name: "vm-shared-solo", Active: true}}, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-shared-solo", "m1.large", "project-shared", "iaas-shared-cust", "excl-shared-1", 8192, 4),
			},
			FlavorRequirements:    map[string]int{"m1.large": 1},
			DomainFilters:         &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{}},
			ExpectNoReservation:   []string{"vm-shared-solo"},
			SkipFailureSimulation: true, // no slot possible → nothing to fail over
		},

		// --- Scenario B: pinned external customer ---
		{
			Name: "Domain filters: pinned external customer with two pinned hosts gets slot and fails over",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-pinned-a-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-pinned", Name: "vm-pinned", Active: true}}, []string{exclTrait}),
				newHypervisor("excl-pinned-a-2", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-pinned", "m1.large", "project-pinned", "iaas-pinned-a", "excl-pinned-a-1", 8192, 4),
			},
			FlavorRequirements:      map[string]int{"m1.large": 1},
			DomainFilters:           &domainFilterConfig{allowedDomains: []string{"iaas-pinned-a", "iaas-shared-*"}, domainHypervisors: map[string][]string{"iaas-pinned-a": {"excl-pinned-a-1", "excl-pinned-a-2"}}},
			AllowedReservationHosts: map[string][]string{"vm-pinned": {"excl-pinned-a-2"}},
			SkipFailureSimulation:   false,
		},
		{
			Name: "Domain filters: pinned external customer with a single pinned host gets no slot",
			Hypervisors: []*hv1.Hypervisor{
				// Only one pinned host, which is where the VM runs → no slot target.
				newHypervisor("excl-pinned-a-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-pinned-solo", Name: "vm-pinned-solo", Active: true}}, []string{exclTrait}),
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-pinned-solo", "m1.large", "project-pinned", "iaas-pinned-a", "excl-pinned-a-1", 8192, 4),
			},
			FlavorRequirements:        map[string]int{"m1.large": 1},
			DomainFilters:             &domainFilterConfig{allowedDomains: []string{"iaas-pinned-a", "iaas-shared-*"}, domainHypervisors: map[string][]string{"iaas-pinned-a": {"excl-pinned-a-1"}}},
			ExpectNoReservation:       []string{"vm-pinned-solo"},
			ForbiddenReservationHosts: map[string][]string{"vm-pinned-solo": {"excl-shared-1", "regular-1"}},
			SkipFailureSimulation:     true, // pinned pool has a single host → no slot
		},

		// --- Scenario C: regular tenant ---
		{
			Name: "Domain filters: regular tenant with two regular hosts gets slot and fails over",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("excl-pinned-a", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-corp", Name: "vm-corp", Active: true}}, nil),
				newHypervisor("regular-2", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-corp", "m1.large", "project-corp", "internal-corp", "regular-1", 8192, 4),
			},
			FlavorRequirements:        map[string]int{"m1.large": 1},
			DomainFilters:             &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{"iaas-pinned-a": {"excl-pinned-a"}}},
			AllowedReservationHosts:   map[string][]string{"vm-corp": {"regular-2"}},
			ForbiddenReservationHosts: map[string][]string{"vm-corp": {"excl-shared-1", "excl-pinned-a"}},
			SkipFailureSimulation:     false,
		},
		{
			Name: "Domain filters: regular tenant with a single regular host gets no slot",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				// Only one regular host, which is where the VM runs → no slot target.
				newHypervisor("regular-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-corp-solo", Name: "vm-corp-solo", Active: true}}, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-corp-solo", "m1.large", "project-corp", "internal-corp", "regular-1", 8192, 4),
			},
			FlavorRequirements:        map[string]int{"m1.large": 1},
			DomainFilters:             &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{}},
			ExpectNoReservation:       []string{"vm-corp-solo"},
			ForbiddenReservationHosts: map[string][]string{"vm-corp-solo": {"excl-shared-1"}},
			SkipFailureSimulation:     true, // single regular host → no slot
		},

		// --- Existing reservation invalidated by the domain filters ---
		{
			// A pre-existing slot allocates an allow-listed external-customer VM on
			// a non-exclusive (regular) host. Once the domain filters are active,
			// that host is no longer valid for the VM, so validation must delete the
			// reservation.
			Name: "Domain filters: existing slot on a forbidden host is invalidated",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-ext", Name: "vm-ext", Active: true}}, []string{exclTrait}),
				newHypervisor("excl-shared-2", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, nil, nil),
			},
			Reservations: []*v1alpha1.Reservation{
				// Slot on a regular host, allocated to an external customer VM.
				newReservation("stale-res", "regular-1", 8192, 4, map[string]string{"vm-ext": "excl-shared-1"}),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-ext", "m1.large", "project-ext", "iaas-shared-cust", "excl-shared-1", 8192, 4),
			},
			FlavorRequirements:           map[string]int{"m1.large": 1},
			DomainFilters:                &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{}},
			ValidateExistingReservations: true,
			// After validation the stale reservation (on the forbidden regular host)
			// must be gone, so the VM holds no slot. Recreation on a valid host
			// happens on a later reconcile cycle and is covered by the
			// "gets slot and fails over" case above.
			ExpectNoReservation:   []string{"vm-ext"},
			SkipFailureSimulation: true,
		},

		// --- Fail-closed / unresolved domain (never get an exclusive slot) ---
		{
			Name: "Domain filters: iaas domain absent from allow-list gets no slot",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("excl-shared-2", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-rogue", Name: "vm-rogue", Active: true}}, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-rogue", "m1.large", "project-rogue", "iaas-rogue", "regular-1", 8192, 4),
			},
			FlavorRequirements:    map[string]int{"m1.large": 1},
			DomainFilters:         &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{}},
			ExpectNoReservation:   []string{"vm-rogue"},
			SkipFailureSimulation: true, // blocked by allow-list → no slot
		},
		{
			Name: "Domain filters: unresolved (empty) domain with enough regular hosts fails over off exclusive hosts",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-unknown", Name: "vm-unknown", Active: true}}, nil),
				newHypervisor("regular-2", 32, 64, 0, 0, nil, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-unknown", "m1.large", "project-unknown", "", "regular-1", 8192, 4),
			},
			FlavorRequirements:        map[string]int{"m1.large": 1},
			DomainFilters:             &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{}},
			AllowedReservationHosts:   map[string][]string{"vm-unknown": {"regular-2"}},
			ForbiddenReservationHosts: map[string][]string{"vm-unknown": {"excl-shared-1"}},
			// Unknown domain is treated as a regular tenant: it gets a slot on a
			// regular host and fails over there, never onto the exclusive host.
			SkipFailureSimulation: false,
		},
		{
			Name: "Domain filters: unresolved (empty) domain with only one regular host gets no slot",
			Hypervisors: []*hv1.Hypervisor{
				newHypervisor("excl-shared-1", 32, 64, 0, 0, nil, []string{exclTrait}),
				newHypervisor("regular-1", 32, 64, 0, 0, []hv1.Instance{{ID: "vm-unknown-solo", Name: "vm-unknown-solo", Active: true}}, nil),
			},
			VMs: []reservations.VM{
				newVMWithDomain("vm-unknown-solo", "m1.large", "project-unknown", "", "regular-1", 8192, 4),
			},
			FlavorRequirements:        map[string]int{"m1.large": 1},
			DomainFilters:             &domainFilterConfig{allowedDomains: []string{"iaas-shared-*"}, domainHypervisors: map[string][]string{}},
			ExpectNoReservation:       []string{"vm-unknown-solo"},
			ForbiddenReservationHosts: map[string][]string{"vm-unknown-solo": {"excl-shared-1"}},
			SkipFailureSimulation:     true, // single regular host → no slot
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runIntegrationTest(t, tc)
		})
	}
}

// runIntegrationTest executes a single integration test case.
func runIntegrationTest(t *testing.T, tc IntegrationTestCase) {
	t.Helper()

	// Create test environment
	var env *IntegrationTestEnv
	switch {
	case tc.DomainFilters != nil:
		env = newIntegrationTestEnvWithOptions(t, tc.VMs, tc.Hypervisors, tc.Reservations, envOptions{domainFilters: tc.DomainFilters})
	case tc.UseTraitsFilter:
		env = newIntegrationTestEnvWithTraitsFilter(t, tc.VMs, tc.Hypervisors, tc.Reservations)
	default:
		env = newIntegrationTestEnv(t, tc.VMs, tc.Hypervisors, tc.Reservations)
	}
	defer env.Close()

	t.Log("Initial state:")
	env.LogStateSummary()

	// Determine number of reconciles (default: 1)
	reconcileCount := tc.ReconcileCount
	if reconcileCount == 0 {
		reconcileCount = 1
	}

	// Run reconciles
	for i := 1; i <= reconcileCount; i++ {
		if err := env.TriggerFailoverReconcile(tc.FlavorRequirements); err != nil {
			t.Logf("Reconcile %d returned error (may be expected): %v", i, err)
		}
	}

	// Optionally run per-reservation validation, which applies the domain filters
	// via the scheduler and deletes reservations that are no longer valid.
	if tc.ValidateExistingReservations {
		if err := env.TriggerReservationValidation(tc.FlavorRequirements); err != nil {
			t.Logf("Reservation validation returned error (may be expected): %v", err)
		}
	}

	t.Logf("State after reconcile")
	env.LogStateSummary()
	if len(tc.VMsToRemove) > 0 {
		for resName, vmHosts := range tc.VMsToRemove {
			for vmUUID, expectedHost := range vmHosts {
				env.VerifyVMRemovedFromReservation(resName, vmUUID, expectedHost)
			}
		}
	}

	// Verify reservation count
	if tc.ExpectedMaxRes > 0 {
		env.VerifyReservationCountInRange(tc.ExpectedMinRes, tc.ExpectedMaxRes)
	} else if tc.ExpectedMinRes > 0 {
		reservations := env.ListReservations()
		if len(reservations) < tc.ExpectedMinRes {
			t.Errorf("Expected at least %d reservation(s), got %d", tc.ExpectedMinRes, len(reservations))
		}
	}

	// Verify specific VMs have reservations
	for _, vmUUID := range tc.VerifyVMReservation {
		for _, vm := range tc.VMs {
			if vm.UUID == vmUUID {
				env.VerifyVMHasFailoverReservation(vmUUID, vm.CurrentHypervisor)
				break
			}
		}
	}

	// Domain-filter verification: slot presence and placement constraints.
	for _, vmUUID := range tc.ExpectNoReservation {
		if hosts := env.reservationHostsForVM(vmUUID); len(hosts) != 0 {
			t.Errorf("VM %s should have no failover slot, got hosts %v", vmUUID, hosts)
		}
	}
	for vmUUID, allowed := range tc.AllowedReservationHosts {
		hosts := env.reservationHostsForVM(vmUUID)
		if len(hosts) == 0 {
			t.Errorf("VM %s expected a failover slot, got none", vmUUID)
		}
		for _, h := range hosts {
			if !slices.Contains(allowed, h) {
				t.Errorf("VM %s slot host %s not in allowed set %v", vmUUID, h, allowed)
			}
		}
	}
	for vmUUID, forbidden := range tc.ForbiddenReservationHosts {
		for _, h := range env.reservationHostsForVM(vmUUID) {
			if slices.Contains(forbidden, h) {
				t.Errorf("VM %s slot placed on forbidden host %s", vmUUID, h)
			}
		}
	}

	// Verify all VMs have required reservations (skipped for domain-filter cases,
	// where some VMs are intentionally denied a slot).
	if tc.DomainFilters == nil {
		env.VerifyVMsHaveRequiredReservations(tc.FlavorRequirements)
	}

	// Run failure simulation unless skipped
	if !tc.SkipFailureSimulation {
		allHosts := make([]string, len(tc.Hypervisors))
		for i, hv := range tc.Hypervisors {
			allHosts[i] = hv.Name
		}
		env.VerifyEvacuationForAllFailureCombinations(tc.FlavorRequirements, allHosts, 8192, 4)
	}
}

// ============================================================================
// Test Environment
// ============================================================================

// IntegrationTestEnv provides a test environment with:
// - Fake k8s client for CRD operations (reservations, hypervisors)
// - Real HTTP server for NovaExternalScheduler endpoint
// - MockVMSource for listing VMs
type IntegrationTestEnv struct {
	T                *testing.T
	Scheme           *runtime.Scheme
	K8sClient        client.Client
	Server           *httptest.Server
	NovaController   *nova.FilterWeigherPipelineController
	VMSource         reservations.VMSource
	SchedulerBaseURL string
}

func (env *IntegrationTestEnv) Close() {
	env.Server.Close()
}

// ============================================================================
// Environment Helper Methods
// ============================================================================

// SendPlacementRequest sends a placement request to the scheduler and returns the response.
func (env *IntegrationTestEnv) SendPlacementRequest(req novaapi.ExternalSchedulerRequest) novaapi.ExternalSchedulerResponse {
	env.T.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		env.T.Fatalf("Failed to marshal request: %v", err)
	}

	resp, err := http.Post(env.SchedulerBaseURL+"/scheduler/nova/external", "application/json", bytes.NewReader(body))
	if err != nil {
		env.T.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		env.T.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var response novaapi.ExternalSchedulerResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		env.T.Fatalf("Failed to decode response: %v", err)
	}

	return response
}

// ListVMs returns all VMs from the VMSource.
func (env *IntegrationTestEnv) ListVMs() []reservations.VM {
	vms, err := env.VMSource.ListVMs(context.Background())
	if err != nil {
		env.T.Fatalf("Failed to list VMs: %v", err)
	}
	return vms
}

// reservationHostsForVM returns the reservation hosts that hold a failover slot
// allocation for the given VM.
func (env *IntegrationTestEnv) reservationHostsForVM(vmUUID string) []string {
	var hosts []string
	for _, res := range env.ListReservations() {
		if res.Spec.Type != v1alpha1.ReservationTypeFailover || res.Status.FailoverReservation == nil {
			continue
		}
		if _, ok := res.Status.FailoverReservation.Allocations[vmUUID]; ok {
			hosts = append(hosts, res.Status.Host)
		}
	}
	return hosts
}

// ListReservations returns all reservations.
func (env *IntegrationTestEnv) ListReservations() []v1alpha1.Reservation {
	var list v1alpha1.ReservationList
	if err := env.K8sClient.List(context.Background(), &list); err != nil {
		env.T.Fatalf("Failed to list reservations: %v", err)
	}
	return list.Items
}

// ListHypervisors returns all hypervisors.
func (env *IntegrationTestEnv) ListHypervisors() []hv1.Hypervisor {
	var list hv1.HypervisorList
	if err := env.K8sClient.List(context.Background(), &list); err != nil {
		env.T.Fatalf("Failed to list hypervisors: %v", err)
	}
	return list.Items
}

// LogStateSummary logs a summary of the current state.
func (env *IntegrationTestEnv) LogStateSummary() {
	env.T.Helper()

	hypervisors := env.ListHypervisors()
	vms := env.ListVMs()
	reservationsList := env.ListReservations()

	vmsByHypervisor := make(map[string][]reservations.VM)
	for _, vm := range vms {
		vmsByHypervisor[vm.CurrentHypervisor] = append(vmsByHypervisor[vm.CurrentHypervisor], vm)
	}

	resByHypervisor := make(map[string][]v1alpha1.Reservation)
	for _, res := range reservationsList {
		if res.Status.Host != "" {
			resByHypervisor[res.Status.Host] = append(resByHypervisor[res.Status.Host], res)
		}
	}

	env.T.Log("=== State Summary ===")
	for _, hv := range hypervisors {
		hypervisorName := hv.Name

		var vmParts []string
		for _, vm := range vmsByHypervisor[hypervisorName] {
			memoryMB := int64(0)
			vcpus := int64(0)
			if mem, ok := vm.Resources[string(hv1.ResourceMemory)]; ok {
				memoryMB = mem.Value() / (1024 * 1024)
			}
			if cpu, ok := vm.Resources[string(hv1.ResourceCPU)]; ok {
				vcpus = cpu.Value()
			}
			vmParts = append(vmParts, fmt.Sprintf("%s(%dMB,%dCPU)", vm.UUID, memoryMB, vcpus))
		}

		var resParts []string
		for _, res := range resByHypervisor[hypervisorName] {
			memoryMB := int64(0)
			vcpus := int64(0)
			if mem, ok := res.Spec.Resources[hv1.ResourceMemory]; ok {
				memoryMB = mem.Value() / (1024 * 1024)
			}
			if cpu, ok := res.Spec.Resources[hv1.ResourceCPU]; ok {
				vcpus = cpu.Value()
			}

			var usedByParts []string
			if res.Status.FailoverReservation != nil {
				for vmID, vmHost := range res.Status.FailoverReservation.Allocations {
					usedByParts = append(usedByParts, fmt.Sprintf("%s@%s", vmID, vmHost))
				}
			}
			usedByStr := ""
			if len(usedByParts) > 0 {
				usedByStr = fmt.Sprintf("; used_by=[%s]", strings.Join(usedByParts, ","))
			}

			resParts = append(resParts, fmt.Sprintf("%s(%dMB;%dCPU%s)", res.Name, memoryMB, vcpus, usedByStr))
		}

		var parts []string
		if len(vmParts) > 0 {
			parts = append(parts, strings.Join(vmParts, "; "))
		}
		if len(resParts) > 0 {
			parts = append(parts, strings.Join(resParts, "; "))
		}

		summary := "(empty)"
		if len(parts) > 0 {
			summary = strings.Join(parts, "; ")
		}

		env.T.Logf("%s: %s", hypervisorName, summary)
	}
	env.T.Log("=====================")
}

// TriggerFailoverReconcile creates a FailoverReservationController and triggers its Reconcile method.
func (env *IntegrationTestEnv) TriggerFailoverReconcile(flavorRequirements map[string]int) error {
	env.T.Helper()

	// Drive the same in-process pipeline the testHTTPAPI uses, so the failover
	// reservation flow exercises the real scheduler over the env's nova controller.
	scheduler := compute.NewPlacement(env.NovaController, passthroughGatherer{})

	config := FailoverConfig{
		ReconcileInterval:          metav1.Duration{Duration: time.Minute},
		Creator:                    "test-failover-controller",
		FlavorFailoverRequirements: flavorRequirements,
	}
	config.ApplyDefaults()

	controller := NewFailoverReservationController(
		env.K8sClient,
		env.VMSource,
		config,
		scheduler,
		nil,
	)

	_, err := controller.ReconcilePeriodic(context.Background())
	return err
}

// TriggerReservationValidation runs the per-reservation Reconcile (validate and
// acknowledge) for every existing failover reservation. This exercises the
// scheduler-based validation path (validateVMViaSchedulerEvacuation), which
// applies the domain filters and deletes reservations whose allocated VMs are no
// longer allowed on the reservation host.
func (env *IntegrationTestEnv) TriggerReservationValidation(flavorRequirements map[string]int) error {
	env.T.Helper()

	scheduler := compute.NewPlacement(env.NovaController, passthroughGatherer{})
	config := FailoverConfig{
		ReconcileInterval:          metav1.Duration{Duration: time.Minute},
		Creator:                    "test-failover-controller",
		FlavorFailoverRequirements: flavorRequirements,
	}
	config.ApplyDefaults()
	controller := NewFailoverReservationController(env.K8sClient, env.VMSource, config, scheduler, nil)

	for _, res := range env.ListReservations() {
		if res.Spec.Type != v1alpha1.ReservationTypeFailover {
			continue
		}
		req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: res.Namespace, Name: res.Name}}
		if _, err := controller.Reconcile(context.Background(), req); err != nil {
			return err
		}
	}
	return nil
}

// ============================================================================
// Verification Helpers
// ============================================================================

// VerifyReservationCountInRange checks that the number of reservations is within the expected range.
func (env *IntegrationTestEnv) VerifyReservationCountInRange(minCount, maxCount int) {
	env.T.Helper()
	reservationsList := env.ListReservations()
	count := len(reservationsList)
	if count < minCount || count > maxCount {
		env.T.Errorf("Expected %d-%d reservations, got %d", minCount, maxCount, count)
	} else {
		env.T.Logf("Reservation count %d is within expected range [%d, %d]", count, minCount, maxCount)
	}
}

// VerifyVMHasFailoverReservation checks that a VM has a failover reservation on a different hypervisor
// and that the reservation host is in the same availability zone as the VM.
func (env *IntegrationTestEnv) VerifyVMHasFailoverReservation(vmUUID, vmCurrentHypervisor string) {
	env.T.Helper()
	reservationsList := env.ListReservations()
	hypervisors := env.ListHypervisors()
	vms := env.ListVMs()

	// Find the VM to get its AZ
	var vmAZ string
	for _, vm := range vms {
		if vm.UUID == vmUUID {
			vmAZ = vm.AvailabilityZone
			break
		}
	}

	// Build a map of hypervisor name -> AZ for quick lookup
	hypervisorAZ := make(map[string]string)
	for _, hv := range hypervisors {
		if az, ok := hv.Labels[corev1.LabelTopologyZone]; ok {
			hypervisorAZ[hv.Name] = az
		}
	}

	for _, res := range reservationsList {
		if res.Spec.Type != v1alpha1.ReservationTypeFailover {
			continue
		}
		if res.Status.FailoverReservation != nil {
			if _, exists := res.Status.FailoverReservation.Allocations[vmUUID]; exists {
				if res.Status.Host == vmCurrentHypervisor {
					env.T.Errorf("Failover reservation for VM %s is on the same hypervisor %s", vmUUID, vmCurrentHypervisor)
				}

				// Verify the reservation host is in the same AZ as the VM
				resHostAZ := hypervisorAZ[res.Status.Host]
				if vmAZ != resHostAZ {
					env.T.Errorf("Failover reservation for VM %s (AZ: %s) is on hypervisor %s which is in wrong AZ: %s",
						vmUUID, vmAZ, res.Status.Host, resHostAZ)
				}

				env.T.Logf("VM %s (AZ: %s) has failover reservation %s on hypervisor %s (AZ: %s)",
					vmUUID, vmAZ, res.Name, res.Status.Host, resHostAZ)
				return
			}
		}
	}
	env.T.Errorf("No failover reservation found for VM %s", vmUUID)
}

// VerifyVMRemovedFromReservation checks that a specific VM with a specific host allocation
// is not in a specific reservation's allocations. The expectedHost parameter allows
// verifying that a VM was removed from a reservation where it was allocated with a specific host,
// while allowing the VM to be re-added with a different host.
func (env *IntegrationTestEnv) VerifyVMRemovedFromReservation(reservationName, vmUUID, expectedHost string) {
	env.T.Helper()
	reservationsList := env.ListReservations()

	for _, res := range reservationsList {
		if res.Name != reservationName {
			continue
		}
		if res.Status.FailoverReservation == nil {
			env.T.Logf("VM %s correctly removed from reservation %s (no allocations)", vmUUID, reservationName)
			return
		}
		if allocatedHost, exists := res.Status.FailoverReservation.Allocations[vmUUID]; exists {
			if allocatedHost == expectedHost {
				env.T.Errorf("VM %s should have been removed from reservation %s (was allocated with host %s) but is still present with same host", vmUUID, reservationName, expectedHost)
			} else {
				env.T.Logf("VM %s was re-added to reservation %s with different host (old: %s, new: %s) - this is allowed", vmUUID, reservationName, expectedHost, allocatedHost)
			}
		} else {
			env.T.Logf("VM %s correctly removed from reservation %s", vmUUID, reservationName)
		}
		return
	}
	env.T.Logf("Reservation %s not found (may have been deleted)", reservationName)
}

// VerifyVMsHaveRequiredReservations checks that each VM has the required number of failover reservations.
func (env *IntegrationTestEnv) VerifyVMsHaveRequiredReservations(flavorRequirements map[string]int) bool {
	env.T.Helper()

	vms := env.ListVMs()
	reservationsList := env.ListReservations()

	vmReservationHosts := make(map[string][]string)
	for _, res := range reservationsList {
		if res.Spec.Type != v1alpha1.ReservationTypeFailover {
			continue
		}
		if res.Status.FailoverReservation != nil {
			for vmUUID := range res.Status.FailoverReservation.Allocations {
				vmReservationHosts[vmUUID] = append(vmReservationHosts[vmUUID], res.Status.Host)
			}
		}
	}

	env.T.Log("╔══════════════════════════════════════════════════════════════════╗")
	env.T.Log("║ SANITY CHECK: Verifying each VM has required reservations        ║")
	env.T.Log("╠══════════════════════════════════════════════════════════════════╣")

	allPassed := true
	for _, vm := range vms {
		requiredCount, needsFailover := flavorRequirements[vm.FlavorName]
		if !needsFailover || requiredCount == 0 {
			env.T.Logf("║   ⏭️  %s: flavor %s doesn't require failover", vm.UUID, vm.FlavorName)
			continue
		}

		actualCount := len(vmReservationHosts[vm.UUID])
		if actualCount >= requiredCount {
			env.T.Logf("║   ✅ %s: has %d/%d reservations on hosts %v", vm.UUID, actualCount, requiredCount, vmReservationHosts[vm.UUID])
		} else {
			env.T.Logf("║   ❌ %s: has %d/%d reservations (MISSING %d)", vm.UUID, actualCount, requiredCount, requiredCount-actualCount)
			env.T.Errorf("VM %s has %d reservations but needs %d", vm.UUID, actualCount, requiredCount)
			allPassed = false
		}
	}

	env.T.Log("╚══════════════════════════════════════════════════════════════════╝")
	return allPassed
}

// VerifyEvacuationForAllFailureCombinations tests that VMs can be evacuated for all
// possible host failure combinations up to the configured failure tolerance.
func (env *IntegrationTestEnv) VerifyEvacuationForAllFailureCombinations(
	flavorRequirements map[string]int,
	allHosts []string,
	memoryMB, vcpus uint64,
) {

	env.T.Helper()

	if !env.VerifyVMsHaveRequiredReservations(flavorRequirements) {
		env.T.Error("Sanity check failed: not all VMs have required reservations, skipping failure simulations")
		return
	}

	toleranceGroups := make(map[int][]string)
	for fn, tolerance := range flavorRequirements {
		toleranceGroups[tolerance] = append(toleranceGroups[tolerance], fn)
	}

	for tolerance, flavorNames := range toleranceGroups {
		if tolerance == 0 {
			continue
		}

		env.T.Logf("Testing failure tolerance %d for flavors: %v", tolerance, flavorNames)

		hostCombinations := generateHostCombinations(allHosts, tolerance)
		env.T.Logf("Generated %d host failure combinations for tolerance %d", len(hostCombinations), tolerance)

		for _, failedHosts := range hostCombinations {
			env.T.Run("FailedHosts_"+strings.Join(failedHosts, "_"), func(t *testing.T) {
				env.simulateHostFailure(failedHosts, allHosts, memoryMB, vcpus, flavorRequirements)
			})
		}
	}
}

// simulateHostFailure simulates a host failure by sending evacuation requests for all VMs
// on the failed hosts and verifying they can be placed on reservation hosts.
func (env *IntegrationTestEnv) simulateHostFailure(failedHosts, allHosts []string, memoryMB, vcpus uint64, flavorRequirements map[string]int) {
	env.T.Helper()

	vms := env.ListVMs()
	reservationsList := env.ListReservations()

	reservationsByHost := make(map[string][]v1alpha1.Reservation)
	for _, res := range reservationsList {
		if res.Spec.Type == v1alpha1.ReservationTypeFailover {
			reservationsByHost[res.Status.Host] = append(reservationsByHost[res.Status.Host], res)
		}
	}

	failedHostSet := make(map[string]bool)
	for _, h := range failedHosts {
		failedHostSet[h] = true
	}

	availableHosts := make([]string, 0)
	for _, h := range allHosts {
		if !failedHostSet[h] {
			availableHosts = append(availableHosts, h)
		}
	}

	affectedVMs := make([]reservations.VM, 0)
	for _, vm := range vms {
		if failedHostSet[vm.CurrentHypervisor] {
			affectedVMs = append(affectedVMs, vm)
		}
	}

	usedReservations := make(map[string]bool)

	env.T.Log("╔══════════════════════════════════════════════════════════════════╗")
	env.T.Logf("║ FAILURE SCENARIO: Hosts %v failed", failedHosts)
	env.T.Log("╠══════════════════════════════════════════════════════════════════╣")
	env.T.Logf("║ AFFECTED VMs: %d VMs need evacuation", len(affectedVMs))
	env.T.Log("╠══════════════════════════════════════════════════════════════════╣")
	env.T.Log("║ EVACUATION MOVES:")

	successful := 0
	failed := 0

	for _, vm := range affectedVMs {
		if _, needsFailover := flavorRequirements[vm.FlavorName]; !needsFailover {
			env.T.Logf("║   ⏭️  %s: flavor %s doesn't require failover, skipping", vm.UUID, vm.FlavorName)
			continue
		}

		externalHosts := make([]novaapi.ExternalSchedulerHost, len(availableHosts))
		weights := make(map[string]float64)
		for i, h := range availableHosts {
			externalHosts[i] = novaapi.ExternalSchedulerHost{ComputeHost: h}
			weights[h] = 1.0
		}

		// Build extra specs including VM's flavor extra specs (for traits)
		extraSpecs := map[string]string{
			"capabilities:hypervisor_type": "qemu",
		}
		for k, v := range vm.FlavorExtraSpecs {
			extraSpecs[k] = v
		}

		// Mirror Nova by passing the VM's domain via the domain_name hint, so the
		// domain-based filters (when configured) apply to evacuations too.
		schedulerHints := map[string]any{"_nova_check_type": []any{"evacuate"}}
		if vm.DomainName != "" {
			schedulerHints["domain_name"] = vm.DomainName
		}

		request := novaapi.ExternalSchedulerRequest{
			Pipeline: "test-filter-pipeline",
			Hosts:    externalHosts,
			Weights:  weights,
			Spec: novaapi.NovaObject[novaapi.NovaSpec]{
				Data: novaapi.NovaSpec{
					InstanceUUID:   vm.UUID,
					ProjectID:      vm.ProjectID,
					SchedulerHints: schedulerHints,
					Flavor: novaapi.NovaObject[novaapi.NovaFlavor]{
						Data: novaapi.NovaFlavor{
							Name:       vm.FlavorName,
							VCPUs:      vcpus,
							MemoryMB:   memoryMB,
							ExtraSpecs: extraSpecs,
						},
					},
				},
			},
		}

		response := env.SendPlacementRequest(request)

		if len(response.Hosts) == 0 {
			env.T.Logf("║   ❌ %s: %s → NO HOSTS AVAILABLE", vm.UUID, vm.CurrentHypervisor)
			env.T.Errorf("No hypervisors available for evacuating VM %s from failed hypervisor %s", vm.UUID, vm.CurrentHypervisor)
			failed++
			continue
		}

		selectedHost := ""
		selectedReservation := ""
		// Only consider the first 3 hosts (simulating Nova's behavior of using top hosts)
		hostsToCheck := response.Hosts
		if len(hostsToCheck) > 3 {
			hostsToCheck = hostsToCheck[:3]
		}
		for _, candidateHost := range hostsToCheck {
			for _, res := range reservationsByHost[candidateHost] {
				if res.Status.FailoverReservation != nil {
					if _, vmUsesThis := res.Status.FailoverReservation.Allocations[vm.UUID]; vmUsesThis {
						if !usedReservations[res.Name] {
							usedReservations[res.Name] = true
							selectedHost = candidateHost
							selectedReservation = res.Name
							break
						}
					}
				}
			}
			if selectedHost != "" {
				break
			}
		}

		if selectedHost == "" {
			env.T.Logf("║   ❌ %s: %s → NO RESERVATION HOST FOUND", vm.UUID, vm.CurrentHypervisor)
			env.T.Errorf("VM %s has no reservation hypervisor available for evacuation from %s", vm.UUID, vm.CurrentHypervisor)
			failed++
			continue
		}

		env.T.Logf("║   ✅ %s: %s → %s (using reservation %s)", vm.UUID, vm.CurrentHypervisor, selectedHost, selectedReservation)
		successful++
	}

	env.T.Log("╠══════════════════════════════════════════════════════════════════╣")
	env.T.Logf("║ RESULT: %d/%d VMs successfully evacuated", successful, len(affectedVMs))
	env.T.Log("╚══════════════════════════════════════════════════════════════════╝")

	if failed > 0 {
		env.T.Errorf("Failed to evacuate %d VMs", failed)
	}
}

// ============================================================================
// Helper Functions
// ============================================================================

var metricsRegistered sync.Once
var sharedMonitor lib.FilterWeigherPipelineMonitor

func getSharedMonitor() lib.FilterWeigherPipelineMonitor {
	metricsRegistered.Do(func() {
		sharedMonitor = lib.NewPipelineMonitor()
	})
	return sharedMonitor
}

// MockVMSource implements VMSource for testing without requiring a database.
type MockVMSource struct {
	VMs []reservations.VM
}

// NewMockVMSource creates a new MockVMSource with the given VMs.
func NewMockVMSource(vms []reservations.VM) *MockVMSource {
	return &MockVMSource{VMs: vms}
}

// ListVMsByProject returns VMs filtered by project ID.
func (s *MockVMSource) ListVMsByProject(_ context.Context, projectID string) ([]reservations.VM, error) {
	var result []reservations.VM
	for _, vm := range s.VMs {
		if vm.ProjectID == projectID {
			result = append(result, vm)
		}
	}
	return result, nil
}

// ListVMs returns the configured VMs.
func (s *MockVMSource) ListVMs(_ context.Context) ([]reservations.VM, error) {
	return s.VMs, nil
}

// ListVMsOnHypervisors returns VMs that are on the given hypervisors.
// For the mock, this simply returns all VMs (filtering is not needed for tests).
func (s *MockVMSource) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]reservations.VM, error) {
	return s.VMs, nil
}

// GetVM returns a specific VM by UUID.
// Returns nil, nil if the VM is not found.
func (s *MockVMSource) GetVM(_ context.Context, vmUUID string) (*reservations.VM, error) {
	for i := range s.VMs {
		if s.VMs[i].UUID == vmUUID {
			return &s.VMs[i], nil
		}
	}
	return nil, nil
}

// IsServerActive returns true if the server is found in the mock VMs.
func (s *MockVMSource) IsServerActive(_ context.Context, vmUUID string) (bool, error) {
	for i := range s.VMs {
		if s.VMs[i].UUID == vmUUID {
			return true, nil
		}
	}
	return false, nil
}

// GetDeletedVMInfo returns nil, nil (no deleted VMs in mock).
func (s *MockVMSource) GetDeletedVMInfo(_ context.Context, _ string) (*reservations.DeletedVMInfo, error) {
	return nil, nil
}

// envOptions configures the pipelines registered by newIntegrationTestEnvWithOptions.
type envOptions struct {
	// traitsFilter adds filter_has_requested_traits to every pipeline.
	traitsFilter bool
	// domainFilters, when set, prepends filter_external_customer and
	// filter_domain_pinning (with the given params) to every pipeline.
	domainFilters *domainFilterConfig
}

// domainFilterConfig holds the allow-list and pinning map for the domain-based
// filters exercised by the failover integration tests.
type domainFilterConfig struct {
	allowedDomains    []string
	domainHypervisors map[string][]string
}

// newIntegrationTestEnv creates a complete test environment with HTTP server and VMSource.
func newIntegrationTestEnv(t *testing.T, vms []reservations.VM, hypervisors []*hv1.Hypervisor, reservations []*v1alpha1.Reservation) *IntegrationTestEnv {
	t.Helper()
	return newIntegrationTestEnvWithOptions(t, vms, hypervisors, reservations, envOptions{})
}

// newIntegrationTestEnvWithTraitsFilter creates a test environment with the filter_has_requested_traits filter enabled.
func newIntegrationTestEnvWithTraitsFilter(t *testing.T, vms []reservations.VM, hypervisors []*hv1.Hypervisor, reservations []*v1alpha1.Reservation) *IntegrationTestEnv {
	t.Helper()
	return newIntegrationTestEnvWithOptions(t, vms, hypervisors, reservations, envOptions{traitsFilter: true})
}

// newIntegrationTestEnvWithOptions builds the test environment and registers the
// failover pipelines, assembling each pipeline's filter list from the options so
// the traits filter and the domain-based filters can be toggled per test.
func newIntegrationTestEnvWithOptions(t *testing.T, vms []reservations.VM, hypervisors []*hv1.Hypervisor, reservations []*v1alpha1.Reservation, opts envOptions) *IntegrationTestEnv {
	t.Helper()

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

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		WithIndex(&v1alpha1.Reservation{}, "spec.type", func(obj client.Object) []string {
			res := obj.(*v1alpha1.Reservation)
			return []string{string(res.Spec.Type)}
		}).
		Build()

	novaController := &nova.FilterWeigherPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[compute.PlacementRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[compute.PlacementRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor: getSharedMonitor(),
	}

	// filters assembles a pipeline's filter list: optional domain filters first,
	// then capacity (when wanted), then the optional traits filter, then AZ.
	buildFilters := func(includeCapacity bool) []v1alpha1.FilterSpec {
		var fs []v1alpha1.FilterSpec
		if opts.domainFilters != nil {
			fs = append(fs,
				v1alpha1.FilterSpec{Name: "filter_external_customer", Params: v1alpha1.Parameters{
					{Key: "domainNamePrefixes", StringListValue: new([]string{"iaas-"})},
					{Key: "allowedDomainNames", StringListValue: new(opts.domainFilters.allowedDomains)},
				}},
				v1alpha1.FilterSpec{Name: "filter_domain_pinning", Params: v1alpha1.Parameters{
					{Key: "domainHypervisors", StringListMapValue: new(opts.domainFilters.domainHypervisors)},
				}},
			)
		}
		if includeCapacity {
			fs = append(fs, v1alpha1.FilterSpec{Name: "filter_has_enough_capacity"})
		}
		if opts.traitsFilter {
			fs = append(fs, v1alpha1.FilterSpec{Name: "filter_has_requested_traits"})
		}
		fs = append(fs, v1alpha1.FilterSpec{Name: "filter_correct_az"})
		return fs
	}

	evacuationWeigher := []v1alpha1.WeigherSpec{{Name: "kvm_failover_evacuation"}}
	pipelines := []v1alpha1.Pipeline{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "test-filter-pipeline"},
			Spec: v1alpha1.PipelineSpec{
				Type:     v1alpha1.PipelineTypeFilterWeigher,
				Filters:  buildFilters(true),
				Weighers: evacuationWeigher,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: PipelineNewFailoverReservation},
			Spec: v1alpha1.PipelineSpec{
				Type:     v1alpha1.PipelineTypeFilterWeigher,
				Filters:  buildFilters(true),
				Weighers: evacuationWeigher,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "kvm-hana-bin-packing"},
			Spec: v1alpha1.PipelineSpec{
				Type:     v1alpha1.PipelineTypeFilterWeigher,
				Filters:  buildFilters(true),
				Weighers: evacuationWeigher,
			},
		},
	}

	ctx := context.Background()
	for _, pipeline := range pipelines {
		result := lib.InitNewFilterWeigherPipeline(
			ctx, k8sClient, pipeline.Name,
			filters.Index, pipeline.Spec.Filters,
			weighers.Index, pipeline.Spec.Weighers,
			novaController.Monitor,
		)
		if len(result.FilterErrors) > 0 || len(result.WeigherErrors) > 0 {
			t.Fatalf("Failed to init pipeline %s: filters=%v, weighers=%v", pipeline.Name, result.FilterErrors, result.WeigherErrors)
		}
		novaController.Pipelines[pipeline.Name] = result.Pipeline
		novaController.PipelineConfigs[pipeline.Name] = pipeline
	}

	api := &testHTTPAPI{scheduler: compute.NewPlacement(novaController, passthroughGatherer{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/scheduler/nova/external", api.NovaExternalScheduler)
	server := httptest.NewServer(mux)

	return &IntegrationTestEnv{
		T:                t,
		Scheme:           scheme,
		K8sClient:        k8sClient,
		Server:           server,
		NovaController:   novaController,
		VMSource:         NewMockVMSource(vms),
		SchedulerBaseURL: server.URL,
	}
}

// testHTTPAPI is a simplified HTTP API for testing that delegates to the controller.
// It does NOT shuffle evacuation hosts, ensuring deterministic test results.
type testHTTPAPI struct {
	scheduler *compute.Placement
}

// NovaExternalScheduler handles the POST request from the Nova scheduler.
func (api *testHTTPAPI) NovaExternalScheduler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "invalid request method", http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()

	var requestData novaapi.ExternalSchedulerRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "failed to decode request body", http.StatusBadRequest)
		return
	}

	pipelineName := requestData.Pipeline
	if pipelineName == "" {
		pipelineName = "test-filter-pipeline"
	}

	// Resolve the scheduling intent from the request hints, mirroring the
	// production handler (external_scheduler_api.go).
	intent, err := requestData.GetIntent()
	if err != nil {
		intent = novaapi.CreateIntent
	}

	req := nova.ToSchedulerRequest(requestData, intent, pipelineName)
	out, err := api.scheduler.Run(r.Context(), req, nil, nil)
	if err != nil {
		http.Error(w, "failed to process scheduling decision", http.StatusInternalServerError)
		return
	}

	response := novaapi.ExternalSchedulerResponse{Hosts: out.OrderedHosts}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
}

// ============================================================================
// Object Creation Functions
// ============================================================================

// defaultTestAZ is the default availability zone used for tests when not explicitly specified.
const defaultTestAZ = "az-a"

// newHypervisor creates a Hypervisor CRD with the given parameters.
// Uses defaultTestAZ as the availability zone.
func newHypervisor(name string, cpuCap, memoryGi, cpuAlloc, memoryGiAlloc int, instances []hv1.Instance, traits []string) *hv1.Hypervisor {
	return newHypervisorWithAZ(name, cpuCap, memoryGi, cpuAlloc, memoryGiAlloc, instances, traits, defaultTestAZ)
}

// newHypervisorWithAZ creates a Hypervisor CRD with the given parameters including availability zone.
func newHypervisorWithAZ(name string, cpuCap, memoryGi, cpuAlloc, memoryGiAlloc int, instances []hv1.Instance, traits []string, az string) *hv1.Hypervisor {
	labels := make(map[string]string)
	if az != "" {
		labels[corev1.LabelTopologyZone] = az
	}
	capacity := map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceCPU:    resource.MustParse(strconv.Itoa(cpuCap)),
		hv1.ResourceMemory: resource.MustParse(strconv.Itoa(memoryGi) + "Gi"),
	}
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: hv1.HypervisorSpec{
			Overcommit: map[hv1.ResourceName]float64{
				hv1.ResourceCPU:    1.0,
				hv1.ResourceMemory: 1.0,
			},
		},
		Status: hv1.HypervisorStatus{
			Capacity:          capacity,
			EffectiveCapacity: capacity,
			Allocation:        map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: resource.MustParse(strconv.Itoa(cpuAlloc)), hv1.ResourceMemory: resource.MustParse(strconv.Itoa(memoryGiAlloc) + "Gi")},
			NumInstances:      len(instances),
			Instances:         instances,
			Traits:            traits,
		},
	}
}

// newReservation creates a Reservation CRD with the given parameters.
func newReservation(name, host string, memoryMB, vcpus uint64, allocations map[string]string) *v1alpha1.Reservation { //nolint:unparam
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelFailover,
				"cortex.cloud/creator":        "test",
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources: map[hv1.ResourceName]resource.Quantity{
				hv1.ResourceMemory: resource.MustParse(strconv.FormatUint(memoryMB, 10) + "Mi"),
				hv1.ResourceCPU:    resource.MustParse(strconv.FormatUint(vcpus, 10)),
			},
			FailoverReservation: &v1alpha1.FailoverReservationSpec{
				ResourceGroup: "m1.large",
			},
		},
		Status: v1alpha1.ReservationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReservationConditionReady,
					Status: metav1.ConditionTrue,
					Reason: "ReservationActive",
				},
			},
			Host: host,
			FailoverReservation: &v1alpha1.FailoverReservationStatus{
				Allocations: allocations,
			},
		},
	}
}

// newVM creates a VM with the given parameters.
// Uses defaultTestAZ as the availability zone.
func newVM(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64) reservations.VM {
	return newVMWithAZ(uuid, flavorName, projectID, host, memoryMB, vcpus, defaultTestAZ)
}

// newVMWithAZ creates a VM with the given parameters including availability zone.
func newVMWithAZ(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64, az string) reservations.VM {
	return reservations.VM{
		UUID:              uuid,
		FlavorName:        flavorName,
		ProjectID:         projectID,
		CurrentHypervisor: host,
		AvailabilityZone:  az,
		Resources: map[string]resource.Quantity{
			string(hv1.ResourceMemory): resource.MustParse(strconv.FormatUint(memoryMB, 10) + "Mi"),
			string(hv1.ResourceCPU):    resource.MustParse(strconv.FormatUint(vcpus, 10)),
		},
		FlavorExtraSpecs: make(map[string]string),
	}
}

// newVMWithExtraSpecs creates a VM with the given parameters including extra specs.
// Uses defaultTestAZ as the availability zone.
func newVMWithExtraSpecs(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64, extraSpecs map[string]string) reservations.VM { //nolint:unparam
	return newVMWithExtraSpecsAndAZ(uuid, flavorName, projectID, host, memoryMB, vcpus, extraSpecs, defaultTestAZ)
}

// newVMWithExtraSpecsAndAZ creates a VM with the given parameters including extra specs and availability zone.
func newVMWithExtraSpecsAndAZ(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64, extraSpecs map[string]string, az string) reservations.VM {
	vm := newVMWithAZ(uuid, flavorName, projectID, host, memoryMB, vcpus, az)
	if extraSpecs != nil {
		vm.FlavorExtraSpecs = extraSpecs
	}
	return vm
}

// newVMWithDomain creates a VM with a domain name set, so the failover scheduling
// flow exercises the domain-based filters.
func newVMWithDomain(uuid, flavorName, projectID, domainName, host string, memoryMB, vcpus uint64) reservations.VM { //nolint:unparam // flavor/project vary in real usage
	vm := newVM(uuid, flavorName, projectID, host, memoryMB, vcpus)
	vm.DomainName = domainName
	return vm
}

// generateHostCombinations generates all combinations of hosts up to size n.
func generateHostCombinations(hosts []string, maxSize int) [][]string {
	var result [][]string
	for size := 1; size <= maxSize && size <= len(hosts); size++ {
		result = append(result, combinations(hosts, size)...)
	}
	return result
}

// combinations generates all combinations of size k from the given slice.
func combinations(items []string, k int) [][]string {
	if k == 0 {
		return [][]string{{}}
	}
	if len(items) < k {
		return nil
	}

	var result [][]string
	for _, combo := range combinations(items[1:], k-1) {
		result = append(result, append([]string{items[0]}, combo...))
	}
	result = append(result, combinations(items[1:], k)...)
	return result
}
