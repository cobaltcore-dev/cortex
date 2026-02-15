package plugins

import (
	"slices"
	"testing"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/prometheus"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	testlibDB "github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing"
)

func TestGenericExtractor_Extract(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer dbEnv.Close()
	// Create dependency table
	if err := testDB.CreateTable(
		testDB.AddTable(prometheus.GenericMetric{}),
	); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	metrics := []any{
		&prometheus.GenericMetric{Name: "node_cpu_seconds_total", Host: "node-01", Value: 0.81},
		&prometheus.GenericMetric{Name: "node_cpu_seconds_total", Host: "node-02", Value: 0.37},
	}
	if err := testDB.Insert(metrics...); err != nil {
		t.Fatalf("failed to insert manila storage pools: %v", err)
	}

	extractor := &GenericExtractor{}
	config := v1alpha1.KnowledgeSpec{}
	if err := extractor.Init(&testDB, nil, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	datasources := []*v1alpha1.Datasource{
		{
			Spec: v1alpha1.DatasourceSpec{
				Type: v1alpha1.DatasourceTypePrometheus,
				Prometheus: v1alpha1.PrometheusDatasource{
					Alias: "node_cpu_seconds_total",
				},
			},
		},
	}

	features, err := extractor.Extract(datasources, []*v1alpha1.Knowledge{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var actual []Generic
	for _, f := range features {
		actual = append(actual, f.(Generic))
	}

	expected := []Generic{
		{Host: "node-01", Value: 0.81},
		{Host: "node-02", Value: 0.37},
	}

	if len(actual) != len(expected) {
		t.Errorf("expected %d rows, got %d", len(expected), len(actual))
	}

	for _, exp := range expected {
		if !slices.ContainsFunc(actual, func(m Generic) bool {
			return m.Host == exp.Host && m.Value == exp.Value
		}) {
			t.Errorf("expected to find %+v in actual results %+v", exp, actual)
		}
	}
}
