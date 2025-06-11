// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/db/containers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type MockTable struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (m MockTable) TableName() string {
	return "mock_table"
}

func (m MockTable) Indexes() []Index {
	return nil
}

func TestNewDB(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	port, err := strconv.Atoi(container.GetPort())
	if err != nil {
		t.Fatalf("failed to convert port: %v", err)
	}
	config := conf.DBConnectionConfig{
		Host:     "localhost",
		Port:     port,
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config, nil, Monitor{})
	db.Close()
}

func TestDB_CreateTable(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !db.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestDB_AddTable(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	if table == nil {
		t.Fatal("expected table to be added")
	}
}

func TestDB_TableExists(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !db.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestDB_Close(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	db.Close()
	defer dbEnv.Close()

	if err := db.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestReplaceAll(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert initial records
	initialRecords := []MockTable{
		{ID: 1, Name: "record1"},
		{ID: 2, Name: "record2"},
	}
	for _, record := range initialRecords {
		err = db.Insert(&record)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}

	// Replace with new records
	newRecords := []MockTable{
		{ID: 1, Name: "new_record1"},
		{ID: 4, Name: "new_record2"},
	}
	err = ReplaceAll(db, newRecords...)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify old records are deleted
	var count int
	err = db.SelectOne(&count, "SELECT COUNT(*) FROM mock_table WHERE id IN (1, 2)")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 old records, got %d", count)
	}

	// Verify new records are inserted
	err = db.SelectOne(&count, "SELECT COUNT(*) FROM mock_table WHERE id IN (3, 4)")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 new records, got %d", count)
	}
}

// Test all sorts of data types.
type BulkMockTable struct {
	A int        `db:"a,primarykey"`
	B string     `db:"b"`
	C *string    `db:"c"`
	D *int       `db:"d"`
	E *float64   `db:"e"`
	F *bool      `db:"f"`
	G *time.Time `db:"g"`
}

func (BulkMockTable) TableName() string {
	return "bulk_mock_table"
}

func (BulkMockTable) Indexes() []Index {
	return nil
}

func TestBulkInsert(t *testing.T) {
	// Set up the test database environment
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	// Add and create the table
	table := db.AddTable(BulkMockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Prepare test data
	teststr := "test"
	testint := 42
	testfloat := 3.14
	testbool := true
	testtime := time.Now()
	records := []BulkMockTable{
		// Empty values for C, D, E, F, G
		{A: 1, B: "test1", C: nil, D: nil, E: nil, F: nil, G: nil},
		{A: 2, B: "test2", C: nil, D: nil, E: nil, F: nil, G: nil},
		{A: 3, B: "test3", C: nil, D: nil, E: nil, F: nil, G: nil},
		// Non-empty values for C, D, E, F, G
		{A: 4, B: "test4", C: &teststr, D: &testint, E: &testfloat, F: &testbool, G: &testtime},
		{A: 5, B: "test5", C: &teststr, D: &testint, E: &testfloat, F: &testbool, G: &testtime},
		{A: 6, B: "test6", C: &teststr, D: &testint, E: &testfloat, F: &testbool, G: &testtime},
	}

	// Perform bulk insert
	err = BulkInsert(db, db, records...)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the records were inserted
	var count int
	err = db.SelectOne(&count, "SELECT COUNT(*) FROM bulk_mock_table")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != len(records) {
		t.Errorf("expected %d records, got %d", len(records), count)
	}

	// Verify the data matches
	var insertedRecords []BulkMockTable
	_, err = db.Select(&insertedRecords, "SELECT * FROM bulk_mock_table")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for i, record := range records {
		if insertedRecords[i].A != record.A {
			t.Errorf("expected A %d, got %d", record.A, insertedRecords[i].A)
		}
		if insertedRecords[i].B != record.B {
			t.Errorf("expected B %s, got %s", record.B, insertedRecords[i].B)
		}
		if (insertedRecords[i].C == nil) != (record.C == nil) {
			t.Errorf("expected C %v, got %v", record.C, insertedRecords[i].C)
		} else if record.C != nil && *insertedRecords[i].C != *record.C {
			t.Errorf("expected C %s, got %s", *record.C, *insertedRecords[i].C)
		}
		if (insertedRecords[i].D == nil) != (record.D == nil) {
			t.Errorf("expected D %v, got %v", record.D, insertedRecords[i].D)
		} else if record.D != nil && *insertedRecords[i].D != *record.D {
			t.Errorf("expected D %d, got %d", *record.D, *insertedRecords[i].D)
		}
		if (insertedRecords[i].E == nil) != (record.E == nil) {
			t.Errorf("expected E %v, got %v", record.E, insertedRecords[i].E)
		} else if record.E != nil && *insertedRecords[i].E != *record.E {
			t.Errorf("expected E %f, got %f", *record.E, *insertedRecords[i].E)
		}
		if (insertedRecords[i].F == nil) != (record.F == nil) {
			t.Errorf("expected F %v, got %v", record.F, insertedRecords[i].F)
		} else if record.F != nil && *insertedRecords[i].F != *record.F {
			t.Errorf("expected F %t, got %t", *record.F, *insertedRecords[i].F)
		}
		if (insertedRecords[i].G == nil) != (record.G == nil) {
			t.Errorf("expected G %v, got %v", record.G, insertedRecords[i].G)
		} else if record.G != nil {
			// Normalize both timestamps to UTC for comparison
			expectedTime := record.G.UTC().Format(time.RFC3339)
			actualTime := insertedRecords[i].G.UTC().Format(time.RFC3339)
			if expectedTime != actualTime {
				t.Errorf("expected G %s, got %s", expectedTime, actualTime)
			}
		}
	}
}

func TestUnexpectedConnectionLoss(t *testing.T) {
	// The test is only relevant for postgres and not sqlite.
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	container := containers.PostgresContainer{}
	container.Init(t)
	// no need to defer the container close here, as it will be closed in the test below

	port, err := strconv.Atoi(container.GetPort())
	if err != nil {
		t.Fatalf("failed to convert port: %v", err)
	}
	config := conf.DBConnectionConfig{
		Host:     "localhost",
		Port:     port,
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
		Reconnect: conf.DBReconnectConfig{
			MaxRetries:                  10,
			RetryIntervalSeconds:        0,
			LivenessPingIntervalSeconds: 0,
		},
	}

	registry := &monitoring.Registry{Registry: prometheus.NewRegistry()}
	monitor := NewDBMonitor(registry)

	db := NewPostgresDB(config, nil, monitor)
	defer db.Close()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic, but code did not panic")
		}

		if err := db.Db.Ping(); err == nil {
			t.Errorf("expected error, got nil")
		}

		// Check if the connection attempts metric was incremented
		if monitor.connectionAttempts == nil {
			t.Fatalf("expected connection attempts metric to be registered")
		}
		attempts := testutil.ToFloat64(monitor.connectionAttempts)
		if attempts != float64(config.Reconnect.MaxRetries) {
			t.Errorf("expected %v connection attempts, got %v", config.Reconnect.MaxRetries, attempts)
		}
	}()
	container.Close()
	db.CheckLivenessPeriodically()
}
