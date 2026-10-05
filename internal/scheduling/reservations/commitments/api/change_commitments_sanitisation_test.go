// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	commitments "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/commitments"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// ============================================================================
// Error-injecting client wrappers
// ============================================================================

// errInjectClient wraps a client.Client and returns a predefined error for CommittedResource
// Create calls, simulating k8s API or multicluster routing failures at the write path.
type errInjectClient struct {
	client.Client
	crCreateErr error // returned on CommittedResource Create
}

func (c *errInjectClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*v1alpha1.CommittedResource); ok && c.crCreateErr != nil {
		return c.crCreateErr
	}
	return c.Client.Create(ctx, obj, opts...)
}

// rejectAllCRClient wraps a client.Client and immediately sets Ready=False/Rejected on any
// CommittedResource that is successfully created. Used for dry-run tests where probe CR names
// are derived from the HTTP request ID and therefore unknown in advance.
type rejectAllCRClient struct {
	client.Client
}

func (c *rejectAllCRClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if cr, ok := obj.(*v1alpha1.CommittedResource); ok {
		cr.Generation = 1 // simulate k8s generation=1 on first creation
	}
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if cr, ok := obj.(*v1alpha1.CommittedResource); ok {
		cr2 := cr.DeepCopy()
		apimeta.SetStatusCondition(&cr2.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.CommittedResourceConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.CommittedResourceReasonRejected,
			Message:            "no capacity available on any host: detailed internal info goes here",
			ObservedGeneration: cr2.Generation,
		})
		if err := c.Client.Status().Update(ctx, cr2); err != nil {
			_ = err // best-effort; polling loop handles the miss
		}
	}
	return nil
}

// ============================================================================
// Sanitisation test environment
// ============================================================================

// sanitisationEnv bundles a CRTestEnv (for HTTP calls and k8s assertions) with a Prometheus
// registry so metric values can be inspected after each request.
type sanitisationEnv struct {
	te       *CRTestEnv
	registry *prometheus.Registry
}

// newSanitisationEnv builds an HTTPAPI backed by the provided k8s client, registers it on a
// fresh Prometheus registry, and starts a test HTTP server. The server is closed via t.Cleanup.
func newSanitisationEnv(t *testing.T, k8sClient client.Client, cfg *commitments.APIConfig) *sanitisationEnv {
	t.Helper()
	if cfg == nil {
		c := commitments.DefaultAPIConfig()
		c.FlavorGroupResourceConfig = map[string]commitments.FlavorGroupResourcesConfig{
			"*": {
				RAM: commitments.RAMResourceTypeConfig{
					HandlesCommitments: true,
					HandlesDryRun:      true,
					HasCapacity:        true,
				},
			},
		}
		cfg = &c
	}
	api := NewAPIWithConfig(k8sClient, *cfg, nil)
	registry := prometheus.NewRegistry()
	mux := http.NewServeMux()
	api.Init(mux, registry, log.Log)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &sanitisationEnv{
		te:       &CRTestEnv{T: t, K8sClient: k8sClient, HTTPServer: server},
		registry: registry,
	}
}

// buildFakeK8sClientForSanitisation creates a fake k8s client pre-populated with the knowledge CRD.
func buildFakeK8sClientForSanitisation(t *testing.T, flavors []*TestFlavor, infoVersion int64) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(createKnowledgeCRD(buildFlavorGroupsKnowledge(flavors, infoVersion))).
		WithStatusSubresource(&v1alpha1.CommittedResource{}, &v1alpha1.Knowledge{}).
		WithIndex(&v1alpha1.Reservation{}, "spec.committedResourceReservation.commitmentUUID",
			func(obj client.Object) []string {
				res, ok := obj.(*v1alpha1.Reservation)
				if !ok || res.Spec.CommittedResourceReservation == nil {
					return nil
				}
				return []string{res.Spec.CommittedResourceReservation.CommitmentUUID}
			}).
		Build()
}

// ============================================================================
// Prometheus metric helpers
// ============================================================================

// getCounterValueForSanitisation reads a prometheus counter value for the given metric name and
// label set. All entries in wantLabels must match; additional labels on the metric are ignored.
func getCounterValueForSanitisation(t *testing.T, registry *prometheus.Registry, metricName string, wantLabels map[string]string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, m := range family.Metric {
			if sanitisationAllLabelsMatch(m.Label, wantLabels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Errorf("metric %q with labels %v not found in registry", metricName, wantLabels)
	return 0
}

func sanitisationAllLabelsMatch(got []*dto.LabelPair, want map[string]string) bool {
	matched := 0
	for _, lp := range got {
		if v, ok := want[lp.GetName()]; ok && v == lp.GetValue() {
			matched++
		}
	}
	return matched == len(want)
}

// ============================================================================
// Tests — sanitised rejection reasons
// ============================================================================

func TestSanitisedRejectionReasons(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}
	const infoVersion = int64(1234)

	cases := []struct {
		name             string
		makeClient       func(base client.Client) client.Client
		az               string
		dryRun           bool
		wantStatusCode   int
		wantReason       string   // exact match on resp.RejectionReason; "" skips
		wantBodyContains string   // substring match on raw response body; "" skips
		notWantInReason  []string // must not appear in resp.RejectionReason
	}{
		{
			name:           "non-dry-run: controller rejected → sanitised capacity message",
			makeClient:     func(base client.Client) client.Client { return &rejectAllCRClient{Client: base} },
			az:             "az-a",
			dryRun:         false,
			wantStatusCode: http.StatusOK,
			wantReason:     "not sufficient capacity, please try again later",
			notWantInReason: []string{
				"no capacity available on any host",
				"detailed internal info",
			},
		},
		{
			name: "non-dry-run: k8s write error → sanitised per-commitment message",
			makeClient: func(base client.Client) client.Client {
				return &errInjectClient{Client: base, crCreateErr: errors.New("etcd connection refused: internal server error details")}
			},
			az:              "az-a",
			dryRun:          false,
			wantStatusCode:  http.StatusOK,
			wantReason:      "internal error on commitment uuid-san",
			notWantInReason: []string{"etcd", "connection refused"},
		},
		{
			name: "non-dry-run: NoClusterMatchedError → HTTP 200 with unknown AZ rejection reason",
			makeClient: func(base client.Client) client.Client {
				return &errInjectClient{Client: base, crCreateErr: &multicluster.NoClusterMatchedError{}}
			},
			az:             "az-nonexistent",
			dryRun:         false,
			wantStatusCode: http.StatusOK,
			wantReason:     "unknown availability zone: az-nonexistent",
		},
		{
			name: "dry-run: NoClusterMatchedError → HTTP 200 with unknown AZ rejection reason",
			makeClient: func(base client.Client) client.Client {
				return &errInjectClient{Client: base, crCreateErr: &multicluster.NoClusterMatchedError{}}
			},
			az:             "az-unknown-dry",
			dryRun:         true,
			wantStatusCode: http.StatusOK,
			wantReason:     "unknown availability zone: az-unknown-dry",
		},
		{
			name:           "dry-run: probe rejected by controller → sanitised capacity message",
			makeClient:     func(base client.Client) client.Client { return &rejectAllCRClient{Client: base} },
			az:             "az-a",
			dryRun:         true,
			wantStatusCode: http.StatusOK,
			wantReason:     "not sufficient capacity, please try again later",
			notWantInReason: []string{
				"no capacity available on any host",
				"detailed internal info",
			},
		},
		{
			name: "dry-run: generic k8s error → sanitised dry-run error message",
			makeClient: func(base client.Client) client.Client {
				return &errInjectClient{Client: base, crCreateErr: errors.New("some internal k8s storage error")}
			},
			az:              "az-a",
			dryRun:          true,
			wantStatusCode:  http.StatusOK,
			wantReason:      "internal error processing dry-run request",
			notWantInReason: []string{"k8s storage error"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := buildFakeK8sClientForSanitisation(t, []*TestFlavor{m1Small}, infoVersion)
			env := newSanitisationEnv(t, tc.makeClient(base), nil)

			reqJSON := buildRequestJSON(newCommitmentRequest(tc.az, tc.dryRun, infoVersion,
				createCommitment("hw_version_hana_1_ram", "project-A", "uuid-san", "confirmed", 2)))
			resp, body, statusCode := env.te.CallChangeCommitmentsAPI(reqJSON)

			if statusCode != tc.wantStatusCode {
				t.Fatalf("HTTP status = %d, want %d", statusCode, tc.wantStatusCode)
			}
			if tc.wantReason != "" && resp.RejectionReason != tc.wantReason {
				t.Errorf("RejectionReason = %q, want %q", resp.RejectionReason, tc.wantReason)
			}
			if tc.wantBodyContains != "" && !strings.Contains(body, tc.wantBodyContains) {
				t.Errorf("response body = %q, want to contain %q", body, tc.wantBodyContains)
			}
			for _, notWant := range tc.notWantInReason {
				if strings.Contains(resp.RejectionReason, notWant) {
					t.Errorf("RejectionReason %q must not contain %q", resp.RejectionReason, notWant)
				}
			}
		})
	}
}

// ============================================================================
// Tests — metric label "bad_request"
// ============================================================================

func TestMetricUnknownAZ(t *testing.T) {
	log.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	m1Small := &TestFlavor{Name: "m1.small", Group: "hana_1", MemoryMB: 1024, VCPUs: 4}
	const infoVersion = int64(1234)

	base := buildFakeK8sClientForSanitisation(t, []*TestFlavor{m1Small}, infoVersion)
	env := newSanitisationEnv(t, &errInjectClient{
		Client:      base,
		crCreateErr: &multicluster.NoClusterMatchedError{},
	}, nil)

	reqJSON := buildRequestJSON(newCommitmentRequest("az-bad-metric", false, infoVersion,
		createCommitment("hw_version_hana_1_ram", "project-A", "uuid-metric-bad-req", "confirmed", 2)))
	_, _, statusCode := env.te.CallChangeCommitmentsAPI(reqJSON)

	if statusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", statusCode)
	}

	const counterName = "cortex_committed_resource_change_api_requests_total"

	rejectedCount := getCounterValueForSanitisation(t, env.registry, counterName, map[string]string{
		"status_code": "200",
		"dry_run":     "false",
		"result":      "rejected",
	})
	if rejectedCount < 1 {
		t.Errorf("result=rejected counter (status 200) = %g, want ≥1", rejectedCount)
	}

	errorCount := getCounterValueForSanitisation(t, env.registry, counterName, map[string]string{
		"status_code": "200",
		"dry_run":     "false",
		"result":      "error",
	})
	if errorCount != 0 {
		t.Errorf("result=error counter (status 200) = %g, want 0", errorCount)
	}
}
