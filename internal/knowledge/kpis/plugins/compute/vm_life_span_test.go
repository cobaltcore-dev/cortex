// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMLifeSpanKPI_Init(t *testing.T) {
	kpi := &VMLifeSpanKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMLifeSpanKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vmLifeSpan, err := v1alpha1.BoxFeatureList([]any{
		&compute.VMLifeSpanHistogramBucket{FlavorName: "small", Bucket: 60, Value: 100, Count: 10, Sum: 600},
		&compute.VMLifeSpanHistogramBucket{FlavorName: "medium", Bucket: 120, Value: 200, Count: 20, Sum: 2400},
		&compute.VMLifeSpanHistogramBucket{FlavorName: "large", Bucket: 180, Value: 300, Count: 30, Sum: 5400},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMLifeSpanKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "vm-life-span"},
			Status:     v1alpha1.KnowledgeStatus{Raw: vmLifeSpan},
		}).
		Build()
	if err := kpi.Init(nil, client, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ch := make(chan prometheus.Metric, 10)
	kpi.Collect(ch)
	close(ch)

	metricsCount := 0
	for range ch {
		metricsCount++
	}

	if metricsCount == 0 {
		t.Errorf("expected metrics to be collected, got %d", metricsCount)
	}
}
