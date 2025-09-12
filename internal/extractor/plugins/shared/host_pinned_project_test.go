// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"os"
	"reflect"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/nova"
	"github.com/cobaltcore-dev/cortex/testlib"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestHostPinnedProjectsExtractor_Init(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}
	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(HostPinnedProjects{}) {
		t.Error("expected table to be created")
	}
}

func TestHostPinnedProjectsExtractor_Extract_FindComputeHostProjectMapping(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	aggregates := []any{
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_1, project_id_2"}`,
		},
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host2"),
			Metadata:    `{"filter_tenant_id":"project_id_1, project_id_2"}`,
		},
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFeatures := []HostPinnedProjects{
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_2"),
		},
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host2"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host2"),
			ProjectID:     testlib.Ptr("project_id_2"),
		},
	}

	if len(results) != len(expectedFeatures) {
		t.Errorf("expected %d results, got %d", len(expectedFeatures), len(results))
	}

	for i := range results {
		if !reflect.DeepEqual(results[i], expectedFeatures[i]) {
			t.Errorf("expected %v, got %v", expectedFeatures[i], results[i])
		}
	}
}

func TestHostPinnedProjectsExtractor_Extract_SkipAggregatesWithNoFilterTenant(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	aggregates := []any{
		&nova.Aggregate{
			Name:        "ignore-no-filter-tenant",
			UUID:        "ignore",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"something_different":"project_id_1, project_id_2"}`,
		},
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(results) > 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

func TestHostPinnedProjectsExtractor_Extract_SupportEmptyComputeHost(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	aggregates := []any{
		// This aggregate doesn't have a compute host so project_3 and 4 should have an empty entry for the compute host
		&nova.Aggregate{
			Name:        "agg2",
			UUID:        "agg2",
			ComputeHost: nil,
			Metadata:    `{"filter_tenant_id":"project_id_3, project_id_4"}`,
		},
		// Because of this aggregate project 3 and 4 should additionally have a host-4 as pinned
		&nova.Aggregate{
			Name:        "agg3",
			UUID:        "agg3",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_3, project_id_4"}`,
		},
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFeatures := []HostPinnedProjects{
		{
			AggregateName: testlib.Ptr("agg3"),
			AggregateUUID: testlib.Ptr("agg3"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_3"),
		},
		{
			AggregateName: testlib.Ptr("agg3"),
			AggregateUUID: testlib.Ptr("agg3"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_4"),
		},
		{
			AggregateName: testlib.Ptr("agg2"),
			AggregateUUID: testlib.Ptr("agg2"),
			ComputeHost:   nil,
			ProjectID:     testlib.Ptr("project_id_3"),
		},
		{
			AggregateName: testlib.Ptr("agg2"),
			AggregateUUID: testlib.Ptr("agg2"),
			ComputeHost:   nil,
			ProjectID:     testlib.Ptr("project_id_4"),
		},
	}

	if len(results) != len(expectedFeatures) {
		t.Errorf("expected %d results, got %d", len(expectedFeatures), len(results))
	}

	for i := range results {
		if !reflect.DeepEqual(results[i], expectedFeatures[i]) {
			t.Errorf("expected %v, got %v", expectedFeatures[i], results[i])
		}
	}
}

func TestHostPinnedProjectsExtractor_Extract_FilterOutEmptyFilterTenantLists(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	aggregates := []any{
		// Doesn't have any filter_tenant_id set, so this aggregate is supposed to be ignored
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: nil,
			Metadata:    `{"filter_tenant_id":""}`,
		},
		&nova.Aggregate{
			Name:        "agg2",
			UUID:        "agg2",
			ComputeHost: nil,
			Metadata:    `{"filter_tenant_id":[]}`,
		},
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(results) > 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

func TestHostPinnedProjectsExtractor_Extract_FindAllHypervisorsIfNoAggregateIsProvided(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	hypervisors := []any{
		// Ironic hypervisor should be filtered out
		&nova.Hypervisor{
			ID:             "1",
			ServiceHost:    "host1",
			HypervisorType: "ironic",
		},
		&nova.Hypervisor{
			ID:             "2",
			ServiceHost:    "host2",
			HypervisorType: "not-ironic",
		},
		&nova.Hypervisor{
			ID:             "3",
			ServiceHost:    "host3",
			HypervisorType: "other-not-ironic",
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("failed to insert hypervisors: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFeatures := []HostPinnedProjects{
		{
			AggregateName: nil,
			AggregateUUID: nil,
			ComputeHost:   testlib.Ptr("host2"),
			ProjectID:     nil,
		},
		{
			AggregateName: nil,
			AggregateUUID: nil,
			ComputeHost:   testlib.Ptr("host3"),
			ProjectID:     nil,
		},
	}

	if len(results) != len(expectedFeatures) {
		t.Errorf("expected %d results, got %d", len(expectedFeatures), len(results))
	}

	for i := range results {
		if !reflect.DeepEqual(results[i], expectedFeatures[i]) {
			t.Errorf("expected %v, got %v", expectedFeatures[i], results[i])
		}
	}
}

func TestHostPinnedProjectsExtractor_Extract_FindAllHypervisorsWithNoFilterIfAggregatesExist(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	hypervisors := []any{
		&nova.Hypervisor{
			ID:             "1",
			ServiceHost:    "host1",
			HypervisorType: "not-ironic",
		},
		&nova.Hypervisor{
			ID:             "2",
			ServiceHost:    "host2",
			HypervisorType: "other-not-ironic",
		},
		&nova.Hypervisor{
			ID:             "3",
			ServiceHost:    "host3",
			HypervisorType: "non-filter-host",
		},
	}

	aggregates := []any{
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_1"}`,
		},
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host2"),
			Metadata:    `{"filter_tenant_id":"project_id_1"}`,
		},
		&nova.Aggregate{
			Name:        "az1",
			UUID:        "az1",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"type":"az"}`,
		},
		&nova.Aggregate{
			Name:        "az1",
			UUID:        "az1",
			ComputeHost: testlib.Ptr("host2"),
			Metadata:    `{"type":"az"}`,
		},
		// Host 3 is part of an availability zone aggregate, but has no filter_tenant_id
		&nova.Aggregate{
			Name:        "az1",
			UUID:        "az1",
			ComputeHost: testlib.Ptr("host3"),
			Metadata:    `{"type":"az"}`,
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("failed to insert hypervisors: %v", err)
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFeatures := []HostPinnedProjects{
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host2"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
		{
			AggregateName: nil,
			AggregateUUID: nil,
			ComputeHost:   testlib.Ptr("host3"),
			ProjectID:     nil,
		},
	}

	if len(results) != len(expectedFeatures) {
		t.Errorf("expected %d results, got %d", len(expectedFeatures), len(results))
	}

	for i := range results {
		if !reflect.DeepEqual(results[i], expectedFeatures[i]) {
			t.Errorf("expected %v, got %v", expectedFeatures[i], results[i])
		}
	}
}

func TestHostPinnedProjectsExtractor_Extract_DuplicateHostsAndProjectsInAggregate(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	hypervisors := []any{
		&nova.Hypervisor{
			ID:             "1",
			ServiceHost:    "host1",
			HypervisorType: "not-ironic",
		},
	}

	aggregates := []any{
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_1, project_id_1"}`,
		},
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_1, project_id_1"}`,
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("failed to insert hypervisors: %v", err)
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFeatures := []HostPinnedProjects{
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
		{
			AggregateName: testlib.Ptr("agg2"),
			AggregateUUID: testlib.Ptr("agg2"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
	}

	if len(results) != len(expectedFeatures) {
		t.Errorf("expected %d results, got %d", len(expectedFeatures), len(results))
	}

	for i := range results {
		if !reflect.DeepEqual(results[i], expectedFeatures[i]) {
			t.Errorf("expected %v, got %v", expectedFeatures[i], results[i])
		}
	}
}

func TestHostPinnedProjectsExtractor_Extract_DoubleHostProjectAssignmentOfMultipleAggregates(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

	dbEnv := testlibDB.SetupDBEnv(t)
	testDB := db.DB{DbMap: dbEnv.DbMap}
	defer testDB.Close()
	defer dbEnv.Close()

	if err := testDB.CreateTable(
		testDB.AddTable(nova.Aggregate{}),
		testDB.AddTable(nova.Hypervisor{}),
	); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	hypervisors := []any{
		&nova.Hypervisor{
			ID:             "1",
			ServiceHost:    "host1",
			HypervisorType: "not-ironic",
		},
	}

	aggregates := []any{
		&nova.Aggregate{
			Name:        "agg1",
			UUID:        "agg1",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_1"}`,
		},
		&nova.Aggregate{
			Name:        "agg2",
			UUID:        "agg2",
			ComputeHost: testlib.Ptr("host1"),
			Metadata:    `{"filter_tenant_id":"project_id_1"}`,
		},
	}

	if err := testDB.Insert(hypervisors...); err != nil {
		t.Fatalf("failed to insert hypervisors: %v", err)
	}

	if err := testDB.Insert(aggregates...); err != nil {
		t.Fatalf("failed to insert aggregates: %v", err)
	}

	extractor := &HostPinnedProjectsExtractor{}
	config := conf.FeatureExtractorConfig{
		Name:           "host_pinned_projects_extractor",
		Options:        conf.NewRawOpts("{}"),
		RecencySeconds: nil,
	}

	if err := extractor.Init(testDB, config); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err := extractor.Extract(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var results []HostPinnedProjects
	table := HostPinnedProjects{}.TableName()
	if _, err := testDB.Select(&results, "SELECT * FROM "+table); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFeatures := []HostPinnedProjects{
		{
			AggregateName: testlib.Ptr("agg1"),
			AggregateUUID: testlib.Ptr("agg1"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
		{
			AggregateName: testlib.Ptr("agg2"),
			AggregateUUID: testlib.Ptr("agg2"),
			ComputeHost:   testlib.Ptr("host1"),
			ProjectID:     testlib.Ptr("project_id_1"),
		},
	}

	if len(results) != len(expectedFeatures) {
		t.Errorf("expected %d results, got %d", len(expectedFeatures), len(results))
	}

	for i := range results {
		if !reflect.DeepEqual(results[i], expectedFeatures[i]) {
			t.Errorf("expected %v, got %v", expectedFeatures[i], results[i])
		}
	}
}
