// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mockFeatureExtractor struct {
	extractError error
	features     []Feature
}

func (m *mockFeatureExtractor) Init(client.Client, v1alpha1.KnowledgeSpec) error { return nil }
func (m *mockFeatureExtractor) NewInputs() any                                   { return &struct{}{} }
func (m *mockFeatureExtractor) FeatureType() reflect.Type                        { return reflect.TypeFor[int]() }
func (m *mockFeatureExtractor) Extract(context.Context, any) ([]Feature, error) {
	return m.features, m.extractError
}

func TestMonitorFeatureExtractor_Extract(t *testing.T) {
	monitor := NewMonitor()
	wrapped := monitorFeatureExtractor("test", &mockFeatureExtractor{features: []Feature{1, 2, 3}}, monitor)
	features, err := wrapped.Extract(t.Context(), nil)
	if err != nil || len(features) != 3 {
		t.Fatalf("expected 3 features, got %v, %v", features, err)
	}
	if got := testutil.ToFloat64(monitor.stepFeatureCounter.WithLabelValues("test")); got != 3 {
		t.Errorf("expected feature counter 3, got %v", got)
	}
	if got := testutil.CollectAndCount(monitor.stepRunTimer); got != 1 {
		t.Errorf("expected one observed run, got %d", got)
	}

	expectedErr := errors.New("extract failed")
	wrapped = monitorFeatureExtractor("failing", &mockFeatureExtractor{extractError: expectedErr}, monitor)
	if _, err := wrapped.Extract(t.Context(), nil); !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}
