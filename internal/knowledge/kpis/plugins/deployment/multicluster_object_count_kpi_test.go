// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeReader is a stub multiclusterReader that returns canned per-cluster
// metadata. It lets the KPI be tested without any real or fake clusters.
type fakeReader struct {
	routeLabels []map[string]string
	perCluster  []multicluster.ClusterObjectMetadata
	err         error
}

func (f *fakeReader) ConfiguredRouteLabels(schema.GroupVersionKind) []map[string]string {
	return f.routeLabels
}

func (f *fakeReader) ListMetadataPerCluster(context.Context, schema.GroupVersionKind, ...client.ListOption) ([]multicluster.ClusterObjectMetadata, error) {
	return f.perCluster, f.err
}

// items builds n PartialObjectMetadata entries to represent a count of n.
func items(n int) []metav1.PartialObjectMetadata {
	out := make([]metav1.PartialObjectMetadata, n)
	return out
}

func TestMulticlusterObjectCountKPI_GetName(t *testing.T) {
	kpi := &MulticlusterObjectCountKPI{}
	if got := kpi.GetName(); got != "multicluster_object_count_kpi" {
		t.Errorf("GetName() = %q, want %q", got, "multicluster_object_count_kpi")
	}
}

func TestMulticlusterObjectCountKPI_Init_RejectsNonMCLClient(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatal(err)
	}
	plainClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kpi := &MulticlusterObjectCountKPI{}
	if err := kpi.Init(nil, plainClient, conf.NewRawOpts(`{"gvks":[]}`)); err == nil {
		t.Fatal("expected error when passing a non-*multicluster.Client")
	}
}

// initWithReader builds a KPI whose descriptors are derived from the given
// route labels, bypassing Init's *multicluster.Client type assertion so the
// KPI logic can be tested against a fakeReader. Uses the production
// buildObjectCountSchema helper so the test exercises the same schema path.
func initWithReader(t *testing.T, r *fakeReader, gvkStrs ...string) *MulticlusterObjectCountKPI {
	t.Helper()
	kpi := &MulticlusterObjectCountKPI{}
	kpi.mcl = r

	var gvks []schema.GroupVersionKind
	for _, gvkStr := range gvkStrs {
		gvk, err := parseGVK(gvkStr)
		if err != nil {
			t.Fatalf("parseGVK(%q): %v", gvkStr, err)
		}
		gvks = append(gvks, gvk)
		kpi.descs = append(kpi.descs, gvkDesc{gvk: gvk})
	}

	labelKeys, desc, err := buildObjectCountSchema(r, gvks)
	if err != nil {
		t.Fatalf("buildObjectCountSchema: %v", err)
	}
	kpi.labelKeys = labelKeys
	kpi.sharedDesc = desc
	return kpi
}

func TestMulticlusterObjectCountKPI_Init_RejectsInvalidGVK(t *testing.T) {
	kpi := &MulticlusterObjectCountKPI{}
	// A *multicluster.Client with no configured GVKs still passes the type
	// assertion; the invalid GVK string must be rejected during parsing.
	mcl := &multicluster.Client{}
	opts := conf.NewRawOpts(`{"gvks":["not-a-valid-gvk"]}`)
	if err := kpi.Init(nil, mcl, opts); err == nil {
		t.Fatal("expected error for invalid GVK string")
	}
}

func TestMulticlusterObjectCountKPI_Init_RejectsEmptyVersionOrKind(t *testing.T) {
	cases := []string{"apps//DeploymentList", "/v1/"}
	for _, raw := range cases {
		if _, err := parseGVK(raw); err == nil {
			t.Errorf("parseGVK(%q): expected error for empty segment, got nil", raw)
		}
	}
}

func TestMulticlusterObjectCountKPI_Describe(t *testing.T) {
	r := &fakeReader{routeLabels: []map[string]string{{"availabilityZone": "az-1"}}}
	kpi := initWithReader(t, r, "/v1/ConfigMapList")

	ch := make(chan *prometheus.Desc, 5)
	kpi.Describe(ch)
	close(ch)
	var count int
	for range ch {
		count++
	}
	if count != 1 {
		t.Errorf("Describe: expected 1 descriptor, got %d", count)
	}
}

func TestMulticlusterObjectCountKPI_Collect(t *testing.T) {
	r := &fakeReader{
		routeLabels: []map[string]string{
			{"availabilityZone": "az-1"},
			{"availabilityZone": "az-2"},
		},
		perCluster: []multicluster.ClusterObjectMetadata{
			{Labels: map[string]string{"availabilityZone": "az-1"}, Items: items(2)},
			{Labels: map[string]string{"availabilityZone": "az-2"}, Items: items(1)},
		},
	}
	kpi := initWithReader(t, r, "/v1/ConfigMapList")

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	// Collect into a map: availabilityZone -> count value.
	byAZ := map[string]float64{}
	for m := range ch {
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		var az, isHome string
		for _, lp := range metric.Label {
			switch lp.GetName() {
			case "availability_zone":
				az = lp.GetValue()
			case "is_home":
				isHome = lp.GetValue()
			}
		}
		if isHome != "false" {
			t.Errorf("az %q: expected is_home=false for remote cluster, got %q", az, isHome)
		}
		byAZ[az] = metric.Gauge.GetValue()
	}

	if len(byAZ) != 2 {
		t.Fatalf("expected metrics for 2 clusters, got %d: %v", len(byAZ), byAZ)
	}
	if byAZ["az-1"] != 2 {
		t.Errorf("az-1: expected count 2, got %g", byAZ["az-1"])
	}
	if byAZ["az-2"] != 1 {
		t.Errorf("az-2: expected count 1, got %g", byAZ["az-2"])
	}
}

func TestMulticlusterObjectCountKPI_Collect_HomeCluster(t *testing.T) {
	r := &fakeReader{
		perCluster: []multicluster.ClusterObjectMetadata{
			{Items: items(1), IsHome: true},
		},
	}
	kpi := initWithReader(t, r, "/v1/ConfigMapList")

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	var collected int
	for m := range ch {
		collected++
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		var isHome string
		for _, lp := range metric.Label {
			if lp.GetName() == "is_home" {
				isHome = lp.GetValue()
			}
		}
		if isHome != "true" {
			t.Errorf("expected is_home=true for home cluster, got %q", isHome)
		}
		if got := metric.Gauge.GetValue(); got != 1 {
			t.Errorf("expected count 1, got %g", got)
		}
	}
	if collected != 1 {
		t.Fatalf("expected 1 metric, got %d", collected)
	}
}

func TestMulticlusterObjectCountKPI_SnakeCaseLabels(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"availabilityZone", "availability_zone"},
		{"az", "az"},
		{"myLabelKey", "my_label_key"},
		{"alreadysnake", "alreadysnake"},
	}
	for _, tt := range tests {
		if got := toSnakeCase(tt.input); got != tt.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestMulticlusterObjectCountKPI_UnionLabelSchema verifies that two GVKs with
// differing routing-label sets share a single descriptor whose label schema is
// the union of both sets, and that Prometheus collection succeeds without
// duplicate-descriptor errors.
func TestMulticlusterObjectCountKPI_UnionLabelSchema(t *testing.T) {
	// GVK A clusters use "availabilityZone"; GVK B clusters use "region".
	// The union descriptor must carry both keys.
	r := &fakeReader{
		routeLabels: []map[string]string{
			{"availabilityZone": "az-1"},
			{"region": "us-east"},
		},
		perCluster: []multicluster.ClusterObjectMetadata{
			{Labels: map[string]string{"availabilityZone": "az-1"}, Items: items(3)},
			{Labels: map[string]string{"region": "us-east"}, Items: items(5)},
		},
	}
	kpi := initWithReader(t, r, "/v1/ConfigMapList", "apps/v1/DeploymentList")

	// Registering with a real prometheus.Registry would panic if two descriptors
	// share the same metric name but have different label schemas.
	reg := prometheus.NewRegistry()
	if err := reg.Register(kpi); err != nil {
		t.Fatalf("Register failed (likely duplicate/inconsistent descriptor): %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather failed: %v", err)
	}
	var total int
	for _, mf := range mfs {
		total += len(mf.Metric)
	}
	// Two GVKs × two clusters each = 4 metrics.
	if total != 4 {
		t.Errorf("expected 4 metrics, got %d", total)
	}

	// Every metric must have both union keys present (empty string for missing).
	for _, mf := range mfs {
		for _, m := range mf.Metric {
			labelMap := map[string]string{}
			for _, lp := range m.Label {
				labelMap[lp.GetName()] = lp.GetValue()
			}
			if _, ok := labelMap["availability_zone"]; !ok {
				t.Errorf("metric missing label availability_zone: %v", labelMap)
			}
			if _, ok := labelMap["region"]; !ok {
				t.Errorf("metric missing label region: %v", labelMap)
			}
		}
	}
}
