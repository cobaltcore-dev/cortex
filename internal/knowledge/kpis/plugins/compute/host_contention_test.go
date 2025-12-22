// Copyright 2025 SAP SE
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

func TestVMwareHostContentionKPI_Init(t *testing.T) {
	kpi := &VMwareHostContentionKPI{}
	if err := kpi.Init(nil, nil, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestVMwareHostContentionKPI_Collect(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	vropsHostsystemContentionLongTerm, err := v1alpha1.BoxFeatureList([]any{
		&compute.VROpsHostsystemContentionLongTerm{ComputeHost: "host1", AvgCPUContention: 10.5, MaxCPUContention: 20.0},
		&compute.VROpsHostsystemContentionLongTerm{ComputeHost: "host2", AvgCPUContention: 15.0, MaxCPUContention: 25.0},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	kpi := &VMwareHostContentionKPI{}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.Knowledge{
			ObjectMeta: v1.ObjectMeta{Name: "vmware-long-term-contended-hosts"},
			Status:     v1alpha1.KnowledgeStatus{Raw: vropsHostsystemContentionLongTerm},
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
