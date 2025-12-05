// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVMMigrationStatisticsKPI_Init(t *testing.T) {
	kpi := &VMMigrationStatisticsKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMMigrationStatisticsKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vmHostResidency, err := v1alpha1.BoxFeatureList([]any{
		&shared.VMHostResidencyHistogramBucket{FlavorName: "small", Bucket: 60, Value: 100, Count: 10, Sum: 600},
		&shared.VMHostResidencyHistogramBucket{FlavorName: "medium", Bucket: 120, Value: 200, Count: 20, Sum: 2400},
		&shared.VMHostResidencyHistogramBucket{FlavorName: "large", Bucket: 180, Value: 300, Count: 30, Sum: 5400},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMMigrationStatisticsKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "vm-host-residency"},
			Status:     v1alpha1.KnowledgeStatus{Raw: vmHostResidency},
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
