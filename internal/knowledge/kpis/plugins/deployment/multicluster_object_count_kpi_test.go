// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

var (
	cmListGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}
)

func newKPITestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// FakeCluster is a minimal cluster.Cluster backed by a fake client.
type fakeCluster struct {
	cluster.Cluster
	FakeClient client.Client
}

func (f *fakeCluster) GetClient() client.Client { return f.FakeClient }
func (f *fakeCluster) GetCache() cache.Cache    { return nil }

// NewFakeCluster returns a FakeCluster pre-loaded with the given objects.
func newFakeCluster(scheme *runtime.Scheme, objs ...client.Object) *fakeCluster {
	return &fakeCluster{
		FakeClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
	}
}

// RemoteEntry describes one remote cluster to register in the client.
type RemoteEntry struct {
	Cluster cluster.Cluster
	Labels  map[string]string
	GVKs    []schema.GroupVersionKind
}

// NewClient builds a *multicluster.Client with a home cluster and the given
// remote entries. It uses AddRemoteCluster so no real REST config is needed.
func newFakeMCLClient(scheme *runtime.Scheme, home cluster.Cluster, homeGVKs []schema.GroupVersionKind, remotes []RemoteEntry) *multicluster.Client {
	c := &multicluster.Client{
		HomeScheme:  scheme,
		HomeCluster: home,
	}
	for _, gvk := range homeGVKs {
		c.AddHomeGVK(gvk)
	}
	for _, r := range remotes {
		c.AddRemoteCluster(r.Cluster, r.Labels, r.GVKs...)
	}
	return c
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

func TestMulticlusterObjectCountKPI_Init_RejectsInvalidGVK(t *testing.T) {
	scheme := newKPITestScheme(t)
	mcl := newFakeMCLClient(scheme, newFakeCluster(scheme), nil, nil)
	kpi := &MulticlusterObjectCountKPI{}
	opts := conf.NewRawOpts(`{"gvks":["not-a-valid-gvk"]}`)
	if err := kpi.Init(nil, mcl, opts); err == nil {
		t.Fatal("expected error for invalid GVK string")
	}
}

func TestMulticlusterObjectCountKPI_Describe(t *testing.T) {
	scheme := newKPITestScheme(t)
	remote := newFakeCluster(scheme)
	mcl := newFakeMCLClient(scheme, newFakeCluster(scheme), nil, []RemoteEntry{
		{
			Cluster: remote,
			Labels:  map[string]string{"availabilityZone": "az-1"},
			GVKs:    []schema.GroupVersionKind{cmListGVK},
		},
	})

	kpi := &MulticlusterObjectCountKPI{}
	opts := conf.NewRawOpts(`{"gvks":["/v1/ConfigMapList"]}`)
	if err := kpi.Init(nil, mcl, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

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
	scheme := newKPITestScheme(t)

	az1 := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-1", Namespace: "default"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-2", Namespace: "default"}},
	)
	az2 := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-3", Namespace: "default"}},
	)

	mcl := newFakeMCLClient(scheme, newFakeCluster(scheme), nil, []RemoteEntry{
		{Cluster: az1, Labels: map[string]string{"availabilityZone": "az-1"}, GVKs: []schema.GroupVersionKind{cmListGVK}},
		{Cluster: az2, Labels: map[string]string{"availabilityZone": "az-2"}, GVKs: []schema.GroupVersionKind{cmListGVK}},
	})

	kpi := &MulticlusterObjectCountKPI{}
	opts := conf.NewRawOpts(`{"gvks":["/v1/ConfigMapList"]}`)
	if err := kpi.Init(nil, mcl, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

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
		var az string
		for _, lp := range metric.Label {
			if lp.GetName() == "availability_zone" {
				az = lp.GetValue()
			}
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
	scheme := newKPITestScheme(t)

	home := newFakeCluster(scheme,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-home", Namespace: "default"}},
	)

	mcl := newFakeMCLClient(scheme, home, []schema.GroupVersionKind{cmListGVK}, nil)

	kpi := &MulticlusterObjectCountKPI{}
	opts := conf.NewRawOpts(`{"gvks":["/v1/ConfigMapList"]}`)
	if err := kpi.Init(nil, mcl, opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	var collected int
	var count float64
	for m := range ch {
		collected++
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			t.Fatalf("failed to write metric: %v", err)
		}
		count = metric.Gauge.GetValue()
	}
	if collected != 1 {
		t.Fatalf("expected 1 metric (home cluster), got %d", collected)
	}
	if count != 1 {
		t.Errorf("expected count 1, got %g", count)
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
