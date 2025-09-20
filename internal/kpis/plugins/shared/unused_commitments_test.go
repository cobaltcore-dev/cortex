// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/prometheus/client_golang/prometheus"
	prometheusgo "github.com/prometheus/client_model/go"
)

type UnusedCommitmentsMetric struct {
	AvailabilityZone string
	Resource         string
	Value            float64
}

func UnusedCommitmentsKPI_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	kpi := &UnusedCommitmentsKPI{}
	if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestUnusedCommitmentsKPI_Collect(t *testing.T) {
	tests := []struct {
		name     string
		mockData []any
		expected map[string]UnusedCommitmentsMetric
	}{
		{
			name:     "should not export any metrics when no data exists",
			mockData: []any{},
			expected: map[string]UnusedCommitmentsMetric{},
		},
		{
			name: "should report 0 if there are no commitments but usage exists",
			mockData: []any{
				// Utilization
				&shared.ProjectResourceUtilization{
					ProjectID:        "project-1",
					AvailabilityZone: testlib.Ptr("az1"),
					TotalVCPUsUsed:   2,
					TotalRAMUsedMB:   4096,
				},
				&shared.ProjectResourceUtilization{
					ProjectID:        "project-2",
					AvailabilityZone: testlib.Ptr("az2"),
					TotalVCPUsUsed:   10,
					TotalRAMUsedMB:   2048,
				},

				// Availability zones
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az1")},
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az2")},
			},
			expected: map[string]UnusedCommitmentsMetric{
				"az1|cpu": {
					AvailabilityZone: "az1",
					Resource:         "cpu",
					Value:            0,
				},
				"az2|cpu": {
					AvailabilityZone: "az2",
					Resource:         "cpu",
					Value:            0,
				},
				"az1|ram": {
					AvailabilityZone: "az1",
					Resource:         "ram",
					Value:            0,
				},
				"az2|ram": {
					AvailabilityZone: "az2",
					Resource:         "ram",
					Value:            0,
				},
			},
		},
		{
			name: "should report 0 for all availability zones and resources when no commitments exist",
			mockData: []any{
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az1")},
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az2")},
			},
			expected: map[string]UnusedCommitmentsMetric{
				"az1|cpu": {
					AvailabilityZone: "az1",
					Resource:         "cpu",
					Value:            0,
				},
				"az2|cpu": {
					AvailabilityZone: "az2",
					Resource:         "cpu",
					Value:            0,
				},
				"az1|ram": {
					AvailabilityZone: "az1",
					Resource:         "ram",
					Value:            0,
				},
				"az2|ram": {
					AvailabilityZone: "az2",
					Resource:         "ram",
					Value:            0,
				},
			},
		},
		{
			name: "should report the commitment values when there is no usage",
			mockData: []any{
				// Commitments
				&shared.ProjectResourceCommitments{
					ProjectID:           "project-1",
					AvailabilityZone:    "az1",
					TotalVCPUsCommitted: 4,
					TotalRAMCommittedMB: 8192,
				},
				&shared.ProjectResourceCommitments{
					ProjectID:           "project-2",
					AvailabilityZone:    "az2",
					TotalVCPUsCommitted: 10,
					TotalRAMCommittedMB: 2048,
				},

				// Availability zones
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az1")},
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az2")},
			},
			expected: map[string]UnusedCommitmentsMetric{
				"az1|cpu": {
					AvailabilityZone: "az1",
					Resource:         "cpu",
					Value:            4,
				},
				"az2|cpu": {
					AvailabilityZone: "az2",
					Resource:         "cpu",
					Value:            10,
				},
				"az1|ram": {
					AvailabilityZone: "az1",
					Resource:         "ram",
					Value:            8192,
				},
				"az2|ram": {
					AvailabilityZone: "az2",
					Resource:         "ram",
					Value:            2048,
				},
			},
		},
		{
			name: "should report difference between commitments and usage",
			mockData: []any{
				// Utilization
				&shared.ProjectResourceUtilization{
					ProjectID:        "project-1",
					AvailabilityZone: testlib.Ptr("az1"),
					TotalVCPUsUsed:   2,
					TotalRAMUsedMB:   4096,
				},
				&shared.ProjectResourceUtilization{
					ProjectID:        "project-2",
					AvailabilityZone: testlib.Ptr("az2"),
					TotalVCPUsUsed:   10,
					TotalRAMUsedMB:   2048,
				},
				// Commitments
				&shared.ProjectResourceCommitments{
					ProjectID:           "project-1",
					AvailabilityZone:    "az1",
					TotalVCPUsCommitted: 4,
					TotalRAMCommittedMB: 8192,
				},
				&shared.ProjectResourceCommitments{
					ProjectID:           "project-2",
					AvailabilityZone:    "az2",
					TotalVCPUsCommitted: 10,
					TotalRAMCommittedMB: 2048,
				},

				// Availability zones
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az1")},
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az2")},
			},
			expected: map[string]UnusedCommitmentsMetric{
				"az1|cpu": {
					AvailabilityZone: "az1",
					Resource:         "cpu",
					Value:            4 - 2,
				},
				"az2|cpu": {
					AvailabilityZone: "az2",
					Resource:         "cpu",
					Value:            0, // 10 - 10
				},
				"az1|ram": {
					AvailabilityZone: "az1",
					Resource:         "ram",
					Value:            8192 - 4096,
				},
				"az2|ram": {
					AvailabilityZone: "az2",
					Resource:         "ram",
					Value:            0, // 2048 - 2048
				},
			},
		},
		{
			name: "should report 0 if the usage is higher than the commitments",
			mockData: []any{
				// Utilization
				&shared.ProjectResourceUtilization{
					ProjectID:        "project-1",
					AvailabilityZone: testlib.Ptr("az1"),
					TotalVCPUsUsed:   4,
					TotalRAMUsedMB:   8192,
				},

				// Commitments
				&shared.ProjectResourceCommitments{
					ProjectID:           "project-1",
					AvailabilityZone:    "az1",
					TotalVCPUsCommitted: 2,
					TotalRAMCommittedMB: 2048,
				},

				// Availability zones
				&shared.HostAZ{AvailabilityZone: testlib.Ptr("az1")},
			},
			expected: map[string]UnusedCommitmentsMetric{
				"az1|cpu": {
					AvailabilityZone: "az1",
					Resource:         "cpu",
					Value:            0,
				},
				"az1|ram": {
					AvailabilityZone: "az1",
					Resource:         "ram",
					Value:            0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			testDB := db.DB{DbMap: dbEnv.DbMap}
			defer testDB.Close()
			defer dbEnv.Close()

			if err := testDB.CreateTable(
				testDB.AddTable(shared.ProjectResourceCommitments{}),
				testDB.AddTable(shared.ProjectResourceUtilization{}),
				testDB.AddTable(shared.HostAZ{}),
			); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if err := testDB.Insert(tt.mockData...); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			kpi := &UnusedCommitmentsKPI{}
			if err := kpi.Init(testDB, conf.NewRawOpts("{}")); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			ch := make(chan prometheus.Metric, 100)
			kpi.Collect(ch)
			close(ch)

			actualMetrics := make(map[string]UnusedCommitmentsMetric, 0)

			for metric := range ch {
				var m prometheusgo.Metric
				if err := metric.Write(&m); err != nil {
					t.Fatalf("failed to write metric: %v", err)
				}

				labels := make(map[string]string)
				for _, label := range m.Label {
					labels[label.GetName()] = label.GetValue()
				}

				key := labels["availability_zone"] + "|" + labels["resource"]

				actualMetrics[key] = UnusedCommitmentsMetric{
					AvailabilityZone: labels["availability_zone"],
					Resource:         labels["resource"],
					Value:            m.GetGauge().GetValue(),
				}
			}

			if len(tt.expected) != len(actualMetrics) {
				t.Errorf("expected %d metrics, got %d", len(tt.expected), len(actualMetrics))
			}

			for key, expected := range tt.expected {
				actual, ok := actualMetrics[key]
				if !ok {
					t.Errorf("expected metric %q not found", key)
					continue
				}

				if !reflect.DeepEqual(expected, actual) {
					t.Errorf("metric %q: expected %+v, got %+v", key, expected, actual)
				}
			}
		})
	}
}
