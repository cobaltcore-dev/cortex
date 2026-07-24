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
// KPI logic can be tested against a fakeReader.
func initWithReader(t *testing.T, r *fakeReader, gvkStr string) *MulticlusterObjectCountKPI {
	t.Helper()
	kpi := &MulticlusterObjectCountKPI{}
	kpi.mcl = r
	gvk, err := parseGVK(gvkStr)
	if err != nil {
		t.Fatalf("parseGVK: %v", err)
	}
	keySet := map[string]bool{}
	var labelKeys []string
	for _, lm := range r.routeLabels {
		for k := range lm {
			snake := toSnakeCase(k)
			if !keySet[snake] {
				keySet[snake] = true
				labelKeys = append(labelKeys, snake)
			}
		}
	}
	varLabels := append([]string{"group", "version", "kind", "is_home"}, labelKeys...)
	desc := prometheus.NewDesc(
		"cortex_multicluster_object_count",
		"Number of objects of a given GVK per cluster",
		varLabels,
		nil,
	)
	kpi.descs = append(kpi.descs, gvkDesc{gvk: gvk, labelKeys: labelKeys, desc: desc})
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
