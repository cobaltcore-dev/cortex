// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/db/containers"
)

type MockTable struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (m MockTable) TableName() string {
	return "mock_table"
}

func TestNewDB(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
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

	if err := db.DbMap.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpsert(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert a new record
	mockRecord := MockTable{ID: 1, Name: "test"}
	err = Upsert(db, &mockRecord)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the record was inserted
	var insertedRecord MockTable
	err = db.SelectOne(&insertedRecord, "SELECT * FROM mock_table WHERE id = :id", map[string]interface{}{"id": mockRecord.ID})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if insertedRecord.Name != "test" {
		t.Errorf("expected name to be 'test', got %s", insertedRecord.Name)
	}

	// Update the existing record
	mockRecord.Name = "updated"
	err = Upsert(db, &mockRecord)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the record was updated
	var updatedRecord MockTable
	err = db.SelectOne(&updatedRecord, "SELECT * FROM mock_table WHERE id = :id", map[string]interface{}{"id": mockRecord.ID})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if updatedRecord.Name != "updated" {
		t.Errorf("expected name to be 'updated', got %s", updatedRecord.Name)
	}
}
