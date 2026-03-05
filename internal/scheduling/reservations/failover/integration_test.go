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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	novaapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ============================================================================
// Integration Tests
// ============================================================================

// IntegrationTestCase defines a unified test case for all failover reservation integration tests.
type IntegrationTestCase struct {
	Name               string
	Hypervisors        []*hv1.Hypervisor
	Reservations       []*v1alpha1.Reservation
	VMs                []VM
	FlavorRequirements map[string]int

	// Verification options
	ExpectedMinRes      int                          // Minimum expected reservations after reconcile
	ExpectedMaxRes      int                          // Maximum expected reservations after reconcile (0 = no max check)
	VerifyVMReservation []string                     // VM UUIDs to verify have reservations
	VMsToRemove         map[string]map[string]string // reservationName -> vmUUID -> expectedHost (verify VM removed from reservation)

	// Test behavior options
	ReconcileCount        int  // Number of reconciles to run (default: 1)
	SkipFailureSimulation bool // Set to true to skip failure simulation tests (default: run simulation)
	UseTraitsFilter       bool // Use traits filter pipeline instead of default
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			},
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
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
			VMs: []VM{
				newVMWithExtraSpecs("vm-hana-1", "m1.hana", "project-A", "host1", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "required"}),
				newVMWithExtraSpecs("vm-hana-2", "m1.hana", "project-A", "host2", 8192, 4, map[string]string{"trait:CUSTOM_HANA": "required"}),
			},
			FlavorRequirements: map[string]int{"m1.hana": 1},
			ExpectedMinRes:     1, // Both HANA VMs can share reservation on host3
			UseTraitsFilter:    true,
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
	if tc.UseTraitsFilter {
		env = newIntegrationTestEnvWithTraitsFilter(t, tc.VMs, tc.Hypervisors, tc.Reservations)
	} else {
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

	// Verify all VMs have required reservations
	env.VerifyVMsHaveRequiredReservations(tc.FlavorRequirements)

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
	VMSource         VMSource
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
func (env *IntegrationTestEnv) ListVMs() []VM {
	vms, err := env.VMSource.ListVMs(context.Background())
	if err != nil {
		env.T.Fatalf("Failed to list VMs: %v", err)
	}
	return vms
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

	vmsByHypervisor := make(map[string][]VM)
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
			if mem, ok := vm.Resources["memory"]; ok {
				memoryMB = mem.Value() / (1024 * 1024)
			}
			if cpu, ok := vm.Resources["vcpus"]; ok {
				vcpus = cpu.Value()
			}
			vmParts = append(vmParts, fmt.Sprintf("%s(%dMB,%dCPU)", vm.UUID, memoryMB, vcpus))
		}

		var resParts []string
		for _, res := range resByHypervisor[hypervisorName] {
			memoryMB := int64(0)
			vcpus := int64(0)
			if mem, ok := res.Spec.Resources["memory"]; ok {
				memoryMB = mem.Value() / (1024 * 1024)
			}
			if cpu, ok := res.Spec.Resources["cpu"]; ok {
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

	schedulerClient := reservations.NewSchedulerClient(env.SchedulerBaseURL + "/scheduler/nova/external")

	config := FailoverConfig{
		ReconcileInterval:          time.Minute,
		Creator:                    "test-failover-controller",
		FlavorFailoverRequirements: flavorRequirements,
	}

	controller := NewFailoverReservationController(
		env.K8sClient,
		env.VMSource,
		config,
		schedulerClient,
	)

	_, err := controller.ReconcilePeriodic(context.Background())
	return err
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

	affectedVMs := make([]VM, 0)
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

		request := novaapi.ExternalSchedulerRequest{
			Pipeline: "nova-external-scheduler-kvm-all-filters-enabled",
			Hosts:    externalHosts,
			Weights:  weights,
			Spec: novaapi.NovaObject[novaapi.NovaSpec]{
				Data: novaapi.NovaSpec{
					InstanceUUID: vm.UUID,
					ProjectID:    vm.ProjectID,
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
		for _, candidateHost := range response.Hosts {
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

// ListVMsOnHypervisors returns VMs that are on the given hypervisors.
// For the mock, this simply returns all VMs (filtering is not needed for tests).
func (s *MockVMSource) ListVMsOnHypervisors(_ context.Context, _ *hv1.HypervisorList, _ bool) ([]VM, error) {
	return s.VMs, nil
}

// GetVM returns a specific VM by UUID.
// Returns nil, nil if the VM is not found.
func (s *MockVMSource) GetVM(_ context.Context, vmUUID string) (*VM, error) {
	for i := range s.VMs {
		if s.VMs[i].UUID == vmUUID {
			return &s.VMs[i], nil
		}
	}
	return nil, nil
}

// newIntegrationTestEnv creates a complete test environment with HTTP server and VMSource.
func newIntegrationTestEnv(t *testing.T, vms []VM, hypervisors []*hv1.Hypervisor, reservations []*v1alpha1.Reservation) *IntegrationTestEnv {
	t.Helper()

	// Combine hypervisors and reservations into a single objects slice
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
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor: getSharedMonitor(),
	}

	// Register all pipelines needed for testing
	pipelines := []v1alpha1.Pipeline{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nova-external-scheduler-kvm-all-filters-enabled",
			},
			Spec: v1alpha1.PipelineSpec{
				Type: v1alpha1.PipelineTypeFilterWeigher,
				Filters: []v1alpha1.FilterSpec{
					{Name: "filter_has_enough_capacity"},
					{Name: "filter_correct_az"},
				},
				Weighers: []v1alpha1.WeigherSpec{
					{Name: "kvm_failover_evacuation"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: PipelineReuseFailoverReservation,
			},
			Spec: v1alpha1.PipelineSpec{
				Type: v1alpha1.PipelineTypeFilterWeigher,
				Filters: []v1alpha1.FilterSpec{
					{Name: "filter_has_requested_traits"},
					{Name: "filter_correct_az"},
				},
				Weighers: []v1alpha1.WeigherSpec{},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: PipelineNewFailoverReservation,
			},
			Spec: v1alpha1.PipelineSpec{
				Type: v1alpha1.PipelineTypeFilterWeigher,
				Filters: []v1alpha1.FilterSpec{
					{Name: "filter_has_enough_capacity"},
					{Name: "filter_has_requested_traits"},
					{Name: "filter_correct_az"},
				},
				Weighers: []v1alpha1.WeigherSpec{
					{Name: "kvm_failover_evacuation"},
				},
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

	api := &testHTTPAPI{delegate: novaController}
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

// newIntegrationTestEnvWithTraitsFilter creates a test environment with the filter_has_requested_traits filter enabled.
func newIntegrationTestEnvWithTraitsFilter(t *testing.T, vms []VM, hypervisors []*hv1.Hypervisor, reservations []*v1alpha1.Reservation) *IntegrationTestEnv {
	t.Helper()

	// Combine hypervisors and reservations into a single objects slice
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
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]]{
			Client:          k8sClient,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[novaapi.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		Monitor: getSharedMonitor(),
	}

	// Register all pipelines needed for testing (with traits filter enabled)
	pipelines := []v1alpha1.Pipeline{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nova-external-scheduler-kvm-all-filters-enabled",
			},
			Spec: v1alpha1.PipelineSpec{
				Type: v1alpha1.PipelineTypeFilterWeigher,
				Filters: []v1alpha1.FilterSpec{
					{Name: "filter_has_enough_capacity"},
					{Name: "filter_has_requested_traits"},
					{Name: "filter_correct_az"},
				},
				Weighers: []v1alpha1.WeigherSpec{
					{Name: "kvm_failover_evacuation"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: PipelineReuseFailoverReservation,
			},
			Spec: v1alpha1.PipelineSpec{
				Type: v1alpha1.PipelineTypeFilterWeigher,
				Filters: []v1alpha1.FilterSpec{
					{Name: "filter_has_requested_traits"},
					{Name: "filter_correct_az"},
				},
				Weighers: []v1alpha1.WeigherSpec{},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: PipelineNewFailoverReservation,
			},
			Spec: v1alpha1.PipelineSpec{
				Type: v1alpha1.PipelineTypeFilterWeigher,
				Filters: []v1alpha1.FilterSpec{
					{Name: "filter_has_enough_capacity"},
					{Name: "filter_has_requested_traits"},
					{Name: "filter_correct_az"},
				},
				Weighers: []v1alpha1.WeigherSpec{
					{Name: "kvm_failover_evacuation"},
				},
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

	api := &testHTTPAPI{delegate: novaController}
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
type testHTTPAPI struct {
	delegate nova.HTTPAPIDelegate
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

	rawBytes, err := json.Marshal(requestData)
	if err != nil {
		http.Error(w, "failed to marshal request", http.StatusInternalServerError)
		return
	}
	raw := runtime.RawExtension{Raw: rawBytes}

	pipelineName := requestData.Pipeline
	if pipelineName == "" {
		pipelineName = "nova-external-scheduler-kvm-all-filters-enabled"
	}

	decision := &v1alpha1.Decision{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Decision",
			APIVersion: "cortex.cloud/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "nova-",
		},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			PipelineRef: corev1.ObjectReference{
				Name: pipelineName,
			},
			ResourceID: requestData.Spec.Data.InstanceUUID,
			NovaRaw:    &raw,
		},
	}

	ctx := r.Context()
	if err := api.delegate.ProcessNewDecisionFromAPI(ctx, decision); err != nil {
		http.Error(w, "failed to process scheduling decision", http.StatusInternalServerError)
		return
	}

	if decision.Status.Result == nil {
		http.Error(w, "decision didn't produce a result", http.StatusInternalServerError)
		return
	}

	hosts := decision.Status.Result.OrderedHosts
	response := novaapi.ExternalSchedulerResponse{Hosts: hosts}
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
	return &hv1.Hypervisor{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Status: hv1.HypervisorStatus{
			Capacity:     map[string]resource.Quantity{"cpu": resource.MustParse(strconv.Itoa(cpuCap)), "memory": resource.MustParse(strconv.Itoa(memoryGi) + "Gi")},
			Allocation:   map[string]resource.Quantity{"cpu": resource.MustParse(strconv.Itoa(cpuAlloc)), "memory": resource.MustParse(strconv.Itoa(memoryGiAlloc) + "Gi")},
			NumInstances: len(instances),
			Instances:    instances,
			Traits:       traits,
		},
	}
}

// newReservation creates a Reservation CRD with the given parameters.
func newReservation(name, host string, memoryMB, vcpus uint64, allocations map[string]string) *v1alpha1.Reservation { //nolint:unparam
	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"cortex.sap.com/type":    "failover",
				"cortex.sap.com/creator": "test",
			},
		},
		Spec: v1alpha1.ReservationSpec{
			Type:       v1alpha1.ReservationTypeFailover,
			TargetHost: host,
			Resources: map[string]resource.Quantity{
				"memory": resource.MustParse(strconv.FormatUint(memoryMB, 10) + "Mi"),
				"cpu":    resource.MustParse(strconv.FormatUint(vcpus, 10)),
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
func newVM(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64) VM { //nolint:unparam
	return newVMWithAZ(uuid, flavorName, projectID, host, memoryMB, vcpus, defaultTestAZ)
}

// newVMWithAZ creates a VM with the given parameters including availability zone.
func newVMWithAZ(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64, az string) VM {
	return VM{
		UUID:              uuid,
		FlavorName:        flavorName,
		ProjectID:         projectID,
		CurrentHypervisor: host,
		AvailabilityZone:  az,
		Resources: map[string]resource.Quantity{
			"memory": resource.MustParse(strconv.FormatUint(memoryMB, 10) + "Mi"),
			"vcpus":  resource.MustParse(strconv.FormatUint(vcpus, 10)),
		},
		FlavorExtraSpecs: make(map[string]string),
	}
}

// newVMWithExtraSpecs creates a VM with the given parameters including extra specs.
// Uses defaultTestAZ as the availability zone.
func newVMWithExtraSpecs(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64, extraSpecs map[string]string) VM { //nolint:unparam
	return newVMWithExtraSpecsAndAZ(uuid, flavorName, projectID, host, memoryMB, vcpus, extraSpecs, defaultTestAZ)
}

// newVMWithExtraSpecsAndAZ creates a VM with the given parameters including extra specs and availability zone.
func newVMWithExtraSpecsAndAZ(uuid, flavorName, projectID, host string, memoryMB, vcpus uint64, extraSpecs map[string]string, az string) VM {
	vm := newVMWithAZ(uuid, flavorName, projectID, host, memoryMB, vcpus, az)
	if extraSpecs != nil {
		vm.FlavorExtraSpecs = extraSpecs
	}
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
