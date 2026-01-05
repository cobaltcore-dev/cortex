// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNetAppStoragePoolCPUUsageKPI_Init(t *testing.T) {
	kpi := &NetAppStoragePoolCPUUsageKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNetAppStoragePoolCPUUsageKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	storagePoolCPUUsage, err := v1alpha1.BoxFeatureList([]any{
		&storage.StoragePoolCPUUsage{StoragePoolName: "pool1", MaxCPUUsagePct: 80.5, AvgCPUUsagePct: 60.0},
		&storage.StoragePoolCPUUsage{StoragePoolName: "pool2", MaxCPUUsagePct: 90.0, AvgCPUUsagePct: 70.0},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &NetAppStoragePoolCPUUsageKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "netapp-storage-pool-cpu-usage-manila"},
			Status:     v1alpha1.KnowledgeStatus{Raw: storagePoolCPUUsage},
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
