// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

//nolint:unparam // test helper functions have fixed parameters for simplicity
package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	. "github.com/majewsky/gg/option"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/liquid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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

func TestHandleChangeCommitments(t *testing.T) {
	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}

	testCases := []CommitmentChangeTestCase{
		// --- Basic flow ---
		{
			Name:    "New CR: controller accepts → API returns accepted",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-new", "confirmed", 2)),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-new"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-new": true},
		},
		{
			Name:    "New CR: controller rejects → API returns rejection reason",
			Flavors: []*TestFlavor{m1Small},
			CROutcomes: map[string]string{
				"commitment-uuid-rej": "not sufficient capacity",
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-rej", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse("commitment uuid-rej: not sufficient capacity"),
		},
		// --- Planned state ---
		{
			Name:    "Planned CR: controller sets Ready=False/Planned → API accepts",
			Flavors: []*TestFlavor{m1Small},
			CROutcomes: map[string]string{
				"commitment-uuid-plan": v1alpha1.CommittedResourceReasonPlanned,
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-plan", "planned", 2)),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-plan"},
		},
		// --- Update existing CR ---
		{
			Name:    "Resize up: existing CR updated with new amount, accepted",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-resize", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-resize", "confirmed", 2)),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-resize"},
		},
		// --- Rollback: new CR deleted on batch failure ---
		{
			Name:    "Rollback new CR: newly created CRD deleted on rejection",
			Flavors: []*TestFlavor{m1Small},
			CROutcomes: map[string]string{
				"commitment-uuid-rollback": "not sufficient capacity",
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-rollback", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse("uuid-rollback: not sufficient capacity"),
			ExpectedDeletedCRs:  []string{"commitment-uuid-rollback"},
		},
		// --- Rollback: updated CR spec restored on batch failure ---
		{
			Name:    "Rollback updated CR: spec restored on rejection",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-restore", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CROutcomes: map[string]string{
				"commitment-uuid-restore": "not sufficient capacity",
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-restore", "confirmed", 4)),
			ExpectedAPIResponse: newAPIResponse("uuid-restore: not sufficient capacity"),
			// CRD still exists but amount restored to 1024 MiB
			ExpectedCRSpecs: map[string]int64{"commitment-uuid-restore": 1024 * 1024 * 1024},
		},
		// --- Batch rollback: one failure rolls back all ---
		{
			Name:    "Batch rollback: project-B fails → project-A new CR also rolled back",
			Flavors: []*TestFlavor{m1Small},
			CROutcomes: map[string]string{
				"commitment-uuid-b": "not sufficient capacity",
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-a", "confirmed", 2),
				createCommitment("hw_version_hana_1_ram", "project-B", "uuid-b", "confirmed", 2),
			),
			ExpectedAPIResponse: newAPIResponse("uuid-b: not sufficient capacity"),
			ExpectedDeletedCRs:  []string{"commitment-uuid-a", "commitment-uuid-b"},
		},
		// --- AZ immutability ---
		{
			// AZ is immutable once set on a CommittedResource. Attempting to change it via
			// change-commitments must be rejected immediately, before any polling or controller
			// interaction, and the CR must remain at its original spec.
			Name:    "AZ change on existing CR: must be rejected",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-az-stale", State: v1alpha1.CommitmentStatusConfirmed,
					AmountMiB: 1024, ProjectID: "project-A", AZ: "az-old", ReadyCondition: true},
			},
			CommitmentRequest: newCommitmentRequest("az-new", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-az-stale", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse("cannot change availability zone"),
			// CR spec must not have changed.
			ExpectedCRSpecs: map[string]int64{"commitment-uuid-az-stale": 1024 * 1024 * 1024},
		},
		// --- Timeout ---
		{
			Name:    "Timeout: no condition set → rollback and timeout error",
			Flavors: []*TestFlavor{m1Small},
			CROutcomes: map[string]string{
				"commitment-uuid-timeout": "", // empty string = no condition set (controller not responding)
			},
			NoCondition: []string{"commitment-uuid-timeout"},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-timeout", "confirmed", 2)),
			CustomConfig: func() *commitments.APIConfig {
				cfg := commitments.DefaultAPIConfig()
				cfg.WatchTimeout = metav1.Duration{}
				cfg.WatchPollInterval = metav1.Duration{Duration: 100 * time.Millisecond}
				cfg.FlavorGroupResourceConfig = map[string]commitments.FlavorGroupResourcesConfig{
					"*": {RAM: commitments.ResourceTypeConfig{HandlesCommitments: true, HasCapacity: true}},
				}
				return &cfg
			}(),
			ExpectedAPIResponse: newAPIResponse("timeout reached while processing commitment changes"),
			ExpectedDeletedCRs:  []string{"commitment-uuid-timeout"},
		},
		// --- Input validation ---
		{
			Name:    "Invalid commitment UUID: rejected before CRD write",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", strings.Repeat("x", 50), "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse("unexpected commitment format"),
			ExpectedDeletedCRs:  []string{"commitment-" + strings.Repeat("x", 50)},
		},
		{
			Name:    "Unknown flavor group: rejected without CRD write",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_nonexistent_ram", "project-A", "uuid-unk", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse("flavor group not found"),
		},
		// --- Infrastructure ---
		{
			Name:    "Version mismatch: 409 Conflict",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 9999,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-v", "confirmed", 2)),
			EnvInfoVersion:      1234, // env is at 1234, request claims 9999 → mismatch
			ExpectedAPIResponse: APIResponseExpectation{StatusCode: 409},
		},
		{
			Name:    "API disabled: 503 Service Unavailable",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-dis", "confirmed", 2)),
			CustomConfig: func() *commitments.APIConfig {
				cfg := commitments.DefaultAPIConfig()
				cfg.EnableChangeCommitments = false
				return &cfg
			}(),
			ExpectedAPIResponse: APIResponseExpectation{StatusCode: 503},
		},
		{
			Name:    "Knowledge not ready: 503 Service Unavailable",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-kr", "confirmed", 2)),
			EnvInfoVersion:      -1, // skip Knowledge CRD creation
			ExpectedAPIResponse: APIResponseExpectation{StatusCode: 503},
		},
		{
			Name:    "Dry run: not supported yet",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", true, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-dry", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse("Dry run not supported"),
		},
		{
			Name:                "Empty request: no CRDs created",
			Flavors:             []*TestFlavor{m1Small},
			CommitmentRequest:   newCommitmentRequest("az-a", false, 1234),
			ExpectedAPIResponse: newAPIResponse(),
		},
		// --- Deletion ---
		{
			Name:    "Deletion: existing CRD is deleted",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-del", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				deleteCommitment("hw_version_hana_1_ram", "project-A", "uuid-del", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse(),
			ExpectedDeletedCRs:  []string{"commitment-uuid-del"},
		},
		{
			Name:    "Deletion: non-existing CRD is a no-op",
			Flavors: []*TestFlavor{m1Small},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				deleteCommitment("hw_version_hana_1_ram", "project-A", "uuid-absent", "confirmed", 2)),
			ExpectedAPIResponse: newAPIResponse(),
		},
		{
			Name:    "Deletion rollback: delete succeeds but later commitment fails → CRD re-created",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-del-rb", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CROutcomes: map[string]string{
				"commitment-uuid-new-rb": "not enough capacity",
			},
			// project-A deletion sorts before project-B creation; deletion succeeds then creation fails.
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				deleteCommitment("hw_version_hana_1_ram", "project-A", "uuid-del-rb", "confirmed", 2),
				createCommitment("hw_version_hana_1_ram", "project-B", "uuid-new-rb", "confirmed", 2),
			),
			ExpectedAPIResponse:    newAPIResponse("not enough capacity"),
			ExpectedCreatedCRNames: []string{"commitment-uuid-del-rb"}, // re-created during rollback
		},
		// --- Non-confirming changes (RequiresConfirmation=false → AllowRejection=false, no watch) ---
		{
			Name:    "Non-confirming: guaranteed→confirmed, AllowRejection=false, watch skipped",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-guar", State: v1alpha1.CommitmentStatusGuaranteed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			// Controller would reject, but we skip watching for non-confirming changes.
			CROutcomes: map[string]string{"commitment-uuid-guar": "not enough capacity"},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				TestCommitment{
					ResourceName:   "hw_version_hana_1_ram",
					ProjectID:      "project-A",
					ConfirmationID: "uuid-guar",
					OldState:       "guaranteed",
					State:          "confirmed",
					Amount:         2,
				}),
			ExpectedAPIResponse:    newAPIResponse(), // no rejection even though controller would reject
			ExpectedCreatedCRNames: []string{"commitment-uuid-guar"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-guar": false},
		},
		{
			Name:    "Non-confirming: planned, AllowRejection=false",
			Flavors: []*TestFlavor{m1Small},
			// CROutcomes not set: controller accepts (irrelevant since watch is skipped).
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-plan-nc", "planned", 2)),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-plan-nc"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-plan-nc": false},
		},
		// --- Pending state ---
		{
			Name:    "None→pending: non-confirming, AllowRejection=false, watch skipped",
			Flavors: []*TestFlavor{m1Small},
			// pending creates Reservation slots (like confirmed) but RequiresConfirmation=false.
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-pend", "pending", 2)),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-pend"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-pend": false},
			ExpectedCRSpecs:        map[string]int64{"commitment-uuid-pend": 2 * 1024 * 1024 * 1024},
		},
		// --- Inactive state transitions via upsert ---
		{
			Name:    "confirmed→expired: non-confirming upsert, AllowRejection=false, watch skipped",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-to-exp", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				TestCommitment{
					ResourceName:   "hw_version_hana_1_ram",
					ProjectID:      "project-A",
					ConfirmationID: "uuid-to-exp",
					OldState:       "confirmed",
					State:          "expired",
					Amount:         1,
				}),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-to-exp"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-to-exp": false},
			ExpectedCRSpecs:        map[string]int64{"commitment-uuid-to-exp": 0},
		},
		{
			Name:    "confirmed→superseded: confirming upsert, AllowRejection=true, controller accepts",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-to-sup", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			// confirmed→superseded is a confirming change (not in the liquid API's free-transition list).
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				TestCommitment{
					ResourceName:   "hw_version_hana_1_ram",
					ProjectID:      "project-A",
					ConfirmationID: "uuid-to-sup",
					OldState:       "confirmed",
					State:          "superseded",
					Amount:         1,
				}),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-to-sup"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-to-sup": true},
			ExpectedCRSpecs:        map[string]int64{"commitment-uuid-to-sup": 0},
		},
		// --- Resize ---
		{
			Name:    "Resize down: confirmed→confirmed with less capacity, RequiresConfirmation=true",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-dn", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 4 * 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				TestCommitment{
					ResourceName:   "hw_version_hana_1_ram",
					ProjectID:      "project-A",
					ConfirmationID: "uuid-dn",
					OldState:       "confirmed",
					OldAmount:      4,
					State:          "confirmed",
					Amount:         2,
				}),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedCreatedCRNames: []string{"commitment-uuid-dn"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-dn": true},
			ExpectedCRSpecs:        map[string]int64{"commitment-uuid-dn": 2 * 1024 * 1024 * 1024},
		},
		// --- Mixed batch success ---
		{
			Name:    "Mixed batch: delete + create both succeed without rollback",
			Flavors: []*TestFlavor{m1Small},
			ExistingCRs: []*TestCR{
				{CommitmentUUID: "uuid-mbdel", State: v1alpha1.CommitmentStatusConfirmed, AmountMiB: 1024, ProjectID: "project-A", AZ: "az-a"},
			},
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				deleteCommitment("hw_version_hana_1_ram", "project-A", "uuid-mbdel", "confirmed", 1),
				createCommitment("hw_version_hana_1_ram", "project-B", "uuid-mbnew", "confirmed", 2),
			),
			ExpectedAPIResponse:    newAPIResponse(),
			ExpectedDeletedCRs:     []string{"commitment-uuid-mbdel"},
			ExpectedCreatedCRNames: []string{"commitment-uuid-mbnew"},
			ExpectedAllowRejection: map[string]bool{"commitment-uuid-mbnew": true},
		},
		// --- Pre-write validation failure rollback ---
		{
			Name:    "Pre-write validation failure: first CR written then rolled back on second CR's unknown flavor group",
			Flavors: []*TestFlavor{m1Small},
			// project-A (valid) sorts before project-B (invalid): A's CR is written, then B's
			// unknown flavor group triggers a pre-watch rollback that deletes A's CR.
			CommitmentRequest: newCommitmentRequest("az-a", false, 1234,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-pva", "confirmed", 2),
				createCommitment("hw_version_nonexistent_ram", "project-B", "uuid-pvb", "confirmed", 2),
			),
			ExpectedAPIResponse: newAPIResponse("flavor group not found"),
			ExpectedDeletedCRs:  []string{"commitment-uuid-pva"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			runChangeCommitmentsTest(t, tc)
		})
	}
}

func runChangeCommitmentsTest(t *testing.T, tc CommitmentChangeTestCase) {
	t.Helper()

	env := newCRTestEnv(t, tc)
	defer env.Close()

	reqJSON := buildRequestJSON(tc.CommitmentRequest)
	resp, _, statusCode := env.CallChangeCommitmentsAPI(reqJSON)

	env.VerifyAPIResponse(tc.ExpectedAPIResponse, resp, statusCode)

	if len(tc.ExpectedCreatedCRNames) > 0 {
		env.VerifyCRsExist(tc.ExpectedCreatedCRNames)
	}
	if tc.ExpectedAllowRejection != nil {
		env.VerifyAllowRejection(tc.ExpectedAllowRejection)
	}
	for crName, expectedAmountBytes := range tc.ExpectedCRSpecs {
		env.VerifyCRAmountBytes(crName, expectedAmountBytes)
	}
	for _, crName := range tc.ExpectedDeletedCRs {
		env.VerifyCRAbsent(crName)
	}
}

// ============================================================================
// Test Types
// ============================================================================

const (
	defaultFlavorDiskGB       = 40
	flavorGroupsKnowledgeName = "flavor-groups"
	knowledgeRecencyDuration  = 60 * time.Second
)

type CommitmentChangeTestCase struct {
	Name    string
	Flavors []*TestFlavor
	// ExistingCRs: CommittedResource CRDs present before the API call.
	ExistingCRs []*TestCR
	// CROutcomes: what condition the fake controller sets per crName.
	// Value = rejection reason if non-empty and not a named reason constant.
	// Value = CommittedResourceReasonPlanned to simulate a planned outcome.
	// Absent entry = controller accepts (Ready=True).
	CROutcomes map[string]string
	// NoCondition: crNames for which the fake controller sets no condition (simulate stall/timeout).
	NoCondition         []string
	CommitmentRequest   CommitmentChangeRequest
	ExpectedAPIResponse APIResponseExpectation
	// Post-call assertions.
	ExpectedCreatedCRNames []string
	ExpectedAllowRejection map[string]bool  // crName → expected AllowRejection value
	ExpectedCRSpecs        map[string]int64 // crName → expected Amount.Value() in bytes
	ExpectedDeletedCRs     []string
	CustomConfig           *commitments.APIConfig
	EnvInfoVersion         int64
}

// TestCR defines a pre-existing CommittedResource CRD.
type TestCR struct {
	CommitmentUUID string
	State          v1alpha1.CommitmentStatus
	AmountMiB      int64
	ProjectID      string
	AZ             string
	// ReadyCondition pre-sets Ready=True (Generation=1, ObservedGeneration=1) on the CR to simulate
	// a CR that was previously accepted. Use together with NoCondition to test that the polling loop
	// does not treat this stale condition as a valid outcome for a subsequent spec update.
	ReadyCondition bool
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
	OldState       string // empty = None (no prior status)
	State          string // empty = None (deletion)
	Amount         uint64
	OldAmount      uint64 // if non-zero, used for TotalBefore totals instead of Amount (for resize-down)
}

type APIResponseExpectation struct {
	StatusCode             int
	RejectReasonSubstrings []string
}

type TestFlavor struct {
	Name        string
	Group       string
	MemoryMB    int64
	VCPUs       int64
	DiskGB      uint64
	VideoRAMMiB *uint64
}

func (f *TestFlavor) ToFlavorInGroup() compute.FlavorInGroup {
	diskGB := f.DiskGB
	if diskGB == 0 {
		diskGB = defaultFlavorDiskGB
	}
	extraSpecs := map[string]string{
		"quota:hw_version": f.Group,
	}
	if f.VideoRAMMiB != nil {
		extraSpecs["hw_video:ram_max_mb"] = strconv.FormatUint(*f.VideoRAMMiB, 10)
	}
	return compute.FlavorInGroup{
		Name:       f.Name,
		MemoryMB:   uint64(f.MemoryMB), //nolint:gosec // test values are always positive
		VCPUs:      uint64(f.VCPUs),    //nolint:gosec // test values are always positive
		DiskGB:     diskGB,
		ExtraSpecs: extraSpecs,
	}
}

type FlavorGroupsKnowledge struct {
	InfoVersion int64
	Groups      []compute.FlavorGroupFeature
}

// TestFlavorGroup groups a flat list of FlavorInGroup by hw_version extra spec
// and builds a FlavorGroupsKnowledge. Used by usage_test.go and report_usage_test.go.
type TestFlavorGroup struct {
	infoVersion int64
	flavors     []compute.FlavorInGroup
}

func (tg TestFlavorGroup) ToFlavorGroupsKnowledge() FlavorGroupsKnowledge {
	groupMap := make(map[string][]compute.FlavorInGroup)
	for _, f := range tg.flavors {
		name := f.ExtraSpecs["quota:hw_version"]
		groupMap[name] = append(groupMap[name], f)
	}

	sortedNames := make([]string, 0, len(groupMap))
	for n := range groupMap {
		sortedNames = append(sortedNames, n)
	}
	sort.Strings(sortedNames)

	var groups []compute.FlavorGroupFeature
	for _, name := range sortedNames {
		gFlavors := groupMap[name]
		sort.Slice(gFlavors, func(i, j int) bool { return gFlavors[i].MemoryMB > gFlavors[j].MemoryMB })
		smallest := gFlavors[len(gFlavors)-1]
		largest := gFlavors[0]

		var minR, maxR uint64 = ^uint64(0), 0
		for _, f := range gFlavors {
			if f.VCPUs == 0 {
				continue
			}
			r := f.MemoryMB / f.VCPUs
			if r < minR {
				minR = r
			}
			if r > maxR {
				maxR = r
			}
		}
		var ratio, ratioMin, ratioMax *uint64
		if minR == maxR && maxR != 0 {
			ratio = &minR
		} else if maxR != 0 {
			ratioMin = &minR
			ratioMax = &maxR
		}
		groups = append(groups, compute.FlavorGroupFeature{
			Name:            name,
			Flavors:         gFlavors,
			SmallestFlavor:  smallest,
			LargestFlavor:   largest,
			RamCoreRatio:    ratio,
			RamCoreRatioMin: ratioMin,
			RamCoreRatioMax: ratioMax,
		})
	}
	return FlavorGroupsKnowledge{InfoVersion: tg.infoVersion, Groups: groups}
}

// ============================================================================
// Fake Controller Client
// ============================================================================

// fakeControllerClient wraps a client.Client and simulates the CommittedResource
// controller by immediately setting conditions after any Create or Update of a
// CommittedResource CRD. Entries in noCondition suppress condition-setting to
// simulate a stalled controller (used for timeout tests).
type fakeControllerClient struct {
	client.Client
	outcomes    map[string]string // crName → rejection reason (or reason constant); absent = accept
	noCondition map[string]struct{}
	mu          sync.Mutex
}

func (c *fakeControllerClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if cr, ok := obj.(*v1alpha1.CommittedResource); ok {
		cr.Generation = 1 // k8s sets generation=1 on first creation
	}
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if cr, ok := obj.(*v1alpha1.CommittedResource); ok {
		c.setConditionFor(ctx, cr.Name)
	}
	return nil
}

func (c *fakeControllerClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if cr, ok := obj.(*v1alpha1.CommittedResource); ok {
		// k8s increments generation on each spec change; simulate that here so the
		// polling loop can detect stale conditions from a prior generation.
		existing := &v1alpha1.CommittedResource{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(cr), existing); err == nil {
			cr.Generation = existing.Generation + 1
		}
	}
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if cr, ok := obj.(*v1alpha1.CommittedResource); ok {
		c.setConditionFor(ctx, cr.Name)
	}
	return nil
}

func (c *fakeControllerClient) setConditionFor(ctx context.Context, crName string) {
	c.mu.Lock()
	_, skip := c.noCondition[crName]
	outcome, hasOutcome := c.outcomes[crName]
	c.mu.Unlock()

	if skip {
		return
	}

	cr := &v1alpha1.CommittedResource{}
	if err := c.Get(ctx, client.ObjectKey{Name: crName}, cr); err != nil {
		return
	}

	var cond metav1.Condition
	switch {
	case !hasOutcome || outcome == "":
		// Default: controller accepts.
		cond = metav1.Condition{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.CommittedResourceReasonAccepted,
			Message:            "accepted",
			ObservedGeneration: cr.Generation,
		}
	case outcome == v1alpha1.CommittedResourceReasonPlanned:
		cond = metav1.Condition{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.CommittedResourceReasonPlanned,
			Message:            "commitment is not yet active",
			ObservedGeneration: cr.Generation,
		}
	default:
		cond = metav1.Condition{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.CommittedResourceReasonRejected,
			Message:            outcome,
			ObservedGeneration: cr.Generation,
		}
	}

	meta.SetStatusCondition(&cr.Status.Conditions, cond)
	if err := c.Client.Status().Update(ctx, cr); err != nil {
		return // best-effort: if the update races with another write, the polling loop retries
	}
}

// ============================================================================
// Test Environment
// ============================================================================

type CRTestEnv struct {
	T          *testing.T
	K8sClient  client.Client
	HTTPServer *httptest.Server
}

func newCRTestEnv(t *testing.T, tc CommitmentChangeTestCase) *CRTestEnv {
	t.Helper()
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}

	objects := make([]client.Object, 0)

	// Knowledge CRD (InfoVersion=-1 simulates "not ready").
	envInfoVersion := tc.CommitmentRequest.InfoVersion
	if tc.EnvInfoVersion != 0 {
		envInfoVersion = tc.EnvInfoVersion
	}
	if envInfoVersion != -1 {
		objects = append(objects, createKnowledgeCRD(buildFlavorGroupsKnowledge(tc.Flavors, envInfoVersion)))
	}

	// Pre-existing CommittedResource CRDs.
	for _, tcr := range tc.ExistingCRs {
		objects = append(objects, tcr.toCommittedResource())
	}

	baseClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.CommittedResource{}, &v1alpha1.Knowledge{}).
		WithIndex(&v1alpha1.Reservation{}, "spec.committedResourceReservation.commitmentUUID", func(obj client.Object) []string {
			res, ok := obj.(*v1alpha1.Reservation)
			if !ok || res.Spec.CommittedResourceReservation == nil {
				return nil
			}
			return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
		}).
		Build()

	noCondition := make(map[string]struct{})
	for _, name := range tc.NoCondition {
		noCondition[name] = struct{}{}
	}

	wrapped := &fakeControllerClient{
		Client:      baseClient,
		outcomes:    tc.CROutcomes,
		noCondition: noCondition,
	}

	var api *HTTPAPI
	if tc.CustomConfig != nil {
		api = NewAPIWithConfig(wrapped, *tc.CustomConfig, nil)
	} else {
		// Default test config: all flavor groups accept RAM commitments via wildcard.
		cfg := commitments.DefaultAPIConfig()
		cfg.FlavorGroupResourceConfig = map[string]commitments.FlavorGroupResourcesConfig{
			"*": {
				RAM:       commitments.ResourceTypeConfig{HandlesCommitments: true, HasCapacity: true},
				Cores:     commitments.ResourceTypeConfig{HasCapacity: true},
				Instances: commitments.ResourceTypeConfig{HasCapacity: true},
			},
		}
		api = NewAPIWithConfig(wrapped, cfg, nil)
	}
	mux := http.NewServeMux()
	registry := prometheus.NewRegistry()
	api.Init(mux, registry, log.Log)

	return &CRTestEnv{
		T:          t,
		K8sClient:  wrapped,
		HTTPServer: httptest.NewServer(mux),
	}
}

func (env *CRTestEnv) Close() {
	if env.HTTPServer != nil {
		env.HTTPServer.Close()
	}
}

func (env *CRTestEnv) CallChangeCommitmentsAPI(reqJSON string) (resp liquid.CommitmentChangeResponse, respBody string, statusCode int) {
	env.T.Helper()
	url := env.HTTPServer.URL + "/commitments/v1/change-commitments"
	httpResp, err := http.Post(url, "application/json", bytes.NewReader([]byte(reqJSON))) //nolint:gosec,noctx
	if err != nil {
		env.T.Fatalf("HTTP request failed: %v", err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		env.T.Fatalf("failed to read response: %v", err)
	}
	if httpResp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &resp); err != nil {
			env.T.Fatalf("failed to unmarshal response: %v", err)
		}
	}
	return resp, string(raw), httpResp.StatusCode
}

func (env *CRTestEnv) VerifyAPIResponse(expected APIResponseExpectation, resp liquid.CommitmentChangeResponse, statusCode int) {
	env.T.Helper()
	expectedCode := expected.StatusCode
	if expectedCode == 0 {
		expectedCode = http.StatusOK
	}
	if statusCode != expectedCode {
		env.T.Errorf("expected status %d, got %d", expectedCode, statusCode)
	}
	for _, sub := range expected.RejectReasonSubstrings {
		if !strings.Contains(resp.RejectionReason, sub) {
			env.T.Errorf("rejection reason %q does not contain %q", resp.RejectionReason, sub)
		}
	}
}

func (env *CRTestEnv) VerifyCRsExist(names []string) {
	env.T.Helper()
	for _, name := range names {
		cr := &v1alpha1.CommittedResource{}
		if err := env.K8sClient.Get(context.Background(), client.ObjectKey{Name: name}, cr); err != nil {
			env.T.Errorf("expected CommittedResource %q to exist, but got: %v", name, err)
		}
	}
}

func (env *CRTestEnv) VerifyCRAbsent(name string) {
	env.T.Helper()
	cr := &v1alpha1.CommittedResource{}
	err := env.K8sClient.Get(context.Background(), client.ObjectKey{Name: name}, cr)
	if err == nil {
		env.T.Errorf("expected CommittedResource %q to be absent after rollback, but it still exists", name)
	} else if !apierrors.IsNotFound(err) {
		env.T.Errorf("unexpected error checking if CommittedResource %q is absent: %v", name, err)
	}
}

func (env *CRTestEnv) VerifyAllowRejection(expected map[string]bool) {
	env.T.Helper()
	for crName, want := range expected {
		cr := &v1alpha1.CommittedResource{}
		if err := env.K8sClient.Get(context.Background(), client.ObjectKey{Name: crName}, cr); err != nil {
			env.T.Errorf("CommittedResource %q not found: %v", crName, err)
			continue
		}
		if cr.Spec.AllowRejection != want {
			env.T.Errorf("CommittedResource %q: AllowRejection=%v, want %v", crName, cr.Spec.AllowRejection, want)
		}
	}
}

func (env *CRTestEnv) VerifyCRAmountBytes(crName string, wantBytes int64) {
	env.T.Helper()
	cr := &v1alpha1.CommittedResource{}
	if err := env.K8sClient.Get(context.Background(), client.ObjectKey{Name: crName}, cr); err != nil {
		env.T.Errorf("CommittedResource %q not found: %v", crName, err)
		return
	}
	got := cr.Spec.Amount.Value()
	if got != wantBytes {
		env.T.Errorf("CommittedResource %q: Amount=%d bytes, want %d bytes", crName, got, wantBytes)
	}
}

// ============================================================================
// TestCR → v1alpha1.CommittedResource
// ============================================================================

func (tc *TestCR) toCommittedResource() *v1alpha1.CommittedResource {
	amount := resource.NewQuantity(tc.AmountMiB*1024*1024, resource.BinarySI)
	cr := &v1alpha1.CommittedResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: "commitment-" + tc.CommitmentUUID,
		},
		Spec: v1alpha1.CommittedResourceSpec{
			CommitmentUUID:   tc.CommitmentUUID,
			FlavorGroupName:  "hana_1",
			ResourceType:     v1alpha1.CommittedResourceTypeMemory,
			Amount:           *amount,
			AvailabilityZone: tc.AZ,
			ProjectID:        tc.ProjectID,
			State:            tc.State,
		},
	}
	if tc.ReadyCondition {
		cr.Generation = 1
		meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.CommittedResourceReasonAccepted,
			Message:            "accepted",
			LastTransitionTime: metav1.Now(),
			ObservedGeneration: 1,
		})
	}
	return cr
}

// ============================================================================
// Request / Response helpers
// ============================================================================

func newAPIResponse(rejectSubstrings ...string) APIResponseExpectation {
	return APIResponseExpectation{
		StatusCode:             http.StatusOK,
		RejectReasonSubstrings: rejectSubstrings,
	}
}

func newCommitmentRequest(az string, dryRun bool, infoVersion int64, commitments ...TestCommitment) CommitmentChangeRequest {
	return CommitmentChangeRequest{
		AZ:          az,
		DryRun:      dryRun,
		InfoVersion: infoVersion,
		Commitments: commitments,
	}
}

func createCommitment(resourceName, projectID, uuid, state string, amount uint64, _ ...string) TestCommitment {
	return TestCommitment{
		ResourceName:   liquid.ResourceName(resourceName),
		ProjectID:      projectID,
		ConfirmationID: uuid,
		State:          state,
		Amount:         amount,
	}
}

// deleteCommitment builds a TestCommitment representing a removal (OldStatus=oldState, NewStatus=None).
func deleteCommitment(resourceName, projectID, uuid, oldState string, amount uint64) TestCommitment {
	return TestCommitment{
		ResourceName:   liquid.ResourceName(resourceName),
		ProjectID:      projectID,
		ConfirmationID: uuid,
		OldState:       oldState,
		State:          "", // NewStatus = None
		Amount:         amount,
	}
}

func buildRequestJSON(req CommitmentChangeRequest) string {
	byProject := make(map[liquid.ProjectUUID]liquid.ProjectCommitmentChangeset)
	for _, tc := range req.Commitments {
		pid := liquid.ProjectUUID(tc.ProjectID)
		if byProject[pid].ByResource == nil {
			byProject[pid] = liquid.ProjectCommitmentChangeset{
				ByResource: make(map[liquid.ResourceName]liquid.ResourceCommitmentChangeset),
			}
		}
		var oldStatus Option[liquid.CommitmentStatus]
		if tc.OldState != "" {
			oldStatus = Some(liquid.CommitmentStatus(tc.OldState))
		} else {
			oldStatus = None[liquid.CommitmentStatus]()
		}
		var newStatus Option[liquid.CommitmentStatus]
		if tc.State != "" {
			newStatus = Some(liquid.CommitmentStatus(tc.State))
		} else {
			newStatus = None[liquid.CommitmentStatus]()
		}
		commitment := liquid.Commitment{
			UUID:      liquid.CommitmentUUID(tc.ConfirmationID),
			Amount:    tc.Amount,
			OldStatus: oldStatus,
			NewStatus: newStatus,
			ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
		}
		byResource := byProject[pid].ByResource[tc.ResourceName]
		byResource.Commitments = append(byResource.Commitments, commitment)

		// Compute per-resource totals so RequiresConfirmation() behaves correctly.
		// OldAmount overrides Amount for TotalBefore (resize-down: old amount != new amount).
		oldAmt := tc.Amount
		if tc.OldAmount != 0 {
			oldAmt = tc.OldAmount
		}
		if oldStatus == Some(liquid.CommitmentStatusConfirmed) {
			byResource.TotalConfirmedBefore += oldAmt
		}
		if newStatus == Some(liquid.CommitmentStatusConfirmed) {
			byResource.TotalConfirmedAfter += tc.Amount
		}
		if oldStatus == Some(liquid.CommitmentStatusGuaranteed) {
			byResource.TotalGuaranteedBefore += oldAmt
		}
		if newStatus == Some(liquid.CommitmentStatusGuaranteed) {
			byResource.TotalGuaranteedAfter += tc.Amount
		}

		byProject[pid].ByResource[tc.ResourceName] = byResource
	}

	request := liquid.CommitmentChangeRequest{
		InfoVersion: req.InfoVersion,
		AZ:          liquid.AvailabilityZone(req.AZ),
		DryRun:      req.DryRun,
		ByProject:   byProject,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		panic("failed to marshal request: " + err.Error())
	}
	return string(raw)
}

// ============================================================================
// FlavorGroup Knowledge helpers
// ============================================================================

func buildFlavorGroupsKnowledge(flavors []*TestFlavor, infoVersion int64) FlavorGroupsKnowledge {
	groupMap := make(map[string][]compute.FlavorInGroup)
	for _, f := range flavors {
		groupMap[f.Group] = append(groupMap[f.Group], f.ToFlavorInGroup())
	}

	sortedNames := make([]string, 0, len(groupMap))
	for n := range groupMap {
		sortedNames = append(sortedNames, n)
	}
	sort.Strings(sortedNames)

	var groups []compute.FlavorGroupFeature
	for _, name := range sortedNames {
		gFlavors := groupMap[name]
		sort.Slice(gFlavors, func(i, j int) bool { return gFlavors[i].MemoryMB > gFlavors[j].MemoryMB })

		smallest := gFlavors[len(gFlavors)-1]
		largest := gFlavors[0]

		var minR, maxR uint64 = ^uint64(0), 0
		for _, f := range gFlavors {
			if f.VCPUs == 0 {
				continue
			}
			r := f.MemoryMB / f.VCPUs
			if r < minR {
				minR = r
			}
			if r > maxR {
				maxR = r
			}
		}
		var ratio, ratioMin, ratioMax *uint64
		if minR == maxR && maxR != 0 {
			ratio = &minR
		} else if maxR != 0 {
			ratioMin = &minR
			ratioMax = &maxR
		}
		groups = append(groups, compute.FlavorGroupFeature{
			Name:            name,
			Flavors:         gFlavors,
			SmallestFlavor:  smallest,
			LargestFlavor:   largest,
			RamCoreRatio:    ratio,
			RamCoreRatioMin: ratioMin,
			RamCoreRatioMax: ratioMax,
		})
	}
	return FlavorGroupsKnowledge{InfoVersion: infoVersion, Groups: groups}
}

func createKnowledgeCRD(fgk FlavorGroupsKnowledge) *v1alpha1.Knowledge {
	raw, err := v1alpha1.BoxFeatureList(fgk.Groups)
	if err != nil {
		panic("failed to box flavor group features: " + err.Error())
	}

	lastChange := metav1.NewTime(time.Unix(fgk.InfoVersion, 0))
	return &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{
			Name: flavorGroupsKnowledgeName,
		},
		Spec: v1alpha1.KnowledgeSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
		},
		Status: v1alpha1.KnowledgeStatus{
			Conditions:        []metav1.Condition{{Type: v1alpha1.KnowledgeConditionReady, Status: metav1.ConditionTrue, Reason: "Extracted"}},
			Raw:               raw,
			LastContentChange: lastChange,
		},
	}
}

// ============================================================================
// MockVMSource (kept for compatibility with handler.go / report_usage tests)
// ============================================================================

type MockVMSource struct {
	vms []VM
	mu  sync.Mutex
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

func NewMockVMSource(vms []VM) *MockVMSource {
	return &MockVMSource{vms: vms}
}

func (m *MockVMSource) ListVMs(_ context.Context) ([]VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]VM, len(m.vms))
	copy(result, m.vms)
	return result, nil
}

// ============================================================================
// TestVM (kept for tests in other files that still use it)
// ============================================================================

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
