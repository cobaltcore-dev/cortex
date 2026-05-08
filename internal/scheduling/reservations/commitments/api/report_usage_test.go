// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/liquid"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// ============================================================================
// Integration Tests for Usage API
// ============================================================================

func TestReportUsageIntegration(t *testing.T) {
	// Flavor definitions - smallest flavor in group determines the "unit"
	// hana_1 group: smallest = 1024 MB, so 1 unit = 1 GB
	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}  // 1 unit
	m1Large := &TestFlavor{Name: "m1.large", Group: "hana_1", MemoryMB: 4096, VCPUs: 16} // 4 units
	m1XL := &TestFlavor{Name: "m1.xl", Group: "hana_1", MemoryMB: 8192, VCPUs: 32}       // 8 units

	// gp_1 group: smallest = 1024 MB = 1 GiB, so 1 unit = 1 GiB
	gpSmall := &TestFlavor{Name: "gp.small", Group: "gp_1", MemoryMB: 1024, VCPUs: 1}   // 1 unit
	gpMedium := &TestFlavor{Name: "gp.medium", Group: "gp_1", MemoryMB: 2048, VCPUs: 4} // 2 units

	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	testCases := []UsageReportTestCase{
		{
			Name:         "Empty project - no VMs, no commitments",
			ProjectID:    "project-empty",
			Flavors:      []*TestFlavor{m1Small},
			VMs:          []*TestVMUsage{},
			Reservations: []*UsageTestReservation{},
			AllAZs:       []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {Usage: 0, VMs: []ExpectedVMUsage{}},
					},
				},
			},
		},
		{
			Name:      "Single VM with matching commitment - fully assigned",
			ProjectID: "project-A",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				newTestVMUsage("vm-001", m1Large, "project-A", "az-a", "host-1", baseTime),
			},
			Reservations: []*UsageTestReservation{
				// 4 units capacity (4 × 1024 MB)
				{CommitmentID: "commit-1", Flavor: m1Small, ProjectID: "project-A", AZ: "az-a", Count: 4},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 4, // 4096 MB / 1024 MB = 4 units
							VMs: []ExpectedVMUsage{
								{UUID: "vm-001", CommitmentID: "commit-1", MemoryMB: 4096},
							},
						},
					},
				},
			},
		},
		{
			Name:      "Single VM, no commitment - PAYG",
			ProjectID: "project-B",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				newTestVMUsage("vm-002", m1Large, "project-B", "az-a", "host-1", baseTime),
			},
			Reservations: []*UsageTestReservation{}, // No commitments
			AllAZs:       []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 4,
							VMs: []ExpectedVMUsage{
								{UUID: "vm-002", CommitmentID: "", MemoryMB: 4096}, // PAYG
							},
						},
					},
				},
			},
		},
		{
			Name:      "VM overflow to PAYG when commitment capacity exhausted",
			ProjectID: "project-C",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				// 3 VMs × 4 units = 12 units total
				newTestVMUsage("vm-001", m1Large, "project-C", "az-a", "host-1", baseTime),
				newTestVMUsage("vm-002", m1Large, "project-C", "az-a", "host-2", baseTime.Add(1*time.Hour)),
				newTestVMUsage("vm-003", m1Large, "project-C", "az-a", "host-3", baseTime.Add(2*time.Hour)),
			},
			Reservations: []*UsageTestReservation{
				// Only 8 units capacity (8 × 1024 MB = 8 GB)
				{CommitmentID: "commit-1", Flavor: m1Small, ProjectID: "project-C", AZ: "az-a", Count: 8},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 12, // 12 units total
							VMs: []ExpectedVMUsage{
								{UUID: "vm-001", CommitmentID: "commit-1", MemoryMB: 4096}, // 4 units → commit-1 (4/8)
								{UUID: "vm-002", CommitmentID: "commit-1", MemoryMB: 4096}, // 4 units → commit-1 (8/8)
								{UUID: "vm-003", CommitmentID: "", MemoryMB: 4096},         // 4 units → PAYG (overflow)
							},
						},
					},
				},
			},
		},
		{
			Name:      "Deterministic ordering - oldest VMs assigned first",
			ProjectID: "project-D",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				// VMs with different creation times - newest first in input (should be reordered)
				newTestVMUsage("vm-newest", m1Large, "project-D", "az-a", "host-1", baseTime.Add(2*time.Hour)),
				newTestVMUsage("vm-oldest", m1Large, "project-D", "az-a", "host-2", baseTime),
				newTestVMUsage("vm-middle", m1Large, "project-D", "az-a", "host-3", baseTime.Add(1*time.Hour)),
			},
			Reservations: []*UsageTestReservation{
				// Only 4 units capacity - only oldest VM should be assigned
				{CommitmentID: "commit-1", Flavor: m1Small, ProjectID: "project-D", AZ: "az-a", Count: 4},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 12,
							VMs: []ExpectedVMUsage{
								{UUID: "vm-oldest", CommitmentID: "commit-1", MemoryMB: 4096}, // Oldest → assigned
								{UUID: "vm-middle", CommitmentID: "", MemoryMB: 4096},         // PAYG
								{UUID: "vm-newest", CommitmentID: "", MemoryMB: 4096},         // PAYG
							},
						},
					},
				},
			},
		},
		{
			Name:      "Same creation time - largest VMs assigned first",
			ProjectID: "project-E",
			Flavors:   []*TestFlavor{m1Small, m1Large, m1XL},
			VMs: []*TestVMUsage{
				// All same creation time, different sizes
				newTestVMUsage("vm-small", m1Small, "project-E", "az-a", "host-1", baseTime), // 1 unit
				newTestVMUsage("vm-large", m1Large, "project-E", "az-a", "host-2", baseTime), // 4 units
				newTestVMUsage("vm-xl", m1XL, "project-E", "az-a", "host-3", baseTime),       // 8 units
			},
			Reservations: []*UsageTestReservation{
				// 8 units capacity - only xl fits exactly
				{CommitmentID: "commit-1", Flavor: m1Small, ProjectID: "project-E", AZ: "az-a", Count: 8},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 13, // 1 + 4 + 8 = 13 units
							VMs: []ExpectedVMUsage{
								{UUID: "vm-xl", CommitmentID: "commit-1", MemoryMB: 8192}, // Largest → assigned (8/8)
								{UUID: "vm-large", CommitmentID: "", MemoryMB: 4096},      // PAYG
								{UUID: "vm-small", CommitmentID: "", MemoryMB: 1024},      // PAYG
							},
						},
					},
				},
			},
		},
		{
			Name:      "Multiple commitments - fill oldest commitment first",
			ProjectID: "project-F",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				newTestVMUsage("vm-001", m1Large, "project-F", "az-a", "host-1", baseTime),
				newTestVMUsage("vm-002", m1Large, "project-F", "az-a", "host-2", baseTime.Add(1*time.Hour)),
			},
			Reservations: []*UsageTestReservation{
				// Two commitments, 4 units each
				{CommitmentID: "commit-old", Flavor: m1Small, ProjectID: "project-F", AZ: "az-a", Count: 4, StartTime: baseTime.Add(-2 * time.Hour)},
				{CommitmentID: "commit-new", Flavor: m1Small, ProjectID: "project-F", AZ: "az-a", Count: 4, StartTime: baseTime.Add(-1 * time.Hour)},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 8,
							VMs: []ExpectedVMUsage{
								{UUID: "vm-001", CommitmentID: "commit-old", MemoryMB: 4096}, // → oldest commitment
								{UUID: "vm-002", CommitmentID: "commit-new", MemoryMB: 4096}, // → newer commitment
							},
						},
					},
				},
			},
		},
		{
			Name:      "Multi-AZ - VMs in different AZs assigned separately",
			ProjectID: "project-G",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				newTestVMUsage("vm-az-a", m1Large, "project-G", "az-a", "host-1", baseTime),
				newTestVMUsage("vm-az-b", m1Large, "project-G", "az-b", "host-2", baseTime),
			},
			Reservations: []*UsageTestReservation{
				// Commitment only in az-a
				{CommitmentID: "commit-a", Flavor: m1Small, ProjectID: "project-G", AZ: "az-a", Count: 4},
			},
			AllAZs: []string{"az-a", "az-b"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 4,
							VMs: []ExpectedVMUsage{
								{UUID: "vm-az-a", CommitmentID: "commit-a", MemoryMB: 4096},
							},
						},
						"az-b": {
							Usage: 4,
							VMs: []ExpectedVMUsage{
								{UUID: "vm-az-b", CommitmentID: "", MemoryMB: 4096}, // PAYG - no commitment in az-b
							},
						},
					},
				},
			},
		},
		{
			Name:      "Multiple flavor groups - separate resources",
			ProjectID: "project-H",
			Flavors:   []*TestFlavor{m1Small, m1Large, gpSmall, gpMedium},
			VMs: []*TestVMUsage{
				newTestVMUsage("vm-hana", m1Large, "project-H", "az-a", "host-1", baseTime),
				newTestVMUsage("vm-gp", gpMedium, "project-H", "az-a", "host-2", baseTime),
			},
			Reservations: []*UsageTestReservation{
				{CommitmentID: "commit-hana", Flavor: m1Small, ProjectID: "project-H", AZ: "az-a", Count: 4},
				{CommitmentID: "commit-gp", Flavor: gpSmall, ProjectID: "project-H", AZ: "az-a", Count: 4},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 4, // 4096 MB / 1024 MB = 4 units
							VMs: []ExpectedVMUsage{
								{UUID: "vm-hana", CommitmentID: "commit-hana", MemoryMB: 4096},
							},
						},
					},
				},
				"hw_version_gp_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 2, // 2048 MB / 1024 MB = 2 units
							VMs: []ExpectedVMUsage{
								{UUID: "vm-gp", CommitmentID: "commit-gp", MemoryMB: 2048},
							},
						},
					},
				},
			},
		},
		{
			Name:      "VM with video RAM - video_ram_mib present, hw_version absent",
			ProjectID: "project-vram",
			Flavors: func() []*TestFlavor {
				vram := uint64(16)
				return []*TestFlavor{
					m1Small,
					{Name: "m1.gpu", Group: "hana_1", MemoryMB: 4096, VCPUs: 16, VideoRAMMiB: &vram},
				}
			}(),
			VMs: func() []*TestVMUsage {
				vram := uint64(16)
				f := &TestFlavor{Name: "m1.gpu", Group: "hana_1", MemoryMB: 4096, VCPUs: 16, VideoRAMMiB: &vram}
				return []*TestVMUsage{newTestVMUsage("vm-gpu", f, "project-vram", "az-a", "host-1", baseTime)}
			}(),
			Reservations: []*UsageTestReservation{
				{CommitmentID: "commit-1", Flavor: m1Small, ProjectID: "project-vram", AZ: "az-a", Count: 4},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 4,
							VMs: func() []ExpectedVMUsage {
								vram := uint64(16)
								return []ExpectedVMUsage{
									{UUID: "vm-gpu", CommitmentID: "commit-1", MemoryMB: 4096, VideoRAMMiB: &vram},
								}
							}(),
						},
					},
				},
			},
		},
		{
			Name:               "Invalid project ID - 400 Bad Request",
			ProjectID:          "",
			Flavors:            []*TestFlavor{m1Small},
			VMs:                []*TestVMUsage{},
			Reservations:       []*UsageTestReservation{},
			AllAZs:             []string{"az-a"},
			ExpectedStatusCode: http.StatusBadRequest,
		},
		{
			Name:               "Method not POST - 405 Method Not Allowed",
			ProjectID:          "project-X",
			UseGET:             true,
			Flavors:            []*TestFlavor{m1Small},
			VMs:                []*TestVMUsage{},
			Reservations:       []*UsageTestReservation{},
			AllAZs:             []string{"az-a"},
			ExpectedStatusCode: http.StatusMethodNotAllowed,
		},
		{
			Name:      "VM with empty AZ - normalized to unknown",
			ProjectID: "project-empty-az",
			Flavors:   []*TestFlavor{m1Small, m1Large},
			VMs: []*TestVMUsage{
				// VM with empty AZ (e.g., ERROR or BUILDING state VM not yet scheduled)
				newTestVMUsageWithEmptyAZ("vm-error", m1Large, "project-empty-az", "host-1", baseTime),
				// Normal VM with valid AZ
				newTestVMUsage("vm-ok", m1Large, "project-empty-az", "az-a", "host-2", baseTime.Add(1*time.Hour)),
			},
			Reservations: []*UsageTestReservation{
				// Commitment in az-a
				{CommitmentID: "commit-1", Flavor: m1Small, ProjectID: "project-empty-az", AZ: "az-a", Count: 8},
			},
			AllAZs: []string{"az-a"},
			Expected: map[string]ExpectedResourceUsage{
				"hw_version_hana_1_ram": {
					PerAZ: map[string]ExpectedAZUsage{
						"az-a": {
							Usage: 4,
							VMs: []ExpectedVMUsage{
								{UUID: "vm-ok", CommitmentID: "commit-1", MemoryMB: 4096},
							},
						},
						"unknown": {
							Usage: 4, // VM with empty AZ normalized to "unknown"
							VMs: []ExpectedVMUsage{
								{UUID: "vm-error", CommitmentID: "", MemoryMB: 4096}, // PAYG - no commitment in "unknown" AZ
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runUsageReportTest(t, tc)
		})
	}
}

// ============================================================================
// Test Types
// ============================================================================

type UsageReportTestCase struct {
	Name               string
	ProjectID          string
	UseGET             bool // Use GET instead of POST
	Flavors            []*TestFlavor
	VMs                []*TestVMUsage
	Reservations       []*UsageTestReservation
	AllAZs             []string
	Expected           map[string]ExpectedResourceUsage
	ExpectedStatusCode int // 0 means expect 200 OK
}

// UsageTestReservation represents a commitment reservation for usage tests.
type UsageTestReservation struct {
	CommitmentID string
	Flavor       *TestFlavor
	ProjectID    string
	AZ           string
	Count        int       // Number of reservation slots to create
	StartTime    time.Time // For commitment ordering
}

type TestVMUsage struct {
	UUID      string
	Flavor    *TestFlavor
	ProjectID string
	AZ        string
	Host      string
	CreatedAt time.Time
	OSType    string // pre-computed os_type, e.g. "windows8Server64Guest" or "unknown"
}

func newTestVMUsage(uuid string, flavor *TestFlavor, projectID, az, host string, createdAt time.Time) *TestVMUsage {
	return &TestVMUsage{
		UUID:      uuid,
		Flavor:    flavor,
		ProjectID: projectID,
		AZ:        az,
		Host:      host,
		CreatedAt: createdAt,
	}
}

func newTestVMUsageWithEmptyAZ(uuid string, flavor *TestFlavor, projectID, host string, createdAt time.Time) *TestVMUsage {
	return &TestVMUsage{
		UUID:      uuid,
		Flavor:    flavor,
		ProjectID: projectID,
		AZ:        "", // Empty AZ simulates ERROR/BUILDING state VMs
		Host:      host,
		CreatedAt: createdAt,
	}
}

type ExpectedResourceUsage struct {
	PerAZ map[string]ExpectedAZUsage
}

type ExpectedAZUsage struct {
	Usage uint64 // Usage in multiples of smallest flavor
	VMs   []ExpectedVMUsage
}

type ExpectedVMUsage struct {
	UUID         string
	CommitmentID string  // Empty string = PAYG
	MemoryMB     uint64  // For verification
	VideoRAMMiB  *uint64 // nil = expect field absent
	OSType       string  // Empty string = skip check
}

// ============================================================================
// Mock UsageDBClient
// ============================================================================

type mockUsageDBClient struct {
	rows map[string][]commitments.VMRow // projectID -> rows
	err  error
}

func newMockUsageDBClient() *mockUsageDBClient {
	return &mockUsageDBClient{
		rows: make(map[string][]commitments.VMRow),
	}
}

func (m *mockUsageDBClient) ListProjectVMs(_ context.Context, projectID string) ([]commitments.VMRow, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.rows[projectID], nil
}

func (m *mockUsageDBClient) addVM(vm *TestVMUsage) {
	extraSpecs := map[string]string{
		"quota:hw_version": vm.Flavor.Group,
	}
	if vm.Flavor.VideoRAMMiB != nil {
		extraSpecs["hw_video:ram_max_mb"] = strconv.FormatUint(*vm.Flavor.VideoRAMMiB, 10)
	}
	extrasJSON, _ := json.Marshal(extraSpecs) //nolint:errcheck // test helper, always valid
	osType := vm.OSType
	if osType == "" {
		osType = "unknown"
	}
	row := commitments.VMRow{
		ID:           vm.UUID,
		Name:         vm.UUID,
		Status:       "ACTIVE",
		Created:      vm.CreatedAt.Format(time.RFC3339),
		AZ:           vm.AZ,
		Hypervisor:   vm.Host,
		FlavorName:   vm.Flavor.Name,
		FlavorRAM:    uint64(vm.Flavor.MemoryMB), //nolint:gosec
		FlavorVCPUs:  uint64(vm.Flavor.VCPUs),    //nolint:gosec
		FlavorDisk:   vm.Flavor.DiskGB,
		FlavorExtras: string(extrasJSON),
		OSType:       osType,
	}
	m.rows[vm.ProjectID] = append(m.rows[vm.ProjectID], row)
}

// ============================================================================
// Test Environment for Usage API
// ============================================================================

type UsageTestEnv struct {
	T            *testing.T
	Scheme       *runtime.Scheme
	K8sClient    client.Client
	DBClient     *mockUsageDBClient
	FlavorGroups FlavorGroupsKnowledge
	HTTPServer   *httptest.Server
	API          *HTTPAPI
}

func newUsageTestEnv(
	t *testing.T,
	vms []*TestVMUsage,
	reservations []*UsageTestReservation,
	flavorGroups FlavorGroupsKnowledge,
) *UsageTestEnv {

	t.Helper()

	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := hv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hv1 scheme: %v", err)
	}

	// Convert test reservations to K8s objects
	var k8sReservations []client.Object
	reservationCounters := make(map[string]int)
	for _, tr := range reservations {
		for range tr.Count {
			number := reservationCounters[tr.CommitmentID]
			reservationCounters[tr.CommitmentID]++
			k8sRes := tr.toK8sReservation(number)
			k8sReservations = append(k8sReservations, k8sRes)
		}
	}

	// Create Knowledge CRD
	knowledgeCRD := createKnowledgeCRD(flavorGroups)
	k8sReservations = append(k8sReservations, knowledgeCRD)

	// Create CommittedResource CRDs (one per unique commitment).
	// The usage reconciler writes assignment results into these; CalculateUsage reads them back.
	seenCommitments := make(map[string]bool)
	var crObjects []client.Object
	for _, tr := range reservations {
		if seenCommitments[tr.CommitmentID] {
			continue
		}
		seenCommitments[tr.CommitmentID] = true
		crObjects = append(crObjects, tr.toCommittedResourceCRD())
	}

	k8sReservations = append(k8sReservations, crObjects...)
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(k8sReservations...).
		WithStatusSubresource(&v1alpha1.Reservation{}).
		WithStatusSubresource(&v1alpha1.Knowledge{}).
		WithStatusSubresource(&v1alpha1.CommittedResource{}).
		WithIndex(&v1alpha1.Reservation{}, "spec.type", func(obj client.Object) []string {
			res := obj.(*v1alpha1.Reservation)
			return []string{string(res.Spec.Type)}
		}).
		WithIndex(&v1alpha1.CommittedResource{}, "spec.commitmentUUID", func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok {
				return nil
			}
			return []string{cr.Spec.CommitmentUUID}
		}).
		WithIndex(&v1alpha1.CommittedResource{}, "spec.projectID", func(obj client.Object) []string {
			cr, ok := obj.(*v1alpha1.CommittedResource)
			if !ok || cr.Spec.ProjectID == "" {
				return nil
			}
			return []string{cr.Spec.ProjectID}
		}).
		Build()

	// Create mock DB client with VMs
	dbClient := newMockUsageDBClient()
	for _, vm := range vms {
		dbClient.addVM(vm)
	}

	// Run usage reconciler to populate CommittedResource.Status with VM assignments.
	// CalculateUsage reads from this status, so the API returns the correct commitment assignments.
	if len(crObjects) > 0 {
		rec := &commitments.UsageReconciler{
			Client:  k8sClient,
			Conf:    commitments.UsageReconcilerConfig{CooldownInterval: metav1.Duration{Duration: 0}},
			UsageDB: dbClient,
			Monitor: commitments.NewUsageReconcilerMonitor(),
		}
		ctx := context.Background()
		for _, obj := range crObjects {
			cr := obj.(*v1alpha1.CommittedResource)
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: cr.Name}}
			if _, err := rec.Reconcile(ctx, req); err != nil {
				t.Fatalf("usage reconciler failed for %s: %v", cr.Name, err)
			}
		}
	}

	// Create API with mock DB client
	api := NewAPIWithConfig(k8sClient, commitments.DefaultAPIConfig(), dbClient)
	mux := http.NewServeMux()
	registry := prometheus.NewRegistry()
	api.Init(mux, registry, log.Log)
	httpServer := httptest.NewServer(mux)

	return &UsageTestEnv{
		T:            t,
		Scheme:       scheme,
		K8sClient:    k8sClient,
		DBClient:     dbClient,
		FlavorGroups: flavorGroups,
		HTTPServer:   httpServer,
		API:          api,
	}
}

func (env *UsageTestEnv) Close() {
	if env.HTTPServer != nil {
		env.HTTPServer.Close()
	}
}

func (env *UsageTestEnv) CallReportUsageAPI(projectID string, allAZs []string, useGET bool) (report liquid.ServiceUsageReport, statusCode int) {
	env.T.Helper()

	// Build request body
	reqBody := liquid.ServiceUsageRequest{
		AllAZs: make([]liquid.AvailabilityZone, len(allAZs)),
	}
	for i, az := range allAZs {
		reqBody.AllAZs[i] = liquid.AvailabilityZone(az)
	}
	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		env.T.Fatalf("Failed to marshal request: %v", err)
	}

	// Build URL
	url := env.HTTPServer.URL + "/commitments/v1/projects/" + projectID + "/report-usage"

	method := http.MethodPost
	if useGET {
		method = http.MethodGet
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(reqJSON)) //nolint:noctx
	if err != nil {
		env.T.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		env.T.Fatalf("Failed to make HTTP request: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		env.T.Fatalf("Failed to read response body: %v", err)
	}

	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(respBytes, &report); err != nil {
			env.T.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, string(respBytes))
		}
	}

	return report, resp.StatusCode
}

// ============================================================================
// Test Runner
// ============================================================================

func runUsageReportTest(t *testing.T, tc UsageReportTestCase) {
	t.Helper()

	// Build flavor groups
	var flavorInGroups []compute.FlavorInGroup
	for _, f := range tc.Flavors {
		flavorInGroups = append(flavorInGroups, f.ToFlavorInGroup())
	}
	flavorGroups := TestFlavorGroup{
		infoVersion: 1234,
		flavors:     flavorInGroups,
	}.ToFlavorGroupsKnowledge()

	// Create test environment
	env := newUsageTestEnv(t, tc.VMs, tc.Reservations, flavorGroups)
	defer env.Close()

	// Call API
	report, statusCode := env.CallReportUsageAPI(tc.ProjectID, tc.AllAZs, tc.UseGET)

	// Check status code
	expectedStatus := tc.ExpectedStatusCode
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}
	if statusCode != expectedStatus {
		t.Errorf("Expected status code %d, got %d", expectedStatus, statusCode)
		return
	}

	// If not 200 OK, no need to verify response body
	if expectedStatus != http.StatusOK {
		return
	}

	// Verify response
	verifyUsageReport(t, tc, report, flavorGroups)
}

func verifyUsageReport(t *testing.T, tc UsageReportTestCase, actual liquid.ServiceUsageReport, _ FlavorGroupsKnowledge) {
	t.Helper()

	for resourceName, expectedResource := range tc.Expected {
		// The test uses _ram resources in Expected, but:
		// - _ram resource has usage but NO subresources
		// - _instances resource has usage (count) AND subresources (VM details)
		// So we check _ram for usage and derive _instances for VM subresources

		actualResource, exists := actual.Resources[liquid.ResourceName(resourceName)]
		if !exists {
			t.Errorf("Resource %s not found in response", resourceName)
			continue
		}

		// Derive the instances resource name from the ram resource name
		// hw_version_hana_1_ram -> hw_version_hana_1_instances
		instancesResourceName := resourceName[:len(resourceName)-4] + "_instances" // replace "_ram" with "_instances"
		actualInstancesResource := actual.Resources[liquid.ResourceName(instancesResourceName)]

		for azName, expectedAZ := range expectedResource.PerAZ {
			az := liquid.AvailabilityZone(azName)
			actualAZ, exists := actualResource.PerAZ[az]
			if !exists {
				t.Errorf("AZ %s not found in resource %s", azName, resourceName)
				continue
			}

			// Verify RAM usage
			if actualAZ.Usage != expectedAZ.Usage {
				t.Errorf("Resource %s AZ %s: expected usage %d, got %d",
					resourceName, azName, expectedAZ.Usage, actualAZ.Usage)
			}

			// VM subresources are on the _instances resource, not _ram
			if actualInstancesResource == nil {
				t.Errorf("Instances resource %s not found", instancesResourceName)
				continue
			}
			actualInstancesAZ, exists := actualInstancesResource.PerAZ[az]
			if !exists {
				t.Errorf("AZ %s not found in instances resource %s", azName, instancesResourceName)
				continue
			}

			// Verify VM count
			if len(actualInstancesAZ.Subresources) != len(expectedAZ.VMs) {
				t.Errorf("Resource %s AZ %s: expected %d VMs, got %d",
					instancesResourceName, azName, len(expectedAZ.VMs), len(actualInstancesAZ.Subresources))
				continue
			}

			// Build actual VM map for comparison (parse attributes)
			actualVMs := make(map[string]vmAttributes)
			actualRawVMs := make(map[string]map[string]json.RawMessage) // for checking absent fields
			for _, sub := range actualInstancesAZ.Subresources {
				var attrs vmAttributes
				attrs.ID = sub.ID
				if err := json.Unmarshal(sub.Attributes, &attrs); err != nil {
					t.Errorf("Failed to unmarshal attributes for VM %s: %v", sub.ID, err)
					continue
				}
				actualVMs[sub.ID] = attrs

				var rawAttrs map[string]json.RawMessage
				if err := json.Unmarshal(sub.Attributes, &rawAttrs); err == nil {
					actualRawVMs[sub.ID] = rawAttrs
				}
			}

			// Verify each expected VM
			for _, expectedVM := range expectedAZ.VMs {
				actualVM, exists := actualVMs[expectedVM.UUID]
				if !exists {
					t.Errorf("Resource %s AZ %s: VM %s not found", instancesResourceName, azName, expectedVM.UUID)
					continue
				}

				// Verify commitment assignment
				if actualVM.CommitmentID != expectedVM.CommitmentID {
					if expectedVM.CommitmentID == "" {
						t.Errorf("Resource %s AZ %s VM %s: expected PAYG (empty), got commitment %s",
							instancesResourceName, azName, expectedVM.UUID, actualVM.CommitmentID)
					} else {
						t.Errorf("Resource %s AZ %s VM %s: expected commitment %s, got %s",
							instancesResourceName, azName, expectedVM.UUID, expectedVM.CommitmentID, actualVM.CommitmentID)
					}
				}

				// Verify memory (now nested in flavor)
				if actualVM.Flavor.MemoryMiB != expectedVM.MemoryMB {
					t.Errorf("Resource %s AZ %s VM %s: expected RAM %d MB, got %d MB",
						instancesResourceName, azName, expectedVM.UUID, expectedVM.MemoryMB, actualVM.Flavor.MemoryMiB)
				}

				// Verify video_ram_mib
				if expectedVM.VideoRAMMiB != nil {
					if actualVM.Flavor.VideoMemoryMiB == nil {
						t.Errorf("Resource %s AZ %s VM %s: expected video_ram_mib %d, got nil",
							instancesResourceName, azName, expectedVM.UUID, *expectedVM.VideoRAMMiB)
					} else if *actualVM.Flavor.VideoMemoryMiB != *expectedVM.VideoRAMMiB {
						t.Errorf("Resource %s AZ %s VM %s: expected video_ram_mib %d, got %d",
							instancesResourceName, azName, expectedVM.UUID, *expectedVM.VideoRAMMiB, *actualVM.Flavor.VideoMemoryMiB)
					}
				} else {
					if actualVM.Flavor.VideoMemoryMiB != nil {
						t.Errorf("Resource %s AZ %s VM %s: expected no video_ram_mib, got %d",
							instancesResourceName, azName, expectedVM.UUID, *actualVM.Flavor.VideoMemoryMiB)
					}
				}

				// Verify os_type when specified
				if expectedVM.OSType != "" && actualVM.OSType != expectedVM.OSType {
					t.Errorf("Resource %s AZ %s VM %s: expected os_type %q, got %q",
						instancesResourceName, azName, expectedVM.UUID, expectedVM.OSType, actualVM.OSType)
				}

				// Assert HWVersion is absent from the serialized output (must not appear per LIQUID schema)
				if rawFlavor, ok := actualRawVMs[expectedVM.UUID]; ok {
					if flavorRaw, ok := rawFlavor["flavor"]; ok {
						var flavorMap map[string]json.RawMessage
						if err := json.Unmarshal(flavorRaw, &flavorMap); err == nil {
							if _, present := flavorMap["hw_version"]; present {
								t.Errorf("Resource %s AZ %s VM %s: hw_version must not appear in output (tagged json:\"-\")",
									instancesResourceName, azName, expectedVM.UUID)
							}
						}
					}
				}
			}
		}
	}
}

// vmAttributes is used to parse the subresource attributes JSON.
// Uses the liquid-nova format with nested flavor structure.
type vmAttributes struct {
	ID           string            `json:"-"` // set from Subresource.ID
	Status       string            `json:"status"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Flavor       vmFlavorAttrs     `json:"flavor"`
	OSType       string            `json:"os_type,omitempty"`
	CommitmentID string            `json:"commitment_id,omitempty"`
}

// vmFlavorAttrs is the nested flavor info within vm attributes.
type vmFlavorAttrs struct {
	Name           string  `json:"name"`
	VCPUs          uint64  `json:"vcpu"`
	MemoryMiB      uint64  `json:"ram_mib"`
	DiskGiB        uint64  `json:"disk_gib"`
	VideoMemoryMiB *uint64 `json:"video_ram_mib,omitempty"`
}

// ============================================================================
// Helper Functions
// ============================================================================

// toCommittedResourceCRD creates a minimal CommittedResource CRD for this commitment.
// Used by the test setup to pre-populate the CR objects that the usage reconciler writes status into.
func (tr *UsageTestReservation) toCommittedResourceCRD() *v1alpha1.CommittedResource {
	amount := resource.MustParse(strconv.FormatInt(tr.Flavor.MemoryMB*int64(tr.Count), 10) + "Mi")
	spec := v1alpha1.CommittedResourceSpec{
		CommitmentUUID:   tr.CommitmentID,
		ProjectID:        tr.ProjectID,
		DomainID:         "test-domain",
		AvailabilityZone: tr.AZ,
		FlavorGroupName:  tr.Flavor.Group,
		ResourceType:     v1alpha1.CommittedResourceTypeMemory,
		State:            v1alpha1.CommitmentStatusConfirmed,
		Amount:           amount,
	}
	if !tr.StartTime.IsZero() {
		spec.StartTime = &metav1.Time{Time: tr.StartTime}
	}
	return &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{Name: "cr-" + tr.CommitmentID},
		Spec:       spec,
		Status: v1alpha1.CommittedResourceStatus{
			AcceptedSpec: &spec,
			// Simulate the CR controller having accepted the current generation (0 for fake client).
			// Without this, the usage reconciler's readiness gate blocks usage calculation.
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.CommittedResourceConditionReady,
					Status:             metav1.ConditionTrue,
					Reason:             v1alpha1.CommittedResourceReasonAccepted,
					ObservedGeneration: 0,
					LastTransitionTime: metav1.Now(),
				},
			},
		},
	}
}

// toK8sReservation converts a UsageTestReservation to a K8s Reservation.
func (tr *UsageTestReservation) toK8sReservation(number int) *v1alpha1.Reservation {
	name := fmt.Sprintf("commitment-%s-%d", tr.CommitmentID, number)

	memoryMB := tr.Flavor.MemoryMB

	spec := v1alpha1.ReservationSpec{
		Type: v1alpha1.ReservationTypeCommittedResource,
		Resources: map[hv1.ResourceName]resource.Quantity{
			"memory": resource.MustParse(strconv.FormatInt(memoryMB, 10) + "Mi"),
			"cpu":    resource.MustParse(strconv.FormatInt(tr.Flavor.VCPUs, 10)),
		},
		CommittedResourceReservation: &v1alpha1.CommittedResourceReservationSpec{
			CommitmentUUID: tr.CommitmentID,
			ProjectID:      tr.ProjectID,
			ResourceName:   tr.Flavor.Name,
			ResourceGroup:  tr.Flavor.Group,
			Allocations:    map[string]v1alpha1.CommittedResourceAllocation{},
		},
	}

	if tr.AZ != "" {
		spec.AvailabilityZone = tr.AZ
	}

	// Set StartTime for commitment ordering
	if !tr.StartTime.IsZero() {
		spec.StartTime = &metav1.Time{Time: tr.StartTime}
	}

	status := v1alpha1.ReservationStatus{
		Conditions: []metav1.Condition{
			{
				Type:   v1alpha1.ReservationConditionReady,
				Status: metav1.ConditionTrue,
				Reason: "ReservationActive",
			},
		},
		CommittedResourceReservation: &v1alpha1.CommittedResourceReservationStatus{
			Allocations: map[string]string{},
		},
	}

	labels := map[string]string{
		v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource,
	}

	return &v1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Labels:            labels,
			CreationTimestamp: metav1.Time{Time: tr.StartTime},
		},
		Spec:   spec,
		Status: status,
	}
}
